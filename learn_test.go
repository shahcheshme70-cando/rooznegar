package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLearning(t *testing.T) {
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
	const ahmad, boss = 101, 900
	h.cl.parse = func(string) map[string]any { return map[string]any{} }
	h.at("2026-11-06 21:00")
	h.text("telegram", boss, code("مدیر"))
	h.text("telegram", ahmad, code("احمد طهماسبی"))
	// plenty of work so a plan goes every day
	for i := 0; i < 12; i++ {
		toolAddTask(a, u, map[string]any{"person": "احمد طهماسبی", "title": fmt.Sprintf("طرح %d", i), "notify": false})
	}
	day := time.Date(2026, 11, 7, 0, 0, 0, 0, loadLoc("Asia/Tehran"))
	var learned []sent
	for i := 0; i < 10; i++ {
		d0 := day.AddDate(0, 0, i)
		at := func(hh, mm int) { h.now = d0.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute) }
		at(7, 1)
		a.Tick()
		at(8, 30)
		h.press("telegram", ahmad, "m:start")
		at(16, 31)
		a.Tick()
		at(19, 0+i%3) // reports around 19:00, well after the 16:30 question
		h.text("telegram", ahmad, "امروز روی طرح‌ها کار کردم")
		at(20, 46)
		a.Tick()
		for _, m := range h.tg.take() {
			if m.Chat == boss && strings.Contains(m.Text, "🧠") {
				learned = append(learned, m)
			}
		}
	}
	all := dump(learned)
	if !strings.Contains(all, "سؤال گزارش به ۱۸:۴۵") {
		t.Fatalf("report time not learned:\n%s", all)
	}
	if !strings.Contains(all, "یادآوری شروع خاموش شد") {
		t.Fatalf("nudge not learned:\n%s", all)
	}
	var p Person
	var tu string
	a.Store.View(func(d *Data) {
		p = *d.PersonByName("احمد طهماسبی")
		for _, x := range d.Tunings {
			if x.Field == "report" {
				tu = x.ID
			}
		}
	})
	if p.Rhythm.Report != "18:45" || p.Rhythm.Nudge != "" {
		t.Fatalf("rhythm: %+v", p.Rhythm)
	}
	// manager says no: back, and never again for him
	h.press("telegram", boss, "tu:"+tu)
	a.Store.View(func(d *Data) { p = *d.PersonByName("احمد طهماسبی") })
	if p.Rhythm.Report != "16:30" || !p.NoTune {
		t.Fatalf("undo: %+v %v", p.Rhythm, p.NoTune)
	}
	// feedback, by prefix and inside a report
	h.text("telegram", ahmad, "نظر: پیام‌ها یه کم زیاده")
	ms := h.tg.take()
	if find(ms, boss, "نظر احمد طهماسبی دربارهٔ ربات") == nil || find(ms, ahmad, "ممنون") == nil {
		t.Fatalf("feedback:\n%s", dump(ms))
	}
	// unanswered messages: re-asked after 24h, manager told after 48h
	h.at("2026-11-17 10:00")
	toolTell(a, u, map[string]any{"person": "احمد طهماسبی", "text": "فایل‌های سالن ۸ را بفرست"})
	h.tg.take()
	h.at("2026-11-18 10:01")
	a.Tick()
	if find(h.tg.take(), ahmad, "یادآوری: فایل‌های سالن ۸") == nil {
		t.Fatal("no re-ask")
	}
	h.at("2026-11-19 10:02")
	a.Tick()
	if find(h.tg.take(), boss, "دو بار پرسیده شد و جواب نداد") == nil {
		t.Fatal("no escalation")
	}
	out, _ := toolPersonStatus(a, u, map[string]any{"person": "احمد"})
	if !strings.Contains(out, "الگو:") || !strings.Contains(out, "گزارش می‌دهد") {
		t.Fatalf("pattern line:\n%s", out)
	}
	t.Log(strings.SplitN(out, "\n", 3)[1])
}
