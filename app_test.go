package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- fake messenger ----------

type sent struct {
	Chat   int64
	Text   string
	Markup string
	Edit   bool
}

type fakeTG struct {
	mu   sync.Mutex
	msgs []sent
	n    int64
}

func (f *fakeTG) handler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/file/") {
		w.Write([]byte("%PDF-fake"))
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	method := parts[len(parts)-1]
	bot := parts[len(parts)-2]
	var body map[string]any
	if (method == "sendVideo" || method == "sendPhoto") && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.ParseMultipartForm(8 << 20)
		f.mu.Lock()
		f.n++
		name := ""
		for _, fh := range r.MultipartForm.File {
			name = fh[0].Filename
		}
		f.msgs = append(f.msgs, sent{Chat: atoi(r.FormValue("chat_id")), Text: "upload|" + method + "|" + name + "|" + r.FormValue("caption")})
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.n, "video": map[string]any{"file_id": "FID-" + bot}}})
		f.mu.Unlock()
		return
	}
	if method == "sendDocument" {
		r.ParseMultipartForm(1 << 20)
		f.mu.Lock()
		f.n++
		file, hdr, err := r.FormFile("document")
		name := ""
		if err == nil {
			name = hdr.Filename
			file.Close()
		}
		f.msgs = append(f.msgs, sent{Chat: atoi(r.FormValue("chat_id")), Text: "sendDocument|" + bot + "|" + name + "|" + r.FormValue("caption")})
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.n}})
		f.mu.Unlock()
		return
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "getFile":
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": "docs/plan.pdf"}})
		return
	case "copyMessage", "editMessageCaption":
		f.n++
		chat := int64(body["chat_id"].(float64))
		f.msgs = append(f.msgs, sent{Chat: chat, Text: method + "|" + fmtAny(body["caption"])})
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.n}})
	case "sendVideo", "sendPhoto":
		f.n++
		f.msgs = append(f.msgs, sent{Chat: int64(body["chat_id"].(float64)), Text: "byid|" + method + "|" + fmtAny(body["video"]) + "|" + fmtAny(body["caption"])})
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.n, "video": map[string]any{"file_id": fmtAny(body["video"])}}})
	case "sendMessage", "editMessageText":
		f.n++
		mk, _ := json.Marshal(body["reply_markup"])
		chat := int64(body["chat_id"].(float64))
		f.msgs = append(f.msgs, sent{Chat: chat, Text: body["text"].(string), Markup: string(mk), Edit: method == "editMessageText"})
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.n, "chat": map[string]any{"id": chat}}})
	default:
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}
}

func (f *fakeTG) take() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.msgs
	f.msgs = nil
	return out
}

// ---------- fake Claude ----------

type fakeClaude struct {
	parse func(prompt string) map[string]any
	calls int
}

func (c *fakeClaude) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tools    []any `json:"tools"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	c.calls++
	prompt := req.Messages[0].Content
	if len(req.Tools) > 0 {
		json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "record_report", "input": c.parse(prompt)}},
			"usage": map[string]any{"input_tokens": 700, "output_tokens": 90}})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "نیمهٔ روز. حسن تصویرهای بعدی را هم تحویل داد."}}})
}

// task id for a title inside the prompt
func idFor(prompt, title string) string {
	re := regexp.MustCompile(`task_id=(\w+) \| ` + regexp.QuoteMeta(title))
	m := re.FindStringSubmatch(prompt)
	if m == nil {
		return ""
	}
	return m[1]
}

// ---------- harness ----------

type harness struct {
	t    *testing.T
	app  *App
	tg   *fakeTG
	cl   *fakeClaude
	now  time.Time
	srvs []*httptest.Server
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, tg: &fakeTG{}, cl: &fakeClaude{}}
	tgs := httptest.NewServer(http.HandlerFunc(h.tg.handler))
	cls := httptest.NewServer(http.HandlerFunc(h.cl.handler))
	h.srvs = append(h.srvs, tgs, cls)
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.DataDir, cfg.TelegramToken, cfg.TelegramAPI, cfg.BaleToken, cfg.BaleAPI = dir, "T", tgs.URL, "B", tgs.URL
	cfg.AnthropicKey, cfg.AnthropicURL, cfg.BaseURL = "k", cls.URL, "https://rz.example"
	cfg.BatchSeconds = 0
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.app = NewApp(cfg, st)
	h.app.Now = func() time.Time { return h.now }
	return h
}

func (h *harness) close() {
	for _, s := range h.srvs {
		s.Close()
	}
}

func (h *harness) at(s string) {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loadLoc("Asia/Tehran"))
	if err != nil {
		h.t.Fatal(err)
	}
	h.now = t
}

func (h *harness) text(channel string, chat int64, text string) {
	h.app.handleUpdate(context.Background(), h.app.Msgr[channel], tgUpdate{Message: &tgMessage{Chat: tgChat{ID: chat}, Text: text}})
}

func (h *harness) press(channel string, chat int64, data string) {
	h.app.handleUpdate(context.Background(), h.app.Msgr[channel], tgUpdate{Callback: &tgCallback{ID: "c", Data: data, Message: &tgMessage{MessageID: 1, Chat: tgChat{ID: chat}, Text: "orig"}}})
}

func find(ms []sent, chat int64, sub string) *sent {
	for i := range ms {
		if ms[i].Chat == chat && strings.Contains(ms[i].Text, sub) {
			return &ms[i]
		}
	}
	return nil
}

func dataIn(markup, prefix string) string {
	re := regexp.MustCompile(`"callback_data":"(` + regexp.QuoteMeta(prefix) + `[^"]*)"`)
	m := re.FindStringSubmatch(markup)
	if m == nil {
		return ""
	}
	return m[1]
}

func dump(ms []sent) string {
	var sb strings.Builder
	for _, m := range ms {
		sb.WriteString("→ " + strings.ReplaceAll(m.Text, "\n", " ⏎ ") + "\n")
	}
	return sb.String()
}

const (
	chatAhmad = 101
	chatHasan = 102
	chatNasim = 103
	chatBoss  = 900
	chatSami  = 201
)

func TestJalali(t *testing.T) {
	cases := map[string]string{"2026-10-27": "1405/08/05", "2026-11-04": "1405/08/13", "2026-03-21": "1405/01/01", "2025-03-20": "1403/12/30"}
	for g, want := range cases {
		tt, _ := time.Parse("2006-01-02", g)
		if got := jalaliNum(tt); got != want {
			t.Errorf("%s: got %s want %s", g, got, want)
		}
	}
	tt, _ := time.Parse("2006-01-02", "2026-11-04")
	if faDate(tt) != "چهارشنبه ۱۳ آبان" {
		t.Errorf("faDate: %s", faDate(tt))
	}
}

func TestDay(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	h.app.Cfg.UnapprovedPlan = "wait" // this test covers the approval path; autopilot has its own test
	a := h.app
	h.at("2026-11-03 10:00")

	// ---- seed ----
	seedJSON := `{
	 "people":[
	  {"name":"احمد طهماسبی","line":"گرافیک","tz":"Europe/Istanbul","rhythm":{"plan":"07:00","nudge":"09:40","noon":"12:30","report":"16:30"}},
	  {"name":"حسن جعفری‌نیا","line":"گرافیک","rhythm":{"report":"17:00"}},
	  {"name":"نسیم","line":"گرافیک","rhythm":{"plan":"08:00","report":"17:00"}},
	  {"name":"ایلیا","line":"گرافیک","rhythm":{"report":"17:00"}},
	  {"name":"علیرضا حاجی سامی","line":"ساخت","channel":"bale","rhythm":{"report":"19:00"}},
	  {"name":"مدیر","role":"admin"}
	 ],
	 "projects":[{"id":"khane","name":"نمایشگاه خانه‌تکانی"},{"id":"hall8","name":"سالن ۸ حرف نو"},{"id":"sanandaj","name":"حرف نو سنندج"}],
	 "tasks":[
	  {"key":"lay","person":"ایلیا","project":"نمایشگاه خانه‌تکانی","title":"لی‌اوت تابلو ۳۳ تا ۳۶"},
	  {"key":"typo","person":"احمد طهماسبی","project":"نمایشگاه خانه‌تکانی","title":"تایپوی تابلو ۳۳ تا ۳۶","after":"lay","priority":5},
	  {"person":"نسیم","project":"نمایشگاه خانه‌تکانی","title":"بارگذاری تابلو ۳۳ تا ۳۶","after":"typo"},
	  {"person":"احمد طهماسبی","project":"سالن ۸ حرف نو","title":"سالن ۸، ایستگاه ۱","kind":"multi","detail":"سرتیترها با سبک ۱۲ تایپوی اول"},
	  {"person":"علیرضا حاجی سامی","project":"حرف نو سنندج","title":"لایت‌باکس سنندج","kind":"metric","unit":"متر","daily_target":65,"total":518}
	 ]}`
	sf := a.Cfg.DataDir + "/seed.json"
	os.WriteFile(sf, []byte(seedJSON), 0o600)
	if msg, err := seed(a, sf); err != nil {
		t.Fatal(err)
	} else {
		t.Log(msg)
	}

	// ---- linking ----
	codes := map[string]string{}
	a.Store.View(func(d *Data) {
		for _, p := range d.People {
			codes[firstName(p)] = p.LinkCode
		}
	})
	h.text("telegram", chatAhmad, "/start "+codes["احمد"])
	h.text("telegram", chatHasan, "/start "+codes["حسن"])
	h.text("telegram", chatNasim, "/start "+codes["نسیم"])
	h.text("telegram", chatBoss, "/start "+codes["مدیر"])
	h.text("bale", chatSami, codes["علیرضا"]) // code alone, via Bale
	h.text("telegram", 555, "/start 999999")
	ms := h.tg.take()
	if find(ms, chatAhmad, "خوش آمدی") == nil || find(ms, chatSami, "خوش آمدی") == nil || find(ms, 555, "پیدا نشد") == nil {
		t.Fatalf("linking failed:\n%s", dump(ms))
	}

	// Ilya's layout is done (from the dashboard) -> Ahmad's typo becomes ready
	a.Store.Do(func(d *Data) error {
		for _, x := range d.Tasks {
			if strings.HasPrefix(x.Title, "لی‌اوت") {
				a.markDone(d, x, "test")
			}
		}
		return nil
	})

	// ---- evening before: drafts at 20:30, summary at 20:45 (Tehran) ----
	h.at("2026-11-03 20:30")
	a.Tick()
	h.at("2026-11-03 20:45")
	a.Tick()
	ms = h.tg.take()
	card := find(ms, chatBoss, "برنامهٔ فردای احمد")
	if card == nil || !strings.Contains(card.Text, "تایپوی تابلو ۳۳ تا ۳۶") {
		t.Fatalf("no plan card for Ahmad:\n%s", dump(ms))
	}
	if find(ms, chatBoss, "خلاصهٔ امشب") == nil {
		t.Fatalf("no summary:\n%s", dump(ms))
	}
	h.press("telegram", chatBoss, dataIn(card.Markup, "pl:"))
	h.tg.take()

	// ---- Ahmad's day (Istanbul is 30 min behind Tehran) ----
	h.at("2026-11-04 07:30") // 07:00 Istanbul
	a.Tick()
	ms = h.tg.take()
	plan := find(ms, chatAhmad, "صبح بخیر احمد")
	if plan == nil || !strings.Contains(plan.Text, "تایپوی تابلو ۳۳ تا ۳۶") || !strings.Contains(plan.Text, "از ایلیا رسیده") {
		t.Fatalf("morning plan wrong:\n%s", dump(ms))
	}
	t.Logf("PLAN:\n%s", plan.Text)

	h.at("2026-11-04 10:10") // 09:40 Istanbul, not started -> nudge
	a.Tick()
	ms = h.tg.take()
	nudge := find(ms, chatAhmad, "را شروع کردی؟")
	if nudge == nil {
		t.Fatalf("no nudge:\n%s", dump(ms))
	}
	h.press("telegram", chatAhmad, "n:later")
	ms = h.tg.take()
	if find(ms, chatAhmad, "قبول") == nil {
		t.Fatalf("later reply missing:\n%s", dump(ms))
	}
	a.Tick() // no second nudge
	if ms = h.tg.take(); len(ms) != 0 {
		t.Fatalf("unexpected extra messages:\n%s", dump(ms))
	}

	h.at("2026-11-04 13:00") // noon Istanbul
	a.Tick()
	ms = h.tg.take()
	if find(ms, chatAhmad, "نیمهٔ روز") == nil {
		t.Fatalf("no noon message:\n%s", dump(ms))
	}

	h.at("2026-11-04 17:00") // 16:30 Istanbul
	a.Tick()
	ms = h.tg.take()
	if find(ms, chatAhmad, "امروز چه شد") == nil {
		t.Fatalf("no report question:\n%s", dump(ms))
	}

	h.cl.parse = func(p string) map[string]any {
		return map[string]any{
			"delivered": []any{map[string]any{"task_id": idFor(p, "تایپوی تابلو ۳۳ تا ۳۶")}},
			"progress":  []any{map[string]any{"task_id": idFor(p, "سالن ۸، ایستگاه ۱"), "level": "near"}},
			"metrics":   []any{}, "blockers": []any{},
		}
	}
	h.now = h.now.Add(12 * time.Minute)
	h.text("telegram", chatAhmad, "تایپوی ۳۳ تا ۳۶ تمام شد. سالن ۸ هم صحنهٔ ۵ را زدم")
	ms = h.tg.take()
	conf := find(ms, chatAhmad, "ثبت شد")
	if conf == nil || !strings.Contains(conf.Text, "به نسیم خبر دادم") {
		t.Fatalf("confirmation wrong:\n%s", dump(ms))
	}
	eta := find(ms, chatAhmad, "کی تمام می‌شود")
	if eta == nil {
		t.Fatalf("no ETA question:\n%s", dump(ms))
	}
	t.Logf("CONFIRM:\n%s", conf.Text)
	h.press("telegram", chatAhmad, dataIn(eta.Markup, "q:e:"))
	ms = h.tg.take()
	if find(ms, chatAhmad, "خسته نباشی") == nil {
		t.Fatalf("no closing:\n%s", dump(ms))
	}

	// Nasim has the handoff waiting in her news
	var news int
	a.Store.View(func(d *Data) {
		n := d.PersonByName("نسیم")
		for _, x := range d.News {
			if x.PersonID == n.ID && !x.Delivered && strings.Contains(x.Text, "آماده است") {
				news++
			}
		}
	})
	if news != 1 {
		t.Fatalf("handoff news for Nasim: %d", news)
	}

	// ---- Sami in Bale: two days below 65 -> approval to the manager ----
	h.cl.parse = func(p string) map[string]any {
		v := 52.0
		if strings.Contains(p, "۴۸") {
			v = 48
		}
		return map[string]any{"delivered": []any{}, "progress": []any{}, "blockers": []any{},
			"metrics": []any{map[string]any{"task_id": idFor(p, "لایت‌باکس سنندج"), "value": v}}}
	}
	h.at("2026-11-04 19:00")
	a.Tick()
	ms = h.tg.take()
	if find(ms, chatSami, "چند متر") == nil {
		t.Fatalf("no metric question for Sami:\n%s", dump(ms))
	}
	h.text("bale", chatSami, "امروز پنجاه و دو متر زدیم")
	h.tg.take()
	h.at("2026-11-05 19:00")
	a.Tick()
	h.tg.take()
	h.text("bale", chatSami, "۴۸ متر، ترانس‌ها دیر رسید")
	ms = h.tg.take()
	ap := find(ms, chatBoss, "نیروی اضافه")
	if ap == nil {
		t.Fatalf("no extra-crew approval:\n%s", dump(ms))
	}
	h.press("telegram", chatBoss, dataIn(ap.Markup, "a:")) // ok
	ms = h.tg.take()
	if find(ms, chatSami, "تأیید شد") == nil {
		t.Fatalf("Sami not told about approval:\n%s", dump(ms))
	}

	// ---- connector: Claude drafts, manager sends ----
	var token string
	addUser(a.Store, "boss", "admin", "مدیر", "password123")
	a.Store.View(func(d *Data) { token = d.UserByLogin("boss").MCPToken })
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	rpc := func(method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		resp, err := http.Post(srv.URL+"/mcp/"+token, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	callText := func(name string, args map[string]any) string {
		r := rpc("tools/call", map[string]any{"name": name, "arguments": args})
		res, _ := r["result"].(map[string]any)
		if res == nil {
			t.Fatalf("%s: %v", name, r)
		}
		return res["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	init := rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if init["result"] == nil {
		t.Fatalf("initialize: %v", init)
	}
	tl := rpc("tools/list", nil)
	if n := len(tl["result"].(map[string]any)["tools"].([]any)); n < 10 {
		t.Fatalf("tools: %d", n)
	}
	sum := callText("today_summary", nil)
	t.Logf("SUMMARY via connector:\n%s", sum)
	st := callText("person_status", map[string]any{"person": "احمد"})
	if !strings.Contains(st, "نزدیک تمام") {
		t.Fatalf("person_status: %s", st)
	}
	dr := callText("draft_message", map[string]any{"person": "حسن", "text": "متن تابلو ۴۱ و ۴۲ تا پنجشنبه ظهر می‌رسد.", "followup": true})
	if !strings.Contains(dr, "هنوز نرفته") {
		t.Fatalf("draft: %s", dr)
	}
	if ms = h.tg.take(); find(ms, chatHasan, "تابلو ۴۱") != nil {
		t.Fatal("draft was sent without approval")
	}
	id := regexp.MustCompile(`شناسه (\w+)`).FindStringSubmatch(dr)[1]
	if out := callText("send_draft", map[string]any{"draft_id": id}); !strings.Contains(out, "فرستاده شد") {
		t.Fatalf("send: %s", out)
	}
	ms = h.tg.take()
	fu := find(ms, chatHasan, "تابلو ۴۱")
	if fu == nil || dataIn(fu.Markup, "f:") == "" {
		t.Fatalf("followup not delivered with buttons:\n%s", dump(ms))
	}
	h.press("telegram", chatHasan, strings.Replace(dataIn(fu.Markup, "f:"), ":done", ":doing", 1))
	h.tg.take()

	// lead role can't send
	addUser(a.Store, "lead", "lead", "مسئول ساخت", "password123")
	a.Store.Do(func(d *Data) error { d.UserByLogin("lead").Line = "ساخت"; return nil })
	var leadTok string
	a.Store.View(func(d *Data) { leadTok = d.UserByLogin("lead").MCPToken })
	token = leadTok
	tl = rpc("tools/list", nil)
	for _, x := range tl["result"].(map[string]any)["tools"].([]any) {
		if x.(map[string]any)["name"] == "send_draft" || x.(map[string]any)["name"] == "decide" {
			t.Fatal("lead should not see send/decide")
		}
	}
	if out := callText("draft_message", map[string]any{"person": "احمد", "text": "x"}); !strings.Contains(out, "اجازه") {
		t.Fatalf("lead drafted outside line: %s", out)
	}

	// ---- dashboard pages ----
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar}
	resp, err := cl.PostForm(srv.URL+"/login", url.Values{"login": {"boss"}, "password": {"password123"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, p := range []string{"/", "/people", "/people?new=1", "/tasks", "/tasks?new=1", "/projects", "/plans", "/drafts", "/users", "/log"} {
		r, err := cl.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || !strings.Contains(string(b), "خروج") {
			t.Fatalf("%s: %d %s", p, r.StatusCode, shorten(string(b), 300))
		}
		if p == "/" {
			os.WriteFile(os.Getenv("RZ_DUMP"), b, 0o644)
			if !strings.Contains(string(b), "سالن ۸، ایستگاه ۱") {
				t.Fatalf("today page missing chain cell")
			}
		}
	}
	// bad login
	r2, _ := http.PostForm(srv.URL+"/login", url.Values{"login": {"boss"}, "password": {"nope"}})
	if r2.Request.URL.Query().Get("e") != "1" {
		t.Fatal("bad login accepted")
	}
	t.Logf("claude calls: %d", h.cl.calls)
}

func fmtAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestTokenSaving(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	a.Cfg.BatchSeconds = 30
	a.Cfg.TelegramFilesChat = -1001234567
	h.at("2026-11-04 17:00")
	os.WriteFile(a.Cfg.DataDir+"/s.json", []byte(`{"people":[
	 {"name":"احمد طهماسبی","rhythm":{"report":"16:30"}},{"name":"علیرضا حاجی سامی","channel":"bale","rhythm":{"report":"19:00"}},{"name":"مدیر","role":"admin"}],
	 "projects":[{"id":"khane","name":"نمایشگاه خانه‌تکانی"}],
	 "tasks":[{"person":"احمد طهماسبی","project":"نمایشگاه خانه‌تکانی","title":"پوستر خانه‌تکانی"},
	          {"person":"علیرضا حاجی سامی","title":"لایت‌باکس سنندج","kind":"metric","unit":"متر","daily_target":65,"total":518}]}`), 0o600)
	if _, err := seed(a, a.Cfg.DataDir+"/s.json"); err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	a.Store.View(func(d *Data) {
		for _, p := range d.People {
			codes[firstName(p)] = p.LinkCode
		}
	})
	h.text("telegram", chatAhmad, "/start "+codes["احمد"])
	h.text("bale", chatSami, "/start "+codes["علیرضا"])
	h.text("telegram", chatBoss, "/start "+codes["مدیر"])
	h.tg.take()

	// 1. a plain number needs no Claude
	h.text("bale", chatSami, "۴۸")
	ms := h.tg.take()
	if h.cl.calls != 0 || find(ms, chatSami, "۴۸ متر از ۶۵") == nil {
		t.Fatalf("fast path failed (calls=%d):\n%s", h.cl.calls, dump(ms))
	}

	// 2. a file goes to the channel (no download)
	a.handleUpdate(context.Background(), a.Msgr["telegram"], tgUpdate{Message: &tgMessage{MessageID: 77, Chat: tgChat{ID: chatAhmad},
		Document: &tgFile{FileID: "F1", FileName: "poster.pdf"}}})
	ms = h.tg.take()
	if find(ms, -1001234567, "copyMessage|📎 poster.pdf") == nil || find(ms, chatAhmad, "فایل رسید") == nil {
		t.Fatalf("file not copied to channel:\n%s", dump(ms))
	}

	// 3. two messages in a row -> one Claude call; delivery labels the file in the channel
	h.cl.parse = func(p string) map[string]any {
		if !strings.Contains(p, "پوستر تمام شد") || !strings.Contains(p, "فردا می‌روم سراغ بعدی") {
			t.Errorf("messages not merged: %s", p)
		}
		return map[string]any{"delivered": []any{map[string]any{"task_id": idFor(p, "پوستر خانه‌تکانی")}},
			"progress": []any{}, "metrics": []any{}, "blockers": []any{}}
	}
	a.Store.Do(func(d *Data) error { // messages are merged right after the evening question
		p := d.PersonByName("احمد طهماسبی")
		d.Day(p.ID, a.today(p)).Awaiting = "report"
		return nil
	})
	h.text("telegram", chatAhmad, "پوستر تمام شد")
	h.text("telegram", chatAhmad, "فردا می‌روم سراغ بعدی")
	if h.cl.calls != 0 {
		t.Fatal("should wait for the batch window")
	}
	a.FlushAll()
	ms = h.tg.take()
	if h.cl.calls != 1 {
		t.Fatalf("claude calls: %d", h.cl.calls)
	}
	if find(ms, -1001234567, "editMessageCaption|📎 poster.pdf\n✔ پوستر خانه‌تکانی\n📁 #نمایشگاه_خانه_تکانی · #زنجیره_ارزش") == nil {
		t.Fatalf("caption not updated:\n%s", dump(ms))
	}
	var link string
	a.Store.View(func(d *Data) {
		for _, x := range d.Tasks {
			if x.Title == "پوستر خانه‌تکانی" && len(x.Files) == 1 {
				link = a.fileLink(x.Files[0])
			}
		}
	})
	if !strings.HasPrefix(link, "https://t.me/c/1234567/") {
		t.Fatalf("channel link: %q", link)
	}

	// 4. usage recorded; budget cap turns Claude off
	var u Usage
	a.Store.View(func(d *Data) { u = *d.Usage[a.month()] })
	if u.Calls != 1 || u.In != 700 || u.Saved < 1 {
		t.Fatalf("usage: %+v", u)
	}
	a.Cfg.MonthlyBudgetUSD = 0.0001
	a.Tick() // the flag is refreshed on every tick
	if a.Claude.Enabled() {
		t.Fatal("budget cap not applied")
	}
}

func atoi(s string) int64 {
	var n int64
	fmt.Sscan(s, &n)
	return n
}

func TestBaleArchivedInTelegram(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	a.Cfg.TelegramFilesChat = -1009
	a.Cfg.ArchiveReports = true
	h.at("2026-11-04 19:05")
	os.WriteFile(a.Cfg.DataDir+"/s.json", []byte(`{"people":[{"name":"علیرضا حاجی سامی","line":"ساخت","channel":"bale","rhythm":{"report":"19:00"}}],
	 "tasks":[{"person":"علیرضا حاجی سامی","title":"نقشهٔ اجرایی سقف"}]}`), 0o600)
	seed(a, a.Cfg.DataDir+"/s.json")
	var code string
	a.Store.View(func(d *Data) { code = d.People[0].LinkCode })
	h.text("bale", chatSami, "/start "+code)
	h.tg.take()

	a.handleUpdate(context.Background(), a.Msgr["bale"], tgUpdate{Message: &tgMessage{MessageID: 5, Chat: tgChat{ID: chatSami},
		Document: &tgFile{FileID: "B1", FileName: "plan.pdf"}}})
	ms := h.tg.take()
	if find(ms, -1009, "sendDocument|botT|1405-08-13_علیرضا-حاجی-سامی_plan.pdf|📎 plan.pdf") == nil {
		t.Fatalf("bale file not archived in telegram:\n%s", dump(ms))
	}
	// nothing left on the server
	if ents, _ := os.ReadDir(a.Files + "/.tmp"); len(ents) != 0 {
		t.Fatalf("temp files left: %d", len(ents))
	}

	h.cl.parse = func(p string) map[string]any {
		return map[string]any{"delivered": []any{map[string]any{"task_id": idFor(p, "نقشهٔ اجرایی سقف")}}, "progress": []any{}, "metrics": []any{}, "blockers": []any{}}
	}
	h.text("bale", chatSami, "نقشهٔ سقف را تمام کردم، فایلش را فرستادم")
	ms = h.tg.take()
	if find(ms, -1009, "📝 #گزارش · #علیرضا_حاجی_سامی · #خط_ساخت") == nil || find(ms, -1009, "✔ نقشهٔ اجرایی سقف") == nil {
		t.Fatalf("report/caption not archived:\n%s", dump(ms))
	}
}

func TestBargozarUpload(t *testing.T) {
	h := newHarness(t)
	defer h.close()
	a := h.app
	a.Cfg.TelegramFilesChat = -1009

	// fake Bargozar panel
	var mu sync.Mutex
	logged, count := false, 0
	var created map[string]any
	bz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-BZ") != "1" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/login":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["user"] != "rooznegar" || b["pass"] != "pw" {
				w.WriteHeader(401)
				json.NewEncoder(w).Encode(map[string]string{"error": "رمز اشتباه"})
				return
			}
			logged = true
			http.SetCookie(w, &http.Cookie{Name: "s", Value: "ok", Path: "/"})
			w.Write([]byte(`{"user":"rooznegar","role":"user"}`))
		case "/api/req/create":
			if c, err := r.Cookie("s"); err != nil || c.Value != "ok" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"login"}`))
				return
			}
			json.NewDecoder(r.Body).Decode(&created)
			json.NewEncoder(w).Encode(map[string]any{"token": "T1", "url": "https://files.example/u/T1", "dir": created["sub"]})
		case "/api/project":
			files := []map[string]any{{"path": "other/x.jpg", "name": "x.jpg", "url": "https://files.example/f/harfe-no/other/x.jpg"}}
			if count > 0 {
				files = append(files, map[string]any{"path": "تحویل/پوستر-خانه‌تکانی/poster-final.psd", "name": "poster-final.psd", "url": "https://files.example/f/harfe-no/poster-final.psd"})
			}
			json.NewEncoder(w).Encode(map[string]any{"requests": []any{map[string]any{"token": "T1", "dir": "تحویل/پوستر-خانه‌تکانی", "count": count}}, "files": files})
		default:
			w.WriteHeader(404)
		}
	}))
	defer bz.Close()
	a.BZ = newBargozar(bz.URL, "rooznegar", "pw")

	h.at("2026-11-04 15:00")
	os.WriteFile(a.Cfg.DataDir+"/s.json", []byte(`{"people":[{"name":"احمد طهماسبی","line":"گرافیک"}],
	 "projects":[{"id":"khane","name":"نمایشگاه خانه‌تکانی","bargozar":"harfe-no"}],
	 "tasks":[{"person":"احمد طهماسبی","project":"نمایشگاه خانه‌تکانی","title":"پوستر خانه‌تکانی"},
	          {"person":"احمد طهماسبی","project":"نمایشگاه خانه‌تکانی","title":"اینفوگرافی خلاصه"}]}`), 0o600)
	seed(a, a.Cfg.DataDir+"/s.json")
	var code string
	a.Store.View(func(d *Data) { code = d.People[0].LinkCode })
	h.text("telegram", chatAhmad, "/start "+code)
	h.tg.take()

	h.text("telegram", chatAhmad, "لینک آپلود")
	ms := h.tg.take()
	ask := find(ms, chatAhmad, "مال کدام کار است")
	if ask == nil {
		t.Fatalf("no task choice:\n%s", dump(ms))
	}
	var posterID string
	a.Store.View(func(d *Data) {
		for _, x := range d.Tasks {
			if x.Title == "پوستر خانه‌تکانی" {
				posterID = x.ID
			}
		}
	})
	h.press("telegram", chatAhmad, "u:"+posterID)
	ms = h.tg.take()
	if find(ms, chatAhmad, "https://files.example/u/T1") == nil || !logged {
		t.Fatalf("no link:\n%s", dump(ms))
	}
	if created["p"] != "harfe-no" || created["sub"] != "تحویل/پوستر-خانه‌تکانی" {
		t.Fatalf("wrong request: %v", created)
	}

	// nothing yet
	a.CheckUploads(context.Background())
	if ms = h.tg.take(); len(ms) != 0 {
		t.Fatalf("premature: %s", dump(ms))
	}
	// file arrives
	mu.Lock()
	count = 1
	mu.Unlock()
	a.CheckUploads(context.Background())
	ms = h.tg.take()
	if find(ms, chatAhmad, "رسید ✔ ۱ فایل برای «پوستر خانه‌تکانی»") == nil {
		t.Fatalf("no arrival note:\n%s", dump(ms))
	}
	arch := find(ms, -1009, "📎 poster-final.psd")
	if arch == nil || !strings.Contains(arch.Text, "🔗 https://files.example/f/harfe-no/poster-final.psd") || !strings.Contains(arch.Text, "✔ پوستر خانه‌تکانی") {
		t.Fatalf("archive post wrong:\n%s", dump(ms))
	}
	// not announced twice
	a.CheckUploads(context.Background())
	if ms = h.tg.take(); len(ms) != 0 {
		t.Fatalf("duplicate: %s", dump(ms))
	}
	var files []string
	a.Store.View(func(d *Data) { files = d.Task(posterID).Files })
	if len(files) != 1 || a.fileLink(files[0]) != "https://files.example/f/harfe-no/poster-final.psd" {
		t.Fatalf("task files: %v", files)
	}
}
