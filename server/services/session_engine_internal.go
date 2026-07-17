package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aymanbagabas/go-pty"
)

// InternalPtySessionEngine is a SessionEngine that owns each session's process
// and PTY directly — no tmux. The `pcd` server keeps the process alive
// independently of any viewer, buffers output in a per-session ring buffer, and
// fans that output out through the OutputHandler.
//
// It uses go-pty, which maps to a Unix PTY on mac/Linux and a ConPTY on
// Windows, so pcd runs natively on all three with no tmux. The same design can
// later move into a separate pcd-sessiond daemon — with no caller change.
//
// The core invariant:
//
//	Detach removes a viewer and NEVER touches the process.
//	Kill (via Delete/Restart/explicit kill) is the only thing that ends it.
type InternalPtySessionEngine struct {
	mu              sync.RWMutex
	sessions        map[string]*internalPtySession
	handler         OutputHandler
	scrollbackBytes int
}

type internalPtySession struct {
	mu        sync.RWMutex
	info      SessionInfo
	req       CreateSessionRequest
	pty       pty.Pty
	cmd       *pty.Cmd
	buffer    *RingBuffer
	modes     *terminalModes
	queries   *ptyQueryResponder
	flow      *flowControl
	viewers   map[string]struct{}
	status    string
	closeOnce sync.Once
}

// closePty closes the PTY exactly once (idempotent), unblocking the read pump.
func (s *internalPtySession) closePty() {
	s.closeOnce.Do(func() {
		if s.pty != nil {
			s.pty.Close()
		}
	})
}

// NewInternalPtySessionEngine builds the engine. scrollbackBytes bounds each
// session's replay buffer (0 → 512KB default).
func NewInternalPtySessionEngine(scrollbackBytes int) *InternalPtySessionEngine {
	if scrollbackBytes <= 0 {
		scrollbackBytes = 512 * 1024
	}
	return &InternalPtySessionEngine{
		sessions:        make(map[string]*internalPtySession),
		scrollbackBytes: scrollbackBytes,
	}
}

func (e *InternalPtySessionEngine) SetOutputHandler(h OutputHandler) {
	e.mu.Lock()
	e.handler = h
	e.mu.Unlock()
}

func (e *InternalPtySessionEngine) outputHandler() OutputHandler {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.handler
}

func (e *InternalPtySessionEngine) Create(req CreateSessionRequest) (*SessionInfo, error) {
	cols := req.Cols
	rows := req.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to open pty: %w", err)
	}
	// Resize is best-effort; the client sends a real size on attach.
	_ = p.Resize(cols, rows)

	command, cmdArgs := resolveLaunchCommand(req.Command, req.Args)
	cmd := p.Command(command, cmdArgs...)
	cmd.Dir = req.Cwd
	cmd.Env = withAgentPath(append(os.Environ(),
		"TERM=xterm-256color",
		"LANG=ko_KR.UTF-8",
		"LC_ALL=ko_KR.UTF-8",
	))
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	now := time.Now().UTC()
	s := &internalPtySession{
		info: SessionInfo{
			ID:        req.ID,
			Type:      req.Type,
			Command:   req.Command,
			Args:      req.Args,
			Cwd:       req.Cwd,
			Status:    SessionRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		req:     req,
		pty:     p,
		cmd:     cmd,
		buffer:  NewRingBuffer(e.scrollbackBytes),
		modes:   newTerminalModes(),
		queries: &ptyQueryResponder{},
		flow:    newFlowControl(),
		viewers: make(map[string]struct{}),
		status:  SessionRunning,
	}

	e.mu.Lock()
	e.sessions[req.ID] = s
	e.mu.Unlock()

	// Exactly one read pump per session (never one per viewer), plus a waiter
	// that detects natural process exit.
	go e.readPump(s)
	go e.waitProc(s)

	info := s.snapshotInfo()
	return &info, nil
}

// waitProc blocks until the process exits, marks the session exited (unless it
// was explicitly killed), and closes the PTY to unblock the read pump.
func (e *InternalPtySessionEngine) waitProc(s *internalPtySession) {
	if s.cmd != nil {
		_ = s.cmd.Wait()
	}
	s.mu.Lock()
	if s.status != SessionKilled {
		s.status = SessionExited
		s.info.Status = SessionExited
		s.info.UpdatedAt = time.Now().UTC()
	}
	s.mu.Unlock()
	s.closePty()
}

// readPump is the single goroutine draining a session's PTY: it appends output
// to the ring buffer and forwards it to the OutputHandler. When the PTY returns
// an error/EOF the process has ended; unless it was Killed we mark it exited.
func (e *InternalPtySessionEngine) readPump(s *internalPtySession) {
	buf := make([]byte, 4096)
	// carry holds the trailing bytes of an incomplete UTF-8 sequence (e.g. a
	// Korean char split across two PTY reads). We hold them until the next read
	// completes the character — otherwise `string(data)` + JSON marshalling
	// would replace the broken bytes with U+FFFD (visible as mojibake).
	var carry []byte
	emit := func(b []byte) {
		if len(b) == 0 {
			return
		}
		data := make([]byte, len(b))
		copy(data, b)
		s.buffer.Write(data)
		s.modes.scan(data) // remember alt-screen / mouse / cursor-key state for reattach
		// Answer terminal capability queries (DECRQM 2026/2027) the app blocks on.
		// Without this a TUI like Antigravity's `agy` clears the screen and waits
		// forever for a reply that our pass-through PTY never sent — a blank terminal.
		if reply := s.queries.respond(data); len(reply) > 0 {
			_, _ = s.pty.Write(reply)
		}
		if h := e.outputHandler(); h != nil {
			s.flow.added(len(data)) // meter bytes in flight for backpressure
			h(s.info.ID, data)
		}
	}
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, 0, len(carry)+n)
			chunk = append(chunk, carry...)
			chunk = append(chunk, buf[:n]...)
			head, tail := splitIncompleteUTF8(chunk)
			carry = append(carry[:0], tail...)
			emit(head)
			// Backpressure: if the viewer is drowning in unacked output, block here
			// (the PTY fills, the process slows) until it catches up — VS Code's
			// terminal flow control. Self-heals on detach / lost ack so it can't wedge.
			s.flow.wait()
		}
		if err != nil {
			emit(carry) // flush leftovers on exit (don't drop bytes)
			return
		}
	}
}

// splitIncompleteUTF8 splits b into a head that ends on a UTF-8 rune boundary and
// a tail that is the start of an incomplete multi-byte rune (to be completed by
// the next read). If b ends cleanly, tail is empty.
func splitIncompleteUTF8(b []byte) (head, tail []byte) {
	if len(b) == 0 {
		return b, nil
	}
	// Walk back over at most the max UTF-8 rune length to find the last lead byte.
	for i := len(b) - 1; i >= 0 && i > len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return b, nil // last rune is complete
			}
			return b[:i], b[i:] // hold the incomplete trailing bytes
		}
	}
	return b, nil
}

// Attach registers a viewer and returns the scrollback to replay. It NEVER
// starts or restarts the process.
func (e *InternalPtySessionEngine) Attach(sessionID, viewerID string) (*AttachResult, error) {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	s.mu.Lock()
	s.viewers[viewerID] = struct{}{}
	nv := len(s.viewers)
	s.mu.Unlock()
	// Reset backpressure for the (re)attaching viewer: the fresh client will ack
	// the replay below from a clean slate, so any prior in-flight count is stale.
	s.flow.setViewers(nv)

	// Prepend the current DEC private modes (alt-screen, mouse tracking, SGR,
	// application cursor keys, bracketed paste) so a reattaching viewer restores
	// them even when the bounded ring has evicted the app's original enable
	// sequences — otherwise a long "이어하기" session scrolls/renders wrong.
	replay := s.buffer.Snapshot()
	if prefix := s.modes.prefix(); len(prefix) > 0 {
		replay = append(prefix, replay...)
	}
	return &AttachResult{SessionID: sessionID, Replay: replay}, nil
}

// Detach removes a viewer. It MUST NOT close the PTY or kill the process — even
// when the last viewer leaves, the process keeps running.
func (e *InternalPtySessionEngine) Detach(sessionID, viewerID string) error {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	delete(s.viewers, viewerID)
	nv := len(s.viewers)
	s.mu.Unlock()
	// Release any backpressure hold — the leaving viewer won't ack, and with no
	// viewer we must not throttle (output keeps filling the ring for later replay).
	s.flow.setViewers(nv)
	return nil
}

// Ack records that a viewer has processed n bytes of output, draining the
// backpressure backlog so the read pump can resume. n is the byte count the
// client parsed (matching len(data) the server metered); over-acking replay is
// harmless (the counter floors at zero).
func (e *InternalPtySessionEngine) Ack(sessionID string, n int) {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil || n <= 0 {
		return
	}
	s.flow.ack(n)
}

func (e *InternalPtySessionEngine) Write(sessionID string, data []byte) error {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	s.mu.RLock()
	running := s.status == SessionRunning
	p := s.pty
	s.mu.RUnlock()
	if !running || p == nil {
		return fmt.Errorf("session %s is not running", sessionID)
	}
	_, err := p.Write(data)
	return err
}

func (e *InternalPtySessionEngine) Resize(sessionID string, cols, rows int) error {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return nil
	}
	s.mu.RLock()
	p := s.pty
	s.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p.Resize(cols, rows)
}

// Kill terminates the process and PTY. This is the ONLY path that ends a session.
func (e *InternalPtySessionEngine) Kill(sessionID string) error {
	e.mu.Lock()
	s := e.sessions[sessionID]
	delete(e.sessions, sessionID)
	e.mu.Unlock()
	if s == nil {
		return nil
	}

	s.mu.Lock()
	s.status = SessionKilled
	s.info.Status = SessionKilled
	s.info.UpdatedAt = time.Now().UTC()
	cmd := s.cmd
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	s.closePty()
	return nil
}

func (e *InternalPtySessionEngine) Restart(sessionID string) error {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	s.mu.RLock()
	req := s.req
	s.mu.RUnlock()
	if req.Command == "" {
		return fmt.Errorf("session %s cannot be restarted (unknown command)", sessionID)
	}
	_ = e.Kill(sessionID)
	_, err := e.Create(req)
	return err
}

func (e *InternalPtySessionEngine) HasSession(sessionID string) bool {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status == SessionRunning
}

func (e *InternalPtySessionEngine) Get(sessionID string) (*SessionInfo, error) {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	info := s.snapshotInfo()
	return &info, nil
}

func (e *InternalPtySessionEngine) List() ([]SessionInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]SessionInfo, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, s.snapshotInfo())
	}
	return out, nil
}

// npmCLIPackages maps a launcher command to the npm package that provides it, so
// a missing agent CLI can be auto-installed on first launch.
var npmCLIPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
}

// scriptInstallCommands maps a launcher command to a shell install command, for
// agent CLIs that aren't distributed via npm. Antigravity's `agy` uses Google's
// official curl|bash installer (which drops the binary in ~/.local/bin). Like the
// npm path, this runs inside the session on first launch so the user sees the
// install progress and is carried straight into the CLI. Unix-only.
var scriptInstallCommands = map[string]string{
	"agy": "curl -fsSL https://antigravity.google/cli/install.sh | bash",
}

// loginCommands maps an agent CLI to the shell snippet that ensures it's signed
// in, chained right after a fresh install so the user is taken straight through
// auth. An entry is only needed for CLIs that expose an explicit, separate login
// command: codex has `codex login` (and `codex login status` to skip it when
// already authenticated). Antigravity's `agy` has no standalone login command —
// it offers Google OAuth sign-in the first time the CLI itself starts — so simply
// exec-ing it (below) already flows into sign-in and it needs no entry here.
var loginCommands = map[string]string{
	"codex": "{ codex login status >/dev/null 2>&1 || codex login; }",
}

// npmGlobalBin resolves the directory npm installs global CLIs into (e.g.
// ~/.npm-global/bin), computed once via `npm prefix -g`. The server's own PATH
// frequently omits this dir because the user's `export PATH=...:$HOME/.npm-global/bin`
// lives in ~/.bashrc, which a non-interactive server (and `bash -l`, a login
// shell) never sources. Looking here explicitly lets us (a) detect an
// already-installed agent CLI instead of reinstalling it on every launch, and
// (b) run the binary we just installed.
var (
	npmGlobalBinOnce sync.Once
	npmGlobalBinDir  string
)

func npmGlobalBin() string {
	npmGlobalBinOnce.Do(func() {
		npm, err := exec.LookPath("npm")
		if err != nil {
			return
		}
		out, err := exec.Command(npm, "prefix", "-g").Output()
		if err != nil {
			return
		}
		prefix := strings.TrimSpace(string(out))
		if prefix == "" {
			return
		}
		if runtime.GOOS == "windows" {
			npmGlobalBinDir = prefix // .cmd shims live in the prefix root on Windows
		} else {
			npmGlobalBinDir = filepath.Join(prefix, "bin")
		}
	})
	return npmGlobalBinDir
}

// localBinDir is $HOME/.local/bin — where non-npm agent installers (e.g. the
// Antigravity `agy` CLI's curl|bash installer) drop their binary. Like the npm
// global bin dir, it's usually only added to PATH from ~/.bashrc, which the
// server (and `bash -l`) never sources.
func localBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// agentBinDirs are the directories that hold agent CLIs but are typically missing
// from the server's own PATH: the npm global bin dir and ~/.local/bin.
func agentBinDirs() []string {
	dirs := make([]string, 0, 2)
	if d := npmGlobalBin(); d != "" {
		dirs = append(dirs, d)
	}
	if d := localBinDir(); d != "" {
		dirs = append(dirs, d)
	}
	return dirs
}

// withAgentPath prepends the agent bin dirs to PATH in a copy of env, so the
// spawned CLI (and anything it launches) resolves installed agents even when the
// server was started without those dirs on PATH.
func withAgentPath(env []string) []string {
	dirs := agentBinDirs()
	if len(dirs) == 0 {
		return env
	}
	prefix := strings.Join(dirs, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") && !replaced {
			out = append(out, "PATH="+prefix+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="))
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, "PATH="+prefix)
	}
	return out
}

// findAgentCommand returns an absolute path to command if it's installed — either
// on PATH or in one of the agent bin dirs — or "" if it genuinely isn't.
func findAgentCommand(command string) string {
	if p, err := exec.LookPath(command); err == nil {
		return p
	}
	for _, dir := range agentBinDirs() {
		cand := filepath.Join(dir, command)
		if runtime.GOOS == "windows" {
			cand += ".cmd"
		}
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// resolveLaunchCommand decides what to actually spawn for a requested command:
//   - if it's already installed (on PATH or npm global bin), run it (with the
//     Windows .cmd shim + absolute path);
//   - if it's a known agent CLI that isn't installed, install it via npm, chain
//     its login flow, then exec it — all inside the session so the user sees the
//     progress and is carried straight through sign-in;
//   - otherwise run it as-is (it will fail with "not found", as before).
func resolveLaunchCommand(command string, args []string) (string, []string) {
	if resolved := findAgentCommand(command); resolved != "" {
		return normalizeCommand(resolved, args)
	}
	if pkg, ok := npmCLIPackages[command]; ok {
		return bootstrapInstallCommand(command, args, pkg)
	}
	// Non-npm CLIs (e.g. Antigravity's `agy`) install via a curl|bash script —
	// Unix-only; on Windows fall through and let it run/fail as-is.
	if runtime.GOOS != "windows" {
		if script, ok := scriptInstallCommands[command]; ok {
			return bootstrapScriptInstallCommand(command, args, script)
		}
	}
	return normalizeCommand(command, args)
}

// normalizeCommand applies the Windows .cmd shim and resolves the executable
// against PATH (go-pty looks bare names up relative to the working dir, not PATH).
func normalizeCommand(command string, args []string) (string, []string) {
	command, args = windowsShim(command, args)
	if resolved, err := exec.LookPath(command); err == nil {
		command = resolved
	}
	return command, args
}

// bootstrapInstallCommand builds a command that installs the CLI (npm -g), makes
// the freshly-installed binary reachable, chains its login flow, and then runs
// it — so the npm output streams into the terminal and the user is carried from
// install → sign-in → running CLI in one go. Requires Node/npm to be available.
//
// The PATH step is essential: `npm install -g` drops the binary into
// `$(npm prefix -g)/bin` (e.g. ~/.npm-global/bin), and that dir is usually only
// added to PATH from ~/.bashrc — which the `bash -l` login shell here does NOT
// source — so a bare `exec claude` right after install would fail "command not
// found". Prepending the global bin dir guarantees the just-installed CLI runs.
func bootstrapInstallCommand(command string, args []string, pkg string) (string, []string) {
	run := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if runtime.GOOS == "windows" {
		login := ""
		if snip, ok := loginCommands[command]; ok {
			login = " && " + strings.NewReplacer(">/dev/null", ">nul", "{ ", "(", "; }", ")").Replace(snip)
		}
		line := "echo Installing " + command + " (first run)... && npm install -g " + pkg +
			" && for /f \"delims=\" %g in ('npm prefix -g') do set \"PATH=%g;%PATH%\"" +
			login + " && " + run
		shell := "cmd"
		if resolved, err := exec.LookPath("cmd"); err == nil {
			shell = resolved
		}
		return shell, []string{"/c", line}
	}
	login := ""
	if snip, ok := loginCommands[command]; ok {
		login = " && " + snip
	}
	line := "echo 'Installing " + command + " (first run)...'; npm install -g " + pkg +
		" && export PATH=\"$(npm prefix -g)/bin:$PATH\"" +
		login + " && exec " + run
	shell := "bash"
	if resolved, err := exec.LookPath("bash"); err == nil {
		shell = resolved
	}
	return shell, []string{"-lc", line}
}

// bootstrapScriptInstallCommand builds a command that installs a non-npm agent
// CLI via its official shell installer, puts ~/.local/bin (where these installers
// drop the binary) on PATH, and then execs it — so the install output streams
// into the terminal and the user goes install → first-run sign-in → running CLI
// in one go. Unix-only (the installers are curl|bash).
func bootstrapScriptInstallCommand(command string, args []string, script string) (string, []string) {
	run := strings.TrimSpace(command + " " + strings.Join(args, " "))
	login := ""
	if snip, ok := loginCommands[command]; ok {
		login = " && " + snip
	}
	line := "echo 'Installing " + command + " (first run)...'; " + script +
		" && export PATH=\"$HOME/.local/bin:$PATH\"" +
		login + " && exec " + run
	shell := "bash"
	if resolved, err := exec.LookPath("bash"); err == nil {
		shell = resolved
	}
	return shell, []string{"-lc", line}
}

// windowsShim adapts a command for Windows ConPTY. Windows CreateProcess (used
// by go-pty's ConPTY) cannot launch batch/script shims like `.cmd`/`.bat`/`.ps1`
// directly — and npm-installed CLIs (claude, codex) are `.cmd` shims. So
// on Windows, unless the command is already an `.exe`, run it through `cmd.exe /c`
// (which resolves the shim via PATHEXT). No-op on macOS/Linux.
func windowsShim(command string, args []string) (string, []string) {
	if runtime.GOOS != "windows" {
		return command, args
	}
	if strings.HasSuffix(strings.ToLower(command), ".exe") {
		return command, args
	}
	shimArgs := append([]string{"/c", command}, args...)
	return "cmd", shimArgs
}

func (s *internalPtySession) snapshotInfo() SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := s.info
	info.Status = s.status
	return info
}
