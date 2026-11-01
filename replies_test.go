package main

import (
	"context"
	"strings"
	"testing"
)

func TestRepliesGoBackToManager(t *testing.T) {
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
	const nima, boss = 300, 900
	h.at("2026-11-02 10:00")
	h.text("telegram", boss, code("مدیر"))
	h.text("telegram", nima, code("نیما"))
	h.tg.take()
	reply := func(chat int64, text string, to int64) {
		m := &tgMessage{Chat: tgChat{ID: chat}, Text: text}
		if to != 0 {
			m.ReplyTo = &tgMessage{MessageID: to}
		}
		a.handleUpdate(context.Background(), a.Msgr["telegram"], tgUpdate{Message: m})
	}
	// 1. plain message, typed answer without "reply"
	toolTell(a, u, map[string]any{"person": "نیما", "text": "طرح سقف چی شد؟", "followup": false})
	h.tg.take()
	reply(nima, "فردا صبح می‌فرستم", 0)
	ms := h.tg.take()
	fw := find(ms, boss, "💬 نیما در جواب «طرح سقف چی شد؟»")
	if fw == nil || find(ms, nima, "رساندم") == nil {
		t.Fatalf("answer not forwarded:\n%s", dump(ms))
	}
	var reports int
	a.Store.View(func(d *Data) { reports = len(d.Reports) })
	if reports != 0 {
		t.Fatal("answer was stored as a report")
	}
	// 2. manager answers back by replying to the forwarded message
	var fwdID int64
	a.Store.View(func(d *Data) {
		for _, r := range d.Relays {
			if r.Chat == boss {
				fwdID = r.MsgID
			}
		}
	})
	reply(boss, "عالیه، ممنون", fwdID)
	ms = h.tg.take()
	if find(ms, nima, "عالیه، ممنون") == nil || find(ms, boss, "رساندم به نیما") == nil {
		t.Fatalf("manager reply not relayed:\n%s", dump(ms))
	}
	// 3. after the evening question, plain text is the report…
	h.at("2026-11-02 17:01")
	a.Tick()
	h.tg.take()
	h.text("telegram", nima, "امروز روی لیست خرید بودم")
	a.Store.View(func(d *Data) { reports = len(d.Reports) })
	if reports != 1 {
		t.Fatalf("report not stored (%d)", reports)
	}
	// …but a Telegram "reply" to a manager message is still an answer
	toolTell(a, u, map[string]any{"person": "نیما", "text": "فاکتور رو فرستادی؟"})
	var tellMsg int64
	for _, m := range h.tg.take() {
		if strings.Contains(m.Text, "فاکتور رو فرستادی") {
			a.Store.View(func(d *Data) {
				for _, f := range d.Followups {
					if strings.Contains(f.Question, "فاکتور") {
						tellMsg = f.MsgID
					}
				}
			})
		}
	}
	if tellMsg == 0 {
		t.Fatal("message id of the tell not recorded")
	}
	reply(nima, "آره فرستادم", tellMsg)
	if find(h.tg.take(), boss, "در جواب «فاکتور رو فرستادی؟»") == nil {
		t.Fatal("reply-to answer not forwarded")
	}
	var st string
	a.Store.View(func(d *Data) {
		for _, f := range d.Followups {
			if strings.Contains(f.Question, "فاکتور") {
				st = f.Status
			}
		}
	})
	if st != "answered" {
		t.Fatalf("followup status %q", st)
	}
}
