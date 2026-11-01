package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Self-update: a root systemd timer runs `rooznegar self-update` every few minutes.
// It looks at the latest GitHub release of update_repo, and if it is newer than this binary:
// downloads the binary for this machine, checks its SHA-256, swaps it in, restarts the service
// and waits for /healthz to report the new version. If that doesn't happen, it puts the old
// binary back, restarts again and leaves a note the service reports to the managers.

//go:embed CHANGELOG.md
var changelog string

// latestChanges: the bullet lines of the newest CHANGELOG section.
func latestChanges() string {
	var out []string
	in := false
	for _, l := range strings.Split(changelog, "\n") {
		if strings.HasPrefix(l, "## ") {
			if in {
				break
			}
			in = true
			continue
		}
		if in && strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return strings.Join(out, "\n")
}

// semver compare of "1.2.3" strings: -1, 0, 1
func cmpVersion(a, b string) int {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

type ghRelease struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

type updater struct {
	Repo, Token, API string
	HTTP             *http.Client
}

func (u *updater) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if u.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.Token)
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		return nil, fmt.Errorf("github %s: %s %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

func (u *updater) latest(ctx context.Context) (*ghRelease, error) {
	resp, err := u.get(ctx, u.API+"/repos/"+u.Repo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r ghRelease
	return &r, json.NewDecoder(resp.Body).Decode(&r)
}

func (u *updater) asset(ctx context.Context, r *ghRelease, name string, max int64) ([]byte, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			resp, err := u.get(ctx, a.URL, "application/octet-stream")
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			return io.ReadAll(io.LimitReader(resp.Body, max))
		}
	}
	return nil, fmt.Errorf("فایل %s در نسخهٔ %s نیست", name, r.Tag)
}

const updateNote = "update-note.txt" // in DataDir; the service reports it to managers and deletes it

// selfUpdate returns the version it installed ("" when already up to date).
func selfUpdate(cfg Config, exe string, restart func() error, health func(string) bool) (string, error) {
	if cfg.UpdateRepo == "" {
		return "", errors.New("update_repo در تنظیمات خالی است")
	}
	u := &updater{Repo: cfg.UpdateRepo, Token: cfg.UpdateToken, API: cfg.UpdateAPI, HTTP: &http.Client{Timeout: 3 * time.Minute}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rel, err := u.latest(ctx)
	if err != nil {
		return "", err
	}
	want := strings.TrimPrefix(rel.Tag, "v")
	if cmpVersion(want, version) <= 0 {
		return "", nil
	}
	name := "rooznegar-linux-" + runtime.GOARCH
	bin, err := u.asset(ctx, rel, name, 80<<20)
	if err != nil {
		return "", err
	}
	sums, err := u.asset(ctx, rel, "SHA256SUMS", 1<<16)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(bin)
	got := hex.EncodeToString(h[:])
	okSum := false
	for _, l := range strings.Split(string(sums), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && f[0] == got {
			okSum = true
		}
	}
	if !okSum {
		return "", fmt.Errorf("SHA-256 فایل %s نمی‌خواند؛ نصب نشد", name)
	}
	dir := filepath.Dir(exe)
	tmp := filepath.Join(dir, ".rooznegar.new")
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	// the new binary must run here and say the right version
	if out, err := exec.Command(tmp, "version").Output(); err != nil || strings.TrimSpace(string(out)) != want {
		return "", fmt.Errorf("فایل تازه اجرا نشد یا نسخه‌اش %q است، نه %s", strings.TrimSpace(string(out)), want)
	}
	prev := exe + ".prev"
	old, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(prev, old, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, exe); err != nil {
		return "", err
	}
	note := func(s string) { os.WriteFile(filepath.Join(cfg.DataDir, updateNote), []byte(s), 0o644) }
	if err := restart(); err == nil && health(want) {
		log.Printf("به‌روز شد: %s → %s", version, want)
		return want, nil
	}
	// roll back
	os.Rename(prev, exe)
	restart()
	note(fmt.Sprintf("⚠️ به‌روزرسانی به نسخهٔ %s بالا نیامد؛ نسخهٔ %s برگشت و کار می‌کند.", want, version))
	return "", fmt.Errorf("نسخهٔ %s بالا نیامد؛ نسخهٔ قبلی برگشت", want)
}

func runSelfUpdate(cfg Config) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		log.Fatal(err)
	}
	restart := func() error { return exec.Command("systemctl", "restart", "rooznegar").Run() }
	health := func(want string) bool {
		url := "http://" + cfg.Listen + "/healthz"
		for i := 0; i < 40; i++ {
			time.Sleep(time.Second)
			resp, err := http.Get(url)
			if err != nil {
				continue
			}
			var h struct {
				Version string `json:"version"`
			}
			json.NewDecoder(resp.Body).Decode(&h)
			resp.Body.Close()
			if h.Version == want {
				return true
			}
		}
		return false
	}
	v, err := selfUpdate(cfg, exe, restart, health)
	switch {
	case err != nil:
		log.Printf("به‌روزرسانی: %v", err)
		os.Exit(1)
	case v == "":
		fmt.Println("به‌روز است:", version)
	default:
		fmt.Println("نصب شد:", v)
	}
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
}

// announceVersion: after an update (or a failed one), tell the managers once.
func (a *App) announceVersion() {
	var outs []Out
	notePath := filepath.Join(a.Cfg.DataDir, updateNote)
	a.Store.Do(func(d *Data) error {
		if b, err := os.ReadFile(notePath); err == nil {
			outs = append(outs, notifyAdmins(d, strings.TrimSpace(string(b)), nil)...)
			os.Remove(notePath)
		}
		prev := d.Flags["version"]
		if prev == version {
			return nil
		}
		d.Flags["version"] = version
		if prev != "" && cmpVersion(version, prev) > 0 {
			msg := "🔄 دستیار کندو به‌روز شد (نسخهٔ " + fa(version) + ")"
			if c := latestChanges(); c != "" {
				msg += "\n" + c
			}
			outs = append(outs, notifyAdmins(d, msg, nil)...)
		}
		return nil
	})
	a.flush(outs)
}
