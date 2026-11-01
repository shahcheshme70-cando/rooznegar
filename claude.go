package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ---------- Claude (Messages API) ----------

type Claude struct {
	Key     string
	URL     string
	Model   string
	HTTP    *http.Client
	Effort  string            // low: no thinking, short answers
	OnUsage func(in, out int) // token accounting
	Allow   func() bool       // monthly budget guard
}

func (c *Claude) Enabled() bool { return c != nil && c.Key != "" && (c.Allow == nil || c.Allow()) }

type cMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type cResp struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage struct {
		In  int `json:"input_tokens"`
		Out int `json:"output_tokens"`
	} `json:"usage"`
}

func (c *Claude) post(ctx context.Context, body map[string]any) (*cResp, error) {
	if c.Effort != "" {
		body["output_config"] = map[string]any{"effort": c.Effort}
		body["thinking"] = map[string]any{"type": "disabled"}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.URL, "/")+"/v1/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.Key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r cResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("claude: %s", shorten(string(raw), 200))
	}
	if r.Error != nil {
		return nil, errors.New("claude: " + r.Error.Message)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("claude: %s", resp.Status)
	}
	if c.OnUsage != nil {
		c.OnUsage(r.Usage.In, r.Usage.Out)
	}
	return &r, nil
}

// Text returns a short free-text completion.
func (c *Claude) Text(ctx context.Context, system, prompt string, maxTokens int) (string, error) {
	r, err := c.post(ctx, map[string]any{
		"model": c.Model, "max_tokens": maxTokens, "system": system,
		"messages": []cMsg{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range r.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// Tool forces one tool call and returns its input.
func (c *Claude) Tool(ctx context.Context, system, prompt string, tool map[string]any, maxTokens int) (json.RawMessage, error) {
	r, err := c.post(ctx, map[string]any{
		"model": c.Model, "max_tokens": maxTokens, "system": system,
		"tools":       []any{tool},
		"tool_choice": map[string]any{"type": "tool", "name": tool["name"]},
		"messages":    []cMsg{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, err
	}
	for _, b := range r.Content {
		if b.Type == "tool_use" {
			return b.Input, nil
		}
	}
	return nil, errors.New("claude: پاسخ بدون ابزار")
}

// ---------- report parsing ----------

type ParsedReport struct {
	Delivered []struct {
		TaskID string `json:"task_id"`
		Note   string `json:"note"`
	} `json:"delivered"`
	Progress []struct {
		TaskID string `json:"task_id"`
		Level  string `json:"level"`
		ETA    string `json:"eta"`
	} `json:"progress"`
	Metrics []struct {
		TaskID string  `json:"task_id"`
		Value  float64 `json:"value"`
		Note   string  `json:"note"`
	} `json:"metrics"`
	Blockers []struct {
		TaskID string `json:"task_id"`
		Text   string `json:"text"`
		Red    bool   `json:"red"`
	} `json:"blockers"`
	Other       []string `json:"other_work"`
	Absent      bool     `json:"absent"`
	Clarify     string   `json:"clarify_question"`
	BotFeedback string   `json:"bot_feedback"`
	Kind        string   `json:"kind"`    // report | feedback | question | chat
	CallMe      string   `json:"call_me"` // a name they asked to be called
}

var reportTool = map[string]any{
	"name":        "record_report",
	"description": "ثبت گزارش",
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"delivered": arr(map[string]any{"task_id": sType}, "task_id"),
			"progress": arr(map[string]any{"task_id": sType,
				"level": map[string]any{"type": "string", "enum": []string{"start", "half", "near"}},
				"eta":   map[string]any{"type": "string", "enum": []string{"", "today", "tomorrow", "dayafter", "week", "nextweek"}}}, "task_id", "level"),
			"metrics":          arr(map[string]any{"task_id": sType, "value": map[string]any{"type": "number"}}, "task_id", "value"),
			"blockers":         arr(map[string]any{"task_id": sType, "text": sType, "red": map[string]any{"type": "boolean"}}, "text"),
			"other_work":       map[string]any{"type": "array", "items": sType},
			"absent":           map[string]any{"type": "boolean"},
			"clarify_question": sType,
			"bot_feedback":     sType,
			"kind":             map[string]any{"type": "string", "enum": []string{"report", "answer", "feedback", "question", "chat"}},
			"call_me":          sType,
		},
		"required": []string{"kind", "delivered", "progress", "metrics", "blockers"},
	},
}

var sType = map[string]any{"type": "string"}

func arr(props map[string]any, req ...string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": props, "required": req}}
}

const reportSystem = `پیام یک نیرو را به کارهای باز خودش وصل کن و با record_report ثبت کن.
delivered: کار کامل تحویل شد. progress: کار چندروزه جلو رفت (start/half/near؛ eta فقط اگر خودش گفت). metrics: عدد امروز کارهای عددی.
blockers: چیزی که کار را نگه داشته (red اگر امروز یا فردا کار کسی را متوقف می‌کند). other_work: کار خارج از فهرست. absent: امروز نبوده.
clarify_question: حداکثر یک سؤال کوتاه، فقط اگر بدون آن ثبت ممکن نیست.
kind: answer = جواب یا ادامهٔ جوابِ «آخرین پیام مدیر» (اگر در ورودی آمده)؛ report = از کار خودش می‌گوید (انجام داد، دارد انجام می‌دهد، نبود، گیر دارد)؛ feedback = نظر یا گلایه دربارهٔ خود ربات؛ question = سؤال یا درخواستی که باید به مدیر برسد؛ chat = احوال‌پرسی، شوخی یا حرفی که نه گزارش است نه سؤال. اگر کاری ثبت نشده، فقط kind و call_me و bot_feedback مهم است.
call_me: اگر خواسته با اسم یا لقب خاصی صدایش کنند، همان اسم.
bot_feedback: فقط اگر دربارهٔ خود ربات حرفی زده (ساعت یا تعداد پیام‌ها، لحن، سؤال‌ها، مزاحمت، پیشنهاد)، همان را کوتاه بنویس.
قاعده‌ها:
- فقط task_id هایی را که در فهرست آمده به کار ببر. اگر چیزی به هیچ کاری نمی‌خورد، در other_work بگذار.
- «تمام شد / تحویل دادم / فرستادم» یعنی delivered. اگر فقط بخشی از کار را گفته (مثلاً ۲ از ۴ تابلو)، آن کار delivered نیست؛ progress است.
- برای کارهای metric عدد را در metrics بگذار (واحد کار را ببین).
- گره را فقط وقتی ثبت کن که خودش گفته چیزی کار را نگه داشته یا کم است.
- حدس نزن. اگر مطمئن نیستی، ننویس. سؤال روشن‌کننده فقط وقتی که واقعاً لازم است.
- اسم‌ها ممکن است از تبدیل صوت به متن اشتباه آمده باشند؛ با فهرست تیم و کارها تطبیق بده.`

func (c *Claude) ParseReport(ctx context.Context, personName string, team []string, tasks []*Task, d *Data, today, pendingQ, text string) (*ParsedReport, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "نیرو: %s · %s\nتیم: %s\nکارها:\n", personName, today, strings.Join(team, "، "))
	if len(tasks) == 0 {
		sb.WriteString("(کاری ثبت نشده)\n")
	}
	for _, t := range tasks {
		proj := ""
		if p := d.Project(t.ProjectID); p != nil {
			proj = p.Name
		}
		extra := ""
		if t.Kind == "metric" {
			extra = fmt.Sprintf(" (عددی، %s)", t.Unit)
		} else if t.Kind == "multi" {
			extra = " (چندروزه)"
		}
		if proj != "" && !strings.Contains(t.Title, proj) {
			proj = " | " + proj
		} else {
			proj = ""
		}
		fmt.Fprintf(&sb, "- task_id=%s | %s%s%s\n", t.ID, t.Title, proj, extra)
	}
	if pendingQ != "" {
		fmt.Fprintf(&sb, "آخرین پیام مدیر به او: «%s»\n", shorten(pendingQ, 300))
	}
	fmt.Fprintf(&sb, "پیام:\n<<<\n%s\n>>>", text)
	raw, err := c.Tool(ctx, reportSystem, sb.String(), reportTool, 500)
	if err != nil {
		return nil, err
	}
	var p ParsedReport
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---------- speech to text ----------

type STT struct {
	URL   string
	Key   string
	Model string
	HTTP  *http.Client
}

func (s *STT) Enabled() bool { return s != nil && s.URL != "" }

func (s *STT) Transcribe(ctx context.Context, path, hint string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", filepath.Base(path))
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	w.WriteField("model", s.Model)
	w.WriteField("language", "fa")
	if hint != "" {
		w.WriteField("prompt", hint)
	}
	w.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", s.URL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if s.Key != "" {
		req.Header.Set("Authorization", "Bearer "+s.Key)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("تبدیل صوت: %s %s", resp.Status, shorten(string(raw), 150))
	}
	var r struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return strings.TrimSpace(r.Text), nil
}
