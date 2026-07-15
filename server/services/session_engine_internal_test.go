package services

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureHandler collects all output for a session so tests can assert on it.
type captureHandler struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newCapture() *captureHandler { return &captureHandler{data: map[string][]byte{}} }

func (c *captureHandler) handle(sessionID string, data []byte) {
	c.mu.Lock()
	c.data[sessionID] = append(c.data[sessionID], data...)
	c.mu.Unlock()
}

func (c *captureHandler) get(sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.data[sessionID])
}

func newInternalSession(t *testing.T, e *InternalPtySessionEngine, id, command string, args ...string) {
	t.Helper()
	if _, err := e.Create(CreateSessionRequest{
		ID: id, Type: "shell", Command: command, Args: args, Cwd: "/tmp", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !e.HasSession(id) {
		t.Fatal("session should be running immediately after Create")
	}
}

// Create → Write → output reaches the handler; Attach replays scrollback.
func TestInternalWriteAndReplay(t *testing.T) {
	cap := newCapture()
	e := NewInternalPtySessionEngine(64 * 1024)
	e.SetOutputHandler(cap.handle)

	id := "int-io"
	newInternalSession(t, e, id, "cat") // cat echoes stdin to stdout
	defer e.Kill(id)

	if err := e.Write(id, []byte("ping-123\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Wait for the pump to observe the echoed output.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(cap.get(id), "ping-123") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(cap.get(id), "ping-123") {
		t.Fatalf("output handler never received input echo; got %q", cap.get(id))
	}

	// A fresh viewer should get the scrollback replayed.
	res, err := e.Attach(id, "viewer-1")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !bytes.Contains(res.Replay, []byte("ping-123")) {
		t.Fatalf("Attach replay missing prior output; got %q", res.Replay)
	}
}

// The core guarantee: detaching viewers never kills the process.
func TestInternalDetachDoesNotKill(t *testing.T) {
	e := NewInternalPtySessionEngine(0)
	id := "int-detach"
	newInternalSession(t, e, id, "sleep", "300")
	defer e.Kill(id)

	e.Attach(id, "viewer-A")
	e.Attach(id, "viewer-B")

	if err := e.Detach(id, "viewer-A"); err != nil {
		t.Fatalf("Detach A: %v", err)
	}
	if !e.HasSession(id) {
		t.Fatal("session must survive while viewer-B is attached")
	}

	// Last viewer leaves — process MUST still be alive.
	if err := e.Detach(id, "viewer-B"); err != nil {
		t.Fatalf("Detach B: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if !e.HasSession(id) {
		t.Fatal("Detach must NOT kill the process, even for the last viewer")
	}
}

// Kill ends the process; Restart brings up a fresh one.
func TestInternalKillAndRestart(t *testing.T) {
	e := NewInternalPtySessionEngine(0)
	id := "int-kill"
	newInternalSession(t, e, id, "sleep", "300")

	if err := e.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if e.HasSession(id) {
		t.Fatal("Kill must end the session")
	}

	// Recreate and restart.
	newInternalSession(t, e, id, "sleep", "300")
	defer e.Kill(id)
	if err := e.Restart(id); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if !e.HasSession(id) {
		t.Fatal("session should be running after Restart")
	}
}

// A process that exits on its own is reported as exited (not killed).
func TestInternalNaturalExit(t *testing.T) {
	e := NewInternalPtySessionEngine(0)
	id := "int-exit"
	if _, err := e.Create(CreateSessionRequest{
		ID: id, Command: "true", Cwd: "/tmp", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer e.Kill(id)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, err := e.Get(id)
		if err == nil && info.Status == SessionExited {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	info, _ := e.Get(id)
	t.Fatalf("expected status %q after natural exit, got %+v", SessionExited, info)
}

// A missing agent CLI is turned into an install-then-run bootstrap command.
func TestBootstrapInstallCommand(t *testing.T) {
	shell, args := bootstrapInstallCommand("claude", nil, "@anthropic-ai/claude-code")
	if runtime.GOOS == "windows" {
		if len(args) != 2 || args[0] != "/c" {
			t.Fatalf("windows args = %v", args)
		}
	} else {
		if !strings.HasSuffix(shell, "bash") {
			t.Fatalf("shell = %q, want bash", shell)
		}
		if len(args) != 2 || args[0] != "-lc" {
			t.Fatalf("args = %v", args)
		}
		if !strings.Contains(args[1], "npm install -g @anthropic-ai/claude-code") {
			t.Fatalf("missing npm install in %q", args[1])
		}
		if !strings.Contains(args[1], "exec claude") {
			t.Fatalf("missing exec claude in %q", args[1])
		}
		// The freshly-installed binary must be reachable: the global npm bin dir
		// (only ever on PATH via ~/.bashrc, which `bash -l` doesn't source) is
		// prepended before exec, else the run-after-install fails "not found".
		if !strings.Contains(args[1], `export PATH="$(npm prefix -g)/bin:$PATH"`) {
			t.Fatalf("missing global-bin PATH prepend in %q", args[1])
		}
	}
}

// Codex is carried from install straight into its login flow; a plain CLI (or
// one whose first run handles auth, like gemini) is not.
func TestBootstrapInstallCommandLogin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login snippet asserted on unix")
	}
	_, codexArgs := bootstrapInstallCommand("codex", nil, "@openai/codex")
	if !strings.Contains(codexArgs[1], "codex login") {
		t.Fatalf("codex bootstrap should chain login, got %q", codexArgs[1])
	}
	// Skip login when already authenticated.
	if !strings.Contains(codexArgs[1], "codex login status") {
		t.Fatalf("codex bootstrap should guard on login status, got %q", codexArgs[1])
	}
	_, geminiArgs := bootstrapInstallCommand("gemini", nil, "@google/gemini-cli")
	if strings.Contains(geminiArgs[1], "login") {
		t.Fatalf("gemini bootstrap must not chain a login command, got %q", geminiArgs[1])
	}
}

// withAgentPath prepends the npm global bin dir to PATH without dropping the
// existing PATH entries.
func TestWithAgentPath(t *testing.T) {
	dir := npmGlobalBin()
	if dir == "" {
		t.Skip("npm not available; global bin dir unknown")
	}
	out := withAgentPath([]string{"FOO=bar", "PATH=/usr/bin"})
	var gotPath string
	for _, kv := range out {
		if strings.HasPrefix(kv, "PATH=") {
			gotPath = strings.TrimPrefix(kv, "PATH=")
		}
	}
	want := dir + string(os.PathListSeparator) + "/usr/bin"
	if gotPath != want {
		t.Fatalf("PATH = %q, want %q", gotPath, want)
	}
}

// Korean output split across PTY reads must not be corrupted: incomplete
// trailing UTF-8 bytes are held back, not sent as broken bytes.
func TestSplitIncompleteUTF8(t *testing.T) {
	full := []byte("안녕") // 6 bytes: 안 = EC 95 88, 녕 = EB 85 95

	if h, tl := splitIncompleteUTF8(full); string(h) != "안녕" || len(tl) != 0 {
		t.Fatalf("complete: head=%q tail=%v", h, tl)
	}
	// Cut after 4 bytes: "안" (3) complete + first byte of "녕" incomplete.
	if h, tl := splitIncompleteUTF8(full[:4]); string(h) != "안" || len(tl) != 1 {
		t.Fatalf("split: head=%q tail=%v", h, tl)
	}
	// Cut after 5 bytes: "안" + two bytes of "녕" (still incomplete).
	if h, tl := splitIncompleteUTF8(full[:5]); string(h) != "안" || len(tl) != 2 {
		t.Fatalf("split2: head=%q tail=%v", h, tl)
	}
	if h, tl := splitIncompleteUTF8([]byte("abc")); string(h) != "abc" || len(tl) != 0 {
		t.Fatalf("ascii: head=%q tail=%v", h, tl)
	}
}

func TestRingBufferCap(t *testing.T) {
	r := NewRingBuffer(10)
	r.Write([]byte("abcdef"))
	r.Write([]byte("ghijkl")) // total 12 → keep last 10
	got := string(r.Snapshot())
	if got != "cdefghijkl" {
		t.Fatalf("ring buffer cap wrong: got %q want %q", got, "cdefghijkl")
	}
	if r.Len() != 10 {
		t.Fatalf("ring buffer len = %d, want 10", r.Len())
	}
}

// A long session evicts the app's original mode-enable sequences from the
// bounded ring, but Attach must still restore them (the resume-scroll bug):
// the reconnecting viewer has to learn the app owns the mouse + is in alt-screen.
func TestInternalAttachRestoresEvictedModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	cap := newCapture()
	e := NewInternalPtySessionEngine(2 * 1024) // tiny ring so the front is evicted
	e.SetOutputHandler(cap.handle)

	id := "int-modes"
	// Enable alt-screen + mouse tracking + SGR up front, then flood far past 2KB.
	script := `printf '\033[?1049h\033[?1000h\033[?1002h\033[?1003h\033[?1006h'; ` +
		`i=0; while [ $i -lt 400 ]; do printf 'padding padding padding line %d\n' $i; i=$((i+1)); done; sleep 5`
	newInternalSession(t, e, id, "sh", "-c", script)
	defer e.Kill(id)

	// Wait until enough output has flowed to overflow the ring and evict the front.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(cap.get(id), "line 399") {
		time.Sleep(20 * time.Millisecond)
	}

	res, err := e.Attach(id, "viewer-1")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	replay := string(res.Replay)

	// The raw ring must have dropped the original enable sequence (proves eviction).
	if strings.Contains(replay[len(res.Replay)-2*1024:], "line 0\n") {
		// (sanity: front content is gone — not strictly required, informational)
	}
	// Yet the replay must OPEN with the restored modes so the client turns on
	// wheel-forwarding and alt-screen rendering.
	wantPrefix := "\x1b[?1049h\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h"
	if !strings.HasPrefix(replay, wantPrefix) {
		t.Fatalf("replay must start with restored modes %q; got prefix %q", wantPrefix, replay[:min(len(replay), 40)])
	}
	// And the original front sequence must actually be gone from the ring tail
	// (otherwise the test isn't exercising eviction). The ring is 2KB; the
	// enable bytes were the very first written.
	if bytes.Count(res.Replay, []byte("\x1b[?1049h")) != 1 {
		t.Fatalf("expected exactly one (restored) alt-screen enable, ring still holds the original")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
