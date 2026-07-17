package services

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real end-to-end check: spawn `agy` through the actual engine and confirm it
// renders its UI (past the DECRQM block) instead of a blank screen. Skipped
// unless agy is installed and RUN_AGY_E2E=1 (it launches a heavy real binary).
func TestAgyRendersThroughEngine(t *testing.T) {
	if os.Getenv("RUN_AGY_E2E") != "1" {
		t.Skip("set RUN_AGY_E2E=1 to run the live agy render check")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "bin", "agy")); err != nil {
		t.Skip("agy not installed")
	}

	eng := NewInternalPtySessionEngine(512 * 1024)
	var mu sync.Mutex
	var out []byte
	eng.SetOutputHandler(func(_ string, data []byte) {
		mu.Lock()
		out = append(out, data...)
		mu.Unlock()
	})

	if _, err := eng.Create(CreateSessionRequest{
		ID: "agy-e2e", Type: "antigravity", Command: "agy", Cwd: "/tmp", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer eng.Kill("agy-e2e")

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		rendered := len(out) > 400
		mu.Unlock()
		if rendered {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	mu.Lock()
	got := string(out)
	mu.Unlock()
	if len(got) <= 400 {
		t.Fatalf("agy appears blocked/blank: only %d bytes emitted (expected a full render)", len(got))
	}
	t.Logf("agy emitted %d bytes; contains splash=%v", len(got),
		strings.Contains(got, "Antigravity") || strings.Contains(got, "trust"))
}
