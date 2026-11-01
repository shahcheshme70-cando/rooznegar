package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

type liveTG struct {
	fakeTG
	qmu sync.Mutex
	q   []map[string]any
	uid int64
}

func (l *liveTG) push(u map[string]any) {
	l.qmu.Lock()
	l.uid++
	u["update_id"] = l.uid
	l.q = append(l.q, u)
	l.qmu.Unlock()
}

func (l *liveTG) h(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/getUpdates") {
		for i := 0; i < 20; i++ {
			l.qmu.Lock()
			if len(l.q) > 0 {
				q := l.q
				l.q = nil
				l.qmu.Unlock()
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": q})
				return
			}
			l.qmu.Unlock()
			time.Sleep(50 * time.Millisecond)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
		return
	}
	l.handler(w, r)
}

func TestLiveNoHang(t *testing.T) {
	tg := &liveTG{}
	srv := httptest.NewServer(http.HandlerFunc(tg.h))
	defer srv.Close()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.DataDir, cfg.TelegramToken, cfg.TelegramAPI, cfg.BaleToken, cfg.BaleAPI = dir, "T", srv.URL, "B", srv.URL
	cfg.BaseURL = "http://x"
	st, _ := OpenStore(dir)
	a := NewApp(cfg, st)
	if _, err := seed(a, "deploy/seed.json"); err != nil {
		t.Fatal(err)
	}
	addUser(st, "moein", "admin", "مدیر", "password123")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.poll(ctx, a.Msgr["telegram"])
	go a.scheduler(ctx)
	web := httptest.NewServer(a.routes())
	defer web.Close()

	code := func(n string) (c string) {
		a.Store.View(func(d *Data) { c = d.PersonByName(n).LinkCode })
		return
	}
	msg := func(chat int64, text string) {
		tg.push(map[string]any{"message": map[string]any{"message_id": 5, "chat": map[string]any{"id": chat}, "text": text}})
	}
	press := func(chat int64, data string) {
		tg.push(map[string]any{"callback_query": map[string]any{"id": "c", "data": data, "message": map[string]any{"message_id": 7, "chat": map[string]any{"id": chat}, "text": "orig"}}})
	}
	msg(900, code("مدیر"))
	msg(300, code("نیما"))
	time.Sleep(1500 * time.Millisecond)

	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	get := func(p string) {
		resp, err := cl.Get(web.URL + p)
		if err != nil {
			pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			t.Fatalf("GET %s: %v", p, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	cl.PostForm(web.URL+"/login", url.Values{"login": {"moein"}, "password": {"password123"}})
	get("/projects")
	var nima string
	a.Store.View(func(d *Data) { nima = d.PersonByName("نیما").ID })
	cl.PostForm(web.URL+"/drafts/new", url.Values{"to": {nima}, "text": {"سلام نیما، پوستر چی شد؟"}, "followup": {"1"}, "send": {"1"}})
	time.Sleep(500 * time.Millisecond)
	var fid string
	a.Store.View(func(d *Data) {
		for _, f := range d.Followups {
			fid = f.ID
		}
	})
	t.Log("followup", fid)
	for _, data := range []string{"f:" + fid + ":done", "n:start", "n:later", "m:ask"} {
		press(300, data)
		time.Sleep(800 * time.Millisecond)
		get("/projects")
		get("/")
	}
	msg(300, "سلام، پوستر تقریبا تمومه")
	time.Sleep(1500 * time.Millisecond)
	get("/projects")
	t.Log(dump(tg.take()))
}
