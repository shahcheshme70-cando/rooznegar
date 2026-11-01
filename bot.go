package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------- polling ----------

func (a *App) poll(ctx context.Context, m *Messenger) {
	for ctx.Err() == nil {
		var off int64
		a.Store.View(func(d *Data) { off = d.Offsets[m.Name] })
		ups, err := m.Updates(ctx, off, 50)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("%s updates: %v", m.Name, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range ups {
			a.dispatch(ctx, m, u) // in order per chat, in parallel across people
			a.Store.Do(func(d *Data) error {
				if u.UpdateID+1 > d.Offsets[m.Name] {
					d.Offsets[m.Name] = u.UpdateID + 1
				}
				return nil
			})
		}
	}
}

func (a *App) handleUpdate(ctx context.Context, m *Messenger, u tgUpdate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in update: %v", r)
		}
	}()
	if u.Callback != nil {
		a.handleCallback(ctx, m, u.Callback)
		return
	}
	if u.Message != nil {
		a.handleMessage(ctx, m, u.Message)
	}
}

// ---------- messages ----------

func (a *App) handleMessage(ctx context.Context, m *Messenger, msg *tgMessage) {
	chat := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}

	// linking: /start CODE (or just the code)
	if strings.HasPrefix(text, "/start") || (len([]rune(latin(text))) == 6 && isDigits(latin(text))) {
		code := strings.TrimSpace(latin(strings.TrimPrefix(text, "/start")))
		a.link(ctx, m, chat, code)
		return
	}

	var p *Person
	a.Store.View(func(d *Data) {
		if x := d.PersonByChat(m.Name, chat); x != nil {
			cp := *x
			p = &cp
		}
	})
	if p == nil {
		m.Send(ctx, chat, "سلام. برای اتصال به روزنگار، کد شش‌رقمی‌ای را که مدیر داده بفرستید.", nil)
		return
	}
	if !p.Active {
		return
	}
	if text == "/help" || text == "راهنما" {
		m.Send(ctx, chat, helpFor(p.Role), nil)
		return
	}
	if isUploadAsk(text) {
		a.offerUpload(ctx, p, "")
		return
	}

	// files: into the files channel if set (no download, no size limit), else onto the server
	var saved []string
	hasFile := msg.Document != nil || len(msg.Photo) > 0 || (msg.Audio != nil && msg.Voice == nil && text != "")
	if hasFile {
		today := a.today(p)
		caption := func(name string) (c string) {
			a.Store.View(func(d *Data) { c = a.fileCaption(d, p, nil, today, name, text) })
			return
		}
		parts := fileParts(msg)
		tg, tgChan := a.Msgr["telegram"], a.Cfg.TelegramFilesChat
		switch {
		case m.Name == "telegram" && tgChan != 0:
			// same messenger: copy, no download, no size limit
			name := ""
			if len(parts) > 0 {
				name = parts[0].FileName
			}
			if id, err := m.Copy(ctx, tgChan, chat, msg.MessageID, caption(name)); err == nil {
				saved = append(saved, fmt.Sprintf("chan:telegram:%d:%d:%s", tgChan, id, name))
			} else {
				log.Printf("copy to files chat: %v", err)
			}
		case tgChan != 0 && tg.Enabled():
			// from Bale: fetch the file, upload it to the Telegram archive, keep nothing on the server
			tmp := filepath.Join(a.Files, ".tmp")
			for _, f := range parts {
				// archive name: 1405-08-13_نام-نفر_نام-اصلی.pdf
				dt, _ := parseDay(today, time.UTC)
				arch := strings.ReplaceAll(jalaliNum(dt), "/", "-") + "_" + strings.ReplaceAll(p.Name, " ", "-") + "_" + safeName(f.FileName)
				path, err := m.Download(ctx, f.FileID, tmp, arch)
				if err != nil {
					log.Printf("download from %s: %v", m.Name, err)
					a.offerUpload(ctx, p, "این فایل برای ربات بزرگ است. از لینک بارگذار بفرست:")
					continue
				}
				id, err := tg.Upload(ctx, tgChan, path, caption(f.FileName))
				os.Remove(path)
				if err != nil {
					log.Printf("upload to telegram archive: %v", err)
					a.offerUpload(ctx, p, "این فایل برای آرشیو تلگرام بزرگ است. از لینک بارگذار بفرست:")
					continue
				}
				saved = append(saved, fmt.Sprintf("chan:telegram:%d:%d:%s", tgChan, id, f.FileName))
			}
		case a.filesChat(m.Name) != 0:
			fc := a.filesChat(m.Name)
			name := ""
			if len(parts) > 0 {
				name = parts[0].FileName
			}
			if id, err := m.Copy(ctx, fc, chat, msg.MessageID, caption(name)); err == nil {
				saved = append(saved, fmt.Sprintf("chan:%s:%d:%d:%s", m.Name, fc, id, name))
			}
		}
		if len(saved) == 0 {
			dayDir := filepath.Join(a.Files, p.ID, a.today(p))
			if msg.Document != nil {
				if path, err := m.Download(ctx, msg.Document.FileID, dayDir, msg.Document.FileName); err == nil {
					saved = append(saved, path)
				} else {
					m.Send(ctx, chat, "فایل را نتوانستم بگیرم: "+err.Error(), nil)
				}
			}
			if len(msg.Photo) > 0 {
				ph := msg.Photo[len(msg.Photo)-1]
				if path, err := m.Download(ctx, ph.FileID, dayDir, "photo-"+time.Now().Format("150405")+".jpg"); err == nil {
					saved = append(saved, path)
				}
			}
		}
	}
	if len(saved) > 0 {
		var outs []Out
		forwarded, hasTasks := false, false
		a.Store.Do(func(d *Data) error {
			x := d.Person(p.ID)
			ds := d.Day(p.ID, a.today(p))
			ds.Files = append(ds.Files, saved...)
			hasTasks = len(d.OpenTasks(p.ID)) > 0
			// files the manager asked for go to the manager
			var f *Followup
			if msg.ReplyTo != nil {
				for _, y := range d.Followups {
					if y.PersonID == p.ID && y.MsgID != 0 && y.MsgID == msg.ReplyTo.MessageID {
						f = y
					}
				}
			} else if y := a.pendingFollowup(d, x); y != nil && (y.AnsweredAt != nil || a.Now().Sub(y.AskedAt) < 3*time.Hour) {
				f = y
			}
			if f != nil && x != nil {
				var names []string
				for _, ref := range saved {
					n := refName(ref)
					if n == "" {
						n = "فایل"
					}
					if ch, c, id, ok := parseChanRef(ref); ok {
						if l := chanLink(ch, c, id); l != "" {
							n += " " + l
						}
					}
					names = append(names, "📎 "+n)
				}
				note := strings.Join(names, "\n")
				if text != "" {
					note += "\n" + text
				}
				outs = a.forwardAnswer(d, x, f, note, false)
				forwarded = true
			}
			return nil
		})
		switch {
		case forwarded:
			a.flush(outs)
			return
		case text == "" && hasTasks:
			m.Send(ctx, chat, fmt.Sprintf("%s فایل رسید و بایگانی شد ✔ بگو مال کدام کار است، یا در گزارش عصر بگو.", fa(len(saved))), nil)
			return
		case text == "":
			m.Send(ctx, chat, fmt.Sprintf("%s فایل رسید و بایگانی شد ✔", fa(len(saved))), nil)
			return
		}
	}

	voice := false
	if msg.Voice != nil || msg.Audio != nil {
		f := msg.Voice
		if f == nil {
			f = msg.Audio
		}
		if !a.STT.Enabled() {
			hint := "صوت را نمی‌توانم بشنوم. لطفاً بنویس؛ یا میکروفون کیبورد گوشی را بزن و حرف بزن تا خودش تایپ کند."
			if m.Name == "bale" {
				hint = "صوت را نمی‌توانم بشنوم. روی همین پیام صوتی «تبدیل به متن» بله را بزن و متنش را برایم بفرست."
			}
			m.Send(ctx, chat, hint, nil)
			return
		}
		path, err := m.Download(ctx, f.FileID, filepath.Join(a.Files, p.ID, "voice"), "voice-"+time.Now().Format("20060102-150405")+".ogg")
		if err != nil {
			m.Send(ctx, chat, "صوت را نتوانستم بگیرم. لطفاً دوباره بفرست یا بنویس.", nil)
			return
		}
		var hint string
		a.Store.View(func(d *Data) { hint = a.sttHint(d, p) })
		tx, err := a.STT.Transcribe(ctx, path, hint)
		if err != nil || tx == "" {
			log.Printf("stt: %v", err)
			m.Send(ctx, chat, "صوت را نفهمیدم. لطفاً بنویس.", nil)
			return
		}
		text, voice = tx, true
	}
	if text == "" {
		return
	}

	if p.Role == "admin" || p.Role == "senior" {
		if a.managerReply(p, msg, text) {
			return
		}
		if a.handleManagerText(ctx, p, text) {
			return
		}
	}
	if a.replyToManager(p, msg, text, voice) {
		return
	}
	if fb, ok := isFeedback(text); ok && fb != "" {
		var outs []Out
		a.Store.Do(func(d *Data) error {
			if x := d.Person(p.ID); x != nil {
				outs = a.feedback(d, x, fb)
			}
			return nil
		})
		outs = append(outs, outTo(p, "ممنون؛ رسید و به مدیر هم گفتم.", nil))
		a.flush(outs)
		return
	}
	a.queueText(p, text, voice)
}

// queueText merges messages sent close together into one report (one Claude call).
func (a *App) queueText(p *Person, text string, voice bool) {
	var awaiting string
	a.Store.View(func(d *Data) { awaiting = d.Day(p.ID, a.today(p)).Awaiting })
	if awaiting == "" || awaiting == "report" {
		if a.fastPath(p, a.today(p), text) { // a number or "نیستم": answer now, no Claude
			a.countSaved()
			return
		}
	}
	if a.Cfg.BatchSeconds <= 0 || awaiting != "report" {
		a.processText(context.Background(), p, text, voice)
		return
	}
	a.pendMu.Lock()
	defer a.pendMu.Unlock()
	pm := a.pend[p.ID]
	if pm == nil {
		pm = &pendingMsg{}
		a.pend[p.ID] = pm
	}
	pm.texts = append(pm.texts, text)
	pm.voice = pm.voice || voice
	if pm.timer != nil {
		pm.timer.Stop()
	}
	pid := p.ID
	pm.timer = time.AfterFunc(time.Duration(a.Cfg.BatchSeconds)*time.Second, func() { a.flushPending(pid) })
}

func (a *App) flushPending(personID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in pending: %v", r)
		}
	}()
	a.pendMu.Lock()
	pm := a.pend[personID]
	delete(a.pend, personID)
	a.pendMu.Unlock()
	if pm == nil || len(pm.texts) == 0 {
		return
	}
	var p *Person
	a.Store.View(func(d *Data) {
		if x := d.Person(personID); x != nil {
			c := *x
			p = &c
		}
	})
	if p != nil {
		a.processText(context.Background(), p, strings.Join(pm.texts, "\n"), pm.voice)
	}
}

// FlushAll processes everything still waiting (shutdown, tests).
func (a *App) FlushAll() {
	a.pendMu.Lock()
	var ids []string
	for id, pm := range a.pend {
		if pm.timer != nil {
			pm.timer.Stop()
		}
		ids = append(ids, id)
	}
	a.pendMu.Unlock()
	for _, id := range ids {
		a.flushPending(id)
	}
}

const helpText = `من «دستیار کندو»ام 🌱
• هر وقت خواستی بگو امروز چه کردی؛ نوشتن یا ویس، فرقی نمی‌کند.
• پیام مدیر که رسید، همین‌جا جواب بده؛ برایش می‌فرستم.
• فایل کار را همین‌جا بفرست تا بایگانی شود. خیلی بزرگ است؟ بنویس «لینک آپلود».
• گیر کردی یا سؤالی داری؟ همین‌جا بنویس.
• نظری دربارهٔ خودم داری؟ با «نظر:» شروع کن.`

const managerHelp = `• جواب بچه‌ها، گره‌ها و سؤال‌هایشان همین‌جا می‌رسد. برای جواب‌دادن، روی پیامشان «پاسخ» بزن و بنویس.
• «خلاصه» را بفرست تا وضعیت امروز بیاید.
• کار و پیام و فایل را از Claude بده.`

func helpFor(role string) string {
	if role == "admin" || role == "senior" {
		return managerHelp
	}
	return helpText
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (a *App) link(ctx context.Context, m *Messenger, chat int64, code string) {
	var name, role string
	var already bool
	a.Store.Do(func(d *Data) error {
		if x := d.PersonByChat(m.Name, chat); x != nil && code == "" {
			already, name = true, firstName(x)
			return nil
		}
		for _, p := range d.People {
			if code != "" && p.LinkCode == code {
				p.Channel, p.ChatID, p.LinkCode = m.Name, chat, ""
				name, role = firstName(p), p.Role
				d.Log("linked", p.ID, "", m.Name)
				return nil
			}
		}
		return nil
	})
	switch {
	case already:
		m.Send(ctx, chat, "سلام "+name+"، وصلی ✔", nil)
	case name != "":
		m.Send(ctx, chat, "سلام "+name+"، خوش آمدی 🌱\n\n"+helpFor(role), nil)
	default:
		m.Send(ctx, chat, "این کد پیدا نشد یا قبلاً استفاده شده. کد تازه را از مدیر بگیر.", nil)
	}
}

func (a *App) sttHint(d *Data, p *Person) string {
	var names []string
	for _, x := range d.People {
		names = append(names, x.Name)
	}
	for _, t := range d.OpenTasks(p.ID) {
		names = append(names, t.Title)
	}
	for _, pr := range d.Projects {
		names = append(names, pr.Name)
	}
	return shorten(strings.Join(names, "، "), 800)
}

// manager text: short commands; anything else is treated as their own report
func (a *App) handleManagerText(ctx context.Context, p *Person, text string) bool {
	t := normName(text)
	if t == "خلاصه" || t == "/summary" {
		var outs []Out
		a.Store.Do(func(d *Data) error {
			txt, _ := a.summaryText(d, p, a.mgrNow())
			outs = append(outs, outTo(p, txt, nil))
			return nil
		})
		a.flush(outs)
		return true
	}
	var open bool
	a.Store.View(func(d *Data) { open = len(d.OpenTasks(p.ID)) > 0 })
	if !open {
		a.flush([]Out{outTo(p, "سلام "+firstName(p)+" 🌱\n"+managerHelp, nil)})
		return true
	}
	return false
}

// processText: a free message from a staff member.
func (a *App) processText(ctx context.Context, p *Person, text string, voice bool) {
	today := a.today(p)
	// awaiting a specific free-text answer?
	var awaiting, awaitTask string
	a.Store.View(func(d *Data) {
		ds := d.Day(p.ID, today)
		awaiting, awaitTask = ds.Awaiting, ds.AwaitTask
		if awaiting != "" && awaiting != "report" && awaiting != "metric" && !ds.AwaitAt.IsZero() && a.Now().Sub(ds.AwaitAt) > 45*time.Minute {
			awaiting = "" // a question from long ago: this message is something new
		}
	})
	if awaiting != "" && awaiting != "report" {
		if a.answerAwaiting(ctx, p, today, awaiting, awaitTask, text) {
			return
		}
	}

	if a.fastPath(p, today, text) {
		a.countSaved()
		return
	}

	var tasks []*Task
	var team []string
	var dcopy *Data
	var awaitingReport bool
	var clarifyQ, lastText string
	var pendingID, pendingQ string
	var heuristicAnswer bool
	a.Store.View(func(d *Data) {
		dsx := d.Day(p.ID, today)
		awaitingReport = dsx.Awaiting == "report"
		if awaiting == "clarify" {
			clarifyQ, lastText = dsx.ClarifyQ, dsx.LastText
		}
		if f := a.pendingFollowup(d, p); f != nil {
			pendingID, pendingQ = f.ID, f.Question
			heuristicAnswer = a.looksLikeAnswer(d, p, f)
		}
		seen := map[string]bool{}
		add := func(t *Task) {
			if !seen[t.ID] && len(tasks) < a.Cfg.MaxContextTasks {
				seen[t.ID] = true
				c := *t
				tasks = append(tasks, &c)
			}
		}
		for _, t := range a.todaysTasks(d, p, today) { // today's plan first
			if t.Status == "open" {
				add(t)
			}
		}
		for _, t := range d.OpenTasks(p.ID) {
			add(t)
		}
		ds := d.Day(p.ID, today)
		for _, id := range ds.LastDone { // allow corrections inside the edit window
			if t := d.Task(id); t != nil && t.Status == "done" && a.Now().Sub(ds.LastReport) < time.Duration(a.Cfg.EditWindowMin)*time.Minute {
				c := *t
				tasks = append(tasks, &c)
			}
		}
		for _, x := range d.People {
			if x.ID != p.ID && x.Active {
				team = append(team, firstName(x))
			}
		}
		dcopy = &Data{Projects: append([]*Project(nil), d.Projects...)}
	})

	var parsed *ParsedReport
	if !a.Claude.Enabled() {
		a.countSaved()
	} else {
		ctxLine := faDate(a.localNow(p))
		if awaitingReport {
			ctxLine += " · ربات همین الان سؤال گزارش عصر را پرسیده"
		}
		if clarifyQ != "" {
			ctxLine += "\nاین جواب به سؤال روشن‌کنندهٔ ربات است: «" + clarifyQ + "» دربارهٔ گزارش قبلی‌اش: «" + shorten(lastText, 300) + "». kind=report و آن را با گزارش قبلی بفهم."
		}
		c2, cancel := context.WithTimeout(ctx, 50*time.Second)
		pr, err := a.Claude.ParseReport(c2, p.Name, team, tasks, dcopy, ctxLine, pendingQ, text)
		cancel()
		if err != nil {
			log.Printf("parse: %v", err)
		} else {
			parsed = pr
		}
	}

	isAnswer := pendingID != "" && ((parsed != nil && parsed.Kind == "answer") || (parsed == nil && heuristicAnswer))
	var outs []Out
	a.Store.Do(func(d *Data) error {
		if isAnswer {
			if f := d.Followup(pendingID); f != nil {
				if x := d.Person(p.ID); x != nil {
					outs = a.forwardAnswer(d, x, f, text, voice)
					return nil
				}
			}
		}
		outs = a.applyReport(d, p, today, text, voice, parsed)
		return nil
	})
	a.flush(outs)
}

func (a *App) applyReport(d *Data, pp *Person, today, text string, voice bool, pr *ParsedReport) []Out {
	p := d.Person(pp.ID)
	if p == nil {
		return nil
	}
	ds := d.Day(p.ID, today)
	if pr != nil && pr.Kind != "" && pr.Kind != "report" && pr.Kind != "answer" {
		return a.notReport(d, p, text, pr)
	}
	if pr != nil && strings.TrimSpace(pr.CallMe) != "" {
		p.Nick = strings.TrimSpace(pr.CallMe)
	}
	raw, _ := json.Marshal(pr)
	d.Reports = append(d.Reports, &Report{ID: newID(), PersonID: p.ID, Date: today, Text: text, Voice: voice, Parsed: raw, At: a.Now()})
	ds.Reported = true
	ds.Awaiting = ""
	ds.Started = true
	a.stamp(ds, true, true)
	loc := loadLoc(p.TZ)
	todayT, _ := parseDay(today, loc)

	var lines []string
	var outs []Out
	covered := map[string]bool{}
	var doneNow, needETA []string

	if voice {
		lines = append(lines, "شنیدم: «"+shorten(text, 220)+"»")
	}
	if pr != nil {
		if pr.Absent {
			ds.Absent = true
		}
		inEdit := a.Now().Sub(ds.LastReport) < time.Duration(a.Cfg.EditWindowMin)*time.Minute
		for _, x := range pr.Delivered {
			t := d.Task(x.TaskID)
			if t == nil || t.PersonID != p.ID {
				continue
			}
			covered[t.ID] = true
			if t.Status == "done" {
				continue
			}
			outs = append(outs, a.markDone(d, t, p.ID)...)
			doneNow = append(doneNow, t.ID)
			// attach today's files to what was delivered
			outs = append(outs, a.attachFiles(d, p, ds, t, today)...)
			l := "✔ " + t.Title + "، تحویل شد."
			if len(t.Files) > 0 {
				l += " فایل ثبت شد."
			}
			if nx := nextOf(d, t); len(nx) > 0 {
				l += " به " + strings.Join(nx, " و ") + " خبر دادم."
			}
			lines = append(lines, l)
		}
		for _, x := range pr.Progress {
			t := d.Task(x.TaskID)
			if t == nil || t.PersonID != p.ID {
				continue
			}
			covered[t.ID] = true
			if t.Status == "done" && inEdit {
				a.reopen(d, t)
			}
			if t.Status != "open" {
				continue
			}
			changed := t.Progress != x.Level
			t.Progress, t.UpdatedAt = x.Level, a.Now()
			l := "▶ " + t.Title + ": " + progressLabels[x.Level]
			if x.ETA != "" {
				a.setETA(d, t, etaFromChoice(x.ETA, todayT))
			} else if changed || t.ETA == "" {
				needETA = append(needETA, t.ID)
			}
			if t.ETA != "" {
				l += " · موعد: " + relDay(t.ETA, today)
			}
			lines = append(lines, l)
		}
		for _, x := range pr.Metrics {
			t := d.Task(x.TaskID)
			if t == nil || t.PersonID != p.ID || t.Kind != "metric" {
				continue
			}
			covered[t.ID] = true
			outs = append(outs, a.addMetric(d, t, today, x.Value, x.Note)...)
			l := fmt.Sprintf("📏 %s: %s %s", t.Title, fa(x.Value), t.Unit)
			if t.DailyTarget > 0 {
				l += fmt.Sprintf(" از %s", fa(t.DailyTarget))
				if diff := t.DailyTarget - x.Value; diff > 0 {
					l += fmt.Sprintf("، %s کمتر از هدف", fa(diff))
				}
			}
			if t.Total > 0 {
				l += fmt.Sprintf(" · جمع %s از %s", fa(t.DoneQty), fa(t.Total))
			}
			lines = append(lines, l)
		}
		for _, x := range pr.Blockers {
			b := &Blocker{ID: newID(), PersonID: p.ID, TaskID: x.TaskID, Text: x.Text, Status: "open", At: a.Now()}
			d.Blockers = append(d.Blockers, b)
			d.Log("blocker", p.ID, x.TaskID, x.Text)
			lines = append(lines, "⚠ گره: "+x.Text)
			if x.Red {
				tt := ""
				if t := d.Task(x.TaskID); t != nil {
					tt = " (" + t.Title + ")"
				}
				outs = append(outs, relay(notifyAdmins(d, "🔴 گره از "+p.Name+tt+":\n"+x.Text+replyHint, nil), p.ID)...)
			}
		}
		for _, o := range pr.Other {
			lines = append(lines, "• "+o)
			d.Log("other", p.ID, "", o)
		}
	} else {
		d.Log("report_raw", p.ID, "", shorten(text, 200))
	}

	if len(doneNow) > 0 {
		ds.LastDone = doneNow
	}
	ds.LastReport = a.Now()
	ds.LastText = text

	// questions for what the message didn't cover (only in the evening, or when nothing was understood)
	var keep []Question
	for _, q := range ds.Queue {
		if q.Kind == "overdue" {
			keep = append(keep, q)
		}
	}
	ds.Queue = keep
	if pr != nil && strings.TrimSpace(pr.BotFeedback) != "" {
		outs = append(outs, a.feedback(d, p, strings.TrimSpace(pr.BotFeedback))...)
	}
	if pr != nil && pr.Clarify != "" {
		ds.Queue = append(ds.Queue, Question{Kind: "clarify", Text: pr.Clarify})
	}
	for _, id := range needETA {
		ds.Queue = append(ds.Queue, Question{Kind: "eta", TaskID: id})
	}
	evening := ds.ReportAsked || pr == nil
	if evening && !ds.Absent {
		for _, t := range a.todaysTasks(d, p, today) {
			if covered[t.ID] || t.Status != "open" {
				continue
			}
			switch t.Kind {
			case "metric":
				ds.Queue = append(ds.Queue, Question{Kind: "metric", TaskID: t.ID})
			case "multi":
				ds.Queue = append(ds.Queue, Question{Kind: "progress", TaskID: t.ID})
			default:
				ds.Queue = append(ds.Queue, Question{Kind: "single", TaskID: t.ID})
			}
		}
	}

	head := "ثبت شد"
	if len(lines) == 0 {
		head = "ثبت شد، ممنون 🌱"
	} else {
		head += ":\n" + strings.Join(lines, "\n")
	}
	outs = append(outs, outTo(p, head, nil))
	if a.Cfg.ArchiveReports && a.Cfg.TelegramFilesChat != 0 && a.Msgr["telegram"].Enabled() {
		arch := "📝 #گزارش · " + hashtag(p.Name)
		if p.Line != "" {
			arch += " · " + hashtag("خط "+p.Line)
		}
		seenP := map[string]bool{}
		for _, id := range append(append([]string{}, doneNow...), keys(covered)...) {
			if t := d.Task(id); t != nil {
				if pr := projName(d, t.ProjectID); pr != "" && !seenP[pr] {
					seenP[pr] = true
					arch += " · " + hashtag(pr)
				}
			}
		}
		if dt, ok := parseDay(today, time.UTC); ok {
			y, mo, _ := toJalali(dt.Year(), int(dt.Month()), dt.Day())
			arch += "\n📅 " + faDateYear(dt) + " · " + hashtag(jMonths[mo]+" "+fa(y))
		}
		if voice {
			arch += " · 🎙"
		}
		arch += "\n\n" + shorten(text, 3000)
		if len(lines) > 0 {
			arch += "\n\n— ثبت شد —\n" + strings.Join(lines, "\n")
		}
		outs = append(outs, Out{Channel: "telegram", Chat: a.Cfg.TelegramFilesChat, Text: arch})
	}
	outs = append(outs, a.questionsOnly(d, p, ds)...) // the head above already thanked them
	return outs
}

// tasks on today's plan, plus anything in progress
func (a *App) todaysTasks(d *Data, p *Person, today string) []*Task {
	seen := map[string]bool{}
	var out []*Task
	if pl := d.PlanFor(p.ID, today); pl != nil {
		for _, id := range pl.TaskIDs {
			if t := d.Task(id); t != nil && !seen[id] {
				seen[id] = true
				out = append(out, t)
			}
		}
	}
	for _, t := range d.OpenTasks(p.ID) {
		if seen[t.ID] {
			continue
		}
		if t.Kind == "metric" || (t.Progress != "" && t.Progress != "done") {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	return out
}

// nextQuestion pops the queue and asks one thing.
func (a *App) nextQuestion(d *Data, p *Person, ds *DayState) []Out {
	for len(ds.Queue) > 0 {
		q := ds.Queue[0]
		ds.Queue = ds.Queue[1:]
		var t *Task
		if q.TaskID != "" {
			t = d.Task(q.TaskID)
			if t == nil || (t.Status != "open" && q.Kind != "eta") {
				continue
			}
		}
		switch q.Kind {
		case "clarify":
			ds.Awaiting, ds.ClarifyQ = "clarify", q.Text
			ds.AwaitAt = a.Now()
			return []Out{outTo(p, "یک سؤال: "+q.Text, nil)}
		case "progress":
			return []Out{outTo(p, t.Title+" به نظرت چقدر پیش رفت؟", [][]Button{
				{{Text: "تازه شروع", Data: "q:p:" + t.ID + ":start"}, {Text: "حدود نصف", Data: "q:p:" + t.ID + ":half"}},
				{{Text: "نزدیک تمام", Data: "q:p:" + t.ID + ":near"}, {Text: "تمام شد", Data: "q:p:" + t.ID + ":done"}},
				{{Text: "امروز رویش نبودم", Data: "q:p:" + t.ID + ":none"}},
			})}
		case "overdue":
			if t.ETA == "" || t.ETA >= ds.Date {
				continue // already moved or answered
			}
			return []Out{outTo(p, "«"+t.Title+"» موعدش "+relDay(t.ETA, ds.Date)+" بود. کی تمام می‌شود؟", [][]Button{
				{{Text: "تمام شد", Data: "q:p:" + t.ID + ":done"}, {Text: "فردا", Data: "q:e:" + t.ID + ":tomorrow"}},
				{{Text: "پس‌فردا", Data: "q:e:" + t.ID + ":dayafter"}, {Text: "این هفته", Data: "q:e:" + t.ID + ":week"}, {Text: "هفتهٔ بعد", Data: "q:e:" + t.ID + ":nextweek"}},
			})}
		case "eta":
			return []Out{outTo(p, t.Title+": کی تمام می‌شود؟", [][]Button{
				{{Text: "فردا", Data: "q:e:" + t.ID + ":tomorrow"}, {Text: "پس‌فردا", Data: "q:e:" + t.ID + ":dayafter"}},
				{{Text: "این هفته", Data: "q:e:" + t.ID + ":week"}, {Text: "هفتهٔ بعد", Data: "q:e:" + t.ID + ":nextweek"}},
			})}
		case "single":
			return []Out{outTo(p, t.Title+" تمام شد؟", [][]Button{
				{{Text: "تمام شد", Data: "q:s:" + t.ID + ":done"}, {Text: "ادامه دارد", Data: "q:s:" + t.ID + ":cont"}, {Text: "شروع نشد", Data: "q:s:" + t.ID + ":none"}},
			})}
		case "metric":
			ds.Awaiting, ds.AwaitTask = "metric", t.ID
			ds.AwaitAt = a.Now()
			return []Out{outTo(p, fmt.Sprintf("%s: امروز چند %s؟ (فقط عدد)", t.Title, t.Unit), nil)}
		}
	}
	if ds.Reported {
		return []Out{outTo(p, "خسته نباشی. ثبت شد.", nil)}
	}
	return nil
}

func (a *App) answerAwaiting(ctx context.Context, p *Person, today, awaiting, taskID, text string) bool {
	var outs []Out
	handled := true
	a.Store.Do(func(d *Data) error {
		ds := d.Day(p.ID, today)
		switch {
		case awaiting == "metric":
			v, ok := parseNum(text)
			t := d.Task(taskID)
			if !ok || t == nil {
				handled = false // not a number: treat as a normal report
				ds.Awaiting = ""
				return nil
			}
			ds.Awaiting, ds.AwaitTask = "", ""
			outs = append(outs, a.addMetric(d, t, today, v, "")...)
			l := fmt.Sprintf("📏 %s %s", fa(v), t.Unit)
			if t.DailyTarget > 0 {
				l += " از " + fa(t.DailyTarget)
			}
			outs = append(outs, outTo(p, "ثبت شد: "+l, nil))
			outs = append(outs, a.nextQuestion(d, p, ds)...)
		case awaiting == "blocker_text":
			ds.Awaiting, ds.AwaitTask = "", ""
			b := &Blocker{ID: newID(), PersonID: p.ID, TaskID: taskID, Text: text, Status: "open", At: a.Now()}
			d.Blockers = append(d.Blockers, b)
			d.Log("blocker", p.ID, taskID, text)
			tt := ""
			if t := d.Task(taskID); t != nil {
				tt = " (" + t.Title + ")"
			}
			outs = append(outs, relay(notifyAdmins(d, "🔴 گره از "+p.Name+tt+":\n"+text+replyHint, nil), p.ID)...)
			outs = append(outs, outTo(p, "به مدیر رساندم. تا جواب بیاید، اگر کار دیگری از برنامه آماده است سراغ آن برو.", nil))
		case awaiting == "question":
			ds.Awaiting = ""
			outs = append(outs, relay(notifyAdmins(d, "❔ سؤال "+p.Name+":\n"+text+replyHint, nil), p.ID)...)
			outs = append(outs, outTo(p, "سؤالت به مدیر رسید.", nil))
		case strings.HasPrefix(awaiting, "why:"):
			ds.Awaiting = ""
			if ap := d.Approval(strings.TrimPrefix(awaiting, "why:")); ap != nil {
				ap.Status = "pending"
				ap.Detail += "\nتوضیح " + firstName(p) + ": " + text
				outs = append(outs, notifyAdmins(d, "پاسخ "+p.Name+" دربارهٔ «"+ap.Title+"»:\n"+text, approvalRows(ap))...)
			}
			outs = append(outs, outTo(p, "توضیحت به مدیر رسید.", nil))
		case strings.HasPrefix(awaiting, "fstuck:"):
			ds.Awaiting = ""
			if f := d.Followup(strings.TrimPrefix(awaiting, "fstuck:")); f != nil {
				f.Answer = text
				if by := d.Person(f.By); by != nil && by.ChatID != 0 {
					outs = append(outs, relay([]Out{outTo(by, "🔴 "+p.Name+" دربارهٔ «"+shorten(f.Question, 60)+"» گیر دارد:\n"+text+replyHint, nil)}, p.ID)...)
				} else {
					outs = append(outs, relay(notifyAdmins(d, "🔴 "+p.Name+" دربارهٔ «"+shorten(f.Question, 60)+"» گیر دارد:\n"+text+replyHint, nil), p.ID)...)
				}
			}
			outs = append(outs, outTo(p, "رساندم. ممنون.", nil))
		case awaiting == "clarify":
			ds.Awaiting = ""
			handled = false // the answer is parsed as a normal report
		default:
			ds.Awaiting = ""
			handled = false
		}
		return nil
	})
	a.flush(outs)
	return handled
}

// ---------- buttons ----------

func (a *App) handleCallback(ctx context.Context, m *Messenger, cb *tgCallback) {
	if cb.Message == nil {
		return
	}
	chat := cb.Message.Chat.ID
	var p *Person
	a.Store.View(func(d *Data) {
		if x := d.PersonByChat(m.Name, chat); x != nil {
			c := *x
			p = &c
		}
	})
	if p == nil {
		m.Answer(ctx, cb.ID, "اول ربات را وصل کنید.")
		return
	}
	parts := strings.Split(cb.Data, ":")
	if parts[0] == "u" && len(parts) == 2 { // which task gets the Bargozar link
		m.Answer(ctx, cb.ID, "")
		a.flush([]Out{{Channel: m.Name, Chat: chat, EditID: cb.Message.MessageID, Text: cb.Message.Text + "\n\n— در حال ساختن لینک…"}})
		a.makeUploadLink(ctx, p, parts[1])
		return
	}
	today := a.today(p)
	var outs []Out
	ack := ""
	editText := "" // replace the tapped message with this (buttons removed)
	orig := cb.Message.Text

	a.Store.Do(func(d *Data) error {
		ds := d.Day(p.ID, today)
		pl := d.PlanFor(p.ID, today)
		first := func() *Task {
			if pl == nil {
				return nil
			}
			for _, id := range pl.TaskIDs {
				if t := d.Task(id); t != nil && t.Status == "open" {
					return t
				}
			}
			return nil
		}
		switch parts[0] {
		case "m", "n": // morning plan / nudge
			if len(parts) < 2 {
				return nil
			}
			switch parts[1] {
			case "start":
				ds.Started = true
				a.stamp(ds, true, false)
				editText = orig + "\n\n✔ شروع کردم"
				d.Log("started", p.ID, "", "")
				ack = "موفق باشی"
			case "absent":
				ds.Absent = true
				editText = orig + "\n\n— امروز نیستم"
				d.Log("absent", p.ID, "", "")
				outs = append(outs, outTo(p, "باشه. امروز چیزی نمی‌پرسم.", nil))
			case "ask":
				ds.Awaiting = "question"
				ds.AwaitAt = a.Now()
				outs = append(outs, outTo(p, "سؤالت را بنویس تا به مدیر برسانم.", nil))
			case "seen":
				ds.Started = true
				a.stamp(ds, true, false)
				editText = orig + "\n\n✔ دیدم"
				ack = "👍"
			case "later":
				editText = orig + "\n\n— تا نیم ساعت دیگه"
				outs = append(outs, outTo(p, "قبول. اگر چیزی کم بود همین‌جا بنویس.", nil))
			case "stuck":
				ds.Awaiting = "blocker_text"
				ds.AwaitAt = a.Now()
				if t := first(); t != nil {
					ds.AwaitTask = t.ID
				}
				editText = orig + "\n\n— گیر دارم"
				outs = append(outs, outTo(p, "چه چیزی کم است؟ یک خط بنویس.", nil))
			}
		case "q":
			if len(parts) < 4 {
				return nil
			}
			t := d.Task(parts[2])
			if t == nil || t.PersonID != p.ID {
				return nil
			}
			loc := loadLoc(p.TZ)
			todayT, _ := parseDay(today, loc)
			switch parts[1] {
			case "p":
				lvl := parts[3]
				switch lvl {
				case "done":
					outs = append(outs, a.markDone(d, t, p.ID)...)
					editText = t.Title + ": تمام شد ✔"
					if nx := nextOf(d, t); len(nx) > 0 {
						editText += " (به " + strings.Join(nx, " و ") + " خبر دادم)"
					}
				case "none":
					editText = t.Title + ": امروز رویش نبودم"
				default:
					changed := t.Progress != lvl
					t.Progress, t.UpdatedAt = lvl, a.Now()
					editText = t.Title + ": " + progressLabels[lvl]
					if changed || t.ETA == "" {
						ds.Queue = append([]Question{{Kind: "eta", TaskID: t.ID}}, ds.Queue...)
					}
				}
			case "e":
				eta := etaFromChoice(parts[3], todayT)
				slipped := a.setETA(d, t, eta)
				editText = "کی تمام می‌شود؟ " + relDay(eta, today)
				if slipped && slipCount(t) >= 2 {
					d.Log("slip2", p.ID, t.ID, t.Title)
					key := "slip:" + t.ID + ":" + fmt.Sprint(slipCount(t))
					if d.Flags[key] == "" {
						d.Flags[key] = "1"
						outs = append(outs, notifyAdmins(d, fmt.Sprintf("⏳ «%s» (%s) برای بار %s عقب رفت؛ موعد تازه: %s", t.Title, firstName(p), fa(slipCount(t)), relDay(eta, today)), nil)...)
					}
				}
			case "s":
				switch parts[3] {
				case "done":
					outs = append(outs, a.markDone(d, t, p.ID)...)
					editText = t.Title + ": تمام شد ✔"
					outs = append(outs, a.attachFiles(d, p, ds, t, today)...)
				case "cont":
					if t.Kind == "single" {
						t.Kind = "multi"
					}
					editText = t.Title + ": ادامه دارد"
					ds.Queue = append([]Question{{Kind: "progress", TaskID: t.ID}}, ds.Queue...)
				case "none":
					editText = t.Title + ": امروز شروع نشد"
				}
			}
			outs = append(outs, a.nextQuestion(d, p, ds)...)
		case "f": // followup answer
			if len(parts) < 3 {
				return nil
			}
			f := d.Followup(parts[1])
			if f == nil || f.PersonID != p.ID {
				return nil
			}
			now := a.Now()
			f.Status, f.AnsweredAt = parts[2], &now
			lbl := map[string]string{"done": "انجام شد", "doing": "در جریان", "stuck": "گیر دارم"}[parts[2]]
			editText = orig + "\n\n— " + lbl
			d.Log("followup", p.ID, "", f.Question+" → "+lbl)
			if parts[2] == "stuck" {
				ds.Awaiting = "fstuck:" + f.ID
				ds.AwaitAt = a.Now()
				outs = append(outs, outTo(p, "چه چیزی کم است؟", nil))
			} else {
				msg := fmt.Sprintf("%s: «%s» → %s", p.Name, shorten(f.Question, 80), lbl)
				if by := d.Person(f.By); by != nil && by.ChatID != 0 {
					outs = append(outs, relay([]Out{outTo(by, msg, nil)}, p.ID)...)
				} else {
					outs = append(outs, relay(notifyAdmins(d, msg, nil), p.ID)...)
				}
			}
		case "a": // approval (admins only)
			if p.Role != "admin" || len(parts) < 3 {
				ack = "فقط مدیر"
				return nil
			}
			if ap := d.Approval(parts[1]); ap != nil {
				outs = append(outs, a.decideApproval(d, ap, parts[2], p.ID)...)
				editText = orig + "\n\n— " + map[string]string{"ok": "تأیید شد", "no": "رد شد", "ask": "پرسیده شد"}[parts[2]]
			}
		case "tu": // undo a tuning the bot made
			if p.Role != "admin" || len(parts) < 2 {
				ack = "فقط مدیر"
				return nil
			}
			if who := a.undoTuning(d, parts[1]); who != "" {
				editText = orig + "\n\n— برگشت. از این به بعد ساعت‌های " + who + " را خودم عوض نمی‌کنم."
			}
		case "pl": // plan approval
			if p.Role != "admin" || len(parts) < 3 {
				ack = "فقط مدیر"
				return nil
			}
			if pl := d.Plan(parts[1]); pl != nil {
				outs = append(outs, a.approvePlan(d, pl, p.ID)...)
				editText = orig + "\n\n✔ تأیید شد"
			}
		case "d": // draft send/cancel
			if (p.Role != "admin" && p.Role != "senior") || len(parts) < 3 {
				return nil
			}
			dr := d.Draft(parts[1])
			if dr == nil {
				return nil
			}
			if parts[2] == "send" {
				o, err := a.sendDraft(d, dr, p.ID)
				if err != nil {
					ack = err.Error()
					return nil
				}
				outs = append(outs, o...)
				editText = orig + "\n\n✔ فرستاده شد"
			} else {
				dr.Status = "cancelled"
				editText = orig + "\n\n— لغو شد"
			}
		case "r": // remind someone who hasn't reported
			if p.Role != "admin" && p.Role != "senior" {
				return nil
			}
			if x := d.Person(parts[1]); x != nil && x.ChatID != 0 {
				outs = append(outs, outTo(x, "سلام "+firstName(x)+"، گزارش امروز مانده. هر طور راحتی بنویس یا صوتی بفرست.", nil))
				ack = "یادآوری رفت"
				xd := d.Day(x.ID, a.today(x))
				xd.ReportAsked = true
			}
		}
		return nil
	})
	m.Answer(ctx, cb.ID, ack)
	if editText != "" {
		outs = append([]Out{{Channel: m.Name, Chat: chat, EditID: cb.Message.MessageID, Text: editText}}, outs...)
	}
	a.flush(outs)
}

// fastPath handles messages that need no Claude call.
func (a *App) fastPath(p *Person, today, text string) bool {
	t := normName(latin(text))
	t = strings.Trim(t, " .!،")
	absent := t == "نیستم" || t == "امروز نیستم" || t == "مرخصی" || t == "امروز مرخصی هستم" || t == "امروز نمیام" || t == "امروز نمیایم"
	v, isNum := parseNum(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(t, "متر"), "تا")))
	if !absent && !isNum {
		return false
	}
	var outs []Out
	done := false
	a.Store.Do(func(d *Data) error {
		ds := d.Day(p.ID, today)
		if absent {
			ds.Absent, ds.Awaiting = true, ""
			d.Log("absent", p.ID, "", "")
			outs = append(outs, outTo(p, "باشه. امروز چیزی نمی‌پرسم.", nil))
			done = true
			return nil
		}
		var metric []*Task
		for _, x := range d.OpenTasks(p.ID) {
			if x.Kind == "metric" {
				metric = append(metric, x)
			}
		}
		if len(metric) != 1 {
			return nil
		}
		mt := metric[0]
		outs = append(outs, a.addMetric(d, mt, today, v, "")...)
		ds.Reported, ds.Awaiting, ds.Started, ds.LastReport = true, "", true, a.Now()
		a.stamp(ds, true, true)
		d.Reports = append(d.Reports, &Report{ID: newID(), PersonID: p.ID, Date: today, Text: text, At: a.Now()})
		l := fmt.Sprintf("ثبت شد: 📏 %s: %s %s", mt.Title, fa(v), mt.Unit)
		if mt.DailyTarget > 0 {
			l += " از " + fa(mt.DailyTarget)
			if diff := mt.DailyTarget - v; diff > 0 {
				l += "، " + fa(diff) + " کمتر از هدف"
			}
		}
		if mt.Total > 0 {
			l += fmt.Sprintf(" · جمع %s از %s", fa(mt.DoneQty), fa(mt.Total))
		}
		outs = append(outs, outTo(p, l+"\nاگر گره یا مشکلی هست، همین‌جا بنویس.", nil))
		done = true
		return nil
	})
	a.flush(outs)
	return done
}

// fileParts: the document and/or the largest photo of a message
func fileParts(msg *tgMessage) []tgFile {
	var out []tgFile
	if msg.Document != nil {
		out = append(out, *msg.Document)
	}
	if len(msg.Photo) > 0 {
		ph := msg.Photo[len(msg.Photo)-1]
		if ph.FileName == "" {
			ph.FileName = "photo-" + time.Now().Format("150405") + ".jpg"
		}
		out = append(out, ph)
	}
	return out
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isUploadAsk(text string) bool {
	t := normName(text)
	for _, k := range []string{"/upload", "لینک اپلود", "لینک بارگذار", "لینک بارگزار", "فایل بزرگ", "فایل بزرگ دارم", "اپلود"} {
		if t == normName(k) {
			return true
		}
	}
	return false
}

// notReport: chat, feedback or a question for the manager — answered, not counted as a report.
func (a *App) notReport(d *Data, p *Person, text string, pr *ParsedReport) []Out {
	var outs []Out
	ack := ""
	if n := strings.TrimSpace(pr.CallMe); n != "" {
		p.Nick = n
		d.Log("nick", p.ID, "", n)
		ack = "باشه " + n + "، از این به بعد همین‌طور صدات می‌کنم."
	}
	switch pr.Kind {
	case "feedback":
		fb := strings.TrimSpace(pr.BotFeedback)
		if fb == "" {
			fb = text
		}
		outs = append(outs, a.feedback(d, p, fb)...)
		if ack == "" {
			ack = "ممنون که گفتی؛ رسید و درستش می‌کنیم."
		}
	case "question":
		outs = append(outs, relay(notifyAdmins(d, "❔ "+p.Name+":\n"+text+replyHint, nil), p.ID)...)
		d.Log("question", p.ID, "", text)
		if ack == "" {
			ack = "به مدیر رساندم؛ جوابش همین‌جا می‌آید."
		}
	default: // chat
		d.Log("chat", p.ID, "", shorten(text, 120))
		if ack == "" {
			ack = "سلام " + firstName(p) + " 🌱 من این‌جام برای کارها: هر وقت خواستی بگو امروز چه کردی، یا اگر چیزی لازم داری بنویس."
		}
	}
	return append(outs, outTo(p, ack, nil))
}

// questionsOnly: the next queued question, without the closing "خسته نباشی".
func (a *App) questionsOnly(d *Data, p *Person, ds *DayState) []Out {
	o := a.nextQuestion(d, p, ds)
	if len(o) == 1 && o[0].Rows == nil && strings.HasPrefix(o[0].Text, "خسته نباشی") && len(ds.Queue) == 0 && ds.Awaiting != "clarify" && ds.Awaiting != "metric" {
		return nil
	}
	return o
}

// dispatch: each chat gets its own worker, so one slow message (voice, a long Claude call)
// never makes other people wait, while one person's messages stay in order.
func (a *App) dispatch(ctx context.Context, m *Messenger, u tgUpdate) {
	var chat int64
	switch {
	case u.Message != nil:
		chat = u.Message.Chat.ID
	case u.Callback != nil && u.Callback.Message != nil:
		chat = u.Callback.Message.Chat.ID
	}
	key := fmt.Sprintf("%s:%d", m.Name, chat)
	a.workMu.Lock()
	if a.workers == nil {
		a.workers = map[string]chan tgUpdate{}
	}
	ch, ok := a.workers[key]
	if !ok {
		ch = make(chan tgUpdate, 64)
		a.workers[key] = ch
		go a.worker(ctx, m, key, ch)
	}
	ch <- u // under the lock, so an idle worker can't quit with this update unread
	a.workMu.Unlock()
}

func (a *App) worker(ctx context.Context, m *Messenger, key string, ch chan tgUpdate) {
	idle := time.NewTimer(2 * time.Minute)
	defer idle.Stop()
	for {
		select {
		case u := <-ch:
			a.handleUpdate(ctx, m, u)
			idle.Reset(2 * time.Minute)
		case <-idle.C:
			a.workMu.Lock()
			if len(ch) == 0 {
				delete(a.workers, key)
				a.workMu.Unlock()
				return
			}
			a.workMu.Unlock()
			idle.Reset(2 * time.Minute)
		case <-ctx.Done():
			return
		}
	}
}
