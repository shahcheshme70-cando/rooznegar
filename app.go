package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	Cfg    Config
	Store  *Store
	Msgr   map[string]*Messenger
	Claude *Claude
	STT    *STT
	BZ     *Bargozar
	Now    func() time.Time
	Files  string

	workMu  sync.Mutex
	workers map[string]chan tgUpdate
	pendMu  sync.Mutex
	pend    map[string]*pendingMsg // message batching per person

	overBudget atomic.Bool // read without the store lock (Claude.Enabled is called under it)
}

type pendingMsg struct {
	texts []string
	voice bool
	timer *time.Timer
}

func NewApp(cfg Config, st *Store) *App {
	a := &App{Cfg: cfg, Store: st, Msgr: map[string]*Messenger{}, Now: time.Now, Files: filepath.Join(cfg.DataDir, "files")}
	if cfg.TelegramToken != "" {
		a.Msgr["telegram"] = &Messenger{Name: "telegram", Base: cfg.TelegramAPI, Token: cfg.TelegramToken, HTTP: newHTTP(70 * time.Second)}
	}
	if cfg.BaleToken != "" {
		a.Msgr["bale"] = &Messenger{Name: "bale", Base: cfg.BaleAPI, Token: cfg.BaleToken, HTTP: newHTTP(70 * time.Second)}
	}
	a.pend = map[string]*pendingMsg{}
	if cfg.BargozarURL != "" && cfg.BargozarUser != "" {
		a.BZ = newBargozar(cfg.BargozarURL, cfg.BargozarUser, cfg.BargozarPass)
	}
	a.Claude = &Claude{Key: cfg.AnthropicKey, URL: cfg.AnthropicURL, Model: cfg.Model, HTTP: newHTTP(60 * time.Second)}
	a.Claude.OnUsage = a.recordUsage
	a.Claude.Effort = cfg.Effort
	a.Claude.Allow = a.withinBudget
	a.STT = &STT{URL: cfg.STTURL, Key: cfg.STTKey, Model: cfg.STTModel, HTTP: newHTTP(120 * time.Second)}
	st.View(func(d *Data) { a.refreshBudget(d) })
	return a
}

// Out is a message decided under the store lock and sent after it is released.
type Out struct {
	Channel string
	Chat    int64
	Text    string
	Rows    [][]Button
	EditID  int64 // edit this message instead of sending a new one
	Caption int64 // edit the caption of this message (files channel)
	Then    func(d *Data, msgID int64)
}

func outTo(p *Person, text string, rows [][]Button) Out {
	return Out{Channel: p.Channel, Chat: p.ChatID, Text: text, Rows: rows}
}

func (a *App) flush(outs []Out) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, o := range outs {
		m := a.Msgr[o.Channel]
		if !m.Enabled() || o.Chat == 0 {
			continue
		}
		var id int64
		var err error
		if o.Caption != 0 {
			err = m.EditCaption(ctx, o.Chat, o.Caption, o.Text)
		} else if o.EditID != 0 {
			err = m.Edit(ctx, o.Chat, o.EditID, o.Text, o.Rows)
		} else {
			id, err = m.Send(ctx, o.Chat, o.Text, o.Rows)
		}
		if err != nil {
			log.Printf("send %s %d: %v", o.Channel, o.Chat, err)
			continue
		}
		if o.Then != nil {
			a.Store.Do(func(d *Data) error { o.Then(d, id); return nil })
		}
	}
}

func (a *App) localNow(p *Person) time.Time { return a.Now().In(loadLoc(p.TZ)) }
func (a *App) today(p *Person) string       { return dayKey(a.localNow(p)) }
func (a *App) mgrNow() time.Time            { return a.Now().In(loadLoc(a.Cfg.ManagerTZ)) }

func firstName(p *Person) string {
	if p == nil {
		return ""
	}
	if p.Nick != "" {
		return p.Nick
	}
	f := strings.Fields(p.Name)
	if len(f) == 0 {
		return p.Name
	}
	if (f[0] == "سید" || f[0] == "آقای" || f[0] == "خانم") && len(f) > 1 {
		return f[0] + " " + f[1]
	}
	return f[0]
}

func pname(d *Data, id string) string {
	if p := d.Person(id); p != nil {
		return p.Name
	}
	return "؟"
}

func projName(d *Data, id string) string {
	if p := d.Project(id); p != nil {
		return p.Name
	}
	return ""
}

func (a *App) dashURL(path string) string {
	if a.Cfg.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(a.Cfg.BaseURL, "/") + path
}

// ---------- shared actions (bot, dashboard, connector) ----------

// markDone closes a task and hands off to the tasks waiting for it.
func (a *App) markDone(d *Data, t *Task, by string) (outs []Out) {
	if t.Status == "done" {
		return nil
	}
	now := a.Now()
	t.Status, t.Progress, t.DoneAt, t.UpdatedAt = "done", "done", &now, now
	d.Log("done", t.PersonID, t.ID, t.Title)
	for _, n := range d.Tasks {
		if n.After == t.ID && n.Status == "open" {
			n.Ready = true
			n.UpdatedAt = now
			np := d.Person(n.PersonID)
			if np == nil {
				continue
			}
			txt := fmt.Sprintf("«%s» آماده است: %s «%s» را تحویل داد.", n.Title, firstName(d.Person(t.PersonID)), t.Title)
			d.Log("handoff", t.PersonID, t.ID, fmt.Sprintf("%s ← %s", t.Title, np.Name))
			// the chain moves now: during their working day the next person hears it at once
			if np.ChatID != 0 && np.Active {
				ln := a.localNow(np)
				ds := d.Day(np.ID, dayKey(ln))
				if ds.PlanSent && !ds.Absent && workingNow(np, ln) && ds.Auto < maxAutoPerDay {
					ds.Auto++
					outs = append(outs, outTo(np, "🔗 "+txt, [][]Button{{{Text: "دیدم", Data: "n:seen"}, {Text: "سؤال دارم", Data: "m:ask"}}}))
					continue
				}
			}
			d.News = append(d.News, &News{PersonID: np.ID, Text: txt, At: now})
		}
	}
	_ = by
	return outs
}

// nextOf: names of people waiting on this task (for "به X خبر دادم")
func nextOf(d *Data, t *Task) []string {
	var out []string
	for _, n := range d.Tasks {
		if n.After == t.ID {
			if np := d.Person(n.PersonID); np != nil {
				out = append(out, firstName(np))
			}
		}
	}
	return out
}

func (a *App) reopen(d *Data, t *Task) {
	if t.Status != "done" {
		return
	}
	t.Status, t.DoneAt, t.UpdatedAt = "open", nil, a.Now()
	t.Progress = "near"
	for _, n := range d.Tasks {
		if n.After == t.ID && n.Status == "open" {
			n.Ready = false
		}
	}
	d.Log("reopen", t.PersonID, t.ID, t.Title)
}

// setETA records a new estimate and detects slips.
func (a *App) setETA(d *Data, t *Task, eta string) (slipped bool) {
	if eta == "" || eta == t.ETA {
		return false
	}
	if t.ETA != "" && eta > t.ETA {
		slipped = true
	}
	t.ETA = eta
	t.ETAHistory = append(t.ETAHistory, eta)
	if slipped {
		d.Log("slip", t.PersonID, t.ID, fmt.Sprintf("%s → %s", t.Title, eta))
	}
	return slipped
}

func slipCount(t *Task) int {
	n := 0
	for i := 1; i < len(t.ETAHistory); i++ {
		if t.ETAHistory[i] > t.ETAHistory[i-1] {
			n++
		}
	}
	return n
}

// notifyAdmins builds a message to every admin.
func notifyAdmins(d *Data, text string, rows [][]Button) []Out {
	var outs []Out
	for _, p := range d.Admins() {
		outs = append(outs, outTo(p, text, rows))
	}
	return outs
}

func (a *App) addMetric(d *Data, t *Task, date string, v float64, note string) []Out {
	// replace the same day's entry instead of double counting
	for _, m := range d.Metrics {
		if m.TaskID == t.ID && m.Date == date {
			t.DoneQty -= m.Value
			m.Value, m.Note, m.At = v, note, a.Now()
			t.DoneQty += v
			return a.checkBelowTarget(d, t)
		}
	}
	d.Metrics = append(d.Metrics, &MetricEntry{TaskID: t.ID, Date: date, Value: v, Note: note, At: a.Now()})
	t.DoneQty += v
	t.UpdatedAt = a.Now()
	if t.Total > 0 && t.DoneQty >= t.Total {
		return append(a.checkBelowTarget(d, t), a.markDone(d, t, "metric")...)
	}
	return a.checkBelowTarget(d, t)
}

// consecutive days below target -> approval for extra crew
func (a *App) checkBelowTarget(d *Data, t *Task) []Out {
	if t.DailyTarget <= 0 {
		return nil
	}
	ms := d.MetricsFor(t.ID)
	n := a.Cfg.BelowTargetDays
	if len(ms) < n {
		return nil
	}
	for _, m := range ms[len(ms)-n:] {
		if m.Value >= t.DailyTarget {
			return nil
		}
	}
	key := "below:" + t.ID + ":" + ms[len(ms)-1].Date
	if d.Flags[key] != "" {
		return nil
	}
	// don't stack: one pending per task
	for _, ap := range d.Approvals {
		if ap.TaskID == t.ID && ap.Kind == "extra_crew" && ap.Status == "pending" {
			return nil
		}
	}
	d.Flags[key] = "1"
	var vals []string
	for _, m := range ms[len(ms)-n:] {
		vals = append(vals, fa(m.Value))
	}
	ap := &Approval{ID: newID(), Kind: "extra_crew", PersonID: t.PersonID, TaskID: t.ID, Status: "pending", At: a.Now(),
		Title:  fmt.Sprintf("نیروی اضافه برای «%s»", t.Title),
		Detail: fmt.Sprintf("%s روز پشت هم زیر %s %s (%s)", fa(n), fa(t.DailyTarget), t.Unit, strings.Join(vals, " و "))}
	d.Approvals = append(d.Approvals, ap)
	d.Log("approval", t.PersonID, t.ID, ap.Title)
	return notifyAdmins(d, "⚠ "+ap.Title+"\n"+ap.Detail+" · "+pname(d, t.PersonID), approvalRows(ap))
}

func approvalRows(ap *Approval) [][]Button {
	return [][]Button{{{Text: "تأیید", Data: "a:" + ap.ID + ":ok"}, {Text: "رد", Data: "a:" + ap.ID + ":no"}, {Text: "بپرس چرا", Data: "a:" + ap.ID + ":ask"}}}
}

func (a *App) decideApproval(d *Data, ap *Approval, decision, by string) []Out {
	if ap.Status != "pending" && ap.Status != "asked" {
		return nil
	}
	p := d.Person(ap.PersonID)
	switch decision {
	case "ok":
		ap.Status, ap.By = "approved", by
		d.Log("approved", ap.PersonID, ap.TaskID, ap.Title)
		if p != nil && p.ChatID != 0 {
			return []Out{outTo(p, "تأیید شد: "+ap.Title, nil)}
		}
	case "no":
		ap.Status, ap.By = "rejected", by
		d.Log("rejected", ap.PersonID, ap.TaskID, ap.Title)
		if p != nil && p.ChatID != 0 {
			return []Out{outTo(p, "فعلاً نه: "+ap.Title+". اگر لازم بود با مدیر صحبت کن.", nil)}
		}
	case "ask":
		ap.Status, ap.By = "asked", by
		if p != nil && p.ChatID != 0 {
			ds := d.Day(p.ID, a.today(p))
			ds.Awaiting, ds.AwaitTask = "why:"+ap.ID, ap.TaskID
			ds.AwaitAt = a.Now()
			return []Out{outTo(p, "مدیر می‌پرسد: «"+ap.Title+"» برای چیست؟ یک خط بنویس.", nil)}
		}
	}
	return nil
}

// draftPlan builds tomorrow's (or today's) plan for a person.
func (a *App) draftPlan(d *Data, p *Person, date string) *Plan {
	if pl := d.PlanFor(p.ID, date); pl != nil {
		return pl
	}
	tasks := d.OpenTasks(p.ID)
	var ids []string
	for _, t := range tasks {
		if t.Private && p.Role != "finance" && p.Role != "admin" {
			continue
		}
		if !t.Ready && t.Kind != "metric" {
			continue
		}
		ids = append(ids, t.ID)
		if len(ids) >= a.Cfg.MaxPlanItems {
			break
		}
	}
	if len(ids) == 0 {
		return nil
	}
	pl := &Plan{ID: newID(), PersonID: p.ID, Date: date, TaskIDs: ids, Status: "draft", CreatedAt: a.Now()}
	d.Plans = append(d.Plans, pl)
	return pl
}

func (a *App) approvePlan(d *Data, pl *Plan, by string) []Out {
	if pl.Status != "draft" {
		return nil
	}
	pl.Status, pl.ApprovedBy = "approved", by
	d.Log("plan_ok", pl.PersonID, "", "برنامهٔ "+faDay(pl.Date))
	// if its morning already passed (approved late), send now
	p := d.Person(pl.PersonID)
	if p == nil || p.ChatID == 0 {
		return nil
	}
	ln := a.localNow(p)
	if dayKey(ln) == pl.Date {
		ds := d.Day(p.ID, pl.Date)
		start, ok := hm(p.Rhythm.Plan)
		end, ok2 := hm(p.Rhythm.Report)
		if ok && !ds.PlanSent && minutesOfDay(ln) >= start && (!ok2 || minutesOfDay(ln) < end) {
			return a.planMessage(d, p, pl, ds)
		}
	}
	return nil
}

func (a *App) planMessage(d *Data, p *Person, pl *Plan, ds *DayState) []Out {
	var sb strings.Builder
	fmt.Fprintf(&sb, "صبح بخیر %s. برنامهٔ امروز:\n", firstName(p))
	for i, id := range pl.TaskIDs {
		t := d.Task(id)
		if t == nil || t.Status != "open" {
			continue
		}
		fmt.Fprintf(&sb, "\n%s. %s", fa(i+1), t.Title)
		var bits []string
		if pr := projName(d, t.ProjectID); pr != "" && !strings.Contains(t.Title, pr) {
			bits = append(bits, pr)
		}
		if t.Kind == "metric" && t.DailyTarget > 0 {
			bits = append(bits, fmt.Sprintf("هدف امروز %s %s", fa(t.DailyTarget), t.Unit))
		}
		if t.Progress != "" && t.Progress != "done" {
			s := "دیروز گفتی «" + progressLabels[t.Progress] + "»"
			if t.ETA != "" {
				s += " · موعد تو: " + relDay(t.ETA, pl.Date)
			}
			bits = append(bits, s)
		}
		if t.After != "" {
			if prev := d.Task(t.After); prev != nil && prev.Status == "done" {
				bits = append(bits, "از "+firstName(d.Person(prev.PersonID))+" رسیده")
			}
		}
		if i == 0 && len(pl.TaskIDs) > 1 {
			bits = append(bits, "اولویت اول")
		}
		if len(bits) > 0 {
			sb.WriteString("\n   " + strings.Join(bits, " · "))
		}
		if t.Detail != "" {
			sb.WriteString("\n   " + shorten(t.Detail, 160))
		}
	}
	if pl.Note != "" {
		sb.WriteString("\n\n" + pl.Note)
	}
	// handoff news, except what the plan already says ("از ایلیا رسیده")
	var news []string
	for _, n := range d.News {
		if n.PersonID != p.ID || n.Delivered {
			continue
		}
		n.Delivered = true
		dup := false
		for _, id := range pl.TaskIDs {
			if t := d.Task(id); t != nil && strings.Contains(n.Text, "«"+t.Title+"»") {
				dup = true
			}
		}
		if !dup {
			news = append(news, "• "+n.Text)
		}
	}
	if len(news) > 0 {
		sb.WriteString("\n\n" + strings.Join(news, "\n"))
	}
	now := a.Now()
	pl.Status, pl.SentAt = "sent", &now
	ds.PlanSent = true
	d.Log("plan_sent", p.ID, "", "")
	rows := [][]Button{{{Text: "شروع کردم", Data: "m:start"}, {Text: "امروز نیستم", Data: "m:absent"}, {Text: "سؤال دارم", Data: "m:ask"}}}
	return []Out{outTo(p, sb.String(), rows)}
}

func (a *App) takeNews(d *Data, personID string) string {
	var lines []string
	for _, n := range d.News {
		if n.PersonID == personID && !n.Delivered {
			n.Delivered = true
			lines = append(lines, "• "+n.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) sendDraft(d *Data, dr *Draft, by string) ([]Out, error) {
	if dr.Status != "draft" {
		return nil, fmt.Errorf("این پیش‌نویس قبلاً %s شده", map[string]string{"sent": "فرستاده", "cancelled": "لغو"}[dr.Status])
	}
	p := d.Person(dr.ToPersonID)
	if p == nil || p.ChatID == 0 {
		return nil, fmt.Errorf("%s هنوز ربات را وصل نکرده", pname(d, dr.ToPersonID))
	}
	now := a.Now()
	dr.Status, dr.SentAt = "sent", &now
	d.Log("draft_sent", p.ID, "", shorten(dr.Text, 80)+" · "+by)
	f := &Followup{ID: newID(), PersonID: p.ID, Question: dr.Text, Status: "open", AskedAt: now, By: by, Plain: !dr.Followup}
	d.Followups = append(d.Followups, f)
	o := outTo(p, dr.Text, nil)
	if dr.Followup {
		o.Rows = followupRows(f)
	}
	o.Then = func(d *Data, id int64) { f.MsgID = id }
	return []Out{o}, nil
}

func followupRows(f *Followup) [][]Button {
	return [][]Button{{{Text: "انجام شد", Data: "f:" + f.ID + ":done"}, {Text: "در جریان", Data: "f:" + f.ID + ":doing"}, {Text: "گیر دارم", Data: "f:" + f.ID + ":stuck"}}}
}

func (a *App) createDraft(d *Data, to *Person, text, by string, followup bool) *Draft {
	dr := &Draft{ID: newID(), ToPersonID: to.ID, Text: strings.TrimSpace(text), CreatedBy: by, Status: "draft", Followup: followup, At: a.Now()}
	d.Drafts = append(d.Drafts, dr)
	d.Log("draft", to.ID, "", shorten(text, 80))
	return dr
}

// visible: what a dashboard/connector user may see
func canSeeTask(u *User, d *Data, t *Task) bool {
	if u == nil {
		return false
	}
	switch u.Role {
	case "admin":
		return true
	case "finance":
		return t.Private || t.Cycle == "cash"
	case "senior":
		return !t.Private
	case "lead":
		p := d.Person(t.PersonID)
		return !t.Private && p != nil && p.Line == u.Line
	}
	return false
}

func canSeePerson(u *User, p *Person) bool {
	if u == nil {
		return false
	}
	switch u.Role {
	case "admin", "senior":
		return true
	case "lead":
		return p.Line == u.Line
	case "finance":
		return p.Role == "finance" || p.ID == u.PersonID
	}
	return false
}

// canMessage: who may draft/send to whom
func canMessage(u *User, p *Person) bool {
	if u == nil {
		return false
	}
	switch u.Role {
	case "admin", "senior":
		return true
	case "lead":
		return p.Line == u.Line
	}
	return false
}

func sortedPeople(d *Data) []*Person {
	ps := append([]*Person(nil), d.People...)
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps
}

// ---------- token budget ----------

func (a *App) month() string { return a.mgrNow().Format("2006-01") }

func (a *App) usageNow(d *Data) *Usage {
	u := d.Usage[a.month()]
	if u == nil {
		u = &Usage{}
		d.Usage[a.month()] = u
	}
	return u
}

func (a *App) cost(u *Usage) float64 {
	return float64(u.In)/1e6*a.Cfg.PriceInPerM + float64(u.Out)/1e6*a.Cfg.PriceOutPerM
}

func (a *App) recordUsage(in, out int) {
	var outs []Out
	a.Store.Do(func(d *Data) error {
		u := a.usageNow(d)
		u.Calls++
		u.In += in
		u.Out += out
		defer a.refreshBudget(d)
		if b := a.Cfg.MonthlyBudgetUSD; b > 0 {
			c := a.cost(u)
			key := "budget80:" + a.month()
			if c >= 0.8*b && d.Flags[key] == "" {
				d.Flags[key] = "1"
				outs = notifyAdmins(d, fmt.Sprintf("مصرف Claude روزنگار این ماه به حدود %s دلار رسید (سقف %s). بعد از سقف، ربات تا آخر ماه فقط با دکمه کار می‌کند.", fa(roundTo(c, 2)), fa(b)), nil)
			}
		}
		return nil
	})
	a.flush(outs)
}

// withinBudget must never touch the store: it runs inside Store.Do via Claude.Enabled.
func (a *App) withinBudget() bool { return !a.overBudget.Load() }

// refreshBudget recomputes the flag; call with the store lock held.
func (a *App) refreshBudget(d *Data) {
	over := false
	if b := a.Cfg.MonthlyBudgetUSD; b > 0 {
		if u := d.Usage[a.month()]; u != nil {
			over = a.cost(u) >= b
		}
	}
	a.overBudget.Store(over)
}

func (a *App) countSaved() {
	a.Store.Do(func(d *Data) error { a.usageNow(d).Saved++; return nil })
}

func roundTo(f float64, n int) float64 {
	p := 1.0
	for i := 0; i < n; i++ {
		p *= 10
	}
	return float64(int(f*p+0.5)) / p
}

// ---------- files channel ----------

func (a *App) filesChat(channel string) int64 {
	switch channel {
	case "telegram":
		return a.Cfg.TelegramFilesChat
	case "bale":
		return a.Cfg.BaleFilesChat
	}
	return 0
}

// file refs: a server path, or "chan:<messenger>:<chat>:<msgid>" for files kept in a channel
func parseChanRef(ref string) (string, int64, int64, bool) {
	var ch string
	var chat, id int64
	if !strings.HasPrefix(ref, "chan:") {
		return "", 0, 0, false
	}
	parts := strings.SplitN(ref, ":", 5)
	if len(parts) < 4 {
		return "", 0, 0, false
	}
	ch = parts[1]
	if _, err := fmt.Sscan(parts[2], &chat); err != nil {
		return "", 0, 0, false
	}
	if _, err := fmt.Sscan(parts[3], &id); err != nil {
		return "", 0, 0, false
	}
	return ch, chat, id, true
}

// public link to a message in a private Telegram channel/supergroup
func chanLink(ch string, chat, id int64) string {
	if ch != "telegram" {
		return ""
	}
	s := fmt.Sprint(chat)
	if strings.HasPrefix(s, "-100") {
		return "https://t.me/c/" + s[4:] + "/" + fmt.Sprint(id)
	}
	return ""
}

func hashtag(s string) string {
	s = strings.TrimSpace(s)
	r := strings.NewReplacer(" ", "_", "\u200c", "_", "،", "", "(", "", ")", "", "·", "", ".", "", "-", "_", "/", "_")
	return "#" + r.Replace(s)
}

// fileCaption: a fixed, searchable label for every archived file.
//
//	📎 plan.pdf
//	✔ نقشهٔ اجرایی سقف            (or ⏳ کارش هنوز مشخص نشده)
//	📁 #حرف_نو_سنندج · #زنجیره_ارزش
//	👤 #علیرضا_حاجی_سامی · #خط_ساخت
//	📅 چهارشنبه ۱۳ آبان ۱۴۰۵ · #آبان_۱۴۰۵
//	💬 note from the sender
func (a *App) fileCaption(d *Data, p *Person, t *Task, date, name, note string) string {
	var lines []string
	if name != "" {
		lines = append(lines, "📎 "+name)
	}
	if t != nil {
		lines = append(lines, "✔ "+t.Title)
		var tags []string
		if pr := projName(d, t.ProjectID); pr != "" {
			tags = append(tags, hashtag(pr))
		}
		if c := cycleLabels[t.Cycle]; c != "" {
			tags = append(tags, hashtag("زنجیره "+c))
		}
		if len(tags) > 0 {
			lines = append(lines, "📁 "+strings.Join(tags, " · "))
		}
	} else {
		lines = append(lines, "⏳ کارش هنوز مشخص نشده")
	}
	who := "👤 " + hashtag(p.Name)
	if p.Line != "" {
		who += " · " + hashtag("خط "+p.Line)
	}
	lines = append(lines, who)
	if dt, ok := parseDay(date, time.UTC); ok {
		y, m, _ := toJalali(dt.Year(), int(dt.Month()), dt.Day())
		lines = append(lines, "📅 "+faDateYear(dt)+" · "+hashtag(jMonths[m]+" "+fa(y)))
	}
	if note = strings.TrimSpace(note); note != "" {
		lines = append(lines, "💬 "+shorten(note, 300))
	}
	return strings.Join(lines, "\n")
}

func refName(ref string) string {
	parts := strings.SplitN(ref, ":", 5)
	if len(parts) == 5 {
		return parts[4]
	}
	if !strings.HasPrefix(ref, "chan:") {
		return filepath.Base(ref) // a file kept on the server
	}
	return ""
}

// attachFiles links today's received files to a delivered task and labels them in the channel.
func (a *App) attachFiles(d *Data, p *Person, ds *DayState, t *Task, date string) []Out {
	if len(ds.Files) == 0 {
		return nil
	}
	var outs []Out
	for _, f := range ds.Files {
		if ch, chat, id, ok := parseChanRef(f); ok {
			outs = append(outs, Out{Channel: ch, Chat: chat, Caption: id, Text: a.fileCaption(d, p, t, date, refName(f), "")})
		}
	}
	t.Files = append(t.Files, ds.Files...)
	ds.Files = nil
	return outs
}
