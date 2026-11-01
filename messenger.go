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
	"time"
)

// Messenger talks to the Telegram Bot API. Bale's bot API is the same
// protocol on another host, so one client serves both.
type Messenger struct {
	Name  string // telegram | bale
	Base  string // https://api.telegram.org
	Token string
	HTTP  *http.Client
}

type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

type tgUpdate struct {
	UpdateID int64       `json:"update_id"`
	Message  *tgMessage  `json:"message"`
	Callback *tgCallback `json:"callback_query"`
}

type tgMessage struct {
	MessageID int64      `json:"message_id"`
	Chat      tgChat     `json:"chat"`
	From      *tgUser    `json:"from"`
	Text      string     `json:"text"`
	Caption   string     `json:"caption"`
	Voice     *tgFile    `json:"voice"`
	Audio     *tgFile    `json:"audio"`
	Document  *tgFile    `json:"document"`
	Photo     []tgFile   `json:"photo"`
	ReplyTo   *tgMessage `json:"reply_to_message"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
	Duration int    `json:"duration"`
}

type tgCallback struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgResp struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func (m *Messenger) Enabled() bool { return m != nil && m.Token != "" }

func (m *Messenger) call(ctx context.Context, method string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/bot%s/%s", m.Base, m.Token, method), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r tgResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s %s: %v", m.Name, method, err)
	}
	if !r.OK {
		return fmt.Errorf("%s %s: %s", m.Name, method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func keyboard(rows [][]Button) any {
	if len(rows) == 0 {
		return nil
	}
	return map[string]any{"inline_keyboard": rows}
}

func (m *Messenger) Send(ctx context.Context, chat int64, text string, rows [][]Button) (int64, error) {
	body := map[string]any{"chat_id": chat, "text": text}
	if kb := keyboard(rows); kb != nil {
		body["reply_markup"] = kb
	}
	var msg tgMessage
	err := m.call(ctx, "sendMessage", body, &msg)
	return msg.MessageID, err
}

// Edit replaces a message's text and buttons (used after a button is tapped).
func (m *Messenger) Edit(ctx context.Context, chat, msgID int64, text string, rows [][]Button) error {
	body := map[string]any{"chat_id": chat, "message_id": msgID, "text": text}
	if kb := keyboard(rows); kb != nil {
		body["reply_markup"] = kb
	} else {
		body["reply_markup"] = map[string]any{"inline_keyboard": [][]Button{}}
	}
	return m.call(ctx, "editMessageText", body, nil)
}

// Copy re-posts a user's message (with its file) into another chat without
// downloading it, so large files work too. Returns the new message id.
func (m *Messenger) Copy(ctx context.Context, to, from, msgID int64, caption string) (int64, error) {
	var r struct {
		MessageID int64 `json:"message_id"`
	}
	err := m.call(ctx, "copyMessage", map[string]any{"chat_id": to, "from_chat_id": from, "message_id": msgID, "caption": caption}, &r)
	if err != nil { // some servers lack copyMessage: forward instead
		var fm tgMessage
		if err2 := m.call(ctx, "forwardMessage", map[string]any{"chat_id": to, "from_chat_id": from, "message_id": msgID}, &fm); err2 == nil {
			return fm.MessageID, nil
		}
		return 0, err
	}
	return r.MessageID, nil
}

// Upload sends a local file as a document (keeps original quality).
func (m *Messenger) Upload(ctx context.Context, chat int64, path, caption string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() {
		w.WriteField("chat_id", fmt.Sprint(chat))
		if caption != "" {
			w.WriteField("caption", caption)
		}
		fw, err := w.CreateFormFile("document", filepath.Base(path))
		if err == nil {
			_, err = io.Copy(fw, f)
		}
		w.Close()
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/bot%s/sendDocument", m.Base, m.Token), pr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var r tgResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, err
	}
	if !r.OK {
		return 0, fmt.Errorf("%s sendDocument: %s", m.Name, r.Description)
	}
	var msg tgMessage
	json.Unmarshal(r.Result, &msg)
	return msg.MessageID, nil
}

func (m *Messenger) EditCaption(ctx context.Context, chat, msgID int64, caption string) error {
	return m.call(ctx, "editMessageCaption", map[string]any{"chat_id": chat, "message_id": msgID, "caption": caption}, nil)
}

func (m *Messenger) Answer(ctx context.Context, callbackID, text string) {
	m.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text}, nil)
}

func (m *Messenger) Updates(ctx context.Context, offset int64, timeout int) ([]tgUpdate, error) {
	var ups []tgUpdate
	err := m.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeout, "allowed_updates": []string{"message", "callback_query"}}, &ups)
	return ups, err
}

// Download saves a file sent to the bot into dir and returns its path.
func (m *Messenger) Download(ctx context.Context, fileID, dir, name string) (string, error) {
	var f struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := m.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return "", err
	}
	if f.FilePath == "" {
		return "", errors.New("فایل در دسترس ربات نیست (احتمالاً بیش از ۲۰ مگ)")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/file/bot%s/%s", m.Base, m.Token, f.FilePath), nil)
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("دانلود فایل: %s", resp.Status)
	}
	if name == "" {
		name = filepath.Base(f.FilePath)
	}
	name = safeName(name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err == nil {
		p = filepath.Join(dir, newID()+"-"+name)
	}
	out, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(resp.Body, 60<<20)); err != nil {
		return "", err
	}
	return p, nil
}

func safeName(s string) string {
	s = filepath.Base(s)
	out := []rune{}
	for _, r := range s {
		if r == '/' || r == '\\' || r == 0 || r == ':' {
			r = '_'
		}
		out = append(out, r)
	}
	if len(out) == 0 || string(out) == "." || string(out) == ".." {
		return "file"
	}
	return string(out)
}

func newHTTP(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }
