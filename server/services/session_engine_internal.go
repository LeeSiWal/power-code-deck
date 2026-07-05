package services

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

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

	command, cmdArgs := windowsShim(req.Command, req.Args)
	// Resolve the executable against PATH ourselves. go-pty looks a bare command
	// name up relative to the working directory (Dir), not PATH, so we pass an
	// absolute path to avoid "not found in <workingDir>".
	if resolved, lookErr := exec.LookPath(command); lookErr == nil {
		command = resolved
	}
	cmd := p.Command(command, cmdArgs...)
	cmd.Dir = req.Cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"LANG=ko_KR.UTF-8",
		"LC_ALL=ko_KR.UTF-8",
	)
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
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.buffer.Write(data)
			if h := e.outputHandler(); h != nil {
				h(s.info.ID, data)
			}
		}
		if err != nil {
			return
		}
	}
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
	s.mu.Unlock()

	return &AttachResult{SessionID: sessionID, Replay: s.buffer.Snapshot()}, nil
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
	s.mu.Unlock()
	return nil
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

// windowsShim adapts a command for Windows ConPTY. Windows CreateProcess (used
// by go-pty's ConPTY) cannot launch batch/script shims like `.cmd`/`.bat`/`.ps1`
// directly — and npm-installed CLIs (claude, gemini, codex) are `.cmd` shims. So
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
