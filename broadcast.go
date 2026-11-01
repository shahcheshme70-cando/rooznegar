package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Broadcast: one file (video, photo or document) with a caption, from the bot to many people.

type Broadcast struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	By      string    `json:"by"`
	File    string    `json:"file"`
	Caption string    `json:"caption"`
	To      int       `json:"to"`
	OK      int       `json:"ok"`
	Fail    []string  `json:"fail"`
	Done    bool      `json:"done"`
}

func mediaKind(name string) (method, field string) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".mov", ".m4v":
		return "sendVideo", "video"
	case ".jpg", ".jpeg", ".png", ".webp":
		return "sendPhoto", "photo"
	}
	return "sendDocument", "document"
}

// SendMedia sends a file by upload (path) or by a file_id the messenger already has.
// It returns the file_id to reuse for the next recipient.
func (m *Messenger) SendMedia(ctx context.Context, chat int64, path, fileID, caption string) (string, error) {
	method, field := mediaKind(path)
	var raw json.RawMessage
	if fileID != "" {
		body := map[string]any{"chat_id": chat, field: fileID, "caption": caption}
		if field == "video" {
			body["supports_streaming"] = true
		}
		if err := m.call(ctx, method, body, &raw); err != nil {
			return "", err
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		pr, pw := io.Pipe()
		w := multipart.NewWriter(pw)
		go func() {
			w.WriteField("chat_id", fmt.Sprint(chat))
			if caption != "" {
				w.WriteField("caption", caption)
			}
			if field == "video" {
				w.WriteField("supports_streaming", "true")
			}
			fw, err := w.CreateFormFile(field, filepath.Base(path))
			if err == nil {
				_, err = io.Copy(fw, f)
			}
			w.Close()
			pw.CloseWithError(err)
		}()
		req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/bot%s/%s", m.Base, m.Token, method), pr)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
		resp, err := m.HTTP.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var r tgResp
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return "", fmt.Errorf("%s %s: %v", m.Name, method, err)
		}
		if !r.OK {
			return "", fmt.Errorf("%s %s: %s", m.Name, method, r.Description)
		}
		raw = r.Result
	}
	var msg struct {
		Video    *tgFile  `json:"video"`
		Document *tgFile  `json:"document"`
		Photo    []tgFile `json:"photo"`
	}
	json.Unmarshal(raw, &msg)
	switch {
	case msg.Video != nil:
		return msg.Video.FileID, nil
	case msg.Document != nil:
		return msg.Document.FileID, nil
	case len(msg.Photo) > 0:
		return msg.Photo[len(msg.Photo)-1].FileID, nil
	}
	return "", nil
}

// sendBroadcast runs in the background; it reports to the sender when finished.
func (a *App) sendBroadcast(bc *Broadcast, path string, people []Person, byPerson *Person) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in broadcast: %v", r)
		}
	}()
	ids := map[string]string{} // messenger -> file_id after the first upload
	ok := 0
	var fail []string
	for _, p := range people {
		m := a.Msgr[p.Channel]
		if !m.Enabled() || p.ChatID == 0 {
			fail = append(fail, firstName(&p)+" (وصل نیست)")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		id, err := m.SendMedia(ctx, p.ChatID, path, ids[p.Channel], bc.Caption)
		cancel()
		if err != nil && ids[p.Channel] != "" { // a stale file_id: upload again
			ctx, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
			id, err = m.SendMedia(ctx, p.ChatID, path, "", bc.Caption)
			cancel()
		}
		if err != nil {
			log.Printf("broadcast to %s: %v", p.Name, err)
			fail = append(fail, firstName(&p))
			continue
		}
		if id != "" {
			ids[p.Channel] = id
		}
		ok++
	}
	var outs []Out
	a.Store.Do(func(d *Data) error {
		for _, b := range d.Broadcasts {
			if b.ID == bc.ID {
				b.OK, b.Fail, b.Done = ok, fail, true
			}
		}
		d.Log("broadcast", "", "", fmt.Sprintf("%s · %d از %d", filepath.Base(bc.File), ok, len(people)))
		if byPerson != nil && byPerson.ChatID != 0 {
			msg := fmt.Sprintf("📣 «%s» برای %s نفر رفت.", filepath.Base(bc.File), fa(ok))
			if len(fail) > 0 {
				msg += "\nنرسید: " + strings.Join(fail, "، ")
			}
			outs = append(outs, outTo(byPerson, msg, nil))
		}
		return nil
	})
	a.flush(outs)
}

func (a *App) broadcastPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "broadcast")
	a.Store.View(func(d *Data) {
		var ps []*Person
		for _, p := range d.People {
			if p.Active && canMessage(u, p) {
				ps = append(ps, p)
			}
		}
		m["People"] = ps
		m["Lines"] = lines(d)
		var list []map[string]any
		for i := len(d.Broadcasts) - 1; i >= 0 && len(list) < 10; i-- {
			b := d.Broadcasts[i]
			st := "در حال ارسال…"
			if b.Done {
				st = fmt.Sprintf("رسید به %s از %s نفر", fa(b.OK), fa(b.To))
				if len(b.Fail) > 0 {
					st += " · نرسید: " + strings.Join(b.Fail, "، ")
				}
			}
			list = append(list, map[string]any{"Title": filepath.Base(b.File), "Sub": faDate(b.At.In(loadLoc(a.Cfg.ManagerTZ))) + " · " + st, "Caption": b.Caption})
		}
		m["List"] = list
		m["Sent"] = r.URL.Query().Get("sent") != ""
	})
	render(w, "broadcast", m)
}

// saveUpload stores an uploaded file under files/broadcast.
func (a *App) saveUpload(r *http.Request) (string, error) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return "", fmt.Errorf("فایل خوانده نشد (حداکثر ۵۰ مگابایت): %v", err)
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return "", fmt.Errorf("فایلی انتخاب نشده")
	}
	defer f.Close()
	if hdr.Size > 50<<20 {
		return "", fmt.Errorf("فایل از ۵۰ مگابایت بزرگ‌تر است؛ تلگرام از ربات بیشتر از این نمی‌پذیرد")
	}
	dir := filepath.Join(a.Files, "broadcast")
	os.MkdirAll(dir, 0o750)
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"_"+safeName(hdr.Filename))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, f); err != nil {
		return "", err
	}
	return path, nil
}

// startBroadcast picks the recipients and sends in the background.
// aud: all | line | pick | me. pick holds person IDs or names.
func (a *App) startBroadcast(u *User, path, caption, aud, line string, pick map[string]bool, me bool) (*Broadcast, error) {
	if len([]rune(caption)) > 1000 {
		caption = string([]rune(caption)[:1000])
	}
	var people []Person
	var by *Person
	bc := &Broadcast{ID: newID(), At: a.Now(), By: u.Name, File: path, Caption: caption}
	a.Store.Do(func(d *Data) error {
		if p := d.Person(u.PersonID); p != nil {
			c := *p
			by = &c
		}
		for _, p := range d.People {
			if !p.Active || p.ChatID == 0 || !canMessage(u, p) {
				continue
			}
			isMe := by != nil && p.ID == by.ID
			picked := pick[p.ID] || pick[normName(p.Name)] || pick[normName(firstName(p))]
			switch {
			case aud == "me":
				if !isMe {
					continue
				}
			case isMe:
				if !me {
					continue
				}
			case aud == "line" && p.Line != line:
				continue
			case aud == "pick" && !picked:
				continue
			case aud == "all" && p.Role == "admin":
				continue
			}
			people = append(people, *p)
		}
		bc.To = len(people)
		if len(people) > 0 {
			d.Broadcasts = append(d.Broadcasts, bc)
		}
		return nil
	})
	if len(people) == 0 {
		return nil, fmt.Errorf("گیرنده‌ای پیدا نشد (فقط کسانی که ربات را وصل کرده‌اند)")
	}
	go a.sendBroadcast(bc, path, people, by)
	return bc, nil
}

func (a *App) broadcastSend(w http.ResponseWriter, r *http.Request, u *User) {
	path, err := a.saveUpload(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	pick := map[string]bool{}
	for _, id := range r.Form["p"] {
		pick[id] = true
	}
	if _, err := a.startBroadcast(u, path, strings.TrimSpace(r.FormValue("caption")), r.FormValue("aud"), r.FormValue("line"), pick, r.FormValue("me") == "1"); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/broadcast?sent=1", http.StatusSeeOther)
}

// ---------- files from Claude ----------

// FileTicket: a one-time address Claude uploads a file to; the bot then sends it.
type FileTicket struct {
	Token   string    `json:"token"`
	UserID  string    `json:"user_id"`
	Caption string    `json:"caption"`
	Aud     string    `json:"aud"`
	Line    string    `json:"line"`
	People  []string  `json:"people"`
	Me      bool      `json:"me"`
	Expires time.Time `json:"expires"`
	Used    bool      `json:"used"`
}

func (a *App) fileUpload(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	var tk FileTicket
	var u *User
	a.Store.Do(func(d *Data) error {
		for _, t := range d.FileTickets {
			if t.Token == tok && !t.Used && a.Now().Before(t.Expires) {
				t.Used = true
				tk = *t
				if x := d.User(t.UserID); x != nil {
					c := *x
					u = &c
				}
			}
		}
		return nil
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fail := func(code int, msg string) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
	}
	if u == nil {
		time.Sleep(500 * time.Millisecond)
		fail(404, "این نشانی معتبر نیست یا قبلاً استفاده شده؛ از send_file نشانی تازه بگیر")
		return
	}
	path, err := a.saveUpload(r)
	if err != nil {
		fail(400, err.Error())
		return
	}
	pick := map[string]bool{}
	for _, n := range tk.People {
		pick[normName(n)] = true
	}
	caption := tk.Caption
	if c := strings.TrimSpace(r.FormValue("caption")); c != "" {
		caption = c
	}
	bc, err := a.startBroadcast(u, path, caption, tk.Aud, tk.Line, pick, tk.Me)
	if err != nil {
		fail(400, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "to": bc.To, "message": fmt.Sprintf("در حال ارسال به %d نفر؛ نتیجه در تلگرام برای فرستنده می‌آید.", bc.To)})
}
