package main

import (
	"encoding/json"
	"errors"
	"os"
)

type Config struct {
	Listen  string `json:"listen"`   // 127.0.0.1:8095 (behind nginx)
	BaseURL string `json:"base_url"` // https://rooznegar.example.ir
	DataDir string `json:"data_dir"`

	TelegramToken string `json:"telegram_token"`
	TelegramAPI   string `json:"telegram_api"`
	BaleToken     string `json:"bale_token"`
	BaleAPI       string `json:"bale_api"`

	AnthropicKey string `json:"anthropic_key"`
	AnthropicURL string `json:"anthropic_url"`
	Model        string `json:"model"`  // fast model for parsing
	Effort       string `json:"effort"` // low (default) | medium; thinking is turned off

	// Speech to text: any OpenAI-compatible /v1/audio/transcriptions endpoint
	// (OpenAI Whisper, Groq, or a local faster-whisper server). Empty = off.
	STTURL   string `json:"stt_url"`
	STTKey   string `json:"stt_key"`
	STTModel string `json:"stt_model"`

	// Files: copied into a private channel/group where the bot is admin
	// (e.g. -1001234567890). Empty = keep on the server.
	TelegramFilesChat int64 `json:"telegram_files_chat"`
	BaleFilesChat     int64 `json:"bale_files_chat"`
	ArchiveReports    bool  `json:"archive_reports"` // also post each report's text to the Telegram channel

	// Bargozar panel (same server): a normal panel user made for Rooznegar
	BargozarURL  string `json:"bargozar_url"`
	BargozarUser string `json:"bargozar_user"`
	BargozarPass string `json:"bargozar_pass"`

	// Token budget
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd"` // 0 = no cap
	PriceInPerM      float64 `json:"price_in_per_m"`     // USD per million input tokens
	PriceOutPerM     float64 `json:"price_out_per_m"`
	NoonAI           bool    `json:"noon_ai"`       // compose noon message with Claude (else template)
	BatchSeconds     int     `json:"batch_seconds"` // merge messages sent within this window
	MaxContextTasks  int     `json:"max_context_tasks"`

	ManagerTZ       string `json:"manager_tz"`
	UpdateRepo      string `json:"update_repo"`  // owner/repo on GitHub with releases
	UpdateToken     string `json:"update_token"` // read-only token for a private repo
	UpdateAPI       string `json:"update_api"`
	SummaryTime     string `json:"summary_time"`    // nightly summary for admins, manager tz
	PlanDraftTime   string `json:"plan_draft_time"` // drafts for tomorrow, manager tz
	UnapprovedPlan  string `json:"unapproved_plan"` // wait | send
	MaxPlanItems    int    `json:"max_plan_items"`
	EditWindowMin   int    `json:"edit_window_min"`
	FollowupReaskH  int    `json:"followup_reask_hours"`
	BelowTargetDays int    `json:"below_target_days"` // consecutive days below target -> approval
}

func defaultConfig() Config {
	return Config{
		Listen:           "127.0.0.1:8095",
		DataDir:          "/var/lib/rooznegar",
		TelegramAPI:      "https://api.telegram.org",
		BaleAPI:          "https://tapi.bale.ai",
		AnthropicURL:     "https://api.anthropic.com",
		Model:            "claude-haiku-5-5",
		Effort:           "low",
		STTModel:         "whisper-1",
		ManagerTZ:        "Asia/Tehran",
		UpdateAPI:        "https://api.github.com",
		SummaryTime:      "20:45",
		PlanDraftTime:    "20:30",
		UnapprovedPlan:   "send",
		MaxPlanItems:     3,
		EditWindowMin:    10,
		FollowupReaskH:   24,
		BelowTargetDays:  2,
		MonthlyBudgetUSD: 5,
		PriceInPerM:      0.10, // Claude Haiku 5.5
		PriceOutPerM:     0.50,
		BatchSeconds:     15,
		MaxContextTasks:  10,
	}
}

func LoadConfig(path string) (Config, error) {
	c := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, errors.New("config.json نامعتبر است: " + err.Error())
	}
	d := defaultConfig()
	if c.TelegramAPI == "" {
		c.TelegramAPI = d.TelegramAPI
	}
	if c.BaleAPI == "" {
		c.BaleAPI = d.BaleAPI
	}
	if c.AnthropicURL == "" {
		c.AnthropicURL = d.AnthropicURL
	}
	if c.Model == "" {
		c.Model = d.Model
	}
	if c.MaxPlanItems <= 0 {
		c.MaxPlanItems = d.MaxPlanItems
	}
	if c.FollowupReaskH <= 0 {
		c.FollowupReaskH = d.FollowupReaskH
	}
	if c.BelowTargetDays <= 0 {
		c.BelowTargetDays = d.BelowTargetDays
	}
	if c.UnapprovedPlan != "wait" {
		c.UnapprovedPlan = "send"
	}
	if c.SummaryTime == "" {
		c.SummaryTime = d.SummaryTime
	}
	if c.PlanDraftTime == "" {
		c.PlanDraftTime = d.PlanDraftTime
	}
	if c.ManagerTZ == "" {
		c.ManagerTZ = d.ManagerTZ
	}
	if c.UpdateAPI == "" {
		c.UpdateAPI = d.UpdateAPI
	}
	if c.PriceInPerM <= 0 {
		c.PriceInPerM = d.PriceInPerM
	}
	if c.PriceOutPerM <= 0 {
		c.PriceOutPerM = d.PriceOutPerM
	}
	if c.BatchSeconds > 20 { // longer waits feel like a slow bot
		c.BatchSeconds = 15
	}
	if c.BatchSeconds < 0 {
		c.BatchSeconds = 0
	}
	if c.MaxContextTasks <= 0 {
		c.MaxContextTasks = d.MaxContextTasks
	}
	if c.EditWindowMin <= 0 {
		c.EditWindowMin = d.EditWindowMin
	}
	return c, nil
}
