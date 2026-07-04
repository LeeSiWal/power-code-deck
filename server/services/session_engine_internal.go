package services

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

// InternalPtySessionEngine is a SessionEngine that owns each session's process
// and PTY directly — no tmux. The `pcd` server keeps the process alive
// independently of any viewer, buffers output in a per-session ring buffer, and
// fans that output out through the OutputHandler.
//
// This is the mac/Linux-native path (creack/pty). A later variant can swap
// creack/pty for go-pty/ConPTY to run natively on Windows, and the same design
// can move into a separate pcd-sessiond daemon — with no caller change.
//
// The core invariant is identical to the tmux engine:
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
	mu      sync.RWMutex
	info    SessionInfo
	req     CreateSessionRequest
	ptmx    *os.File
	cmd     *exec.Cmd
	buffer  *RingBuffer
	viewers map[string]struct{}
	status  string
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

	cmd := exec.Command(req.Command, req.Args...)
	cmd.Dir = req.Cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"LANG=ko_KR.UTF-8",
		"LC_ALL=ko_KR.UTF-8",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
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
		ptmx:    ptmx,
		cmd:     cmd,
		buffer:  NewRingBuffer(e.scrollbackBytes),
		viewers: make(map[string]struct{}),
		status:  SessionRunning,
	}

	e.mu.Lock()
	e.sessions[req.ID] = s
	e.mu.Unlock()

	// Exactly one read pump per session (never one per viewer).
	go e.readPump(s)

	info := s.snapshotInfo()
	return &info, nil
}

// readPump is the single goroutine draining a session's PTY: it appends output
// to the ring buffer and forwards it to the OutputHandler. When the PTY returns
// an error/EOF the process has ended; unless it was Killed we mark it exited.
func (e *InternalPtySessionEngine) readPump(s *internalPtySession) {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.buffer.Write(data)
			if h := e.outputHandler(); h != nil {
				h(s.info.ID, data)
			}
		}
		if err != nil {
			break
		}
	}

	s.mu.Lock()
	if s.status != SessionKilled {
		s.status = SessionExited
		s.info.Status = SessionExited
		s.info.UpdatedAt = time.Now().UTC()
	}
	s.mu.Unlock()
	s.ptmx.Close()
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
	ptmx := s.ptmx
	s.mu.RUnlock()
	if !running || ptmx == nil {
		return fmt.Errorf("session %s is not running", sessionID)
	}
	_, err := ptmx.Write(data)
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
	ptmx := s.ptmx
	s.mu.RUnlock()
	if ptmx == nil {
		return nil
	}
	return pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
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
	ptmx := s.ptmx
	cmd := s.cmd
	s.mu.Unlock()

	if ptmx != nil {
		ptmx.Close()
	}
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
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

func (s *internalPtySession) snapshotInfo() SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := s.info
	info.Status = s.status
	return info
}
