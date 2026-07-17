package services

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// utf8Locale must pick a locale this host actually has generated. Naming one that
// isn't generated is worse than useless: glibc silently falls back to C/POSIX
// (ASCII), which is how we ended up telling every TUI that a 한글 terminal was
// ANSI_X3.4-1968.
func TestUTF8LocaleExistsOnHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no `locale` binary on windows; the var is ignored there")
	}
	out, err := exec.Command("locale", "-a").Output()
	if err != nil {
		t.Skip("locale -a unavailable")
	}
	have := make(map[string]struct{})
	for _, l := range strings.Split(string(out), "\n") {
		have[strings.ToLower(strings.TrimSpace(l))] = struct{}{}
	}
	got := utf8Locale()
	if !strings.Contains(strings.ToUpper(got), "UTF-8") && !strings.Contains(strings.ToLower(got), "utf8") {
		t.Fatalf("locale %q is not UTF-8", got)
	}
	if _, ok := have[strings.ToLower(got)]; !ok {
		t.Fatalf("locale %q is not generated on this host (have e.g. C.utf8/en_US.utf8); a missing locale falls back to ASCII", got)
	}
}

// End-to-end: a real session's charmap must be UTF-8. This is the check that
// actually matters — the env var being set proves nothing if the locale is absent.
func TestSessionCharmapIsUTF8(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("locale/charmap is a unix concept")
	}
	if _, err := exec.LookPath("locale"); err != nil {
		t.Skip("locale binary unavailable")
	}

	eng := NewInternalPtySessionEngine(64 * 1024)
	var mu sync.Mutex
	var out []byte
	eng.SetOutputHandler(func(_ string, data []byte) {
		mu.Lock()
		out = append(out, data...)
		mu.Unlock()
	})

	if _, err := eng.Create(CreateSessionRequest{
		ID: "locale-check", Type: "shell", Command: "bash",
		Args: []string{"-c", "locale charmap; exit"}, Cwd: "/tmp", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer eng.Kill("locale-check")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := string(out)
		mu.Unlock()
		if strings.Contains(s, "UTF-8") {
			if strings.Contains(s, "setlocale") {
				t.Fatalf("session shell warned about the locale:\n%s", s)
			}
			return // charmap is UTF-8 and the shell had nothing to complain about
		}
		if strings.Contains(s, "ANSI_X3.4-1968") {
			t.Fatalf("session charmap fell back to ASCII — 한글 폭 계산이 깨진다:\n%s", s)
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	t.Fatalf("no charmap seen before timeout:\n%s", string(out))
	mu.Unlock()
}
