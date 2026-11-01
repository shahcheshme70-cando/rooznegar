package main

import (
	"bufio"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// version is set at build time by the release workflow (-ldflags "-X main.version=...").
var version = "0.9.0"

func usage() {
	fmt.Fprint(os.Stderr, `روزنگار `+version+`
استفاده:
  rooznegar serve   [-config /etc/rooznegar/config.json]
  rooznegar user    [-config ...] <login> <role> [نام]      افزودن یا تغییر رمز کاربر داشبورد
                    نقش‌ها: admin | senior | lead | finance
  rooznegar seed    [-config ...] <seed.json>              ورود تیم و پروژه‌های اولیه
  rooznegar check   [-config ...]                           آزمودن اتصال‌ها
  rooznegar self-update [-config ...]                       نصب نسخهٔ تازه از GitHub (با systemd timer)
  rooznegar version
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	if cmd == "version" || cmd == "-v" || cmd == "--version" {
		fmt.Println(version)
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/rooznegar/config.json", "config file")
	fs.Parse(os.Args[2:])
	args := fs.Args()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if cmd == "self-update" {
		runSelfUpdate(cfg)
		return
	}
	st, err := OpenStore(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	app := NewApp(cfg, st)
	lock := filepath.Join(cfg.DataDir, "serve.pid")
	if cmd == "user" || cmd == "seed" {
		if b, err := os.ReadFile(lock); err == nil {
			if _, err := os.Stat("/proc/" + strings.TrimSpace(string(b))); err == nil {
				log.Fatal("سرویس روشن است و تغییرها را پاک می‌کند. اول: sudo systemctl stop rooznegar")
			}
		}
	}

	switch cmd {
	case "serve":
		os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o640)
		defer os.Remove(lock)
		serve(app)
	case "user":
		if len(args) < 2 {
			usage()
			os.Exit(2)
		}
		name := args[0]
		if len(args) > 2 {
			name = strings.Join(args[2:], " ")
		}
		fmt.Print("رمز عبور: ")
		pw, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimSpace(pw)
		if len(pw) < 8 {
			log.Fatal("رمز باید دست‌کم ۸ حرف باشد")
		}
		if err := addUser(st, args[0], args[1], name, pw); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ذخیره شد.")
	case "seed":
		if len(args) < 1 {
			usage()
			os.Exit(2)
		}
		n, err := seed(app, args[0])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(n)
	case "check":
		check(app)
	default:
		usage()
		os.Exit(2)
	}
}

func serve(app *App) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, m := range app.Msgr {
		go app.poll(ctx, m)
		log.Printf("%s: در حال گوش دادن", m.Name)
	}
	if len(app.Msgr) == 0 {
		log.Print("هشدار: هیچ توکن ربات (تلگرام یا بله) تنظیم نشده")
	}
	go app.scheduler(ctx)
	go app.announceVersion()
	go app.watchUploads(ctx)
	go app.watchdog(ctx)
	srv := &http.Server{Addr: app.Cfg.Listen, Handler: app.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("داشبورد روی %s", app.Cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	app.FlushAll()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}

// watchdog: if the store stays locked for long, write every goroutine's stack to the log
// (journalctl) once, so a hang can be diagnosed without guessing.
func (a *App) watchdog(ctx context.Context) {
	reported := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		got := make(chan struct{})
		go func() { a.Store.View(func(*Data) {}); close(got) }()
		select {
		case <-got:
			reported = false
		case <-time.After(20 * time.Second):
			if !reported {
				reported = true
				var b strings.Builder
				pprof.Lookup("goroutine").WriteTo(&b, 2)
				log.Printf("هشدار: داده‌ها بیش از ۲۰ ثانیه قفل مانده. وضعیت برای عیب‌یابی:\n%s", b.String())
			}
		}
	}
}

func check(app *App) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, m := range app.Msgr {
		var me struct {
			Username string `json:"username"`
		}
		if err := m.call(ctx, "getMe", map[string]any{}, &me); err != nil {
			fmt.Printf("✗ %s: %v\n", m.Name, err)
		} else {
			fmt.Printf("✓ %s: @%s\n", m.Name, me.Username)
		}
	}
	if app.Claude.Enabled() {
		if t, err := app.Claude.Text(ctx, "فقط بنویس: سلام", "سلام", 20); err != nil {
			fmt.Printf("✗ Claude: %v\n", err)
		} else {
			fmt.Printf("✓ Claude (%s): %s\n", app.Cfg.Model, t)
		}
	} else {
		fmt.Println("– Claude: کلید API تنظیم نشده (ربات فقط با دکمه کار می‌کند)")
	}
	if app.BZ.Enabled() {
		if err := app.BZ.login(ctx); err != nil {
			fmt.Printf("✗ بارگذار: %v\n", err)
		} else {
			fmt.Printf("✓ بارگذار: ورود به %s\n", app.Cfg.BargozarURL)
			var slug string
			app.Store.View(func(d *Data) {
				for _, p := range d.Projects {
					if p.Bargozar != "" {
						slug = p.Bargozar
						break
					}
				}
			})
			if slug == "" {
				fmt.Println("  هنوز برای هیچ پروژه‌ای «نام پروژه در بارگذار» تعیین نشده")
			} else if in, err := app.BZ.Project(ctx, slug); err != nil {
				fmt.Printf("✗ بارگذار، پروژهٔ %s: %v\n", slug, err)
			} else {
				withURL := 0
				for _, f := range in.Files {
					if f.URL != "" && f.Path != "" {
						withURL++
					}
				}
				fmt.Printf("✓ بارگذار، پروژهٔ %s: %d فایل (%d با لینک)، %d لینک آپلود\n", slug, len(in.Files), withURL, len(in.Requests))
				if len(in.Files) > 0 && withURL == 0 {
					fmt.Println("  هشدار: فهرست فایل‌ها شکل دیگری دارد؛ لینک آپلود ساخته می‌شود ولی رسیدن فایل تشخیص داده نمی‌شود. خروجی را برای Claude بفرستید.")
				}
			}
		}
	} else {
		fmt.Println("– بارگذار: تنظیم نشده (لینک آپلود خاموش)")
	}
	if app.STT.Enabled() {
		fmt.Println("– صوت: تنظیم شده (با یک پیام صوتی واقعی امتحان کنید)")
	} else {
		fmt.Println("– صوت: خاموش")
	}
}

// ---------- users & passwords ----------

func hashPass(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, pw, salt, 120000, 32)
	return "pbkdf2$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(k)
}

func checkPass(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 3 || parts[0] != "pbkdf2" {
		return false
	}
	salt, err1 := hex.DecodeString(parts[1])
	want, err2 := hex.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return false
	}
	k, err := pbkdf2.Key(sha256.New, pw, salt, 120000, 32)
	return err == nil && subtle.ConstantTimeCompare(k, want) == 1
}

func addUser(st *Store, login, role, name, pw string) error {
	if _, ok := roleLabels[role]; !ok || role == "staff" {
		return fmt.Errorf("نقش نامعتبر: %s", role)
	}
	return st.Do(func(d *Data) error {
		u := d.UserByLogin(login)
		if u == nil {
			u = &User{ID: newID(), Login: login, MCPToken: newToken()}
			d.Users = append(d.Users, u)
		}
		u.Name, u.Role, u.PassHash = name, role, hashPass(pw)
		if p := d.PersonByName(name); p != nil {
			u.PersonID = p.ID
		}
		d.linkUsers()
		return nil
	})
}

// ---------- seed ----------

type seedFile struct {
	People []struct {
		Name    string  `json:"name"`
		Role    string  `json:"role"`
		Line    string  `json:"line"`
		TZ      string  `json:"tz"`
		Channel string  `json:"channel"`
		Rhythm  Rhythm  `json:"rhythm"`
		Notes   string  `json:"notes"`
		Value   float64 `json:"daily_value"`
	} `json:"people"`
	Projects []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Sub      string `json:"sub"`
		Bargozar string `json:"bargozar"`
	} `json:"projects"`
	Tasks []struct {
		Key         string  `json:"key"`
		Person      string  `json:"person"`
		Project     string  `json:"project"`
		Title       string  `json:"title"`
		Detail      string  `json:"detail"`
		Cycle       string  `json:"cycle"`
		Kind        string  `json:"kind"`
		Unit        string  `json:"unit"`
		DailyTarget float64 `json:"daily_target"`
		Total       float64 `json:"total"`
		Priority    int     `json:"priority"`
		After       string  `json:"after"` // key of the previous link
		Private     bool    `json:"private"`
	} `json:"tasks"`
}

func seed(app *App, path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var s seedFile
	if err := json.Unmarshal(b, &s); err != nil {
		return "", err
	}
	var np, npr, nt int
	err = app.Store.Do(func(d *Data) error {
		for _, x := range s.People {
			if d.PersonByName(x.Name) != nil && normName(d.PersonByName(x.Name).Name) == normName(x.Name) {
				continue
			}
			role := x.Role
			if role == "" {
				role = "staff"
			}
			ch := x.Channel
			if ch == "" {
				ch = "telegram"
			}
			d.People = append(d.People, &Person{ID: newID(), Name: x.Name, Role: role, Line: x.Line, TZ: x.TZ, Channel: ch,
				Rhythm: x.Rhythm, Notes: x.Notes, Active: true, LinkCode: newLinkCode(), DailyValue: x.Value})
			np++
		}
		for i, x := range s.Projects {
			if d.Project(x.ID) != nil {
				continue
			}
			id := x.ID
			if id == "" {
				id = newID()
			}
			d.Projects = append(d.Projects, &Project{ID: id, Name: x.Name, Sub: x.Sub, Order: i, Active: true, Bargozar: x.Bargozar})
			npr++
		}
		keys := map[string]string{}
		for _, x := range s.Tasks {
			p := d.PersonByName(x.Person)
			if p == nil {
				return fmt.Errorf("نفر پیدا نشد: %s", x.Person)
			}
			pr := d.ProjectByName(x.Project)
			kind := x.Kind
			if kind == "" {
				kind = "single"
			}
			cyc := x.Cycle
			if cyc == "" {
				cyc = "value"
			}
			t := &Task{ID: newID(), PersonID: p.ID, Title: x.Title, Detail: x.Detail, Cycle: cyc, Kind: kind, Unit: x.Unit,
				DailyTarget: x.DailyTarget, Total: x.Total, Priority: x.Priority, Status: "open", Ready: true, Private: x.Private,
				CreatedAt: app.Now(), UpdatedAt: app.Now()}
			if pr != nil {
				t.ProjectID = pr.ID
			}
			if x.After != "" {
				t.After = keys[x.After]
				if prev := d.Task(t.After); prev != nil && prev.Status != "done" {
					t.Ready = false
				}
			}
			if x.Key != "" {
				keys[x.Key] = t.ID
			}
			d.Tasks = append(d.Tasks, t)
			nt++
		}
		return nil
	})
	return fmt.Sprintf("%d نفر، %d پروژه، %d کار وارد شد.", np, npr, nt), err
}
