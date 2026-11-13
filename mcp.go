package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// A minimal MCP server (Streamable HTTP, JSON responses) so each person's
// Claude can read and act on Rooznegar with their own role.

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	roles       string
	run         func(a *App, u *User, args map[string]any) (string, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func argS(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := args[k].(float64); ok {
		return fmt.Sprint(v)
	}
	return ""
}

func argN(args map[string]any, k string) float64 {
	switch v := args[k].(type) {
	case float64:
		return v
	case string:
		f, _ := parseNum(v)
		return f
	}
	return 0
}

var mcpTools []mcpTool

func init() {
	mcpTools = []mcpTool{
		{Name: "today_summary", Description: "خلاصهٔ امروز روزنگار: چه کسی گزارش داد، گره‌ها، حلقه‌هایی که به نفر بعد رسید، عددها، منتظر تأییدها.",
			InputSchema: obj(map[string]any{}), roles: "admin senior lead finance", run: toolSummary},
		{Name: "list_people", Description: "فهرست آدم‌ها با نقش، خط، پیام‌رسان و ریتم روزانه.",
			InputSchema: obj(map[string]any{}), roles: "admin senior lead finance", run: toolPeople},
		{Name: "person_status", Description: "وضعیت یک نفر: الگوی کارش با ربات (ساعت واقعی گزارش، جواب به پیگیری‌ها، عقب رفتن موعدها، تنظیم‌های خودکار)، کارهای باز با پیشرفت و موعد، گزارش‌های چند روز اخیر.",
			InputSchema: obj(map[string]any{"person": str("نام نفر"), "days": map[string]any{"type": "number", "description": "چند روز اخیر (پیش‌فرض ۷)"}}, "person"),
			roles:       "admin senior lead finance", run: toolPersonStatus},
		{Name: "list_tasks", Description: "کارها، با فیلتر اختیاری نفر، پروژه و وضعیت (open/done/all).",
			InputSchema: obj(map[string]any{"person": str("نام نفر"), "project": str("نام پروژه"), "status": str("open | done | all")}),
			roles:       "admin senior lead finance", run: toolTasks},
		{Name: "chains", Description: "جدول سه زنجیرهٔ بازار، ارزش و نقد برای هر پروژه: حلقهٔ فعلی و صاحبش، و حلقه‌های بی‌صاحب.",
			InputSchema: obj(map[string]any{}), roles: "admin senior lead finance", run: toolChains},
		{Name: "add_task", Description: "افزودن یک کار. after: عنوان یا شناسهٔ کاری که این کار منتظر آن است (حلقهٔ قبل).",
			InputSchema: obj(map[string]any{
				"person": str("نام نفر"), "title": str("عنوان کوتاه"), "project": str("نام پروژه"),
				"cycle": str("market | value | cash"), "kind": str("single | multi | metric"),
				"detail": str("نکته یا لینک"), "unit": str("واحد برای metric"),
				"daily_target": map[string]any{"type": "number"}, "total": map[string]any{"type": "number"},
				"priority": map[string]any{"type": "number"}, "after": str("کار قبلی در زنجیره"), "due": str("YYYY-MM-DD"),
				"value":  map[string]any{"type": "number", "description": "ارزش این کار به میلیون تومان (سهمش از قرارداد)، اگر معلوم است"},
				"notify": map[string]any{"type": "boolean", "description": "پیش‌فرض true: ربات خودش به نفر خبر می‌دهد (در ساعت کاری فوراً، وگرنه در برنامهٔ صبح)"},
			}, "person", "title"), roles: "admin senior lead", run: toolAddTask},
		{Name: "update_task", Description: "تغییر یک کار با شناسه: title, detail, priority, due (YYYY-MM-DD), person, status (done/open/dropped).",
			InputSchema: obj(map[string]any{"task_id": str("شناسهٔ کار"), "title": str(""), "detail": str(""), "priority": map[string]any{"type": "number"},
				"value": map[string]any{"type": "number", "description": "ارزش به میلیون تومان"},
				"due":   str("YYYY-MM-DD"), "person": str("نام نفر تازه"), "status": str("done | open | dropped")}, "task_id"),
			roles: "admin senior lead", run: toolUpdateTask},
		{Name: "tell", Description: "وقتی کاربر گفت «به فلانی بگو/پیام بده/یادش بنداز»، همین را صدا بزن: پیام مستقیم از ربات می‌رود (بدون پیش‌نویس). at اختیاری برای یادآوری زمان‌دار به وقت آن نفر (مثل 10:30، فردا 9:00، 2026-11-05 11:00). followup پیش‌فرض true: دکمه‌های «انجام شد / در جریان / گیر دارم» و جوابش به مدیر برمی‌گردد؛ بی‌جواب بماند ۲۴ ساعت بعد دوباره می‌پرسد. متن را کوتاه، گرم و از زبان تیم بنویس، نه رسمی.",
			InputSchema: obj(map[string]any{"person": str("نام گیرنده"), "text": str("متن پیام، فارسی"), "at": str("زمان اختیاری"),
				"followup": map[string]any{"type": "boolean"}, "task_id": str("شناسهٔ کار مرتبط، اختیاری")}, "person", "text"),
			roles: "admin senior lead", run: toolTell},
		{Name: "tell_all", Description: "پیام متنی برای همهٔ وصل‌ها یا یک خط (line). وقتی کاربر گفت «به همه بگو…» یا «به بچه‌های گرافیک بگو…». مستقیم می‌رود. ویدیو و فایل از صفحهٔ «ارسال برای همه» در داشبورد می‌رود.",
			InputSchema: obj(map[string]any{"text": str("متن پیام، فارسی، کوتاه و گرم"), "line": str("اختیاری: فقط این خط"),
				"followup": map[string]any{"type": "boolean", "description": "پیش‌فرض false"}}, "text"),
			roles: "admin senior lead", run: toolTellAll},
		{Name: "send_file", Description: "فرستادن یک فایل (ویدیو، عکس، PDF، …) از طرف ربات به همه، یک خط، چند نفر یا فقط خود کاربر. یک نشانی یک‌بارمصرف (۳۰ دقیقه) برمی‌گرداند؛ فایل را با curl به آن بفرست (دستورش در جواب هست). اگر به اینترنت دسترسی نداری، بگو کاربر فایل را از صفحهٔ «ارسال برای همه» در داشبورد بفرستد.",
			InputSchema: obj(map[string]any{"caption": str("متن زیر فایل"), "audience": str("all | line | people | me (پیش‌فرض all)"),
				"line": str("برای audience=line"), "people": str("برای audience=people: نام‌ها با کاما"),
				"copy_to_me": map[string]any{"type": "boolean", "description": "یک نسخه هم برای خود کاربر (پیش‌فرض true)"}}),
			roles: "admin senior lead", run: toolSendFile},
		{Name: "team_load", Description: "بار و ظرفیت تیم: کارهای باز آماده، منتظر حلقهٔ قبل، عقب‌افتاده، برنامهٔ فردا، ارزش تحویل‌شدهٔ ۷ روز در برابر هدف، کم‌کار و پربار، و یادآورهای زمان‌دار در صف. قبل از سپردن کار تازه یا برای توازن تیم ببین.",
			InputSchema: obj(map[string]any{}), roles: "admin senior lead", run: toolTeamLoad},
		{Name: "draft_message", Description: "فقط وقتی کاربر خواست اول متن را ببیند: پیش‌نویس می‌سازد و نمی‌فرستد (با send_draft یا «بفرست» در داشبورد می‌رود). برای پیام عادی از tell استفاده کن.",
			InputSchema: obj(map[string]any{"person": str("نام گیرنده"), "text": str("متن کوتاه و دوستانه، فارسی"), "followup": map[string]any{"type": "boolean"}}, "person", "text"),
			roles:       "admin senior lead", run: toolDraft},
		{Name: "send_draft", Description: "پیش‌نویس را واقعاً می‌فرستد. فقط وقتی صدا بزن که کاربر همین متن را دیده و صریحاً گفته بفرست.",
			InputSchema: obj(map[string]any{"draft_id": str("شناسهٔ پیش‌نویس")}, "draft_id"), roles: "admin senior", run: toolSendDraft},
		{Name: "list_approvals", Description: "درخواست‌های منتظر تأیید و برنامه‌های فردا که هنوز تأیید نشده‌اند.",
			InputSchema: obj(map[string]any{}), roles: "admin senior", run: toolApprovals},
		{Name: "decide", Description: "تصمیم دربارهٔ یک درخواست تأیید (decision: ok | no | ask) یا تأیید برنامهٔ یک نفر (plan_id). فقط با دستور صریح کاربر.",
			InputSchema: obj(map[string]any{"approval_id": str(""), "plan_id": str(""), "decision": str("ok | no | ask")}), roles: "admin", run: toolDecide},
		{Name: "set_plan", Description: "تعیین کارهای برنامهٔ فردای یک نفر (task_ids با کاما) و یک خط یادداشت. برنامه سر ساعت صبح آن نفر خودکار می‌رود.",
			InputSchema: obj(map[string]any{"person": str("نام نفر"), "task_ids": str("شناسه‌ها با کاما، به ترتیب اولویت"), "note": str("یک خط برای او"), "date": str("YYYY-MM-DD، پیش‌فرض فردا")}, "person", "task_ids"),
			roles:       "admin senior", run: toolSetPlan},
		{Name: "set_cc", Description: "قاعدهٔ نسخه: از این به بعد گزارش‌ها، جواب‌ها، گره‌ها و «انجام شد»های یک نفر همان لحظه برای چند نفر دیگر هم برود. to: نام‌ها با کاما؛ خالی یعنی خاموش.",
			InputSchema: obj(map[string]any{"person": str("نام نفری که گزارش می‌دهد"), "to": str("نام گیرنده‌های نسخه، با کاما؛ خالی = خاموش")}, "person"),
			roles:       "admin", run: toolSetCC},
	}
}

func ccNames(d *Data, p *Person) string {
	var names []string
	for _, id := range p.CC {
		if c := d.Person(id); c != nil {
			names = append(names, c.Name)
		}
	}
	return strings.Join(names, "، ")
}

func toolSetCC(a *App, u *User, args map[string]any) (string, error) {
	var msg string
	err := a.Store.Do(func(d *Data) error {
		p := d.PersonByName(argS(args, "person"))
		if p == nil {
			return fmt.Errorf("نفر پیدا نشد: %s", argS(args, "person"))
		}
		var ids []string
		for _, n := range strings.FieldsFunc(argS(args, "to"), func(r rune) bool { return r == ',' || r == '،' }) {
			if strings.TrimSpace(n) == "" {
				continue
			}
			c := d.PersonByName(strings.TrimSpace(n))
			if c == nil {
				return fmt.Errorf("نفر پیدا نشد: %s", n)
			}
			if c.ID != p.ID {
				ids = append(ids, c.ID)
			}
		}
		p.CC = ids
		d.Log("cc_set", p.ID, "", u.Name)
		if len(ids) == 0 {
			msg = "نسخهٔ گزارش‌های " + p.Name + " خاموش شد."
		} else {
			msg = "از این به بعد گزارش‌ها و جواب‌های " + p.Name + " همان لحظه برای " + ccNames(d, p) + " هم می‌رود."
			for _, id := range ids {
				if c := d.Person(id); c != nil && c.ChatID == 0 {
					msg += "\n" + c.Name + " هنوز به ربات وصل نیست؛ تا وصل نشود چیزی برایش نمی‌رود."
				}
			}
		}
		return nil
	})
	return msg, err
}

func (a *App) mcpHandler(w http.ResponseWriter, r *http.Request) {
	var u *User
	a.Store.View(func(d *Data) {
		if x := d.UserByToken(r.PathValue("token")); x != nil {
			c := *x
			u = &c
		}
	})
	if u == nil {
		time.Sleep(500 * time.Millisecond)
		http.Error(w, "not found", 404)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req rpcReq
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, rpcResp{JSONRPC: "2.0", Error: &rpcErr{-32700, "parse error"}})
		return
	}
	if len(req.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := rpcResp{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		pv := p.ProtocolVersion
		if pv == "" {
			pv = "2025-03-26"
		}
		resp.Result = map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "rooznegar", "version": version},
			"instructions": "روزنگار ربات گزارش کار تیم است. کاربر فعلی: " + u.Name + " (" + roleLabels[u.Role] + "). " +
				"وقتی کاربر می‌گوید فلانی چه کند یا به او چه بگویی، همان را انجام بده (add_task یا tell): ربات خودش خبر می‌دهد، یادآوری و پیگیری می‌کند. بار هر نفر را در نظر بگیر (team_load): نه کسی زیر بار له شود، نه کسی بی‌کار بماند. تأیید درخواست‌ها (decide) فقط با دستور صریح. جواب‌ها را کوتاه و فارسی بده.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		var list []mcpTool
		for _, t := range mcpTools {
			if strings.Contains(t.roles, u.Role) {
				list = append(list, t)
			}
		}
		resp.Result = map[string]any{"tools": list}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		var tool *mcpTool
		for i := range mcpTools {
			if mcpTools[i].Name == p.Name {
				tool = &mcpTools[i]
			}
		}
		if tool == nil || !strings.Contains(tool.roles, u.Role) {
			resp.Error = &rpcErr{-32602, "ابزار در دسترس نیست"}
			break
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		out, err := tool.run(a, u, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "خطا: " + err.Error()}}, "isError": true}
		} else {
			resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": out}}}
		}
	default:
		resp.Error = &rpcErr{-32601, "method not found"}
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, r rpcResp) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(r)
}

// ---------- tools ----------

func viewerPerson(d *Data, u *User) *Person {
	if p := d.Person(u.PersonID); p != nil {
		return p
	}
	return &Person{Name: u.Name, Role: u.Role}
}

func toolSummary(a *App, u *User, _ map[string]any) (string, error) {
	var out string
	a.Store.View(func(d *Data) {
		vp := viewerPerson(d, u)
		vp.Role = u.Role
		out, _ = a.summaryText(d, vp, a.mgrNow())
		var pend []string
		if u.Role == "admin" || u.Role == "senior" {
			for _, ap := range d.Approvals {
				if ap.Status == "pending" {
					pend = append(pend, fmt.Sprintf("[%s] %s · %s", ap.ID, ap.Title, ap.Detail))
				}
			}
			for _, dr := range d.Drafts {
				if dr.Status == "draft" {
					pend = append(pend, fmt.Sprintf("پیش‌نویس [%s] به %s: %s", dr.ID, pname(d, dr.ToPersonID), shorten(dr.Text, 80)))
				}
			}
		}
		if len(pend) > 0 {
			out += "\n\nمنتظر:\n" + strings.Join(pend, "\n")
		}
	})
	return out, nil
}

func toolPeople(a *App, u *User, _ map[string]any) (string, error) {
	var sb strings.Builder
	a.Store.View(func(d *Data) {
		for _, p := range sortedPeople(d) {
			if !canSeePerson(u, p) {
				continue
			}
			st := "وصل"
			if p.ChatID == 0 {
				st = "وصل نشده"
			}
			if !p.Active {
				st = "غیرفعال"
			}
			fmt.Fprintf(&sb, "- %s · %s%s · %s (%s) · ریتم: صبح %s، شروع %s، ظهر %s، عصر %s · %s\n", p.Name, roleLabels[p.Role],
				map[bool]string{true: " · " + p.Line, false: ""}[p.Line != ""], map[string]string{"telegram": "تلگرام", "bale": "بله"}[p.Channel], st,
				dash(p.Rhythm.Plan), dash(p.Rhythm.Nudge), dash(p.Rhythm.Noon), dash(p.Rhythm.Report), p.TZ)
			if names := ccNames(d, p); names != "" {
				fmt.Fprintf(&sb, "  نسخهٔ گزارش‌ها برای: %s\n", names)
			}
		}
	})
	return sb.String(), nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func taskLine(d *Data, t *Task, today string) string {
	var bits []string
	bits = append(bits, fmt.Sprintf("[%s] %s", t.ID, t.Title))
	if pr := projName(d, t.ProjectID); pr != "" {
		bits = append(bits, pr)
	}
	bits = append(bits, cycleLabels[t.Cycle], firstName(d.Person(t.PersonID)))
	switch {
	case t.Status == "done":
		bits = append(bits, "تمام شد")
	case t.Status == "dropped":
		bits = append(bits, "کنار گذاشته شد")
	case !t.Ready:
		if prev := d.Task(t.After); prev != nil {
			bits = append(bits, "منتظر «"+prev.Title+"»")
		}
	case t.Progress != "":
		bits = append(bits, progressLabels[t.Progress])
	}
	if t.ETA != "" {
		bits = append(bits, "موعد "+relDay(t.ETA, today))
		if n := slipCount(t); n > 0 {
			bits = append(bits, fmt.Sprintf("%d بار عقب رفته", n))
		}
	}
	if t.Kind == "metric" {
		s := fmt.Sprintf("%v %s", t.DoneQty, t.Unit)
		if t.Total > 0 {
			s += fmt.Sprintf(" از %v", t.Total)
		}
		if t.DailyTarget > 0 {
			s += fmt.Sprintf(" (هدف روزانه %v)", t.DailyTarget)
		}
		bits = append(bits, s)
	}
	return "- " + strings.Join(bits, " · ")
}

func toolPersonStatus(a *App, u *User, args map[string]any) (string, error) {
	var sb strings.Builder
	var err error
	days := int(argN(args, "days"))
	if days <= 0 {
		days = 7
	}
	a.Store.View(func(d *Data) {
		p := d.PersonByName(argS(args, "person"))
		if p == nil || !canSeePerson(u, p) {
			err = fmt.Errorf("نفر پیدا نشد: %s", argS(args, "person"))
			return
		}
		today := a.today(p)
		ds := d.Day(p.ID, today)
		fmt.Fprintf(&sb, "%s · امروز: %s\n", p.Name, dayStatus(ds))
		sb.WriteString("الگو: " + a.patternLine(d, p) + "\n")
		sb.WriteString("\nکارهای باز:\n")
		for _, t := range d.OpenTasks(p.ID) {
			if canSeeTask(u, d, t) {
				sb.WriteString(taskLine(d, t, today) + "\n")
				if len(t.ETAHistory) > 1 {
					var hs []string
					for _, h := range t.ETAHistory {
						hs = append(hs, faDay(h))
					}
					sb.WriteString("   تاریخچهٔ موعد: " + strings.Join(hs, " ← ") + "\n")
				}
			}
		}
		cut := a.Now().AddDate(0, 0, -days)
		sb.WriteString(fmt.Sprintf("\nگزارش‌های %d روز اخیر:\n", days))
		for _, r := range d.Reports {
			if r.PersonID == p.ID && r.At.After(cut) {
				fmt.Fprintf(&sb, "- %s: %s\n", faDay(r.Date), shorten(r.Text, 300))
			}
		}
		for _, e := range d.Events {
			if e.PersonID == p.ID && e.At.After(cut) && (e.Kind == "done" || e.Kind == "slip" || e.Kind == "blocker" || e.Kind == "absent") {
				if t := d.Task(e.TaskID); t != nil && !canSeeTask(u, d, t) {
					continue
				}
				fmt.Fprintf(&sb, "  · %s %s: %s\n", faDate(e.At.In(loadLoc(p.TZ))), eventLabels[e.Kind], e.Text)
			}
		}
	})
	return sb.String(), err
}

func dayStatus(ds *DayState) string {
	switch {
	case ds.Absent:
		return "نیست"
	case ds.Reported:
		return "گزارش داد"
	case ds.Started:
		return "سر کار"
	case ds.PlanSent:
		return "برنامه رفته، شروع نزده"
	}
	return "هنوز پیامی نرفته"
}

func toolTasks(a *App, u *User, args map[string]any) (string, error) {
	var sb strings.Builder
	a.Store.View(func(d *Data) {
		var p *Person
		if n := argS(args, "person"); n != "" {
			p = d.PersonByName(n)
		}
		var pr *Project
		if n := argS(args, "project"); n != "" {
			pr = d.ProjectByName(n)
		}
		st := argS(args, "status")
		if st == "" {
			st = "open"
		}
		today := dayKey(a.mgrNow())
		var ts []*Task
		for _, t := range d.Tasks {
			if !canSeeTask(u, d, t) || (p != nil && t.PersonID != p.ID) || (pr != nil && t.ProjectID != pr.ID) {
				continue
			}
			if st != "all" && t.Status != st {
				continue
			}
			ts = append(ts, t)
		}
		sort.SliceStable(ts, func(i, j int) bool { return taskLess(ts[i], ts[j]) })
		for _, t := range ts {
			sb.WriteString(taskLine(d, t, today) + "\n")
		}
		if len(ts) == 0 {
			sb.WriteString("کاری پیدا نشد.")
		}
	})
	return sb.String(), nil
}

func toolChains(a *App, u *User, _ map[string]any) (string, error) {
	var sb strings.Builder
	a.Store.View(func(d *Data) {
		for _, row := range a.chains(d, u) {
			fmt.Fprintf(&sb, "%s:\n", row.Project.Name)
			for i, c := range row.Cells {
				lbl := []string{"بازار", "ارزش", "نقد"}[i]
				state := map[string]string{"ok": "بسته شد", "wip": "در جریان", "red": "گره", "none": "بی‌صاحب"}[c.Color]
				fmt.Fprintf(&sb, "  %s [%s]: %s %s\n", lbl, state, c.Text, c.Who)
			}
		}
	})
	return sb.String(), nil
}

func toolAddTask(a *App, u *User, args map[string]any) (string, error) {
	var out string
	var outs []Out
	err := a.Store.Do(func(d *Data) error {
		p := d.PersonByName(argS(args, "person"))
		if p == nil {
			return fmt.Errorf("نفر پیدا نشد: %s", argS(args, "person"))
		}
		if u.Role == "lead" && p.Line != u.Line {
			return fmt.Errorf("فقط برای خط خودتان")
		}
		t := &Task{ID: newID(), PersonID: p.ID, Title: argS(args, "title"), Detail: argS(args, "detail"), Status: "open", Ready: true,
			Cycle: "value", Kind: "single", Unit: argS(args, "unit"), DailyTarget: argN(args, "daily_target"), Total: argN(args, "total"),
			Priority: int(argN(args, "priority")), CreatedAt: a.Now(), UpdatedAt: a.Now()}
		if t.Title == "" {
			return fmt.Errorf("عنوان لازم است")
		}
		if c := argS(args, "cycle"); cycleLabels[c] != "" {
			t.Cycle = c
		}
		if k := argS(args, "kind"); k == "multi" || k == "metric" {
			t.Kind = k
		}
		if pn := argS(args, "project"); pn != "" {
			if pr := d.ProjectByName(pn); pr != nil {
				t.ProjectID = pr.ID
			} else {
				return fmt.Errorf("پروژه پیدا نشد: %s", pn)
			}
		}
		if af := argS(args, "after"); af != "" {
			prev := d.Task(af)
			if prev == nil {
				for _, x := range d.Tasks {
					if normName(x.Title) == normName(af) && x.Status != "dropped" {
						prev = x
					}
				}
			}
			if prev == nil {
				return fmt.Errorf("کار قبلی پیدا نشد: %s", af)
			}
			t.After, t.Ready = prev.ID, prev.Status == "done"
		}
		if due := argS(args, "due"); due != "" {
			if _, err := time.Parse("2006-01-02", due); err == nil {
				a.setETA(d, t, due)
			}
		}
		t.Value = argN(args, "value")
		warn := a.loadOf(d, p).warning()
		d.Tasks = append(d.Tasks, t)
		d.Log("task_saved", p.ID, t.ID, t.Title+" · Claude/"+u.Name)
		out = "ثبت شد: " + taskLine(d, t, dayKey(a.mgrNow()))
		if n, ok := args["notify"].(bool); !ok || n {
			o, how := a.announceTask(d, t, firstName(viewerOrUser(d, u)))
			outs = o
			if how != "" {
				out += "\n" + how
			}
		}
		if warn != "" {
			out += "\nتوجه: " + warn
		}
		return nil
	})
	a.flush(outs)
	return out, err
}

// viewerOrUser: the person behind a dashboard/Claude user, for "کار تازه از معین".
func viewerOrUser(d *Data, u *User) *Person {
	if p := d.Person(u.PersonID); p != nil {
		return p
	}
	return &Person{Name: u.Name}
}

func toolTell(a *App, u *User, args map[string]any) (string, error) {
	var out string
	var outs []Out
	err := a.Store.Do(func(d *Data) error {
		p := d.PersonByName(argS(args, "person"))
		if p == nil {
			return fmt.Errorf("نفر پیدا نشد: %s", argS(args, "person"))
		}
		if !canMessage(u, p) {
			return fmt.Errorf("اجازهٔ پیام به %s را ندارید", p.Name)
		}
		if p.ChatID == 0 {
			return fmt.Errorf("%s هنوز ربات را وصل نکرده (کد اتصالش در صفحهٔ افراد است)", p.Name)
		}
		text := strings.TrimSpace(argS(args, "text"))
		if text == "" {
			return fmt.Errorf("متن خالی است")
		}
		at, err := a.parseWhen(p, argS(args, "at"))
		if err != nil {
			return err
		}
		fu := true
		if v, ok := args["followup"].(bool); ok {
			fu = v
		}
		var r *Reminder
		outs, r = a.tell(d, p, text, at, fu, argS(args, "task_id"), u.PersonID)
		if r.SentAt != nil {
			out = "فرستاده شد به " + p.Name + "."
			if fu {
				out += " جوابش به شما می‌رسد."
			}
		} else {
			out = fmt.Sprintf("یادآوری برای %s گذاشته شد: %s ساعت %s (به وقت خودش).", p.Name, relDay(dayKey(at), dayKey(a.localNow(p))), fa(at.Format("15:04")))
		}
		return nil
	})
	a.flush(outs)
	return out, err
}

func toolSendFile(a *App, u *User, args map[string]any) (string, error) {
	if a.Cfg.BaseURL == "" {
		return "", fmt.Errorf("base_url در تنظیمات سرور خالی است")
	}
	aud := argS(args, "audience")
	switch aud {
	case "", "all":
		aud = "all"
	case "people":
		aud = "pick"
	case "line", "me":
	default:
		return "", fmt.Errorf("audience نامعتبر: %s", aud)
	}
	me := true
	if v, ok := args["copy_to_me"].(bool); ok {
		me = v
	}
	var names []string
	for _, n := range strings.Split(argS(args, "people"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	tk := &FileTicket{Token: newToken(), UserID: u.ID, Caption: argS(args, "caption"), Aud: aud, Line: argS(args, "line"),
		People: names, Me: me, Expires: a.Now().Add(30 * time.Minute)}
	a.Store.Do(func(d *Data) error {
		keep := d.FileTickets[:0]
		for _, t := range d.FileTickets {
			if !t.Used && a.Now().Before(t.Expires) {
				keep = append(keep, t)
			}
		}
		d.FileTickets = append(keep, tk)
		return nil
	})
	url := strings.TrimRight(a.Cfg.BaseURL, "/") + "/up/" + tk.Token
	return "نشانی یک‌بارمصرف (۳۰ دقیقه): " + url + "\nفایل را این‌طور بفرست (حداکثر ۵۰ مگ):\ncurl -sS -F file=@PATH " + url +
		"\nجواب JSON می‌گوید به چند نفر می‌رود. این نشانی را به کسی نشان نده.", nil
}

func toolTellAll(a *App, u *User, args map[string]any) (string, error) {
	text := strings.TrimSpace(argS(args, "text"))
	if text == "" {
		return "", fmt.Errorf("متن خالی است")
	}
	line := argS(args, "line")
	fu, _ := args["followup"].(bool)
	var outs []Out
	var names []string
	a.Store.Do(func(d *Data) error {
		for _, p := range d.People {
			if !p.Active || p.ChatID == 0 || p.Role == "admin" || !canMessage(u, p) || (line != "" && p.Line != line) {
				continue
			}
			o, _ := a.tell(d, p, text, a.Now(), fu, "", u.PersonID)
			outs = append(outs, o...)
			names = append(names, firstName(p))
		}
		return nil
	})
	a.flush(outs)
	if len(names) == 0 {
		return "", fmt.Errorf("گیرنده‌ای پیدا نشد")
	}
	return "فرستاده شد به " + fa(len(names)) + " نفر: " + strings.Join(names, "، "), nil
}

func toolTeamLoad(a *App, u *User, _ map[string]any) (string, error) {
	var sb strings.Builder
	a.Store.View(func(d *Data) {
		mn := a.mgrNow()
		total, _ := a.valueToday(d, mn)
		fmt.Fprintf(&sb, "بار تیم · %s · ارزش تحویل‌شدهٔ امروز: %s میلیون\n", faDate(mn), fa(roundTo(total, 0)))
		for _, l := range a.teamLoad(d, u) {
			flag := ""
			switch {
			case l.P.ChatID == 0:
				flag = " · وصل نیست"
			case l.idle():
				flag = " · ⚪ کار آماده ندارد"
			case l.heavy():
				flag = " · 🟠 پربار"
			}
			fmt.Fprintf(&sb, "- %s (%s): آماده %s، منتظر حلقهٔ قبل %s، عقب‌افتاده %s، برنامهٔ فردا %s",
				l.P.Name, dash(l.P.Line), fa(l.Ready), fa(l.Waiting), fa(l.Overdue), fa(l.Tomorrow))
			if l.Target7 > 0 || l.Value7 > 0 {
				fmt.Fprintf(&sb, "، ارزش ۷ روز %s از %s", fa(roundTo(l.Value7, 0)), fa(roundTo(l.Target7, 0)))
			}
			sb.WriteString(flag + "\n")
		}
		var rs []string
		for _, r := range d.Reminders {
			if r.SentAt == nil && !r.Cancel {
				if p := d.Person(r.PersonID); p != nil {
					rs = append(rs, fmt.Sprintf("- %s · %s %s: %s", p.Name, faDay(dayKey(r.At.In(loadLoc(p.TZ)))), fa(r.At.In(loadLoc(p.TZ)).Format("15:04")), shorten(r.Text, 60)))
				}
			}
		}
		if len(rs) > 0 {
			sb.WriteString("\nیادآورهای در صف:\n" + strings.Join(rs, "\n"))
		}
		var ts []string
		for _, t := range d.Tunings {
			if !t.Undone && a.Now().Sub(t.At) < 14*24*time.Hour {
				ts = append(ts, "- "+firstName(d.Person(t.PersonID))+": "+t.Why)
			}
		}
		if len(ts) > 0 {
			sb.WriteString("\n\nتنظیم‌های خودکار اخیر:\n" + strings.Join(ts, "\n"))
		}
	})
	return strings.TrimSpace(sb.String()), nil
}

func toolUpdateTask(a *App, u *User, args map[string]any) (string, error) {
	var out string
	var outs []Out
	err := a.Store.Do(func(d *Data) error {
		t := d.Task(argS(args, "task_id"))
		if t == nil || !canSeeTask(u, d, t) {
			return fmt.Errorf("کار پیدا نشد")
		}
		if v := argS(args, "title"); v != "" {
			t.Title = v
		}
		if v := argS(args, "detail"); v != "" {
			t.Detail = v
		}
		if _, ok := args["priority"]; ok {
			t.Priority = int(argN(args, "priority"))
		}
		if _, ok := args["value"]; ok {
			t.Value = argN(args, "value")
		}
		if v := argS(args, "due"); v != "" {
			if _, err := time.Parse("2006-01-02", v); err == nil {
				a.setETA(d, t, v)
			}
		}
		if v := argS(args, "person"); v != "" {
			p := d.PersonByName(v)
			if p == nil {
				return fmt.Errorf("نفر پیدا نشد: %s", v)
			}
			if u.Role == "lead" && p.Line != u.Line {
				return fmt.Errorf("فقط برای خط خودتان")
			}
			t.PersonID = p.ID
		}
		switch argS(args, "status") {
		case "done":
			outs = a.markDone(d, t, u.ID)
		case "open":
			a.reopen(d, t)
		case "dropped":
			t.Status = "dropped"
		}
		t.UpdatedAt = a.Now()
		out = "به‌روز شد: " + taskLine(d, t, dayKey(a.mgrNow()))
		return nil
	})
	a.flush(outs)
	return out, err
}

func toolDraft(a *App, u *User, args map[string]any) (string, error) {
	var out string
	err := a.Store.Do(func(d *Data) error {
		p := d.PersonByName(argS(args, "person"))
		if p == nil {
			return fmt.Errorf("نفر پیدا نشد: %s", argS(args, "person"))
		}
		if !canMessage(u, p) {
			return fmt.Errorf("اجازهٔ پیام به %s را ندارید", p.Name)
		}
		text := argS(args, "text")
		if text == "" {
			return fmt.Errorf("متن خالی است")
		}
		fu, _ := args["followup"].(bool)
		dr := a.createDraft(d, p, text, u.PersonID, fu)
		ch := map[string]string{"telegram": "تلگرام", "bale": "بله"}[p.Channel]
		out = fmt.Sprintf("پیش‌نویس آماده شد (شناسه %s) برای %s در %s:\n«%s»\nهنوز نرفته. اگر کاربر گفت بفرست، send_draft را بزن.", dr.ID, p.Name, ch, dr.Text)
		if p.ChatID == 0 {
			out += "\nتوجه: " + p.Name + " هنوز ربات را وصل نکرده."
		}
		return nil
	})
	return out, err
}

func toolSendDraft(a *App, u *User, args map[string]any) (string, error) {
	var outs []Out
	var name string
	err := a.Store.Do(func(d *Data) error {
		dr := d.Draft(argS(args, "draft_id"))
		if dr == nil {
			return fmt.Errorf("پیش‌نویس پیدا نشد")
		}
		p := d.Person(dr.ToPersonID)
		if p == nil || !canMessage(u, p) {
			return fmt.Errorf("اجازه ندارید")
		}
		o, err := a.sendDraft(d, dr, u.PersonID)
		if err != nil {
			return err
		}
		outs, name = o, p.Name
		return nil
	})
	if err != nil {
		return "", err
	}
	a.flush(outs)
	return "فرستاده شد به " + name + ".", nil
}

func toolApprovals(a *App, u *User, _ map[string]any) (string, error) {
	var sb strings.Builder
	a.Store.View(func(d *Data) {
		for _, ap := range d.Approvals {
			if ap.Status == "pending" || ap.Status == "asked" {
				fmt.Fprintf(&sb, "- [%s] %s · %s · %s\n", ap.ID, ap.Title, ap.Detail, pname(d, ap.PersonID))
			}
		}
		today := dayKey(a.mgrNow())
		for _, pl := range d.Plans {
			if pl.Status == "draft" && pl.Date >= today {
				fmt.Fprintf(&sb, "- برنامهٔ %s برای %s [plan %s]:\n", pname(d, pl.PersonID), relDay(pl.Date, today), pl.ID)
				for _, id := range pl.TaskIDs {
					if t := d.Task(id); t != nil {
						sb.WriteString("   " + taskLine(d, t, today) + "\n")
					}
				}
			}
		}
	})
	if sb.Len() == 0 {
		return "چیزی منتظر تأیید نیست.", nil
	}
	return sb.String(), nil
}

func toolDecide(a *App, u *User, args map[string]any) (string, error) {
	var outs []Out
	var res string
	err := a.Store.Do(func(d *Data) error {
		if id := argS(args, "plan_id"); id != "" {
			pl := d.Plan(id)
			if pl == nil {
				return fmt.Errorf("برنامه پیدا نشد")
			}
			outs = a.approvePlan(d, pl, u.PersonID)
			res = "برنامهٔ " + pname(d, pl.PersonID) + " تأیید شد."
			return nil
		}
		ap := d.Approval(argS(args, "approval_id"))
		if ap == nil {
			return fmt.Errorf("درخواست پیدا نشد")
		}
		dec := argS(args, "decision")
		if dec != "ok" && dec != "no" && dec != "ask" {
			return fmt.Errorf("decision باید ok، no یا ask باشد")
		}
		outs = a.decideApproval(d, ap, dec, u.PersonID)
		res = "ثبت شد: " + ap.Title + " → " + map[string]string{"ok": "تأیید", "no": "رد", "ask": "پرسیده شد"}[dec]
		return nil
	})
	a.flush(outs)
	return res, err
}

func toolSetPlan(a *App, u *User, args map[string]any) (string, error) {
	var res string
	err := a.Store.Do(func(d *Data) error {
		p := d.PersonByName(argS(args, "person"))
		if p == nil {
			return fmt.Errorf("نفر پیدا نشد")
		}
		date := argS(args, "date")
		if date == "" {
			date = dayKey(a.localNow(p).AddDate(0, 0, 1))
		}
		var ids []string
		for _, id := range strings.Split(argS(args, "task_ids"), ",") {
			id = strings.TrimSpace(id)
			if t := d.Task(id); t != nil && t.PersonID == p.ID && t.Status == "open" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return fmt.Errorf("هیچ کار باز معتبری برای %s نیست", p.Name)
		}
		pl := d.PlanFor(p.ID, date)
		if pl != nil && pl.Status == "sent" {
			return fmt.Errorf("برنامهٔ این روز قبلاً رفته")
		}
		if pl == nil {
			pl = &Plan{ID: newID(), PersonID: p.ID, Date: date, CreatedAt: a.Now()}
			d.Plans = append(d.Plans, pl)
		}
		pl.TaskIDs, pl.Note, pl.Status = ids, argS(args, "note"), "draft"
		res = fmt.Sprintf("برنامهٔ %s برای %s ذخیره شد (plan %s).", p.Name, relDay(date, dayKey(a.localNow(p))), pl.ID)
		if a.Cfg.UnapprovedPlan == "send" {
			res += " سر ساعت صبحش خودکار می‌رود."
		} else {
			res += " منتظر تأیید مدیر است."
		}
		return nil
	})
	return res, err
}
