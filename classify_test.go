package main

import (
	"strings"
	"testing"
)

func TestNotEverythingIsAReport(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	if _, err := seed(a, "deploy/seed.json"); err != nil {
		t.Fatal(err)
	}
	addUser(a.Store, "moein", "admin", "moein", "password123") // name differs from the person: linked as the only admin
	code := func(n string) (c string) {
		a.Store.View(func(d *Data) { c = d.PersonByName(n).LinkCode })
		return
	}
	const ahmad, boss = 101, 900
	h.at("2026-11-02 11:00")
	h.text("telegram", boss, code("مدیر"))
	h.text("telegram", ahmad, code("احمد طهماسبی"))
	h.tg.take()
	h.cl.parse = func(p string) map[string]any {
		base := map[string]any{"delivered": []any{}, "progress": []any{}, "metrics": []any{}, "blockers": []any{}}
		switch {
		case strings.Contains(p, "ابویحیی"):
			base["kind"], base["call_me"] = "chat", "ابویحیی"
		case strings.Contains(p, "ضعیف"):
			base["kind"], base["bot_feedback"] = "feedback", "کند است و ضعیف عمل می‌کند"
		case strings.Contains(p, "مرخصی"):
			base["kind"] = "question"
		default:
			base["kind"] = "report"
		}
		return base
	}
	h.text("telegram", ahmad, "میتونی ابویحیی صدام کنی")
	ms := h.tg.take()
	if find(ms, ahmad, "باشه ابویحیی") == nil {
		t.Fatalf("nickname:\n%s", dump(ms))
	}
	h.text("telegram", ahmad, "ضعیف عمل کردی، سرعتت کنده")
	ms = h.tg.take()
	if find(ms, boss, "نظر احمد طهماسبی دربارهٔ ربات") == nil {
		t.Fatalf("feedback:\n%s", dump(ms))
	}
	h.text("telegram", ahmad, "فردا می‌تونم مرخصی بگیرم؟")
	if find(h.tg.take(), boss, "❔ احمد طهماسبی") == nil {
		t.Fatal("question not passed to the manager")
	}
	var reports int
	a.Store.View(func(d *Data) { reports = len(d.Reports) })
	if reports != 0 {
		t.Fatalf("%d non-reports stored as reports", reports)
	}
	h.text("telegram", ahmad, "امروز کاور خانه‌تکانی رو زدم")
	a.Store.View(func(d *Data) { reports = len(d.Reports) })
	if reports != 1 {
		t.Fatal("real report not stored")
	}
	// a button answer reaches the manager (the user was linked to the admin person)
	var u *User
	a.Store.View(func(d *Data) { c := *d.UserByLogin("moein"); u = &c })
	if u.PersonID == "" {
		t.Fatal("user not linked to the admin person")
	}
	toolTell(a, u, map[string]any{"person": "احمد", "text": "طرح ۴۲ رسید؟"})
	var fid string
	a.Store.View(func(d *Data) { fid = d.Followups[len(d.Followups)-1].ID })
	h.tg.take()
	h.press("telegram", ahmad, "f:"+fid+":done")
	if find(h.tg.take(), boss, "«طرح ۴۲ رسید؟» → انجام شد") == nil {
		t.Fatal("button answer did not reach the manager")
	}
}
