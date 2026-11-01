package main

import (
	"strings"
	"time"
)

// A typed (or voice) answer to something the manager sent goes back to the manager,
// not into the daily report.
//
// It is an answer when:
//   - it is a reply (Telegram/Bale "reply") to that bot message, or
//   - a message from the manager reached this person in the last 12 hours, is still
//     unanswered, and the bot is not waiting for something else (report, a number, a question).
const replyWindow = 12 * time.Hour

// pendingFollowup: the manager's latest message to this person if it may still be getting an
// answer: unanswered and under 12 hours old, or answered in the last 15 minutes (a follow-on message).
func (a *App) pendingFollowup(d *Data, p *Person) *Followup {
	for i := len(d.Followups) - 1; i >= 0; i-- {
		f := d.Followups[i]
		if f.PersonID != p.ID {
			continue
		}
		now := a.Now()
		if f.AnsweredAt == nil && now.Sub(f.AskedAt) < replyWindow {
			return f
		}
		if f.AnsweredAt != nil && now.Sub(*f.AnsweredAt) < 15*time.Minute {
			return f
		}
		return nil // only the latest message counts
	}
	return nil
}

// replyToManager: a Telegram/Bale "reply" to a manager message is always an answer.
func (a *App) replyToManager(pp *Person, msg *tgMessage, text string, voice bool) bool {
	if msg == nil || msg.ReplyTo == nil {
		return false
	}
	var outs []Out
	a.Store.Do(func(d *Data) error {
		p := d.Person(pp.ID)
		if p == nil {
			return nil
		}
		for _, f := range d.Followups {
			if f.PersonID == p.ID && f.MsgID != 0 && f.MsgID == msg.ReplyTo.MessageID {
				outs = a.forwardAnswer(d, p, f, text, voice)
				return nil
			}
		}
		return nil
	})
	a.flush(outs)
	return len(outs) > 0
}

// forwardAnswer sends an answer (text, or a note about files) to whoever asked.
func (a *App) forwardAnswer(d *Data, p *Person, f *Followup, text string, voice bool) []Out {
	var outs []Out
	now := a.Now()
	if f.Answer != "" {
		f.Answer += "\n"
	}
	f.Answer += text
	first := f.AnsweredAt == nil
	if first {
		f.AnsweredAt = &now
	}
	if f.Status == "open" {
		f.Status = "answered"
	}
	d.Log("reply", p.ID, "", shorten(f.Question, 50)+" → "+shorten(text, 120))
	head := "💬 " + p.Name
	if voice {
		head += " (صوتی)"
	}
	body := head + " در جواب «" + shorten(f.Question, 70) + "»:\n" + text
	body += replyHint
	var fw []Out
	if by := d.Person(f.By); by != nil && by.ChatID != 0 && by.ID != p.ID {
		fw = []Out{outTo(by, body, nil)}
	} else {
		fw = notifyAdmins(d, body, nil)
	}
	outs = append(outs, relay(fw, p.ID)...)
	if first {
		ack := "رساندم ✔"
		if strings.Contains(text, "؟") || strings.Contains(text, "?") {
			ack = "رساندم ✔ جوابش همین‌جا می‌آید."
		}
		outs = append(outs, outTo(p, ack, nil))
	}
	return outs
}

// looksLikeAnswer: without Claude, a plain message counts as an answer only if the manager wrote
// in the last 3 hours and the evening report question hasn't come since.
func (a *App) looksLikeAnswer(d *Data, p *Person, f *Followup) bool {
	if f.AnsweredAt != nil {
		return true // a follow-on within 15 minutes
	}
	ds := d.Day(p.ID, a.today(p))
	if ds.Awaiting == "report" && ds.ReportAskAt.After(f.AskedAt) {
		return false
	}
	return a.Now().Sub(f.AskedAt) < 3*time.Hour
}

// relay marks messages to managers so that a "reply" to any of them goes back to personID.
func relay(outs []Out, personID string) []Out {
	for i := range outs {
		chat := outs[i].Chat
		prev := outs[i].Then
		outs[i].Then = func(d *Data, id int64) {
			if prev != nil {
				prev(d, id)
			}
			d.Relays = append(d.Relays, &Relay{Chat: chat, MsgID: id, PersonID: personID, At: time.Now()})
		}
	}
	return outs
}

const replyHint = "\n\n(برای جواب، روی همین پیام «پاسخ» بزنید)"

// managerReply: the manager answers by replying to a relayed message; it goes back to that person.
func (a *App) managerReply(mp *Person, msg *tgMessage, text string) bool {
	if msg == nil || msg.ReplyTo == nil || text == "" {
		return false
	}
	var outs []Out
	handled := false
	a.Store.Do(func(d *Data) error {
		for i := len(d.Relays) - 1; i >= 0; i-- {
			r := d.Relays[i]
			if r.Chat != mp.ChatID || r.MsgID != msg.ReplyTo.MessageID {
				continue
			}
			p := d.Person(r.PersonID)
			if p == nil || p.ChatID == 0 {
				return nil
			}
			o, _ := a.tell(d, p, "💬 "+realFirst(mp)+": "+text, a.Now(), false, "", mp.ID)
			outs = append(outs, o...)
			outs = append(outs, outTo(mp, "رساندم به "+realFirst(p)+" ✔", nil))
			handled = true
			return nil
		}
		return nil
	})
	a.flush(outs)
	return handled
}

// realFirst: the first name, ignoring a nickname (for messages to managers).
func realFirst(p *Person) string {
	if p == nil {
		return ""
	}
	c := *p
	c.Nick = ""
	return firstName(&c)
}
