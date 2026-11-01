package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBroadcast(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	h.now = time.Now()
	a.Now = time.Now
	if _, err := seed(a, "deploy/seed.json"); err != nil {
		t.Fatal(err)
	}
	addUser(a.Store, "moein", "admin", "مدیر", "password123")
	code := func(n string) (c string) {
		a.Store.View(func(d *Data) { c = d.PersonByName(n).LinkCode })
		return
	}
	h.text("telegram", 900, code("مدیر"))
	h.text("telegram", 300, code("نیما"))
	h.text("telegram", 101, code("احمد طهماسبی"))
	h.text("bale", 201, code("علیرضا حاجی سامی"))
	h.tg.take()
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar}
	cl.PostForm(srv.URL+"/login", url.Values{"login": {"moein"}, "password": {"password123"}})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "dastyar-kandoo.mp4")
	fw.Write([]byte("fake mp4 bytes"))
	mw.WriteField("caption", "سلام به همه")
	mw.WriteField("aud", "all")
	mw.WriteField("me", "1")
	mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/broadcast", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := cl.Do(req)
	if err != nil || resp.StatusCode != 200 || !strings.Contains(resp.Request.URL.String(), "sent=1") {
		var bb bytes.Buffer
		bb.ReadFrom(resp.Body)
		t.Fatalf("send: %v %s %s", err, resp.Request.URL, bb.String()[:300])
	}
	var done bool
	for i := 0; i < 50 && !done; i++ {
		time.Sleep(50 * time.Millisecond)
		a.Store.View(func(d *Data) { done = len(d.Broadcasts) == 1 && d.Broadcasts[0].Done })
	}
	if !done {
		t.Fatal("broadcast did not finish")
	}
	ms := h.tg.take()
	uploads, byid := 0, 0
	for _, m := range ms {
		if strings.HasPrefix(m.Text, "upload|sendVideo|") && strings.HasSuffix(m.Text, "|سلام به همه") {
			uploads++
		}
		if strings.HasPrefix(m.Text, "byid|sendVideo|FID-") {
			byid++
		}
	}
	// 3 telegram (boss, Nima, Ahmad) + 1 bale: one upload per messenger, the rest by file_id
	if uploads != 2 || byid != 2 {
		t.Fatalf("uploads=%d byid=%d\n%s", uploads, byid, dump(ms))
	}
	if find(ms, 900, "برای ۴ نفر رفت") == nil {
		t.Fatalf("no report to sender:\n%s", dump(ms))
	}
	page, _ := cl.Get(srv.URL + "/broadcast")
	var b bytes.Buffer
	b.ReadFrom(page.Body)
	if !strings.Contains(b.String(), "رسید به ۴ از ۴ نفر") {
		t.Fatal("history missing on page")
	}
}

func TestSendFileFromClaude(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	a.Now = time.Now
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
	h.text("telegram", 900, code("مدیر"))
	h.text("telegram", 300, code("نیما"))
	h.text("telegram", 101, code("احمد طهماسبی"))
	h.tg.take()
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	out, err := toolSendFile(a, u, map[string]any{"caption": "ویدیوی معرفی", "audience": "people", "people": "نیما"})
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(out, "https://rz.example/up/")
	tok := strings.Fields(out[i+len("https://rz.example/up/"):])[0]
	post := func() (*http.Response, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "moarefi.mp4")
		fw.Write([]byte("fake"))
		mw.Close()
		resp, err := http.Post(srv.URL+"/up/"+tok, mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return resp, b.String()
	}
	resp, body := post()
	if resp.StatusCode != 200 || !strings.Contains(body, `"to":2`) {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	for i := 0; i < 50; i++ {
		time.Sleep(40 * time.Millisecond)
		var done bool
		a.Store.View(func(d *Data) { done = len(d.Broadcasts) == 1 && d.Broadcasts[0].Done })
		if done {
			break
		}
	}
	ms := h.tg.take()
	if find(ms, 300, "ویدیوی معرفی") == nil || find(ms, 101, "ویدیوی معرفی") != nil {
		t.Fatalf("wrong recipients:\n%s", dump(ms))
	}
	if resp, _ := post(); resp.StatusCode != 404 {
		t.Fatal("ticket reused")
	}
}
