package main

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ctxUser struct{}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.loginPost)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("POST /mcp/{token}", a.mcpHandler)
	mux.HandleFunc("GET /mcp/{token}", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "method not allowed", 405) })

	auth := func(roles string, h func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			u := a.sessionUser(r)
			if u == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			if roles != "" && !strings.Contains(roles, u.Role) {
				http.Error(w, "دسترسی ندارید", http.StatusForbidden)
				return
			}
			if r.Method == "POST" && !sameOrigin(r) {
				http.Error(w, "درخواست نامعتبر", http.StatusForbidden)
				return
			}
			h(w, r, u)
		}
	}
	mux.HandleFunc("GET /{$}", auth("", a.todayPage))
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /people", auth("admin senior lead", a.peoplePage))
	mux.HandleFunc("POST /people/save", auth("admin", a.peopleSave))
	mux.HandleFunc("POST /people/{id}/code", auth("admin", a.peopleCode))
	mux.HandleFunc("GET /tasks", auth("", a.tasksPage))
	mux.HandleFunc("POST /tasks/save", auth("admin senior lead finance", a.tasksSave))
	mux.HandleFunc("POST /tasks/{id}/{act}", auth("admin senior lead finance", a.taskAct))
	mux.HandleFunc("GET /projects", auth("admin senior", a.projectsPage))
	mux.HandleFunc("POST /projects/save", auth("admin senior", a.projectsSave))
	mux.HandleFunc("GET /plans", auth("admin senior", a.plansPage))
	mux.HandleFunc("POST /plans/draft", auth("admin senior", a.plansDraft))
	mux.HandleFunc("POST /plans/{id}/save", auth("admin senior", a.planSave))
	mux.HandleFunc("POST /plans/{id}/approve", auth("admin", a.planApprove))
	mux.HandleFunc("GET /broadcast", auth("admin senior lead", a.broadcastPage))
	mux.HandleFunc("POST /broadcast", auth("admin senior lead", a.broadcastSend))
	mux.HandleFunc("POST /up/{token}", a.fileUpload)
	mux.HandleFunc("GET /drafts", auth("admin senior lead", a.draftsPage))
	mux.HandleFunc("POST /drafts/new", auth("admin senior lead", a.draftNew))
	mux.HandleFunc("POST /drafts/{id}/{act}", auth("admin senior", a.draftAct))
	mux.HandleFunc("POST /approvals/{id}/{act}", auth("admin", a.approvalAct))
	mux.HandleFunc("POST /blockers/{id}/resolve", auth("admin senior lead", a.blockerResolve))
	mux.HandleFunc("GET /users", auth("admin", a.usersPage))
	mux.HandleFunc("POST /users/save", auth("admin", a.usersSave))
	mux.HandleFunc("POST /users/{id}/token", auth("admin", a.userToken))
	mux.HandleFunc("GET /log", auth("admin senior", a.logPage))
	mux.HandleFunc("GET /files/{path...}", auth("admin senior lead", a.serveFile))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// ---------- sessions ----------

func (a *App) sessionUser(r *http.Request) *User {
	c, err := r.Cookie("rz")
	if err != nil {
		return nil
	}
	var u *User
	a.Store.View(func(d *Data) {
		s, ok := d.Sessions[c.Value]
		if ok && time.Now().Before(s.Expires) {
			if x := d.User(s.UserID); x != nil {
				cp := *x
				u = &cp
			}
		}
	})
	return u
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	render(w, "login", map[string]any{"Err": r.URL.Query().Get("e") != ""})
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	login, pw := strings.TrimSpace(r.FormValue("login")), r.FormValue("password")
	var tok string
	a.Store.Do(func(d *Data) error {
		u := d.UserByLogin(login)
		if u == nil || !checkPass(u.PassHash, pw) {
			return nil
		}
		tok = newToken()
		d.Sessions[tok] = Session{UserID: u.ID, Expires: time.Now().Add(30 * 24 * time.Hour)}
		return nil
	})
	if tok == "" {
		time.Sleep(700 * time.Millisecond)
		http.Redirect(w, r, "/login?e=1", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rz", Value: tok, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 3600})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("rz"); err == nil {
		a.Store.Do(func(d *Data) error { delete(d.Sessions, c.Value); return nil })
	}
	http.SetCookie(w, &http.Cookie{Name: "rz", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func back(w http.ResponseWriter, r *http.Request, def string) {
	to := r.FormValue("back")
	if to == "" || !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") {
		to = def
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---------- view models ----------

type vCell struct {
	Color, Text, Who string
}

type vChainRow struct {
	Project *Project
	Cells   []vCell
}

type vPersonToday struct {
	P       *Person
	State   string
	StateOK bool
	Items   []string
	Metric  string
	Pct     int
	Bar     string
}

type vItem struct {
	ID, Title, Sub string
}

func (a *App) chains(d *Data, u *User) []vChainRow {
	var rows []vChainRow
	projects := append([]*Project(nil), d.Projects...)
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Order < projects[j].Order })
	openBlock := map[string]bool{}
	for _, b := range d.Blockers {
		if b.Status == "open" && b.TaskID != "" {
			openBlock[b.TaskID] = true
		}
	}
	for _, pr := range projects {
		if !pr.Active {
			continue
		}
		row := vChainRow{Project: pr}
		any := false
		for _, cyc := range []string{"market", "value", "cash"} {
			var open, done []*Task
			for _, t := range d.Tasks {
				if t.ProjectID != pr.ID || t.Cycle != cyc || !canSeeTask(u, d, t) {
					continue
				}
				if t.Status == "open" {
					open = append(open, t)
				} else if t.Status == "done" {
					done = append(done, t)
				}
			}
			sort.SliceStable(open, func(i, j int) bool { return taskLess(open[i], open[j]) })
			c := vCell{Color: "none", Text: "بی‌صاحب"}
			switch {
			case len(open) > 0:
				t := open[0]
				any = true
				c = vCell{Color: "wip", Text: t.Title, Who: firstName(d.Person(t.PersonID))}
				if t.Progress != "" && t.Progress != "done" {
					c.Who += " · " + progressLabels[t.Progress]
				}
				if t.ETA != "" {
					c.Who += " · موعد " + faDay(t.ETA)
				}
				for _, o := range open {
					if openBlock[o.ID] {
						c.Color = "red"
						c.Text = o.Title
						c.Who = firstName(d.Person(o.PersonID)) + " · گره دارد"
						break
					}
				}
				if len(open) > 1 {
					c.Who += " · " + fa(len(open)-1) + " حلقهٔ دیگر"
				}
			case len(done) > 0:
				any = true
				c = vCell{Color: "ok", Text: done[len(done)-1].Title, Who: "بسته شد"}
			}
			row.Cells = append(row.Cells, c)
		}
		if any || u.Role == "admin" {
			rows = append(rows, row)
		}
	}
	return rows
}

func (a *App) peopleToday(d *Data, u *User) []vPersonToday {
	var out []vPersonToday
	for _, p := range sortedPeople(d) {
		if !p.Active || !canSeePerson(u, p) || p.Role == "admin" {
			continue
		}
		ln := a.localNow(p)
		date := dayKey(ln)
		ds := d.Day(p.ID, date)
		v := vPersonToday{P: p, Bar: "ok"}
		switch {
		case p.ChatID == 0:
			v.State = "ربات وصل نشده"
		case isOff(p, ln):
			v.State, v.StateOK = "تعطیل", true
		case ds.Absent:
			v.State = "امروز نیست"
		case ds.Reported:
			v.State, v.StateOK = "گزارش داد", true
		case ds.Started:
			v.State, v.StateOK = "سر کار", true
		case ds.PlanSent:
			v.State = "برنامه رفت، شروع نزده"
		default:
			v.State = "هنوز پیامی نرفته"
		}
		done, total := 0, 0
		for _, t := range a.todaysTasks(d, p, date) {
			if !canSeeTask(u, d, t) {
				continue
			}
			total++
			s := t.Title
			if t.Status == "done" {
				done++
				s = "✔ " + s
			} else if t.Progress != "" {
				s += " · " + progressLabels[t.Progress]
			}
			if t.Kind == "metric" {
				for _, m := range d.Metrics {
					if m.TaskID == t.ID && m.Date == date {
						v.Metric = fa(m.Value) + " از " + fa(t.DailyTarget) + " " + t.Unit
						if t.DailyTarget > 0 {
							v.Pct = int(m.Value / t.DailyTarget * 100)
							if m.Value < t.DailyTarget {
								v.Bar = "red"
							}
						}
					}
				}
			}
			v.Items = append(v.Items, s)
		}
		for _, t := range d.Tasks {
			if t.PersonID == p.ID && t.DoneAt != nil && dayKey(t.DoneAt.In(ln.Location())) == date && !contains(v.Items, "✔ "+t.Title) && canSeeTask(u, d, t) {
				v.Items = append(v.Items, "✔ "+t.Title)
				done++
				total++
			}
		}
		if v.Metric == "" && total > 0 {
			v.Pct = done * 100 / total
			if !ds.Reported {
				v.Bar = "wip"
			}
		}
		if v.Pct > 100 {
			v.Pct = 100
		}
		out = append(out, v)
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- pages ----------

func (a *App) base(u *User, page string) map[string]any {
	mn := a.mgrNow()
	return map[string]any{"U": u, "Page": page, "Today": faDateYear(mn), "RoleLabel": roleLabels[u.Role]}
}

func (a *App) todayPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "today")
	a.Store.View(func(d *Data) {
		mn := a.mgrNow()
		dayStart := time.Date(mn.Year(), mn.Month(), mn.Day(), 0, 0, 0, 0, mn.Location())
		people := a.peopleToday(d, u)
		reported, expected := 0, 0
		for _, v := range people {
			if v.P.ChatID != 0 && v.State != "تعطیل" && v.State != "امروز نیست" {
				expected++
				if v.State == "گزارش داد" {
					reported++
				}
			}
		}
		var blockers []vItem
		for _, b := range d.Blockers {
			if b.Status != "open" {
				continue
			}
			if t := d.Task(b.TaskID); t != nil && !canSeeTask(u, d, t) {
				continue
			}
			if p := d.Person(b.PersonID); p != nil && !canSeePerson(u, p) {
				continue
			}
			sub := pname(d, b.PersonID) + " · " + faDate(b.At.In(mn.Location()))
			if t := d.Task(b.TaskID); t != nil {
				sub = t.Title + " · " + sub
			}
			blockers = append(blockers, vItem{ID: b.ID, Title: b.Text, Sub: sub})
		}
		var hand []string
		for _, e := range d.Events {
			if e.Kind == "handoff" && !e.At.Before(dayStart) {
				hand = append(hand, e.Text)
			}
		}
		var approvals []vItem
		for _, ap := range d.Approvals {
			if ap.Status == "pending" || ap.Status == "asked" {
				approvals = append(approvals, vItem{ID: ap.ID, Title: ap.Title, Sub: ap.Detail + " · " + pname(d, ap.PersonID)})
			}
		}
		var plans []vItem
		for _, pl := range d.Plans {
			if pl.Status == "draft" && pl.Date >= dayKey(mn) {
				plans = append(plans, vItem{ID: pl.ID, Title: pname(d, pl.PersonID) + " · " + relDay(pl.Date, dayKey(mn)), Sub: strings.ReplaceAll(a.planLines(d, pl), "\n", " · ")})
			}
		}
		var drafts []vItem
		for _, dr := range d.Drafts {
			if dr.Status == "draft" {
				drafts = append(drafts, vItem{ID: dr.ID, Title: "به " + pname(d, dr.ToPersonID), Sub: dr.Text})
			}
		}
		var follows []vItem
		for _, f := range d.Followups {
			if f.Status == "open" || f.Status == "doing" || f.Status == "stuck" {
				st := map[string]string{"open": "بی‌جواب", "doing": "در جریان", "stuck": "گیر دارد"}[f.Status]
				if f.Answer != "" {
					st += ": " + f.Answer
				}
				follows = append(follows, vItem{ID: f.ID, Title: pname(d, f.PersonID) + ": " + f.Question, Sub: st + " · " + faDate(f.AskedAt.In(mn.Location()))})
			}
		}
		// time split this week by cycle (done + reported tasks)
		var cnt = map[string]int{}
		weekAgo := a.Now().AddDate(0, 0, -7)
		for _, t := range d.Tasks {
			if t.UpdatedAt.After(weekAgo) && canSeeTask(u, d, t) {
				cnt[t.Cycle]++
			}
		}
		tot := cnt["market"] + cnt["value"] + cnt["cash"]
		split := map[string]int{}
		if tot > 0 {
			for k, v := range cnt {
				split[k] = v * 100 / tot
			}
		}
		m["Reported"], m["Expected"] = reported, expected
		m["Blockers"], m["Hand"], m["Approvals"], m["Plans"], m["Drafts"], m["Follows"] = blockers, hand, approvals, plans, drafts, follows
		m["Chains"], m["People"] = a.chains(d, u), people
		m["Split"], m["SplitAny"] = split, tot > 0
		m["Waiting"] = len(approvals) + len(plans) + len(drafts)
	})
	render(w, "today", m)
}

func (a *App) peoplePage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "people")
	a.Store.View(func(d *Data) {
		var ps []*Person
		for _, p := range sortedPeople(d) {
			if canSeePerson(u, p) {
				ps = append(ps, p)
			}
		}
		m["People"] = ps
		edit := d.Person(r.URL.Query().Get("edit"))
		if edit == nil && r.URL.Query().Get("new") != "" {
			edit = &Person{Channel: "telegram", TZ: "Asia/Tehran", Role: "staff", Active: true, Rhythm: Rhythm{Report: "17:00"}}
		}
		if edit != nil {
			cp := *edit
			m["Edit"] = &cp
			m["OffSet"] = offSet(cp.Rhythm.Off)
			cc := map[string]bool{}
			for _, id := range cp.CC {
				cc[id] = true
			}
			m["CCSet"] = cc
		}
		m["Weekdays"] = []struct {
			N    int
			Name string
		}{{6, "شنبه"}, {0, "یکشنبه"}, {1, "دوشنبه"}, {2, "سه‌شنبه"}, {3, "چهارشنبه"}, {4, "پنجشنبه"}, {5, "جمعه"}}
		m["Roles"] = []string{"staff", "lead", "finance", "senior", "admin"}
		m["Lines"] = lines(d)
		m["BotNames"] = a.botNames()
	})
	render(w, "people", m)
}

func offSet(off []int) map[int]bool {
	s := map[int]bool{}
	for _, o := range off {
		s[o] = true
	}
	return s
}

func lines(d *Data) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range d.People {
		if p.Line != "" && !seen[p.Line] {
			seen[p.Line] = true
			out = append(out, p.Line)
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) botNames() map[string]bool {
	out := map[string]bool{}
	for k := range a.Msgr {
		out[k] = true
	}
	return out
}

func validHM(s string) string {
	s = latin(strings.TrimSpace(s))
	if _, ok := hm(s); ok {
		return s
	}
	return ""
}

func (a *App) peopleSave(w http.ResponseWriter, r *http.Request, u *User) {
	r.ParseForm()
	a.Store.Do(func(d *Data) error {
		p := d.Person(r.FormValue("id"))
		if p == nil {
			p = &Person{ID: newID(), LinkCode: newLinkCode()}
			d.People = append(d.People, p)
		}
		p.Name = strings.TrimSpace(r.FormValue("name"))
		if p.Name == "" {
			p.Name = "بی‌نام"
		}
		if ch := r.FormValue("channel"); ch == "telegram" || ch == "bale" {
			if ch != p.Channel && p.ChatID != 0 {
				p.ChatID, p.LinkCode = 0, newLinkCode() // channel changed: link again
			}
			p.Channel = ch
		}
		p.TZ = strings.TrimSpace(r.FormValue("tz"))
		if _, err := time.LoadLocation(p.TZ); err != nil || p.TZ == "" {
			p.TZ = "Asia/Tehran"
		}
		if _, ok := roleLabels[r.FormValue("role")]; ok {
			p.Role = r.FormValue("role")
		}
		p.Line = strings.TrimSpace(r.FormValue("line"))
		p.Notes = strings.TrimSpace(r.FormValue("notes"))
		p.DailyValue, _ = parseNum(r.FormValue("daily_value"))
		p.Nick = strings.TrimSpace(r.FormValue("nick"))
		p.NoTune = r.FormValue("no_tune") == "1"
		p.Active = r.FormValue("active") == "1"
		p.Rhythm.Plan, p.Rhythm.Nudge = validHM(r.FormValue("plan")), validHM(r.FormValue("nudge"))
		p.Rhythm.Noon, p.Rhythm.Report = validHM(r.FormValue("noon")), validHM(r.FormValue("report"))
		p.CC = nil
		for _, id := range r.Form["cc"] {
			if c := d.Person(id); c != nil && c.ID != p.ID {
				p.CC = append(p.CC, c.ID)
			}
		}
		p.Rhythm.Off = nil
		for _, v := range r.Form["off"] {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 6 {
				p.Rhythm.Off = append(p.Rhythm.Off, n)
			}
		}
		d.Log("person_saved", p.ID, "", u.Name)
		return nil
	})
	back(w, r, "/people")
}

func (a *App) peopleCode(w http.ResponseWriter, r *http.Request, u *User) {
	a.Store.Do(func(d *Data) error {
		if p := d.Person(r.PathValue("id")); p != nil {
			p.LinkCode, p.ChatID = newLinkCode(), 0
		}
		return nil
	})
	http.Redirect(w, r, "/people?edit="+r.PathValue("id"), http.StatusSeeOther)
}

type vTask struct {
	*Task
	Person, Project, AfterTitle string
	Blocked                     bool
	Links                       []vItem
}

func (a *App) tasksPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "tasks")
	q := r.URL.Query()
	a.Store.View(func(d *Data) {
		var ts []vTask
		blocked := map[string]bool{}
		for _, b := range d.Blockers {
			if b.Status == "open" {
				blocked[b.TaskID] = true
			}
		}
		status := q.Get("status")
		if status == "" {
			status = "open"
		}
		for _, t := range d.Tasks {
			if !canSeeTask(u, d, t) {
				continue
			}
			if status != "all" && t.Status != status {
				continue
			}
			if pid := q.Get("person"); pid != "" && t.PersonID != pid {
				continue
			}
			if pr := q.Get("project"); pr != "" && t.ProjectID != pr {
				continue
			}
			v := vTask{Task: t, Person: pname(d, t.PersonID), Project: projName(d, t.ProjectID), Blocked: blocked[t.ID]}
			if prev := d.Task(t.After); prev != nil {
				v.AfterTitle = prev.Title + " (" + firstName(d.Person(prev.PersonID)) + ")"
			}
			for _, f := range t.Files {
				if l := a.fileLink(f); l != "" {
					name := filepath.Base(f)
					if strings.HasPrefix(f, "chan:") {
						name = "فایل در کانال"
					} else if strings.HasPrefix(f, "bz:") {
						name = "بارگذار: " + filepath.Base(strings.TrimPrefix(f, "bz:"))
					}
					v.Links = append(v.Links, vItem{ID: l, Title: name})
				} else if strings.HasPrefix(f, "chan:") {
					v.Links = append(v.Links, vItem{ID: "#", Title: "فایل در کانال بله"})
				}
			}
			ts = append(ts, v)
		}
		sort.SliceStable(ts, func(i, j int) bool {
			if ts[i].Project != ts[j].Project {
				return ts[i].Project < ts[j].Project
			}
			return taskLess(ts[i].Task, ts[j].Task)
		})
		m["Tasks"], m["Status"], m["QPerson"], m["QProject"] = ts, status, q.Get("person"), q.Get("project")
		var ps []*Person
		for _, p := range sortedPeople(d) {
			if canSeePerson(u, p) && p.Active {
				ps = append(ps, p)
			}
		}
		m["People"], m["Projects"] = ps, d.Projects
		var all []vItem
		for _, t := range d.Tasks {
			if t.Status != "dropped" && canSeeTask(u, d, t) {
				all = append(all, vItem{ID: t.ID, Title: t.Title + " · " + firstName(d.Person(t.PersonID))})
			}
		}
		m["AllTasks"] = all
		if e := d.Task(q.Get("edit")); e != nil && canSeeTask(u, d, e) {
			cp := *e
			m["Edit"] = &cp
		} else if q.Get("new") != "" {
			m["Edit"] = &Task{Cycle: "value", Kind: "single", PersonID: q.Get("person"), ProjectID: q.Get("project")}
		}
	})
	render(w, "tasks", m)
}

func (a *App) tasksSave(w http.ResponseWriter, r *http.Request, u *User) {
	r.ParseForm()
	var errMsg string
	var outs []Out
	a.Store.Do(func(d *Data) error {
		t := d.Task(r.FormValue("id"))
		isNew := t == nil
		if isNew {
			t = &Task{ID: newID(), Status: "open", Ready: true, CreatedAt: a.Now()}
		} else if !canSeeTask(u, d, t) {
			errMsg = "دسترسی ندارید"
			return nil
		}
		p := d.Person(r.FormValue("person"))
		if p == nil {
			errMsg = "نفر را انتخاب کنید"
			return nil
		}
		if u.Role == "lead" && p.Line != u.Line {
			errMsg = "فقط برای خط خودتان"
			return nil
		}
		t.PersonID = p.ID
		t.ProjectID = r.FormValue("project")
		t.Title = strings.TrimSpace(r.FormValue("title"))
		t.Detail = strings.TrimSpace(r.FormValue("detail"))
		if c := r.FormValue("cycle"); cycleLabels[c] != "" {
			t.Cycle = c
		}
		if k := r.FormValue("kind"); k == "single" || k == "multi" || k == "metric" {
			t.Kind = k
		}
		t.Unit = strings.TrimSpace(r.FormValue("unit"))
		t.DailyTarget, _ = parseNum(r.FormValue("daily_target"))
		t.Total, _ = parseNum(r.FormValue("total"))
		t.Value, _ = parseNum(r.FormValue("value"))
		pri, _ := parseNum(r.FormValue("priority"))
		t.Priority = int(pri)
		t.Private = r.FormValue("private") == "1" && (u.Role == "admin" || u.Role == "finance")
		after := r.FormValue("after")
		if after == t.ID {
			after = ""
		}
		t.After = after
		if prev := d.Task(after); prev != nil {
			t.Ready = prev.Status == "done"
		} else {
			t.Ready = true
		}
		if eta := r.FormValue("eta"); eta != "" {
			if _, err := time.Parse("2006-01-02", eta); err == nil {
				a.setETA(d, t, eta)
			}
		}
		t.UpdatedAt = a.Now()
		if t.Title == "" {
			errMsg = "عنوان لازم است"
			return nil
		}
		if isNew {
			d.Tasks = append(d.Tasks, t)
			if r.FormValue("notify") == "1" {
				outs, _ = a.announceTask(d, t, firstName(viewerOrUser(d, u)))
			}
		}
		d.Log("task_saved", t.PersonID, t.ID, t.Title+" · "+u.Name)
		return nil
	})
	if errMsg != "" {
		http.Error(w, errMsg, 400)
		return
	}
	a.flush(outs)
	back(w, r, "/tasks")
}

func (a *App) taskAct(w http.ResponseWriter, r *http.Request, u *User) {
	var outs []Out
	a.Store.Do(func(d *Data) error {
		t := d.Task(r.PathValue("id"))
		if t == nil || !canSeeTask(u, d, t) {
			return nil
		}
		switch r.PathValue("act") {
		case "done":
			outs = a.markDone(d, t, u.ID)
		case "reopen":
			a.reopen(d, t)
		case "drop":
			t.Status, t.UpdatedAt = "dropped", a.Now()
			d.Log("dropped", t.PersonID, t.ID, t.Title)
		}
		return nil
	})
	a.flush(outs)
	back(w, r, "/tasks")
}

func (a *App) projectsPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "projects")
	a.Store.View(func(d *Data) {
		ps := append([]*Project(nil), d.Projects...)
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].Order < ps[j].Order })
		m["Projects"] = ps
		if e := d.Project(r.URL.Query().Get("edit")); e != nil {
			cp := *e
			m["Edit"] = &cp
		} else if r.URL.Query().Get("new") != "" {
			m["Edit"] = &Project{Active: true, Order: len(ps)}
		}
	})
	render(w, "projects", m)
}

func (a *App) projectsSave(w http.ResponseWriter, r *http.Request, u *User) {
	a.Store.Do(func(d *Data) error {
		p := d.Project(r.FormValue("id"))
		if p == nil {
			p = &Project{ID: newID()}
			d.Projects = append(d.Projects, p)
		}
		p.Name = strings.TrimSpace(r.FormValue("name"))
		p.Sub = strings.TrimSpace(r.FormValue("sub"))
		p.Bargozar = strings.Trim(strings.TrimSpace(r.FormValue("bargozar")), "/")
		o, _ := parseNum(r.FormValue("order"))
		p.Order = int(o)
		p.Active = r.FormValue("active") == "1"
		return nil
	})
	back(w, r, "/projects")
}

type vPlan struct {
	*Plan
	Person   string
	Tasks    []vItem
	Open     []vItem
	Selected map[string]bool
	When     string
}

func (a *App) plansPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "plans")
	a.Store.View(func(d *Data) {
		today := dayKey(a.mgrNow())
		var ps []vPlan
		for _, pl := range d.Plans {
			if pl.Date < today || (pl.Status != "draft" && pl.Status != "approved" && pl.Status != "sent") {
				continue
			}
			v := vPlan{Plan: pl, Person: pname(d, pl.PersonID), Selected: map[string]bool{}, When: relDay(pl.Date, today)}
			for _, id := range pl.TaskIDs {
				v.Selected[id] = true
				if t := d.Task(id); t != nil {
					v.Tasks = append(v.Tasks, vItem{ID: id, Title: t.Title})
				}
			}
			for _, t := range d.OpenTasks(pl.PersonID) {
				sub := projName(d, t.ProjectID)
				if !t.Ready {
					sub += " · منتظر حلقهٔ قبل"
				}
				v.Open = append(v.Open, vItem{ID: t.ID, Title: t.Title, Sub: sub})
			}
			ps = append(ps, v)
		}
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].Date != ps[j].Date {
				return ps[i].Date < ps[j].Date
			}
			return ps[i].Person < ps[j].Person
		})
		m["Plans"] = ps
		m["Mode"] = a.Cfg.UnapprovedPlan
	})
	render(w, "plans", m)
}

func (a *App) plansDraft(w http.ResponseWriter, r *http.Request, u *User) {
	a.Store.Do(func(d *Data) error {
		for _, p := range d.People {
			if p.Active && p.Rhythm.Plan != "" {
				t := a.localNow(p).AddDate(0, 0, 1)
				if !isOff(p, t) {
					a.draftPlan(d, p, dayKey(t))
				}
			}
		}
		return nil
	})
	back(w, r, "/plans")
}

func (a *App) planSave(w http.ResponseWriter, r *http.Request, u *User) {
	r.ParseForm()
	approve := r.FormValue("approve") == "1" && u.Role == "admin"
	var outs []Out
	a.Store.Do(func(d *Data) error {
		pl := d.Plan(r.PathValue("id"))
		if pl == nil || pl.Status == "sent" {
			return nil
		}
		var ids []string
		for _, id := range r.Form["task"] {
			if t := d.Task(id); t != nil && t.PersonID == pl.PersonID && t.Status == "open" {
				ids = append(ids, id)
			}
		}
		pl.TaskIDs = ids
		pl.Note = strings.TrimSpace(r.FormValue("note"))
		if pl.Status == "approved" {
			pl.Status = "draft" // edited after approval: approve again
		}
		if approve {
			outs = a.approvePlan(d, pl, u.ID)
		}
		return nil
	})
	a.flush(outs)
	back(w, r, "/plans")
}

func (a *App) planApprove(w http.ResponseWriter, r *http.Request, u *User) {
	var outs []Out
	a.Store.Do(func(d *Data) error {
		if pl := d.Plan(r.PathValue("id")); pl != nil {
			outs = a.approvePlan(d, pl, u.ID)
		}
		return nil
	})
	a.flush(outs)
	back(w, r, "/plans")
}

func (a *App) draftsPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "drafts")
	a.Store.View(func(d *Data) {
		var ds []vItem
		var sent []vItem
		for i := len(d.Drafts) - 1; i >= 0; i-- {
			dr := d.Drafts[i]
			p := d.Person(dr.ToPersonID)
			if p == nil || !canMessage(u, p) {
				continue
			}
			it := vItem{ID: dr.ID, Title: "به " + p.Name, Sub: dr.Text}
			if dr.Followup {
				it.Title += " · پیگیری با دکمه"
			}
			if dr.Status == "draft" {
				ds = append(ds, it)
			} else if len(sent) < 30 {
				it.Title += " · " + map[string]string{"sent": "فرستاده شد", "cancelled": "لغو شد"}[dr.Status]
				sent = append(sent, it)
			}
		}
		var ps []*Person
		for _, p := range sortedPeople(d) {
			if canMessage(u, p) && p.Active {
				ps = append(ps, p)
			}
		}
		m["Drafts"], m["Sent"], m["People"] = ds, sent, ps
		m["CanSend"] = u.Role == "admin" || u.Role == "senior"
	})
	render(w, "drafts", m)
}

func (a *App) draftNew(w http.ResponseWriter, r *http.Request, u *User) {
	var outs []Out
	a.Store.Do(func(d *Data) error {
		p := d.Person(r.FormValue("to"))
		text := strings.TrimSpace(r.FormValue("text"))
		if p == nil || text == "" || !canMessage(u, p) {
			return nil
		}
		dr := a.createDraft(d, p, text, u.PersonID, r.FormValue("followup") == "1")
		if r.FormValue("send") == "1" && (u.Role == "admin" || u.Role == "senior") {
			o, err := a.sendDraft(d, dr, u.PersonID)
			if err == nil {
				outs = o
			}
		}
		return nil
	})
	a.flush(outs)
	back(w, r, "/drafts")
}

func (a *App) draftAct(w http.ResponseWriter, r *http.Request, u *User) {
	var outs []Out
	var errMsg string
	a.Store.Do(func(d *Data) error {
		dr := d.Draft(r.PathValue("id"))
		if dr == nil {
			return nil
		}
		switch r.PathValue("act") {
		case "send":
			o, err := a.sendDraft(d, dr, u.PersonID)
			if err != nil {
				errMsg = err.Error()
			}
			outs = o
		case "cancel":
			if dr.Status == "draft" {
				dr.Status = "cancelled"
			}
		}
		return nil
	})
	if errMsg != "" {
		http.Error(w, errMsg, 400)
		return
	}
	a.flush(outs)
	back(w, r, "/drafts")
}

func (a *App) approvalAct(w http.ResponseWriter, r *http.Request, u *User) {
	var outs []Out
	a.Store.Do(func(d *Data) error {
		if ap := d.Approval(r.PathValue("id")); ap != nil {
			outs = a.decideApproval(d, ap, r.PathValue("act"), u.PersonID)
		}
		return nil
	})
	a.flush(outs)
	back(w, r, "/")
}

func (a *App) blockerResolve(w http.ResponseWriter, r *http.Request, u *User) {
	a.Store.Do(func(d *Data) error {
		for _, b := range d.Blockers {
			if b.ID == r.PathValue("id") {
				b.Status = "resolved"
				d.Log("resolved", b.PersonID, b.TaskID, b.Text+" · "+u.Name)
			}
		}
		return nil
	})
	back(w, r, "/")
}

type vUser struct {
	*User
	Person string
	MCPURL string
}

func (a *App) usersPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "users")
	a.Store.Do(func(d *Data) error { // Do: usageNow may create this month's row
		var us []vUser
		for _, x := range d.Users {
			v := vUser{User: x, Person: pname(d, x.PersonID)}
			if x.PersonID == "" {
				v.Person = "—"
			}
			v.MCPURL = strings.TrimRight(a.Cfg.BaseURL, "/") + "/mcp/" + x.MCPToken
			us = append(us, v)
		}
		m["Users"] = us
		u := a.usageNow(d)
		m["Usage"] = u
		m["Cost"] = fa(roundTo(a.cost(u), 2))
		m["Budget"] = fa(a.Cfg.MonthlyBudgetUSD)
		m["Model"] = a.Cfg.Model
		m["People"] = sortedPeople(d)
		m["Lines"] = lines(d)
		m["Roles"] = []string{"admin", "senior", "lead", "finance"}
		return nil
	})
	render(w, "users", m)
}

func (a *App) usersSave(w http.ResponseWriter, r *http.Request, u *User) {
	var errMsg string
	a.Store.Do(func(d *Data) error {
		login := strings.TrimSpace(r.FormValue("login"))
		x := d.UserByLogin(login)
		if x == nil {
			if len(r.FormValue("password")) < 8 {
				errMsg = "رمز دست‌کم ۸ حرف"
				return nil
			}
			x = &User{ID: newID(), Login: login, MCPToken: newToken()}
			d.Users = append(d.Users, x)
		}
		x.Name = strings.TrimSpace(r.FormValue("name"))
		if role := r.FormValue("role"); role != "staff" && roleLabels[role] != "" {
			x.Role = role
		}
		x.Line = r.FormValue("line")
		x.PersonID = r.FormValue("person")
		if pw := r.FormValue("password"); pw != "" {
			if len(pw) < 8 {
				errMsg = "رمز دست‌کم ۸ حرف"
				return nil
			}
			x.PassHash = hashPass(pw)
		}
		return nil
	})
	if errMsg != "" {
		http.Error(w, errMsg, 400)
		return
	}
	back(w, r, "/users")
}

func (a *App) userToken(w http.ResponseWriter, r *http.Request, u *User) {
	a.Store.Do(func(d *Data) error {
		if x := d.User(r.PathValue("id")); x != nil {
			x.MCPToken = newToken()
		}
		return nil
	})
	back(w, r, "/users")
}

type vEvent struct {
	When, Kind, Who, Text string
}

var eventLabels = map[string]string{
	"done": "تحویل", "handoff": "حلقهٔ بعد", "blocker": "گره", "slip": "عقب رفتن موعد", "slip2": "دو بار عقب",
	"plan_sent": "برنامه رفت", "plan_ok": "برنامه تأیید شد", "started": "شروع", "absent": "نیست", "draft": "پیش‌نویس",
	"draft_sent": "پیام رفت", "approval": "درخواست تأیید", "approved": "تأیید", "rejected": "رد", "linked": "اتصال ربات",
	"followup": "پاسخ پیگیری", "report_raw": "گزارش", "other": "کار دیگر", "reopen": "بازگشایی", "dropped": "کنار گذاشته شد",
	"resolved": "گره باز شد", "task_saved": "کار ذخیره شد", "person_saved": "نفر ذخیره شد",
}

func (a *App) logPage(w http.ResponseWriter, r *http.Request, u *User) {
	m := a.base(u, "log")
	a.Store.View(func(d *Data) {
		loc := loadLoc(a.Cfg.ManagerTZ)
		var es []vEvent
		for i := len(d.Events) - 1; i >= 0 && len(es) < 300; i-- {
			e := d.Events[i]
			if t := d.Task(e.TaskID); t != nil && !canSeeTask(u, d, t) {
				continue
			}
			lt := e.At.In(loc)
			k := eventLabels[e.Kind]
			if k == "" {
				k = e.Kind
			}
			es = append(es, vEvent{When: faDate(lt) + " " + fa(lt.Format("15:04")), Kind: k, Who: pname(d, e.PersonID), Text: e.Text})
		}
		m["Events"] = es
	})
	render(w, "log", m)
}

func (a *App) serveFile(w http.ResponseWriter, r *http.Request, u *User) {
	rel := filepath.Clean("/" + r.PathValue("path"))
	full := filepath.Join(a.Files, rel)
	if !strings.HasPrefix(full, filepath.Clean(a.Files)+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment")
	http.ServeFile(w, r, full)
}

// fileLink turns a stored path into a dashboard link
func (a *App) fileLink(path string) string {
	if ch, chat, id, ok := parseChanRef(path); ok {
		return chanLink(ch, chat, id)
	}
	if strings.HasPrefix(path, "bz:") {
		return strings.TrimPrefix(path, "bz:")
	}
	rel, err := filepath.Rel(a.Files, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return "/files/" + filepath.ToSlash(rel)
}

// ---------- templates ----------

var tpl *template.Template

func init() {
	funcs := template.FuncMap{
		"fa":    func(v any) string { return fa(v) },
		"faDay": faDay,
		"cycle": func(c string) string { return cycleLabels[c] },
		"prog":  func(p string) string { return progressLabels[p] },
		"role":  func(r string) string { return roleLabels[r] },
		"kind": func(k string) string {
			return map[string]string{"single": "یک‌روزه", "multi": "چندروزه", "metric": "عددی"}[k]
		},
		"chan":  func(c string) string { return map[string]string{"telegram": "تلگرام", "bale": "بله"}[c] },
		"pct":   func(n int) string { return strconv.Itoa(n) + "%" },
		"base":  filepath.Base,
		"has":   func(m map[int]bool, k int) bool { return m[k] },
		"sel":   func(m map[string]bool, k string) bool { return m[k] },
		"split": func(m map[string]int, k string) int { return m[k] },
		"latinN": func(f float64) string {
			if f == 0 {
				return ""
			}
			return strconv.FormatFloat(f, 'f', -1, 64)
		},
		"latinI": func(n int) string { return strconv.Itoa(n) },
	}
	tpl = template.Must(template.New("").Funcs(funcs).Parse(templates))
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "خطای صفحه", 500)
		return
	}
	w.Write(buf.Bytes())
}
