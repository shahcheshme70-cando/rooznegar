package main

import (
	"os"
	"runtime/pprof"
	"testing"
	"time"
)

func TestTickDayNoHang(t *testing.T) {
	defer func(s time.Time) { t.Logf("total %v", time.Since(s)) }(time.Now())
	h := newHarness(t)
	defer h.close()
	a := h.app
	if _, err := seed(a, "deploy/seed.json"); err != nil {
		t.Fatal(err)
	}
	h.at("2026-10-30 21:00")
	code := func(n string) (c string) {
		a.Store.View(func(d *Data) { c = d.PersonByName(n).LinkCode })
		return
	}
	for _, x := range []struct {
		n  string
		c  int64
		ch string
	}{{"مدیر", 900, "telegram"}, {"نیما", 300, "telegram"}, {"احمد طهماسبی", 101, "telegram"}, {"علیرضا حاجی سامی", 201, "bale"}} {
		h.text(x.ch, x.c, code(x.n))
	}
	run := func(label string, f func()) {
		done := make(chan bool)
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			t.Fatalf("hang at %s %s", h.now.Format("Mon 15:04"), label)
		}
	}
	start := h.now
	replied := map[int64]bool{}
	for m := 0; m < 20*60; m += 10 {
		h.now = start.Add(time.Duration(m) * time.Minute)
		t0 := time.Now()
		run("tick", a.Tick)
		if el := time.Since(t0); el > 200*time.Millisecond {
			t.Logf("slow tick %s %v", h.now.Format("Mon 15:04"), el)
		}
		for _, s := range h.tg.take() {
			for _, pfx := range []string{"pl:", "a:", "m:", "n:", "q:", "f:", "r:", "d:"} {
				if d := dataIn(s.Markup, pfx); d != "" {
					run("press "+d, func() { h.press("telegram", s.Chat, d) })
				}
			}
			if !s.Edit && s.Chat != 900 && !replied[s.Chat] && h.now.Hour() >= 16 && s.Markup == "null" {
				replied[s.Chat] = true
				run("reply", func() {
					h.text("telegram", s.Chat, "امروز روی پوستر کار کردم حدود نصفش شد")
				})
			}
		}
	}
}
