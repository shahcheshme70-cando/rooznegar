package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Learning: the bot watches how each person actually works with it and adjusts.
// Safe, reversible changes it makes by itself (and tells the manager, with an undo button):
//   - report question moves to when the person really reports
//   - the start nudge is switched off for people who always start before it (and back on if that stops)
//   - the noon message is switched off when someone says the bot writes too much
// Everything else it only reports: unanswered messages, reports that never come, slipping dates,
// what people said about the bot, and who delivers above or below their daily value.

const learnWindow = 14 // days looked back

func (a *App) stamp(ds *DayState, start, report bool) {
	now := a.Now()
	if start && ds.StartAt.IsZero() {
		ds.StartAt = now
	}
	if report && ds.ReportAt.IsZero() {
		ds.ReportAt = now
	}
}

type pattern struct {
	Days        int // working days with contact
	Started     int
	StartBefore int // started before the nudge time
	Asked       int // report question asked
	Reported    int
	ReportMins  []int
	Chased      int
	ChasedNoRep int
	FuAsked     int
	FuAnswered  int
	Slips       int
	Done        int
}

func (pt pattern) medianReport() int {
	if len(pt.ReportMins) == 0 {
		return -1
	}
	m := append([]int(nil), pt.ReportMins...)
	sort.Ints(m)
	return m[len(m)/2]
}

func (a *App) patternOf(d *Data, p *Person) pattern {
	var pt pattern
	loc := loadLoc(p.TZ)
	cut := dayKey(a.Now().In(loc).AddDate(0, 0, -learnWindow))
	nudge, hasNudge := hm(p.Rhythm.Nudge)
	if !hasNudge {
		if t := lastTuning(d, p.ID, "nudge"); t != nil {
			nudge, hasNudge = hm(t.Old)
		}
	}
	for _, l := range d.DayLogs {
		if l.PersonID != p.ID || l.Date < cut || l.Absent {
			continue
		}
		if !l.PlanSent && !l.ReportAsked && l.StartAt.IsZero() && l.ReportAt.IsZero() {
			continue
		}
		pt.Days++
		if !l.StartAt.IsZero() {
			pt.Started++
			if hasNudge && minutesOfDay(l.StartAt.In(loc)) < nudge {
				pt.StartBefore++
			}
		}
		if l.ReportAsked {
			pt.Asked++
		}
		if !l.ReportAt.IsZero() {
			pt.Reported++
			pt.ReportMins = append(pt.ReportMins, minutesOfDay(l.ReportAt.In(loc)))
		}
		if l.Chased {
			pt.Chased++
			if l.ReportAt.IsZero() {
				pt.ChasedNoRep++
			}
		}
	}
	since := a.Now().AddDate(0, 0, -learnWindow)
	for _, f := range d.Followups {
		if f.PersonID == p.ID && !f.Plain && f.AskedAt.After(since) && a.Now().Sub(f.AskedAt) > 24*time.Hour {
			pt.FuAsked++
			if f.Status != "open" {
				pt.FuAnswered++
			}
		}
	}
	for _, e := range d.Events {
		if e.PersonID == p.ID && e.At.After(since) {
			switch e.Kind {
			case "slip":
				pt.Slips++
			case "done":
				pt.Done++
			}
		}
	}
	return pt
}

// patternLine: a short description for Claude and the dashboard.
func (a *App) patternLine(d *Data, p *Person) string {
	pt := a.patternOf(d, p)
	if pt.Days == 0 {
		return "هنوز داده‌ای از روزهای کاری‌اش نیست."
	}
	var bits []string
	bits = append(bits, fmt.Sprintf("%s روز کاری در ۲ هفتهٔ اخیر", fa(pt.Days)))
	bits = append(bits, fmt.Sprintf("در %s روز گزارش داد", fa(pt.Reported)))
	if m := pt.medianReport(); m >= 0 {
		bits = append(bits, "معمولاً ساعت "+fa(fmt.Sprintf("%02d:%02d", m/60, m%60))+" گزارش می‌دهد")
	}
	if pt.FuAsked > 0 {
		bits = append(bits, fmt.Sprintf("جواب پیگیری‌ها %s از %s", fa(pt.FuAnswered), fa(pt.FuAsked)))
	}
	if pt.Slips > 0 {
		bits = append(bits, fmt.Sprintf("%s بار موعد عقب رفت، %s کار تمام شد", fa(pt.Slips), fa(pt.Done)))
	}
	for _, t := range d.Tunings {
		if t.PersonID == p.ID && !t.Undone {
			bits = append(bits, "تنظیم خودکار: "+t.Why)
		}
	}
	return strings.Join(bits, " · ")
}

func lastTuning(d *Data, personID, field string) *Tuning {
	for i := len(d.Tunings) - 1; i >= 0; i-- {
		t := d.Tunings[i]
		if t.PersonID == personID && t.Field == field && !t.Undone {
			return t
		}
	}
	return nil
}

func (a *App) tune(d *Data, p *Person, field, val, why string) *Tuning {
	old := map[string]*string{"report": &p.Rhythm.Report, "nudge": &p.Rhythm.Nudge, "noon": &p.Rhythm.Noon}[field]
	if old == nil || *old == val {
		return nil
	}
	t := &Tuning{ID: newID(), PersonID: p.ID, Field: field, Old: *old, New: val, Why: why, At: a.Now()}
	*old = val
	d.Tunings = append(d.Tunings, t)
	d.Log("tune", p.ID, "", field+": "+t.Old+" → "+val+" · "+why)
	return t
}

func (a *App) undoTuning(d *Data, id string) string {
	for _, t := range d.Tunings {
		if t.ID != id || t.Undone {
			continue
		}
		p := d.Person(t.PersonID)
		if p == nil {
			return ""
		}
		f := map[string]*string{"report": &p.Rhythm.Report, "nudge": &p.Rhythm.Nudge, "noon": &p.Rhythm.Noon}[t.Field]
		*f = t.Old
		t.Undone = true
		p.NoTune = true // the manager decided: stop adjusting this person
		d.Log("tune_undo", p.ID, "", t.Field)
		return firstName(p)
	}
	return ""
}

func round15(m int) int { return (m + 7) / 15 * 15 }

func hmStr(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// learn runs once a day before the nightly summary.
func (a *App) learn(d *Data) (tunes []*Tuning, notes []string) {
	for _, p := range d.People {
		if !p.Active || p.ChatID == 0 || p.Role == "admin" {
			continue
		}
		pt := a.patternOf(d, p)
		if !p.NoTune {
			// 1. ask for the report when they really report
			if rep, ok := hm(p.Rhythm.Report); ok && pt.Asked >= 5 && len(pt.ReportMins) >= 5 {
				if med := pt.medianReport(); med >= rep+60 {
					nm := round15(med - 15)
					if nm > 21*60 {
						nm = 21 * 60
					}
					if nm > rep {
						if t := a.tune(d, p, "report", hmStr(nm), "سؤال گزارش به "+fa(hmStr(nm))+" رفت؛ معمولاً حدود "+fa(hmStr(med))+" گزارش می‌دهد"); t != nil {
							tunes = append(tunes, t)
						}
					}
				}
			}
			// 2. no start nudge for people who always start before it; bring it back if that stops
			if _, ok := hm(p.Rhythm.Nudge); ok && pt.Started >= 6 && pt.StartBefore*10 >= pt.Days*8 {
				if t := a.tune(d, p, "nudge", "", "یادآوری شروع خاموش شد؛ تقریباً همیشه خودش زودتر شروع می‌کند"); t != nil {
					tunes = append(tunes, t)
				}
			} else if p.Rhythm.Nudge == "" {
				if lt := lastTuning(d, p.ID, "nudge"); lt != nil && lt.New == "" && pt.Days >= 4 && pt.StartBefore*2 < pt.Days {
					if t := a.tune(d, p, "nudge", lt.Old, "یادآوری شروع دوباره روشن شد؛ این روزها دیرتر شروع می‌کند"); t != nil {
						lt.Undone = true
						tunes = append(tunes, t)
					}
				}
			}
		}
		// things only the manager can decide
		name := firstName(p)
		if pt.Chased >= 3 && pt.ChasedNoRep >= 3 {
			notes = append(notes, fmt.Sprintf("%s در ۲ هفتهٔ اخیر %s بار حتی بعد از یادآوری گزارش نداد. شاید ساعت گزارشش یا شکل کار با او باید عوض شود.", name, fa(pt.ChasedNoRep)))
		}
		if pt.FuAsked >= 3 && pt.FuAnswered*2 < pt.FuAsked {
			notes = append(notes, fmt.Sprintf("%s به %s پیگیری از %s جواب نداده.", name, fa(pt.FuAsked-pt.FuAnswered), fa(pt.FuAsked)))
		}
		if pt.Slips >= 3 && pt.Slips > pt.Done {
			notes = append(notes, fmt.Sprintf("موعدهای %s زیاد عقب می‌رود (%s بار در برابر %s کار تمام‌شده). قول‌هایش را با ضریب در برنامه بگذارید یا کارش را کوچک‌تر کنید.", name, fa(pt.Slips), fa(pt.Done)))
		}
	}
	return
}

// weeklyReview: value and rhythm per person over the last 7 days, only what stands out.
func (a *App) weeklyReview(d *Data) []string {
	var lines []string
	for _, l := range a.teamLoad(d, nil) {
		p := l.P
		if p.ChatID == 0 || p.Role == "finance" {
			continue
		}
		pt := a.patternOf(d, p)
		var bits []string
		if l.Target7 > 0 {
			pct := int(l.Value7 / l.Target7 * 100)
			bits = append(bits, fmt.Sprintf("ارزش %s از %s (%s٪)", fa(roundTo(l.Value7, 0)), fa(roundTo(l.Target7, 0)), fa(pct)))
		} else if l.Value7 > 0 {
			bits = append(bits, "ارزش "+fa(roundTo(l.Value7, 0)))
		}
		if pt.Days > 0 && pt.Reported*10 < pt.Days*7 {
			bits = append(bits, fmt.Sprintf("گزارش در %s روز از %s", fa(pt.Reported), fa(pt.Days)))
		}
		if l.idle() {
			bits = append(bits, "کار آماده ندارد")
		}
		if len(bits) > 0 {
			lines = append(lines, firstName(p)+": "+strings.Join(bits, "، "))
		}
	}
	var fb []string
	since := a.Now().AddDate(0, 0, -7)
	for _, f := range d.Feedback {
		if f.At.After(since) {
			fb = append(fb, "«"+shorten(f.Text, 100)+"» · "+firstName(d.Person(f.PersonID)))
		}
	}
	if len(fb) > 0 {
		lines = append(lines, "نظر بچه‌ها دربارهٔ ربات: "+strings.Join(fb, "؛ "))
	}
	return lines
}

// feedback: what someone said about the bot itself.
func (a *App) feedback(d *Data, p *Person, text string) []Out {
	d.Feedback = append(d.Feedback, &Feedback{PersonID: p.ID, Text: text, At: a.Now()})
	d.Log("feedback", p.ID, "", text)
	outs := relay(notifyAdmins(d, "💬 نظر "+p.Name+" دربارهٔ ربات:\n"+text+replyHint, nil), p.ID)
	// the one thing it can safely fix by itself: fewer messages
	t := normName(text)
	if !p.NoTune && p.Rhythm.Noon != "" && (strings.Contains(t, "زیاد") || strings.Contains(t, "کمتر")) && strings.Contains(t, "پیام") {
		if tu := a.tune(d, p, "noon", "", "پیام ظهر خاموش شد؛ خودش گفت پیام زیاد است"); tu != nil {
			outs = append(outs, notifyAdmins(d, "🧠 "+firstName(p)+": "+tu.Why, [][]Button{{{Text: "برگردان", Data: "tu:" + tu.ID}}})...)
		}
	}
	return outs
}

func isFeedback(text string) (string, bool) {
	t := strings.TrimSpace(text)
	for _, pre := range []string{"نظر:", "نظر :", "/feedback", "بازخورد:"} {
		if strings.HasPrefix(t, pre) {
			return strings.TrimSpace(strings.TrimPrefix(t, pre)), true
		}
	}
	return "", false
}

// tickLearn: once a day at the summary time, and a weekly review on the manager's last working day.
func (a *App) tickLearn(d *Data) []Out {
	mn := a.mgrNow()
	date := dayKey(mn)
	if !inSlot(minutesOfDay(mn), a.Cfg.SummaryTime) || d.Flags["learn:"+date] != "" {
		return nil
	}
	d.Flags["learn:"+date] = "1"
	tunes, notes := a.learn(d)
	var outs []Out
	for _, t := range tunes {
		outs = append(outs, notifyAdmins(d, "🧠 "+firstName(d.Person(t.PersonID))+": "+t.Why, [][]Button{{{Text: "برگردان", Data: "tu:" + t.ID}}})...)
	}
	var body []string
	for _, n := range notes { // the same note at most once a week
		if last := d.Flags["note:"+n]; last == "" || daysBetween(last, date) >= 7 {
			d.Flags["note:"+n] = date
			body = append(body, "• "+n)
		}
	}
	if mn.Weekday() == time.Thursday {
		if wk := a.weeklyReview(d); len(wk) > 0 {
			body = append(body, "\nمرور هفته:")
			for _, l := range wk {
				body = append(body, "• "+l)
			}
		}
	}
	if len(body) > 0 {
		outs = append(outs, notifyAdmins(d, "🧠 چیزهایی که ربات این روزها دید:\n"+strings.Join(body, "\n"), nil)...)
	}
	return outs
}

func daysBetween(a, b string) int {
	x, e1 := time.Parse("2006-01-02", a)
	y, e2 := time.Parse("2006-01-02", b)
	if e1 != nil || e2 != nil {
		return 999
	}
	return int(y.Sub(x).Hours() / 24)
}
