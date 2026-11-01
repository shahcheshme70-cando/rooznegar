package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Bargozar client: talks to the Bargozar panel the same way its own web page
// does (login, create an upload link, read a project's files). No change to
// Bargozar is needed; it uses a normal panel user made for Rooznegar.

type Bargozar struct {
	Base string
	User string
	Pass string
	HTTP *http.Client
	mu   sync.Mutex
}

func newBargozar(base, user, pass string) *Bargozar {
	jar, _ := cookiejar.New(nil)
	return &Bargozar{Base: strings.TrimRight(base, "/"), User: user, Pass: pass, HTTP: &http.Client{Timeout: 40 * time.Second, Jar: jar}}
}

func (b *Bargozar) Enabled() bool { return b != nil && b.Base != "" && b.User != "" }

func (b *Bargozar) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.Base+"/api/"+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-BZ", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return resp.StatusCode, errors.New("بارگذار: " + e.Error)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("بارگذار: پاسخ نامفهوم")
		}
	}
	return 200, nil
}

func (b *Bargozar) login(ctx context.Context) error {
	_, err := b.do(ctx, "POST", "login", map[string]any{"user": b.User, "pass": b.Pass}, nil)
	return err
}

// call logs in when needed and retries once after a 401.
func (b *Bargozar) call(ctx context.Context, method, path string, body any, out any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	code, err := b.do(ctx, method, path, body, out)
	if code == http.StatusUnauthorized {
		if err := b.login(ctx); err != nil {
			return err
		}
		_, err = b.do(ctx, method, path, body, out)
	}
	return err
}

type bzReq struct {
	Token   string `json:"token"`
	Title   string `json:"title"`
	Dir     string `json:"dir"`
	Count   int    `json:"count"`
	URL     string `json:"url"`
	Expires int64  `json:"expires"`
}

type bzFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Size any    `json:"size"`
}

type bzInfo struct {
	Requests []bzReq  `json:"requests"`
	Files    []bzFile `json:"files"`
}

// CreateUploadLink: an upload link into <project>/<sub>, valid for `days`.
func (b *Bargozar) CreateUploadLink(ctx context.Context, project, sub, title, note string, days int) (bzReq, error) {
	var r bzReq
	err := b.call(ctx, "POST", "req/create", map[string]any{
		"p": project, "dir": "", "sub": sub, "title": title, "note": note, "pass": "", "types": "",
		"need_name": false, "days": days, "max_files": 0, "max_mb": 0,
	}, &r)
	if err == nil && r.URL == "" {
		err = errors.New("بارگذار لینکی برنگرداند")
	}
	return r, err
}

func (b *Bargozar) Project(ctx context.Context, project string) (*bzInfo, error) {
	var in bzInfo
	err := b.call(ctx, "GET", "project?p="+url.QueryEscape(project), nil, &in)
	return &in, err
}

// ---------- in Rooznegar ----------

// Upload: an upload link handed to someone, watched until files arrive.
type Upload struct {
	Token    string    `json:"token"`
	URL      string    `json:"url"`
	Project  string    `json:"project"` // Bargozar project slug
	Dir      string    `json:"dir"`
	TaskID   string    `json:"task_id"`
	PersonID string    `json:"person_id"`
	Seen     []string  `json:"seen"` // file paths already announced
	Count    int       `json:"count"`
	At       time.Time `json:"at"`
	Until    time.Time `json:"until"`
}

const uploadDays = 3

func bzFolder(title string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "", "،", "")
	return "تحویل/" + strings.Join(strings.Fields(r.Replace(title)), "-")
}

// uploadChoices: tasks that can receive a Bargozar link (project mapped).
func (a *App) uploadChoices(d *Data, p *Person, today string) []*Task {
	var out []*Task
	seen := map[string]bool{}
	add := func(t *Task) {
		if seen[t.ID] || len(out) >= 6 {
			return
		}
		if pr := d.Project(t.ProjectID); pr != nil && pr.Bargozar != "" {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	for _, t := range a.todaysTasks(d, p, today) {
		add(t)
	}
	for _, t := range d.OpenTasks(p.ID) {
		add(t)
	}
	// delivered today (often the big file comes right after)
	for _, t := range d.Tasks {
		if t.PersonID == p.ID && t.DoneAt != nil && a.Now().Sub(*t.DoneAt) < 24*time.Hour {
			add(t)
		}
	}
	return out
}

// offerUpload asks which task, or creates the link right away when obvious.
func (a *App) offerUpload(ctx context.Context, p *Person, reason string) {
	if !a.BZ.Enabled() {
		a.flush([]Out{outTo(p, "فعلاً فایل را همین‌جا بفرست. اگر خیلی بزرگ است، لینکش را (درایو یا هر جای دیگر) بفرست.", nil)})
		return
	}
	var choices []*Task
	a.Store.View(func(d *Data) { choices = a.uploadChoices(d, p, a.today(p)) })
	switch len(choices) {
	case 0:
		a.flush([]Out{outTo(p, "کاری که پروژه‌اش در بارگذار تعریف شده باشد برایت پیدا نکردم. به مدیر بگو یا لینک فایل را همین‌جا بفرست.", nil)})
	case 1:
		a.makeUploadLink(ctx, p, choices[0].ID)
	default:
		var rows [][]Button
		for _, t := range choices {
			rows = append(rows, []Button{{Text: shorten(t.Title, 40), Data: "u:" + t.ID}})
		}
		lead := "این فایل مال کدام کار است؟"
		if reason != "" {
			lead = reason + "\n" + lead
		}
		a.flush([]Out{outTo(p, lead, rows)})
	}
}

func (a *App) makeUploadLink(ctx context.Context, p *Person, taskID string) {
	var t Task
	var slug, projTitle string
	ok := false
	a.Store.View(func(d *Data) {
		x := d.Task(taskID)
		if x == nil || x.PersonID != p.ID {
			return
		}
		if pr := d.Project(x.ProjectID); pr != nil && pr.Bargozar != "" {
			t, slug, projTitle, ok = *x, pr.Bargozar, pr.Name, true
		}
	})
	if !ok {
		a.flush([]Out{outTo(p, "پروژهٔ این کار در بارگذار تعریف نشده.", nil)})
		return
	}
	// reuse a live link for the same task
	var live *Upload
	a.Store.View(func(d *Data) {
		for _, u := range d.Uploads {
			if u.TaskID == taskID && u.PersonID == p.ID && a.Now().Before(u.Until) {
				c := *u
				live = &c
			}
		}
	})
	if live != nil {
		a.flush([]Out{outTo(p, "لینک آپلود «"+t.Title+"»:\n"+live.URL+"\n\nفایل‌ها را آن‌جا رها کن؛ هر حجمی، و اگر قطع شد از همان‌جا ادامه می‌دهد.", nil)})
		return
	}
	c2, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	r, err := a.BZ.CreateUploadLink(c2, slug, bzFolder(t.Title), firstName(p)+" · "+t.Title, "از روزنگار · "+projTitle, uploadDays)
	if err != nil {
		log.Printf("bargozar link: %v", err)
		a.flush([]Out{outTo(p, "لینک بارگذار ساخته نشد. چند دقیقهٔ دیگر دوباره «لینک آپلود» را بفرست.", nil)})
		return
	}
	var outs []Out
	a.Store.Do(func(d *Data) error {
		d.Uploads = append(d.Uploads, &Upload{Token: r.Token, URL: r.URL, Project: slug, Dir: r.Dir, TaskID: taskID, PersonID: p.ID,
			At: a.Now(), Until: a.Now().Add(uploadDays * 24 * time.Hour)})
		d.Log("upload_link", p.ID, taskID, r.Dir)
		outs = append(outs, outTo(p, "لینک آپلود «"+t.Title+"»:\n"+r.URL+"\n\nفایل‌ها را آن‌جا رها کن؛ هر حجمی، و اگر قطع شد از همان‌جا ادامه می‌دهد. تا "+fa(uploadDays)+" روز باز است. وقتی رسید خبرت می‌کنم.", nil))
		return nil
	})
	a.flush(outs)
}

// watchUploads checks open links and announces new files (outside the store lock).
func (a *App) watchUploads(ctx context.Context) {
	if !a.BZ.Enabled() {
		return
	}
	tk := time.NewTicker(90 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("panic in uploads: %v", r)
					}
				}()
				a.CheckUploads(ctx)
			}()
		}
	}
}

func (a *App) CheckUploads(ctx context.Context) {
	projects := map[string]bool{}
	a.Store.View(func(d *Data) {
		for _, u := range d.Uploads {
			if a.Now().Before(u.Until.Add(6 * time.Hour)) {
				projects[u.Project] = true
			}
		}
	})
	for slug := range projects {
		c2, cancel := context.WithTimeout(ctx, 40*time.Second)
		info, err := a.BZ.Project(c2, slug)
		cancel()
		if err != nil {
			log.Printf("bargozar project %s: %v", slug, err)
			continue
		}
		var outs []Out
		a.Store.Do(func(d *Data) error {
			outs = a.applyUploads(d, slug, info)
			return nil
		})
		a.flush(outs)
	}
}

func (a *App) applyUploads(d *Data, slug string, info *bzInfo) []Out {
	var outs []Out
	reqs := map[string]bzReq{}
	for _, r := range info.Requests {
		reqs[r.Token] = r
	}
	for _, u := range d.Uploads {
		if u.Project != slug {
			continue
		}
		r, ok := reqs[u.Token]
		if !ok || r.Count == u.Count {
			continue
		}
		u.Count = r.Count
		dir := u.Dir
		if r.Dir != "" {
			dir = r.Dir
		}
		seen := map[string]bool{}
		for _, s := range u.Seen {
			seen[s] = true
		}
		p, t := d.Person(u.PersonID), d.Task(u.TaskID)
		if p == nil {
			continue
		}
		var names []string
		for _, f := range info.Files {
			if !strings.HasPrefix(f.Path, dir+"/") || seen[f.Path] {
				continue
			}
			u.Seen = append(u.Seen, f.Path)
			name := f.Name
			if name == "" {
				name = f.Path[strings.LastIndex(f.Path, "/")+1:]
			}
			names = append(names, name)
			ref := "bz:" + f.URL
			if t != nil {
				t.Files = append(t.Files, ref)
			}
			d.Log("upload", p.ID, u.TaskID, name)
			if fc := a.Cfg.TelegramFilesChat; fc != 0 && a.Msgr["telegram"].Enabled() {
				cap := a.fileCaption(d, p, t, a.today(p), name, "") + "\n🔗 " + f.URL
				outs = append(outs, Out{Channel: "telegram", Chat: fc, Text: cap})
			}
		}
		if len(names) > 0 && p.ChatID != 0 {
			title := ""
			if t != nil {
				title = " برای «" + t.Title + "»"
			}
			outs = append(outs, outTo(p, fmt.Sprintf("رسید ✔ %s فایل%s در بارگذار: %s", fa(len(names)), title, strings.Join(names, "، ")), nil))
		}
	}
	return outs
}
