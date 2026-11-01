package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Autopilot: the manager says who should do what; the bot tells them, reminds,
// chases a missing report once, asks about overdue work and keeps everyone's load
// in view. Nothing here needs the manager to press a button.

const maxAutoPerDay = 5 // unrequested messages per person per day; more goes into the next scheduled message

// workingNow: inside this person's working window (not an off day, 08:00–20:00 local).
func workingNow(p *Person, ln time.Time) bool {
	return !isOff(p, ln) && ln.Hour() >= 8 && ln.Hour() < 20
}

// announceTask tells the person about a new task right away when it makes sense:
// during working hours after today's plan went out. Before the plan it joins today's
// plan; after hours it waits for tomorrow's plan as news.
func (a *App) announceTask(d *Data, t *Task, by string) (outs []Out, how string) {
	p := d.Person(t.PersonID)
	if p == nil || !p.Active {
		return nil, ""
	}
	if p.ChatID == 0 {
		return nil, "هنوز ربات را وصل نکرده؛ بعد از اتصال در برنامه‌اش می‌آید."
	}
	if !t.Ready && t.Kind != "metric" {
		return nil, "منتظر حلقهٔ قبل است؛ وقتی آن تمام شد خبرش می‌کنیم."
	}
	ln := a.localNow(p)
	date := dayKey(ln)
	ds := d.Day(p.ID, date)
	if !isOff(p, ln) && !ds.PlanSent {
		start, ok := hm(p.Rhythm.Plan)
		if ok && minutesOfDay(ln) < start+slotWindow {
			if pl := d.PlanFor(p.ID, date); pl != nil && pl.Status != "sent" {
				if !contains(pl.TaskIDs, t.ID) {
					pl.TaskIDs = append(pl.TaskIDs, t.ID)
				}
			}
			return nil, "در برنامهٔ صبح امروزش می‌آید (" + fa(p.Rhythm.Plan) + ")."
		}
	}
	text := "کار تازه از " + by + ": " + t.Title
	if pr := projName(d, t.ProjectID); pr != "" && !strings.Contains(t.Title, pr) {
		text += " · " + pr
	}
	if t.ETA != "" {
		text += "\nموعد: " + relDay(t.ETA, date)
	}
	if t.Detail != "" {
		text += "\n" + shorten(t.Detail, 200)
	}
	if !workingNow(p, ln) || ds.Absent || ds.Auto >= maxAutoPerDay {
		d.News = append(d.News, &News{PersonID: p.ID, Text: "کار تازه: «" + t.Title + "»", At: a.Now()})
		return nil, "الان ساعت کاری‌اش نیست؛ صبح در برنامه‌اش می‌آید."
	}
	ds.Auto++
	rows := [][]Button{{{Text: "دیدم", Data: "n:seen"}, {Text: "سؤال دارم", Data: "m:ask"}}}
	return []Out{outTo(p, text, rows)}, "همین الان به " + firstName(p) + " خبر داده شد."
}

// tellNow: a message the manager asked for. Sent now, or kept as a reminder for later.
func (a *App) tell(d *Data, p *Person, text string, at time.Time, followup bool, taskID, by string) ([]Out, *Reminder) {
	r := &Reminder{ID: newID(), PersonID: p.ID, Text: text, At: at, Followup: followup, TaskID: taskID, By: by}
	d.Reminders = append(d.Reminders, r)
	if at.After(a.Now().Add(time.Minute)) {
		return nil, r
	}
	return a.sendReminder(d, r), r
}

func (a *App) sendReminder(d *Data, r *Reminder) []Out {
	p := d.Person(r.PersonID)
	if p == nil || p.ChatID == 0 {
		return nil
	}
	now := a.Now()
	r.SentAt = &now
	d.Log("tell", p.ID, r.TaskID, shorten(r.Text, 80))
	// every message from the manager is tracked, so a typed reply goes back to them
	f := &Followup{ID: newID(), PersonID: p.ID, Question: r.Text, Status: "open", AskedAt: now, By: r.By, Plain: !r.Followup}
	d.Followups = append(d.Followups, f)
	o := outTo(p, r.Text, nil)
	if r.Followup {
		o.Rows = followupRows(f)
	}
	o.Then = func(d *Data, id int64) { f.MsgID = id }
	return []Out{o}
}

// tickReminders sends reminders that are due. A reminder that falls on the person's
// day off moves to the next working morning.
func (a *App) tickReminders(d *Data) []Out {
	var outs []Out
	now := a.Now()
	for _, r := range d.Reminders {
		if r.SentAt != nil || r.Cancel || r.At.After(now) {
			continue
		}
		p := d.Person(r.PersonID)
		if p == nil {
			r.Cancel = true
			continue
		}
		ln := a.localNow(p)
		if isOff(p, ln) {
			continue
		}
		outs = append(outs, a.sendReminder(d, r)...)
	}
	return outs
}

// autoPerson: the person-side automatic follow-ups that run on every tick.
func (a *App) autoPerson(d *Data, p *Person, ln time.Time, ds *DayState) []Out {
	var outs []Out
	now := minutesOfDay(ln)
	// one gentle reminder 90 minutes after an unanswered report question
	if rep, ok := hm(p.Rhythm.Report); ok && ds.ReportAsked && !ds.Reported && !ds.Absent && !ds.Chased &&
		now >= rep+90 && ln.Hour() < 21 {
		ds.Chased = true
		ds.Auto++
		outs = append(outs, outTo(p, firstName(p)+"، گزارش امروز مانده. یک خط هم کافی است؛ یا صوتی بفرست.", nil))
	}
	return outs
}

// overdueQuestions: open tasks whose promised date has passed get one question at report time.
func (a *App) overdueQuestions(d *Data, p *Person, date string) []Question {
	var qs []Question
	for _, t := range d.OpenTasks(p.ID) {
		if t.ETA != "" && t.ETA < date && t.Kind != "metric" {
			qs = append(qs, Question{Kind: "overdue", TaskID: t.ID})
		}
	}
	return qs
}

// ---------- load ----------

type load struct {
	P        *Person
	Open     int // open tasks
	Ready    int // open and ready to work on
	Overdue  int
	Waiting  int // waiting on a previous link
	Tomorrow int // items in tomorrow's plan
	Value7   float64
	Target7  float64
}

func (a *App) loadOf(d *Data, p *Person) load {
	l := load{P: p}
	ln := a.localNow(p)
	today := dayKey(ln)
	for _, t := range d.OpenTasks(p.ID) {
		l.Open++
		switch {
		case !t.Ready && t.Kind != "metric":
			l.Waiting++
		default:
			l.Ready++
		}
		if t.ETA != "" && t.ETA < today {
			l.Overdue++
		}
	}
	if pl := d.PlanFor(p.ID, dayKey(ln.AddDate(0, 0, 1))); pl != nil {
		l.Tomorrow = len(pl.TaskIDs)
	}
	cut := a.Now().AddDate(0, 0, -7)
	for _, t := range d.Tasks {
		if t.PersonID == p.ID && t.DoneAt != nil && t.DoneAt.After(cut) {
			l.Value7 += t.Value
		}
	}
	work := 0
	for i := 0; i < 7; i++ {
		if !isOff(p, ln.AddDate(0, 0, -i)) {
			work++
		}
	}
	l.Target7 = p.DailyValue * float64(work)
	return l
}

func (l load) idle() bool  { return l.Ready == 0 }
func (l load) heavy() bool { return l.Ready > 6 || l.Overdue >= 2 }

// loadWarning: one line for whoever is assigning work.
func (l load) warning() string {
	switch {
	case l.Overdue >= 2:
		return fmt.Sprintf("%s الان %s کار عقب‌افتاده دارد؛ شاید بهتر است چیزی از دوشش برداشته شود.", firstName(l.P), fa(l.Overdue))
	case l.Ready > 6:
		return fmt.Sprintf("%s الان %s کار باز آماده دارد؛ برنامهٔ روزانه فقط %s تای اول را می‌آورد.", firstName(l.P), fa(l.Ready), fa(3))
	}
	return ""
}

func (a *App) teamLoad(d *Data, viewer *User) []load {
	var out []load
	for _, p := range d.People {
		if !p.Active || p.Role == "admin" {
			continue
		}
		if viewer != nil && viewer.Role == "lead" && p.Line != viewer.Line {
			continue
		}
		out = append(out, a.loadOf(d, p))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].P.Line < out[j].P.Line })
	return out
}

// valueToday: value of tasks delivered today (manager's day), and per person.
func (a *App) valueToday(d *Data, mn time.Time) (total float64, by map[string]float64) {
	by = map[string]float64{}
	start := time.Date(mn.Year(), mn.Month(), mn.Day(), 0, 0, 0, 0, mn.Location())
	for _, t := range d.Tasks {
		if t.DoneAt != nil && !t.DoneAt.Before(start) && t.Value > 0 {
			total += t.Value
			by[t.PersonID] += t.Value
		}
	}
	return
}

// parseWhen: "", "now", "HH:MM" (today, or tomorrow if passed), "tomorrow HH:MM",
// "فردا ۱۰", "YYYY-MM-DD HH:MM". Interpreted in the person's time zone.
func (a *App) parseWhen(p *Person, s string) (time.Time, error) {
	loc := loadLoc(p.TZ)
	now := a.Now().In(loc)
	s = strings.TrimSpace(latin(s))
	if s == "" || s == "now" || s == "الان" {
		return now, nil
	}
	day, explicit := now, false
	for _, w := range []string{"tomorrow", "فردا"} {
		if strings.HasPrefix(s, w) {
			day, explicit = now.AddDate(0, 0, 1), true
			s = strings.TrimSpace(strings.TrimPrefix(s, w))
		}
	}
	if len(s) >= 10 && s[4] == '-' {
		if dd, err := time.ParseInLocation("2006-01-02", s[:10], loc); err == nil {
			day, explicit = dd, true
			s = strings.TrimSpace(s[10:])
		}
	}
	if s == "" {
		s = "09:00"
	}
	if !strings.Contains(s, ":") {
		s += ":00"
	}
	m, ok := hm(s)
	if !ok {
		return time.Time{}, fmt.Errorf("زمان را نفهمیدم: %s (مثل 10:30 یا فردا 9:00)", s)
	}
	t := time.Date(day.Year(), day.Month(), day.Day(), m/60, m%60, 0, 0, loc)
	if t.Before(now) && !explicit {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}
