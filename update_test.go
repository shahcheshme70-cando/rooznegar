package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelfUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	dir := t.TempDir()
	newBin := filepath.Join(dir, "new")
	if out, err := exec.Command("go", "build", "-o", newBin, "-ldflags", "-X main.version=9.9.9", ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	bin, _ := os.ReadFile(newBin)
	sum := sha256.Sum256(bin)
	name := "rooznegar-linux-" + runtime.GOARCH
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer TOK" {
			http.Error(w, "no", 401)
			return
		}
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9", "assets": []any{
				map[string]any{"name": name, "url": srv.URL + "/a/bin"},
				map[string]any{"name": "SHA256SUMS", "url": srv.URL + "/a/sums"}}})
		case "/a/bin":
			w.Write(bin)
		case "/a/sums":
			w.Write([]byte(sums))
		}
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.DataDir, cfg.UpdateRepo, cfg.UpdateToken, cfg.UpdateAPI = dir, "o/r", "TOK", srv.URL
	exe := filepath.Join(dir, "rooznegar")
	os.WriteFile(exe, []byte("old binary"), 0o755)
	restarts := 0
	restart := func() error { restarts++; return nil }

	// healthy: installed, old kept as .prev
	v, err := selfUpdate(cfg, exe, restart, func(want string) bool { return want == "9.9.9" })
	if err != nil || v != "9.9.9" {
		t.Fatalf("update: %q %v", v, err)
	}
	if got, _ := os.ReadFile(exe); len(got) != len(bin) {
		t.Fatal("binary not replaced")
	}
	if old, _ := os.ReadFile(exe + ".prev"); string(old) != "old binary" {
		t.Fatal("previous binary not kept")
	}

	// unhealthy: rolled back, note left for the managers
	os.WriteFile(exe, []byte("old binary"), 0o755)
	restarts = 0
	if _, err := selfUpdate(cfg, exe, restart, func(string) bool { return false }); err == nil {
		t.Fatal("expected failure")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" || restarts != 2 {
		t.Fatalf("not rolled back (restarts=%d)", restarts)
	}
	if n, _ := os.ReadFile(filepath.Join(dir, updateNote)); !strings.Contains(string(n), "برگشت") {
		t.Fatal("no note for managers")
	}

	// tampered: checksum mismatch, nothing touched
	sums = strings.Repeat("0", 64) + "  " + name + "\n"
	if _, err := selfUpdate(cfg, exe, restart, func(string) bool { return true }); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("checksum not enforced: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("binary touched despite bad checksum")
	}
}

func TestCmpVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		w    int
	}{{"0.9.0", "0.8.2", 1}, {"v0.9.0", "0.9.0", 0}, {"0.10.0", "0.9.9", 1}, {"1.0.0", "1.0.1", -1}} {
		if g := cmpVersion(c.a, c.b); g != c.w {
			t.Errorf("%s vs %s = %d", c.a, c.b, g)
		}
	}
	if !strings.Contains(latestChanges(), "به‌روزرسانی خودکار") {
		t.Error("changelog not embedded")
	}
}
