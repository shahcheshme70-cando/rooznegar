package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ---------- data model ----------

// Rhythm: local times "HH:MM" for each daily touchpoint. Empty = off.
type Rhythm struct {
	Plan   string `json:"plan"`   // morning plan, e.g. 07:00
	Nudge  string `json:"nudge"`  // start nudge if not started, e.g. 09:40
	Noon   string `json:"noon"`   // connection message, e.g. 12:30
	Report string `json:"report"` // end-of-day report, e.g. 16:30
	Off    []int  `json:"off"`    // weekdays off (0=Sunday ... 5=Friday, 6=Saturday)
}

type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Channel  string `json:"channel"` // telegram | bale
	ChatID   int64  `json:"chat_id"`
	LinkCode string `json:"link_code"`
	TZ       string `json:"tz"`
	Rhythm   Rhythm `json:"rhythm"`
	Role     string `json:"role"` // staff | lead | finance | senior | admin
	Line     string `json:"line"` // خط کاری (گرافیک، ساخت، ...)
	Notes    string `json:"notes"`
	Active   bool   `json:"active"`
	// DailyValue: expected value created per working day (million toman), for load and value tracking
	DailyValue float64 `json:"daily_value"`
	NoTune     bool    `json:"no_tune"` // the bot must not adjust this person's rhythm by itself
	Nick       string  `json:"nick"`    // what they asked to be called
	// CC: ids of people who get a copy of this person's reports and of whatever the bot relays from them to managers
	CC []string `json:"cc,omitempty"`
}

type Project struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Sub      string `json:"sub"`
	Order    int    `json:"order"`
	Active   bool   `json:"active"`
	Bargozar string `json:"bargozar"` // project name in Bargozar (e.g. harfe-no)
}

// Task kinds:
//
//	single: one deliverable, done in a day or so
//	multi:  multi-day work; progress asked as level + ETA
//	metric: daily number against a target (e.g. Sami: m² per day)
type Task struct {
	ID          string     `json:"id"`
	PersonID    string     `json:"person_id"`
	ProjectID   string     `json:"project_id"`
	Title       string     `json:"title"`
	Detail      string     `json:"detail"`
	Cycle       string     `json:"cycle"` // market | value | cash
	Kind        string     `json:"kind"`  // single | multi | metric
	Priority    int        `json:"priority"`
	Status      string     `json:"status"` // open | done | dropped
	Ready       bool       `json:"ready"`  // inputs from the previous link have arrived
	Progress    string     `json:"progress"`
	ETA         string     `json:"eta"` // YYYY-MM-DD
	ETAHistory  []string   `json:"eta_history"`
	Unit        string     `json:"unit"`
	DailyTarget float64    `json:"daily_target"`
	Total       float64    `json:"total"`
	DoneQty     float64    `json:"done_qty"`
	Value       float64    `json:"value"`   // contract value of this piece of work (million toman)
	After       string     `json:"after"`   // task id of the previous link in the chain
	Private     bool       `json:"private"` // finance: admin + finance only
	Files       []string   `json:"files"`
	CreatedAt   time.Time  `json:"created_at"`
	DoneAt      *time.Time `json:"done_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type Plan struct {
	ID         string     `json:"id"`
	PersonID   string     `json:"person_id"`
	Date       string     `json:"date"` // person-local YYYY-MM-DD
	TaskIDs    []string   `json:"task_ids"`
	Note       string     `json:"note"`
	Status     string     `json:"status"` // draft | approved | sent | skipped
	ApprovedBy string     `json:"approved_by"`
	CreatedAt  time.Time  `json:"created_at"`
	SentAt     *time.Time `json:"sent_at"`
}

type Report struct {
	ID       string          `json:"id"`
	PersonID string          `json:"person_id"`
	Date     string          `json:"date"`
	Text     string          `json:"text"`
	Voice    bool            `json:"voice"`
	Parsed   json.RawMessage `json:"parsed"`
	At       time.Time       `json:"at"`
}

type MetricEntry struct {
	TaskID string    `json:"task_id"`
	Date   string    `json:"date"`
	Value  float64   `json:"value"`
	Note   string    `json:"note"`
	At     time.Time `json:"at"`
}

type Blocker struct {
	ID       string    `json:"id"`
	PersonID string    `json:"person_id"`
	TaskID   string    `json:"task_id"`
	Text     string    `json:"text"`
	Status   string    `json:"status"` // open | resolved
	At       time.Time `json:"at"`
}

type Approval struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // extra_crew | plan | generic
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	PersonID string    `json:"person_id"`
	TaskID   string    `json:"task_id"`
	Status   string    `json:"status"` // pending | approved | rejected | asked
	By       string    `json:"by"`
	At       time.Time `json:"at"`
}

// Draft: a message waiting for a human "send". Nothing reaches staff without it.
type Draft struct {
	ID         string     `json:"id"`
	ToPersonID string     `json:"to_person_id"`
	Text       string     `json:"text"`
	Followup   bool       `json:"followup"` // sent with done / doing / stuck buttons
	CreatedBy  string     `json:"created_by"`
	Status     string     `json:"status"` // draft | sent | cancelled
	At         time.Time  `json:"at"`
	SentAt     *time.Time `json:"sent_at"`
}

type Followup struct {
	ID         string     `json:"id"`
	PersonID   string     `json:"person_id"`
	Question   string     `json:"question"`
	Status     string     `json:"status"` // open | done | doing | stuck
	Answer     string     `json:"answer"`
	AskedAt    time.Time  `json:"asked_at"`
	Reasked    bool       `json:"reasked"`
	Escalated  bool       `json:"escalated"`
	MsgID      int64      `json:"msg_id"` // the bot message that asked it (to match replies)
	Plain      bool       `json:"plain"`  // a plain message without buttons: only replies are tracked
	By         string     `json:"by"`
	AnsweredAt *time.Time `json:"answered_at"`
}

// News: something the person should hear at their next touchpoint (handoffs).
type News struct {
	PersonID  string    `json:"person_id"`
	Text      string    `json:"text"`
	At        time.Time `json:"at"`
	Delivered bool      `json:"delivered"`
}

type Event struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	PersonID string    `json:"person_id"`
	TaskID   string    `json:"task_id"`
	Text     string    `json:"text"`
}

// Pending question in a conversation (asked one at a time).
type Question struct {
	Kind   string `json:"kind"` // progress | eta | blocker_text | clarify
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
}

type DayState struct {
	Date        string     `json:"date"`
	PlanSent    bool       `json:"plan_sent"`
	Started     bool       `json:"started"`
	NudgeSent   bool       `json:"nudge_sent"`
	NoonSent    bool       `json:"noon_sent"`
	ReportAsked bool       `json:"report_asked"`
	Reported    bool       `json:"reported"`
	Absent      bool       `json:"absent"`
	Queue       []Question `json:"queue"`
	Awaiting    string     `json:"awaiting"` // report | blocker_text | ""
	AwaitTask   string     `json:"await_task"`
	Files       []string   `json:"files"`
	LastReport  time.Time  `json:"last_report"`
	LastDone    []string   `json:"last_done"` // tasks marked done by the last report (edit window)
	Chased      bool       `json:"chased"`    // one automatic reminder after a missing report
	StartAt     time.Time  `json:"start_at"`  // first sign of starting the day
	ReportAt    time.Time  `json:"report_at"` // first report of the day
	AwaitAt     time.Time  `json:"await_at"`  // when Awaiting was set (stale waits expire)
	ReportAskAt time.Time  `json:"report_ask_at"`
	LastText    string     `json:"last_text"` // the last report text (for clarify answers)
	ClarifyQ    string     `json:"clarify_q"`
	Auto        int        `json:"auto"` // automatic (unrequested) messages sent today
}

type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Login    string `json:"login"`
	PassHash string `json:"pass_hash"`
	Role     string `json:"role"`      // admin | senior | lead | finance
	Line     string `json:"line"`      // for lead
	PersonID string `json:"person_id"` // their own Person (for Telegram approvals)
	MCPToken string `json:"mcp_token"`
}

type Data struct {
	People      []*Person            `json:"people"`
	Projects    []*Project           `json:"projects"`
	Tasks       []*Task              `json:"tasks"`
	Plans       []*Plan              `json:"plans"`
	Reports     []*Report            `json:"reports"`
	Metrics     []*MetricEntry       `json:"metrics"`
	Blockers    []*Blocker           `json:"blockers"`
	Approvals   []*Approval          `json:"approvals"`
	Drafts      []*Draft             `json:"drafts"`
	Followups   []*Followup          `json:"followups"`
	News        []*News              `json:"news"`
	Events      []*Event             `json:"events"`
	Days        map[string]*DayState `json:"days"`
	Users       []*User              `json:"users"`
	Sessions    map[string]Session   `json:"sessions"`
	Offsets     map[string]int64     `json:"offsets"` // messenger update offsets
	Flags       map[string]string    `json:"flags"`   // once-only markers
	Usage       map[string]*Usage    `json:"usage"`   // per month "2026-11"
	Uploads     []*Upload            `json:"uploads"` // Bargozar upload links being watched
	Reminders   []*Reminder          `json:"reminders"`
	DayLogs     []*DayLog            `json:"day_logs"` // past days, for learning each person's rhythm
	Tunings     []*Tuning            `json:"tunings"`  // adjustments the bot made by itself
	Feedback    []*Feedback          `json:"feedback"` // what people said about the bot
	Broadcasts  []*Broadcast         `json:"broadcasts"`
	FileTickets []*FileTicket        `json:"file_tickets"`
	Relays      []*Relay             `json:"relays"` // messages to managers that they can answer with "reply"
}

// Relay: a message that reached a manager on behalf of a person; a "reply" to it goes back to that person.
type Relay struct {
	Chat     int64     `json:"chat"`
	MsgID    int64     `json:"msg_id"`
	PersonID string    `json:"person_id"`
	At       time.Time `json:"at"`
}

// DayLog: one finished day of one person.
type DayLog struct {
	PersonID    string    `json:"person_id"`
	Date        string    `json:"date"`
	PlanSent    bool      `json:"plan_sent"`
	StartAt     time.Time `json:"start_at"`
	NudgeSent   bool      `json:"nudge_sent"`
	ReportAsked bool      `json:"report_asked"`
	ReportAt    time.Time `json:"report_at"`
	Chased      bool      `json:"chased"`
	Absent      bool      `json:"absent"`
	Auto        int       `json:"auto"`
}

// Tuning: a change the bot made to someone's rhythm, reversible by the manager.
type Tuning struct {
	ID       string    `json:"id"`
	PersonID string    `json:"person_id"`
	Field    string    `json:"field"` // report | nudge | noon
	Old      string    `json:"old"`
	New      string    `json:"new"`
	Why      string    `json:"why"`
	At       time.Time `json:"at"`
	Undone   bool      `json:"undone"`
}

type Feedback struct {
	PersonID string    `json:"person_id"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
}

// Reminder: a message the manager asked to go to someone, now or at a set time.
type Reminder struct {
	ID       string     `json:"id"`
	PersonID string     `json:"person_id"`
	Text     string     `json:"text"`
	At       time.Time  `json:"at"`
	Followup bool       `json:"followup"`
	TaskID   string     `json:"task_id"`
	By       string     `json:"by"`
	SentAt   *time.Time `json:"sent_at"`
	Cancel   bool       `json:"cancel"`
}

type Usage struct {
	Calls int `json:"calls"`
	In    int `json:"in"`
	Out   int `json:"out"`
	Saved int `json:"saved"` // messages handled without Claude
}

type Session struct {
	UserID  string    `json:"user_id"`
	Expires time.Time `json:"expires"`
}

// ---------- store ----------

type Store struct {
	mu     sync.Mutex
	holder atomic.Int64 // goroutine holding mu (0 = none)
	path   string
	D      *Data
}

// ErrReentrant: code that holds the store lock asked for it again. Before 0.4.1 this
// froze the whole program (dashboard 504, bot silent). Now it fails loudly instead.
var ErrReentrant = errors.New("store: re-entrant lock")

var reentrantHits atomic.Int64 // tests fail if this is ever non-zero

func goid() int64 {
	var b [32]byte
	n := runtime.Stack(b[:], false) // "goroutine 123 [..."
	var id int64
	for _, c := range b[len("goroutine "):n] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + int64(c-'0')
	}
	return id
}

func (s *Store) lock() {
	g := goid()
	if s.holder.Load() == g {
		reentrantHits.Add(1)
		log.Printf("خطای برنامه: قفل تودرتو\n%s", debugStack())
		panic(ErrReentrant)
	}
	s.mu.Lock()
	s.holder.Store(g)
}

func (s *Store) unlock() {
	s.holder.Store(0)
	s.mu.Unlock()
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "data.json"), D: &Data{}}
	b, err := os.ReadFile(s.path)
	if err == nil {
		if err := json.Unmarshal(b, s.D); err != nil {
			return nil, errors.New("data.json خراب است: " + err.Error())
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.D.init()
	return s, nil
}

func (d *Data) init() {
	if d.Days == nil {
		d.Days = map[string]*DayState{}
	}
	if d.Sessions == nil {
		d.Sessions = map[string]Session{}
	}
	if d.Offsets == nil {
		d.Offsets = map[string]int64{}
	}
	if d.Flags == nil {
		d.Flags = map[string]string{}
	}
	if d.Usage == nil {
		d.Usage = map[string]*Usage{}
	}
	d.linkUsers()
}

// Do runs fn under the lock and saves if fn returns nil.
func (s *Store) Do(fn func(d *Data) error) error {
	s.lock()
	defer s.unlock()
	if err := fn(s.D); err != nil {
		return err
	}
	return s.saveLocked()
}

// View runs fn under the lock without saving.
func (s *Store) View(fn func(d *Data)) {
	s.lock()
	defer s.unlock()
	fn(s.D)
}

func (s *Store) saveLocked() error {
	s.D.trim()
	b, err := json.MarshalIndent(s.D, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, s.path)
}

// keep the file bounded
func (d *Data) trim() {
	if len(d.Relays) > 800 {
		d.Relays = d.Relays[len(d.Relays)-800:]
	}
	if len(d.DayLogs) > 4000 {
		d.DayLogs = d.DayLogs[len(d.DayLogs)-4000:]
	}
	if len(d.Events) > 3000 {
		d.Events = d.Events[len(d.Events)-3000:]
	}
	if len(d.News) > 500 {
		keep := d.News[:0]
		for _, n := range d.News {
			if !n.Delivered {
				keep = append(keep, n)
			}
		}
		d.News = keep
	}
	now := time.Now()
	for k, s := range d.Sessions {
		if now.After(s.Expires) {
			delete(d.Sessions, k)
		}
	}
}

// ---------- helpers ----------

func newID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newLinkCode() string {
	const digits = "23456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	return string(b)
}

func (d *Data) Person(id string) *Person {
	for _, p := range d.People {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (d *Data) PersonByChat(channel string, chat int64) *Person {
	for _, p := range d.People {
		if p.Channel == channel && p.ChatID == chat && chat != 0 {
			return p
		}
	}
	return nil
}

func (d *Data) PersonByName(name string) *Person {
	name = normName(name)
	for _, p := range d.People {
		if normName(p.Name) == name {
			return p
		}
	}
	for _, p := range d.People { // partial: first name
		if name != "" && (containsWord(normName(p.Name), name)) {
			return p
		}
	}
	return nil
}

func (d *Data) Project(id string) *Project {
	for _, p := range d.Projects {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (d *Data) ProjectByName(name string) *Project {
	name = normName(name)
	for _, p := range d.Projects {
		if normName(p.Name) == name || p.ID == name {
			return p
		}
	}
	for _, p := range d.Projects {
		if name != "" && containsWord(normName(p.Name), name) {
			return p
		}
	}
	return nil
}

func (d *Data) Task(id string) *Task {
	for _, t := range d.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (d *Data) OpenTasks(personID string) []*Task {
	var out []*Task
	for _, t := range d.Tasks {
		if t.PersonID == personID && t.Status == "open" {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return taskLess(out[i], out[j]) })
	return out
}

// ordering for plans: ready first, then in progress, then priority, then ETA
func taskLess(a, b *Task) bool {
	if a.Ready != b.Ready {
		return a.Ready
	}
	ap, bp := a.Progress != "" && a.Progress != "done", b.Progress != "" && b.Progress != "done"
	if ap != bp {
		return ap
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if a.ETA != b.ETA {
		if a.ETA == "" {
			return false
		}
		if b.ETA == "" {
			return true
		}
		return a.ETA < b.ETA
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

func (d *Data) PlanFor(personID, date string) *Plan {
	for i := len(d.Plans) - 1; i >= 0; i-- {
		p := d.Plans[i]
		if p.PersonID == personID && p.Date == date && p.Status != "skipped" {
			return p
		}
	}
	return nil
}

func (d *Data) Plan(id string) *Plan {
	for _, p := range d.Plans {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (d *Data) Approval(id string) *Approval {
	for _, a := range d.Approvals {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (d *Data) Draft(id string) *Draft {
	for _, x := range d.Drafts {
		if x.ID == id {
			return x
		}
	}
	return nil
}

func (d *Data) Followup(id string) *Followup {
	for _, x := range d.Followups {
		if x.ID == id {
			return x
		}
	}
	return nil
}

func (d *Data) User(id string) *User {
	for _, u := range d.Users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (d *Data) UserByLogin(login string) *User {
	for _, u := range d.Users {
		if u.Login == login {
			return u
		}
	}
	return nil
}

func (d *Data) UserByToken(tok string) *User {
	if tok == "" {
		return nil
	}
	for _, u := range d.Users {
		if u.MCPToken == tok {
			return u
		}
	}
	return nil
}

func (d *Data) Day(personID, date string) *DayState {
	s := d.Days[personID]
	if s != nil && s.Date != date && s.Date < date {
		d.DayLogs = append(d.DayLogs, &DayLog{PersonID: personID, Date: s.Date, PlanSent: s.PlanSent, StartAt: s.StartAt,
			NudgeSent: s.NudgeSent, ReportAsked: s.ReportAsked, ReportAt: s.ReportAt, Chased: s.Chased, Absent: s.Absent, Auto: s.Auto})
	}
	if s == nil || s.Date != date {
		s = &DayState{Date: date}
		d.Days[personID] = s
	}
	return s
}

func (d *Data) Log(kind, personID, taskID, text string) {
	d.Events = append(d.Events, &Event{At: time.Now(), Kind: kind, PersonID: personID, TaskID: taskID, Text: text})
}

func (d *Data) MetricsFor(taskID string) []*MetricEntry {
	var out []*MetricEntry
	for _, m := range d.Metrics {
		if m.TaskID == taskID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func (d *Data) Admins() []*Person {
	var out []*Person
	for _, p := range d.People {
		if p.Active && p.ChatID != 0 && (p.Role == "admin") {
			out = append(out, p)
		}
	}
	return out
}

// people who receive the nightly summary
func (d *Data) SummaryReceivers() []*Person {
	var out []*Person
	for _, p := range d.People {
		if p.Active && p.ChatID != 0 && (p.Role == "admin" || p.Role == "senior") {
			out = append(out, p)
		}
	}
	return out
}

func debugStack() string { return string(debug.Stack()) }

// linkUsers ties dashboard users to their person in the bot (by name, or the only admin),
// so answers and reports reach them in Telegram.
func (d *Data) linkUsers() {
	for _, u := range d.Users {
		if u.PersonID != "" && d.Person(u.PersonID) != nil {
			continue
		}
		u.PersonID = ""
		if p := d.PersonByName(u.Name); p != nil && normName(p.Name) == normName(u.Name) {
			u.PersonID = p.ID
			continue
		}
		var match []*Person
		for _, p := range d.People {
			if p.Role == u.Role && (u.Role == "admin" || u.Role == "senior" || u.Role == "finance") {
				match = append(match, p)
			}
		}
		if len(match) == 1 {
			u.PersonID = match[0].ID
		}
	}
}
