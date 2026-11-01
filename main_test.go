package main

import (
	"fmt"
	"os"
	"testing"
)

// Every test runs with the re-entrant lock detector; one hit anywhere fails the run.
func TestMain(m *testing.M) {
	code := m.Run()
	if n := reentrantHits.Load(); n > 0 {
		fmt.Printf("FAIL: %d re-entrant store lock(s) — this is the bug class that froze 0.4.0\n", n)
		code = 1
	}
	os.Exit(code)
}

// The detector itself must work.
func TestReentrantDetected(t *testing.T) {
	st, _ := OpenStore(t.TempDir())
	defer func() {
		r := recover()
		if r != ErrReentrant {
			t.Fatalf("expected ErrReentrant, got %v", r)
		}
		reentrantHits.Add(-1) // expected here
	}()
	st.Do(func(d *Data) error {
		st.View(func(*Data) {})
		return nil
	})
}
