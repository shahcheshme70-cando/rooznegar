package main

import (
	"context"
	"strings"
	"testing"
)

func TestCCCopies(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	if _, err := seed(a, "deploy/seed.json"); err != nil {
		t.Fatal(err)
	}
	addUser(a.Store, "moein", "admin", "مدیر", "password123")
	var u *User
	a.Store.View(func(d *Data) { c := *d.UserByLogin("moein"); c.PersonID = d.PersonByName("مدیر").ID; u = &c })
	code := func(n string) (c string) {
		a.Store.View(func(d *Data) { c = d.PersonByName(n).LinkCode })
		return
	}
	const nima, boss, maral = 300, 900, 500
	h.at("2026-11-02 10:00")
	h.text("telegram", boss, code("مدیر"))
	h.text("telegram", nima, code("نیما"))
	h.text("telegram", maral, code("مارال"))
	h.tg.take()

	msg, err := toolSetCC(a, u, map[string]any{"person": "نیما", "to": "مارال"})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(msg)
	if s, _ := toolPeople(a, u, nil); !strings.Contains(s, "نسخهٔ گزارش‌ها برای") {
		t.Fatalf("list_people does not show cc:\n%s", s)
	}

	// answer to a manager message: copy goes to CC right away
	toolTell(a, u, map[string]any{"person": "نیما", "text": "۲۰۰ میلیون واریز شد؟", "followup": true})
	h.tg.take()
	a.handleUpdate(context.Background(), a.Msgr["telegram"], tgUpdate{Message: &tgMessage{Chat: tgChat{ID: nima}, Text: "هنوز نه، فردا قول داد"}})
	ms := h.tg.take()
	if find(ms, boss, "هنوز نه، فردا قول داد") == nil || find(ms, maral, "هنوز نه، فردا قول داد") == nil {
		t.Fatalf("answer not copied:\n%s", dump(ms))
	}
	if find(ms, maral, "پاسخ") != nil {
		t.Fatalf("copy should not carry the reply hint:\n%s", dump(ms))
	}

	// evening report: copy too
	h.at("2026-11-02 17:01")
	a.Tick()
	h.tg.take()
	h.text("telegram", nima, "امروز با آقای غلامی صحبت کردم")
	ms = h.tg.take()
	if find(ms, maral, "📝 گزارش نیما") == nil {
		t.Fatalf("report not copied:\n%s", dump(ms))
	}

	// task done by the person: copy
	var tid string
	toolAddTask(a, u, map[string]any{"person": "نیما", "title": "پیگیری ۱۶۰۰", "notify": false})
	a.Store.View(func(d *Data) {
		for _, x := range d.Tasks {
			if x.Title == "پیگیری ۱۶۰۰" {
				tid = x.ID
			}
		}
	})
	var outs []Out
	a.Store.Do(func(d *Data) error { outs = a.markDone(d, d.Task(tid), d.PersonByName("نیما").ID); return nil })
	a.flush(outs)
	if find(h.tg.take(), maral, "پیگیری ۱۶۰۰") == nil {
		t.Fatalf("done not copied: %+v", outs)
	}

	// turned off: no copies
	if _, err := toolSetCC(a, u, map[string]any{"person": "نیما", "to": ""}); err != nil {
		t.Fatal(err)
	}
	h.text("telegram", nima, "یک گزارش دیگر")
	if find(h.tg.take(), maral, "یک گزارش دیگر") != nil {
		t.Fatal("copy sent after cc was cleared")
	}
}
