package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

const slotWindow = 120 // minutes after a slot during which it may still fire (restarts)

func inSlot(now int, slot string) bool {
	m, ok := hm(slot)
	return ok && now >= m && now < m+slotWindow
}

func isOff(p *Person, t time.Time) bool {
	for _, w := range p.Rhythm.Off {
		if int(t.Weekday()) == w {
			return true
		}
	}
	return false
}

func (a *App) scheduler(ctx context.Context) {
	tk := time.NewTicker(20 * time.Second)
	defer tk.Stop()
	a.Tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			a.Tick()
		}
	}
}

// Tick runs everything that is due. Safe to call often.
func (a *App) Tick() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in tick: %v", r)
		}
	}()
	var outs []Out
	var noonFor []string
	a.Store.Do(func(d *Data) error {
		a.refreshBudget(d) // new month resets the budget flag
		d.linkUsers()
		for _, p := range d.People {
			if !p.Active || p.ChatID == 0 {
				continue
			}
			o, noon := a.tickPerson(d, p)
			outs = append(outs, o...)
			if noon {
				noonFor = append(noonFor, p.ID)
			}
		}
		outs = append(outs, a.tickManager(d)...)
		outs = append(outs, a.tickFollowups(d)...)
		outs = append(outs, a.tickReminders(d)...)
		outs = append(outs, a.tickLearn(d)...)
		return nil
	})
	a.flush(outs)
	for _, id := range noonFor {
		a.sendNoon(id)
	}
}

func (a *App) tickPerson(d *Data, p *Person) (outs []Out, noon bool) {
	ln := a.localNow(p)
	if isOff(p, ln) {
		return nil, false
	}
	date := dayKey(ln)
	now := minutesOfDay(ln)
	ds := d.Day(p.ID, date)
	outs = append(outs, a.autoPerson(d, p, ln, ds)...)

	// 1. morning plan
	if !ds.PlanSent && inSlot(now, p.Rhythm.Plan) {
		pl := d.PlanFor(p.ID, date)
		if pl == nil {
			pl = a.draftPlan(d, p, date)
		}
		switch {
		case pl == nil:
			ds.PlanSent = true // nothing to send today
		case pl.Status == "approved" || (pl.Status == "draft" && a.Cfg.UnapprovedPlan == "send"):
			outs = append(outs, a.planMessage(d, p, pl, ds)...)
		case pl.Status == "draft":
			key := "unappr:" + pl.ID
			if d.Flags[key] == "" {
				d.Flags[key] = "1"
				outs = append(outs, notifyAdmins(d, "برنامهٔ امروز "+p.Name+" تأیید نشده و هنوز نرفته:\n"+a.planLines(d, pl),
					[][]Button{{{Text: "تأیید و ارسال", Data: "pl:" + pl.ID + ":ok"}}})...)
			}
		}
	}

	// 2. nudge, only if the day hasn't started
	if ds.PlanSent && !ds.Started && !ds.Absent && !ds.NudgeSent && inSlot(now, p.Rhythm.Nudge) {
		ds.NudgeSent = true
		q := "امروز را شروع کردی؟"
		if pl := d.PlanFor(p.ID, date); pl != nil && pl.Status == "sent" {
			for _, id := range pl.TaskIDs {
				if t := d.Task(id); t != nil && t.Status == "open" {
					q = firstName(p) + "، " + t.Title + " را شروع کردی؟"
					break
				}
			}
			outs = append(outs, outTo(p, q, [][]Button{{{Text: "شروع کردم", Data: "n:start"}, {Text: "تا نیم ساعت دیگه", Data: "n:later"}, {Text: "گیر دارم", Data: "n:stuck"}}}))
		}
	}

	// 3. noon connection (composed outside the lock)
	if !ds.NoonSent && !ds.Absent && inSlot(now, p.Rhythm.Noon) {
		ds.NoonSent = true
		noon = true
	}

	// 4. end-of-day report
	if !ds.ReportAsked && !ds.Absent && inSlot(now, p.Rhythm.Report) {
		ds.ReportAsked = true
		ds.Queue = append(ds.Queue, a.overdueQuestions(d, p, date)...)
		ds.ReportAskAt = a.Now()
		if !ds.Reported && (ds.Awaiting == "" || ds.Awaiting == "report") {
			ds.Awaiting, ds.AwaitAt = "report", a.Now()
			outs = append(outs, outTo(p, a.reportQuestion(d, p), nil))
		} else {
			// they already reported earlier today: only ask what's missing
			for _, t := range a.todaysTasks(d, p, date) {
				if t.Status == "open" && t.UpdatedAt.Before(ds.LastReport.Add(-time.Minute)) {
					k := map[string]string{"metric": "metric", "multi": "progress"}[t.Kind]
					if k == "" {
						k = "single"
					}
					ds.Queue = append(ds.Queue, Question{Kind: k, TaskID: t.ID})
				}
			}
			outs = append(outs, a.questionsOnly(d, p, ds)...) // nothing missing: say nothing
		}
	}
	return outs, noon
}

func (a *App) reportQuestion(d *Data, p *Person) string {
	var metrics []string
	for _, t := range d.OpenTasks(p.ID) {
		if t.Kind == "metric" && t.Unit != "" {
			metrics = append(metrics, "چند "+t.Unit)
		}
	}
	if len(metrics) > 0 {
		return firstName(p) + "، خدا قوت. امروز " + strings.Join(metrics, " و ") + "؟ صوتی هم بفرستی کافی است."
	}
	return "خسته نباشی " + firstName(p) + ". امروز چه شد؟ هر طور راحتی بنویس یا صوتی بفرست؛ فایل کار تحویل‌شده را هم همین‌جا بفرست."
}

func (a *App) planLines(d *Data, pl *Plan) string {
	var sb strings.Builder
	for i, id := range pl.TaskIDs {
		if t := d.Task(id); t != nil {
			fmt.Fprintf(&sb, "%s. %s\n", fa(i+1), t.Title)
		}
	}
	return strings.TrimSpace(sb.String())
}

// sendNoon composes a short, warm midday message. No questions.
func (a *App) sendNoon(personID string) {
	var p Person
	var facts []string
	var news string
	a.Store.Do(func(d *Data) error {
		x := d.Person(personID)
		if x == nil {
			return nil
		}
		p = *x
		news = a.takeNews(d, x.ID)
		today := a.today(x)
		for _, t := range a.todaysTasks(d, x, today) {
			if t.Detail != "" {
				facts = append(facts, "نکتهٔ کار «"+t.Title+"»: "+t.Detail)
			}
		}
		// recent wins of this person
		cut := a.Now().Add(-48 * time.Hour)
		for _, t := range d.Tasks {
			if t.PersonID == x.ID && t.DoneAt != nil && t.DoneAt.After(cut) {
				facts = append(facts, "دیروز/امروز تحویل داد: "+t.Title)
			}
		}
		return nil
	})
	if p.ID == "" {
		return
	}
	var text string
	if a.Cfg.NoonAI && a.Claude.Enabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		prompt := "نام: " + firstName(&p) + "\nخبرها:\n" + news + "\nواقعیت‌ها:\n" + strings.Join(facts, "\n")
		t, err := a.Claude.Text(ctx, `پیام کوتاه نیمهٔ روز برای یک نیروی خلاق می‌نویسی، از طرف ربات تیم.
حداکثر دو جملهٔ کوتاه، فارسی گرم و ساده. اگر خبر خوبی هست، همان را بگو؛ اگر نکته‌ای کاربردی هست، یکی را بگو.
سؤال نپرس، فشار نیاور، اغراق و تعریف تکراری نکن، ایموجی نگذار. فقط از واقعیت‌های داده‌شده استفاده کن؛ چیزی نساز.
اگر هیچ خبر یا نکته‌ای نیست، فقط بنویس: «نیمهٔ روز. اگر چیزی لازم داری همین‌جا بگو.»`, prompt, 200)
		if err == nil && t != "" {
			text = t
		}
	}
	if text == "" {
		switch {
		case news != "":
			text = "نیمهٔ روز.\n" + news
		case len(facts) > 0 && strings.HasPrefix(facts[0], "نکتهٔ کار"):
			text = "نیمهٔ روز. " + strings.Replace(facts[0], "نکتهٔ کار", "یادت باشد، برای", 1)
		default:
			return // nothing worth saying: no message
		}
	}
	a.flush([]Out{outTo(&p, text, nil)})
}

func (a *App) tickManager(d *Data) []Out {
	mn := a.mgrNow()
	date := dayKey(mn)
	now := minutesOfDay(mn)
	var outs []Out
	// draft tomorrow's plans
	if inSlot(now, a.Cfg.PlanDraftTime) && d.Flags["draft:"+date] == "" {
		d.Flags["draft:"+date] = "1"
		for _, p := range d.People {
			if !p.Active || p.Rhythm.Plan == "" {
				continue
			}
			tomorrow := a.localNow(p).AddDate(0, 0, 1)
			if isOff(p, tomorrow) {
				continue
			}
			a.draftPlan(d, p, dayKey(tomorrow))
		}
	}
	// nightly summary + plan cards
	if inSlot(now, a.Cfg.SummaryTime) && d.Flags["summary:"+date] == "" {
		d.Flags["summary:"+date] = "1"
		for _, r := range d.SummaryReceivers() {
			txt, rows := a.summaryText(d, r, mn)
			outs = append(outs, outTo(r, txt, rows))
			if r.Role != "admin" {
				continue
			}
			for _, ap := range d.Approvals {
				if ap.Status == "pending" {
					outs = append(outs, outTo(r, "منتظر تأیید: "+ap.Title+"\n"+ap.Detail, approvalRows(ap)))
				}
			}
			if a.Cfg.UnapprovedPlan == "send" {
				// plans go out by themselves: one compact message, editable until morning
				var lines []string
				for _, pl := range d.Plans {
					if pl.Status != "draft" || pl.Date <= date {
						continue
					}
					if p := d.Person(pl.PersonID); p != nil && p.ChatID != 0 {
						var ts []string
						for _, id := range pl.TaskIDs {
							if t := d.Task(id); t != nil {
								ts = append(ts, t.Title)
							}
						}
						lines = append(lines, "• "+firstName(p)+" ("+fa(p.Rhythm.Plan)+"): "+strings.Join(ts, "، "))
					}
				}
				if len(lines) > 0 {
					var rows [][]Button
					if u := a.dashURL("/plans"); u != "" {
						rows = [][]Button{{{Text: "اصلاح برنامه‌ها", URL: u}}}
					}
					outs = append(outs, outTo(r, "برنامهٔ فردا، صبح خودکار می‌رود:\n"+strings.Join(lines, "\n"), rows))
				}
				continue
			}
			for _, pl := range d.Plans {
				if pl.Status != "draft" || pl.Date <= date {
					continue
				}
				p := d.Person(pl.PersonID)
				if p == nil {
					continue
				}
				head := "برنامهٔ فردای " + p.Name
				if a.Cfg.UnapprovedPlan == "wait" {
					head += " (بدون تأیید نمی‌رود)"
				}
				rows := [][]Button{{{Text: "تأیید", Data: "pl:" + pl.ID + ":ok"}}}
				if u := a.dashURL("/plans"); u != "" {
					rows[0] = append(rows[0], Button{Text: "اصلاح", URL: u})
				}
				outs = append(outs, outTo(r, head+"\n"+a.planLines(d, pl), rows))
			}
		}
	}
	return outs
}

func (a *App) tickFollowups(d *Data) []Out {
	var outs []Out
	for _, f := range d.Followups {
		// asked twice and still silent after 48 hours: tell whoever asked
		if f.Plain {
			continue
		}
		if f.Status == "open" && f.Reasked && !f.Escalated && a.Now().Sub(f.AskedAt) >= 2*time.Duration(a.Cfg.FollowupReaskH)*time.Hour {
			f.Escalated = true
			p := d.Person(f.PersonID)
			msg := fmt.Sprintf("🔕 %s به «%s» دو بار پرسیده شد و جواب نداد.", firstName(p), shorten(f.Question, 80))
			if by := d.Person(f.By); by != nil && by.ChatID != 0 {
				outs = append(outs, outTo(by, msg, nil))
			} else {
				outs = append(outs, notifyAdmins(d, msg, nil)...)
			}
			continue
		}
		if f.Status != "open" || f.Reasked {
			continue
		}
		if a.Now().Sub(f.AskedAt) >= time.Duration(a.Cfg.FollowupReaskH)*time.Hour {
			p := d.Person(f.PersonID)
			if p == nil || p.ChatID == 0 {
				continue
			}
			ln := a.localNow(p)
			if isOff(p, ln) || ln.Hour() < 9 || ln.Hour() >= 19 {
				continue // only in working hours
			}
			f.Reasked = true
			o := outTo(p, "یادآوری: "+f.Question, followupRows(f))
			ff := f
			o.Then = func(d *Data, id int64) { ff.MsgID = id }
			outs = append(outs, o)
		}
	}
	return outs
}

// summaryText: the nightly summary for an admin or senior.
func (a *App) summaryText(d *Data, viewer *Person, mn time.Time) (string, [][]Button) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "خلاصهٔ امشب · %s\n", faDate(mn))
	showPrivate := viewer.Role == "admin"
	var expected, reported int
	var missing []*Person
	for _, p := range d.People {
		if !p.Active || p.ChatID == 0 || p.Rhythm.Report == "" || p.Role == "admin" {
			continue
		}
		ln := a.localNow(p)
		if isOff(p, ln) {
			continue
		}
		ds := d.Day(p.ID, dayKey(ln))
		if ds.Absent {
			continue
		}
		expected++
		if ds.Reported {
			reported++
		} else {
			missing = append(missing, p)
		}
	}
	fmt.Fprintf(&sb, "%s نفر از %s نفر گزارش دادند.\n", fa(reported), fa(expected))

	dayStart := time.Date(mn.Year(), mn.Month(), mn.Day(), 0, 0, 0, 0, mn.Location())
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		sb.WriteString("\n" + title + "\n")
		for _, l := range lines {
			sb.WriteString("• " + l + "\n")
		}
	}

	var blockers []string
	for _, b := range d.Blockers {
		if b.Status == "open" {
			t := d.Task(b.TaskID)
			if t != nil && t.Private && !showPrivate {
				continue
			}
			l := b.Text + " · " + firstName(d.Person(b.PersonID))
			if b.At.Before(dayStart) {
				l += " (از " + faDate(b.At.In(mn.Location())) + ")"
			}
			blockers = append(blockers, l)
		}
	}
	section("🔴 گره‌ها", blockers)

	var hand, slips []string
	for _, e := range d.Events {
		if e.At.Before(dayStart) {
			continue
		}
		switch e.Kind {
		case "handoff":
			hand = append(hand, e.Text)
		case "slip":
			if t := d.Task(e.TaskID); t != nil && slipCount(t) >= 2 {
				slips = append(slips, t.Title+" · "+firstName(d.Person(t.PersonID))+" · "+fa(slipCount(t))+" بار عقب رفت")
			}
		}
	}
	section("✔ حلقه‌هایی که امروز به نفر بعد رسید", hand)
	section("⏳ موعدهای لغزنده", uniq(slips))

	var nums []string
	today := dayKey(mn)
	for _, m := range d.Metrics {
		t := d.Task(m.TaskID)
		if t == nil || m.Date != today {
			continue
		}
		l := fmt.Sprintf("%s: %s %s", t.Title, fa(m.Value), t.Unit)
		if t.DailyTarget > 0 {
			l += " از " + fa(t.DailyTarget)
		}
		if t.Total > 0 {
			l += fmt.Sprintf(" · جمع %s از %s", fa(t.DoneQty), fa(t.Total))
		}
		nums = append(nums, l)
	}
	section("📏 عددها", nums)

	if total, by := a.valueToday(d, mn); total > 0 {
		var parts []string
		for _, p := range d.People {
			if v := by[p.ID]; v > 0 {
				parts = append(parts, firstName(p)+" "+fa(roundTo(v, 0)))
			}
		}
		fmt.Fprintf(&sb, "\n💎 ارزش تحویل‌شدهٔ امروز: %s میلیون (%s)\n", fa(roundTo(total, 0)), strings.Join(parts, "، "))
	}
	var idle, heavy []string
	for _, l := range a.teamLoad(d, nil) {
		if l.P.ChatID == 0 || l.P.Role == "finance" {
			continue
		}
		if l.idle() {
			idle = append(idle, realFirst(l.P))
		} else if l.heavy() {
			s := realFirst(l.P) + " (" + fa(l.Ready) + " کار باز"
			if l.Overdue > 0 {
				s += "، " + fa(l.Overdue) + " عقب‌افتاده"
			}
			heavy = append(heavy, s+")")
		}
	}
	anyTasks := false
	for _, t := range d.Tasks {
		if t.Status == "open" {
			anyTasks = true
			break
		}
	}
	if len(idle) > 0 && anyTasks { // in learning mode (no tasks yet) this line is noise
		sb.WriteString("\n⚪ کار آماده ندارند: " + strings.Join(idle, "، ") + "\n")
	}
	if len(heavy) > 0 {
		sb.WriteString("🟠 پربار: " + strings.Join(heavy, "، ") + "\n")
	}

	var rows [][]Button
	if len(missing) > 0 {
		var names []string
		for _, p := range missing {
			names = append(names, p.Name)
		}
		sb.WriteString("\nگزارش ندادند: " + strings.Join(names, "، ") + "\n")
		var row []Button
		for i, p := range missing {
			if i >= 4 {
				break
			}
			row = append(row, Button{Text: "یادآوری به " + firstName(p), Data: "r:" + p.ID})
		}
		rows = append(rows, row)
	}
	pend := 0
	for _, ap := range d.Approvals {
		if ap.Status == "pending" {
			pend++
		}
	}
	if pend > 0 && viewer.Role == "admin" {
		fmt.Fprintf(&sb, "\n%s مورد منتظر تأیید شماست.", fa(pend))
	}
	if u := a.dashURL("/"); u != "" {
		rows = append(rows, []Button{{Text: "داشبورد", URL: u}})
	}
	return strings.TrimSpace(sb.String()), rows
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
