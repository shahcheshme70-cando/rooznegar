package main

import (
	"strings"
	"testing"
)

func TestAutopilot(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	if _, err := seed(a, "deploy/seed.json"); err != nil {
		t.Fatal(err)
	}
	addUser(a.Store, "moein", "admin", "مدیر", "password123")
	var u *User
	a.Store.View(func(d *Data) { c := *d.UserByLogin("moein"); u = &c })
	code := func(n string) (c string) {
		a.Store.View(func(d *Data) { c = d.PersonByName(n).LinkCode })
		return
	}
	const nima, ahmad, boss = 300, 101, 900
	h.at("2026-11-01 21:00") // Sunday night
	h.text("telegram", boss, code("مدیر"))
	h.text("telegram", nima, code("نیما"))
	h.text("telegram", ahmad, code("احمد طهماسبی"))
	h.tg.take()
	call := func(f func(*App, *User, map[string]any) (string, error), args map[string]any) string {
		out, err := f(a, u, args)
		if err != nil {
			t.Fatalf("tool: %v", err)
		}
		return out
	}

	// after hours: the task waits for tomorrow's plan as news
	out := call(toolAddTask, map[string]any{"person": "نیما", "title": "نقشهٔ سه‌بعدی سنندج", "kind": "multi", "value": 120.0})
	if !strings.Contains(out, "صبح در برنامه") {
		t.Fatalf("after-hours add: %s", out)
	}
	if ms := h.tg.take(); len(ms) != 0 {
		t.Fatalf("nothing should be sent at night: %s", dump(ms))
	}

	// next morning 08:00 Nima's plan goes out by itself (no approval)
	h.at("2026-11-02 08:01")
	a.Tick()
	ms := h.tg.take()
	if m := find(ms, nima, "نقشهٔ سه‌بعدی سنندج"); m == nil {
		t.Fatalf("auto plan missing:\n%s", dump(ms))
	}

	// during the day a new task is announced at once, with a load warning when needed
	h.at("2026-11-02 10:00")
	out = call(toolAddTask, map[string]any{"person": "نیما", "title": "لیست خرید لایت‌باکس"})
	if !strings.Contains(out, "همین الان") {
		t.Fatalf("daytime add: %s", out)
	}
	if m := find(h.tg.take(), nima, "کار تازه از مدیر: لیست خرید لایت‌باکس"); m == nil || dataIn(m.Markup, "n:seen") == "" {
		t.Fatal("daytime announce missing")
	}
	h.press("telegram", nima, "n:seen")
	h.tg.take()

	// tell: now, and at a set time
	out = call(toolTell, map[string]any{"person": "نیما", "text": "طرح سقف را تا ظهر برای مرصاد بفرست"})
	if m := find(h.tg.take(), nima, "طرح سقف"); m == nil || dataIn(m.Markup, "f:") == "" || !strings.Contains(out, "فرستاده شد") {
		t.Fatalf("tell now: %s", out)
	}
	out = call(toolTell, map[string]any{"person": "نیما", "text": "یادت نره با سعید تماس بگیری", "at": "15:00", "followup": false})
	if !strings.Contains(out, "یادآوری") {
		t.Fatalf("tell later: %s", out)
	}
	h.at("2026-11-02 14:59")
	a.Tick()
	if find(h.tg.take(), nima, "سعید") != nil {
		t.Fatal("reminder sent early")
	}
	h.at("2026-11-02 15:00")
	a.Tick()
	if find(h.tg.take(), nima, "یادت نره با سعید") == nil {
		t.Fatal("reminder not sent at 15:00")
	}

	// chain: Ahmad's typo done → Nima hears at once
	call(toolAddTask, map[string]any{"person": "احمد طهماسبی", "title": "تایپوی تابلو ۴۰"})
	call(toolAddTask, map[string]any{"person": "نیما", "title": "لی‌اوت تابلو ۴۰", "after": "تایپوی تابلو ۴۰"})
	h.tg.take()
	var typo string
	a.Store.View(func(d *Data) {
		for _, x := range d.Tasks {
			if x.Title == "تایپوی تابلو ۴۰" {
				typo = x.ID
			}
		}
	})
	h.press("telegram", ahmad, "q:s:"+typo+":done")
	ms = h.tg.take()
	if find(ms, nima, "🔗 «لی‌اوت تابلو ۴۰» آماده است") == nil {
		t.Fatalf("handoff not pushed:\n%s", dump(ms))
	}

	// overdue + report chase
	var map3d string
	a.Store.View(func(d *Data) {
		for _, x := range d.Tasks {
			if x.Title == "نقشهٔ سه‌بعدی سنندج" {
				map3d = x.ID
				a.setETA(d, x, "2026-11-01")
			}
		}
	})
	h.at("2026-11-02 17:00")
	a.Tick()
	if find(h.tg.take(), nima, "امروز چه شد") == nil {
		t.Fatal("report question missing")
	}
	h.at("2026-11-02 18:31")
	a.Tick()
	if find(h.tg.take(), nima, "گزارش امروز مانده") == nil {
		t.Fatal("no automatic chase")
	}
	h.at("2026-11-02 18:40")
	a.Tick()
	if ms := h.tg.take(); find(ms, nima, "گزارش امروز مانده") != nil {
		t.Fatal("chased twice")
	}
	h.cl.parse = func(string) map[string]any { return map[string]any{} }
	h.text("telegram", nima, "امروز روی لیست خرید بودم")
	ms = h.tg.take()
	// an overdue multi-day task: the date question comes first (it settles progress too)
	if m := find(ms, nima, "موعدش دیروز بود"); m == nil || dataIn(m.Markup, "q:e:"+map3d) == "" {
		t.Fatalf("overdue question missing:\n%s", dump(ms))
	}
	// a single-day task past its date gets the overdue question
	a.Store.Do(func(d *Data) error {
		for _, x := range d.Tasks {
			if x.Title == "لیست خرید لایت‌باکس" {
				x.ETA = "2026-11-01"
			}
		}
		d.Day(d.PersonByName("نیما").ID, "2026-11-02").Queue = a.overdueQuestions(d, d.PersonByName("نیما"), "2026-11-02")
		return nil
	})
	var nq []Out
	a.Store.Do(func(d *Data) error {
		p := d.PersonByName("نیما")
		nq = a.nextQuestion(d, p, d.Day(p.ID, "2026-11-02"))
		return nil
	})
	if len(nq) == 0 || !strings.Contains(nq[0].Text, "موعدش دیروز بود") {
		t.Fatalf("overdue question: %+v", nq)
	}
	// slipping twice tells the manager
	h.press("telegram", nima, "q:e:"+map3d+":tomorrow")
	h.tg.take()
	h.at("2026-11-03 18:00")
	h.press("telegram", nima, "q:e:"+map3d+":week")
	if find(h.tg.take(), boss, "عقب رفت") == nil {
		t.Fatal("slip not reported to manager")
	}

	// load and value
	call(toolUpdateTask, map[string]any{"task_id": typo, "value": 10.0})
	out = call(toolTeamLoad, nil)
	if !strings.Contains(out, "نیما") || !strings.Contains(out, "⚪") {
		t.Fatalf("team_load:\n%s", out)
	}
	t.Log(out)
}
