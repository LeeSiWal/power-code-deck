package pty

import (
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aymanbagabas/go-pty"

	"powercodedeck/internal/runtime/session"
)

// Engine is a session.SessionEngine that owns each session's process
// and PTY directly — no tmux. The `pcd` server keeps the process alive
// independently of any viewer, buffers output in a per-session ring buffer, and
// fans that output out through the session.OutputHandler.
//
// It uses go-pty, which maps to a Unix PTY on mac/Linux and a ConPTY on
// Windows, so pcd runs natively on all three with no tmux. The same design can
// later move into a separate pcd-sessiond daemon — with no caller change.
//
// The core invariant:
//
//	Detach removes a viewer and NEVER touches the process.
//	Kill (via Delete/Restart/explicit kill) is the only thing that ends it.
type Engine struct {
	mu              sync.RWMutex
	sessions        map[string]*internalPtySession
	handler         session.OutputHandler
	scrollbackBytes int
	prepareLaunch   PrepareLaunch
}

type internalPtySession struct {
	mu        sync.RWMutex
	info      session.SessionInfo
	req       session.CreateSessionRequest
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

// NewEngine builds the engine. scrollbackBytes bounds each
// session's replay buffer (0 → 512KB default).
func NewEngine(scrollbackBytes int, prepareLaunch PrepareLaunch) *Engine {
	if scrollbackBytes <= 0 {
		scrollbackBytes = 512 * 1024
	}
	return &Engine{
		sessions:        make(map[string]*internalPtySession),
		scrollbackBytes: scrollbackBytes,
		prepareLaunch:   prepareLaunch,
	}
}

func (e *Engine) SetOutputHandler(h session.OutputHandler) {
	e.mu.Lock()
	e.handler = h
	e.mu.Unlock()
}

func (e *Engine) outputHandler() session.OutputHandler {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.handler
}

func (e *Engine) Create(req session.CreateSessionRequest) (*session.SessionInfo, error) {
	cols := req.Cols
	rows := req.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	if e.prepareLaunch == nil {
		return nil, fmt.Errorf("PTY launch preparer is required")
	}
	launch, err := e.prepareLaunch(req)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare process: %w", err)
	}

	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to open pty: %w", err)
	}
	// Resize is best-effort; the client sends a real size on attach.
	_ = p.Resize(cols, rows)

	cmd := p.Command(launch.Command, launch.Args...)
	cmd.Dir = req.Cwd
	cmd.Env = launch.Env
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	now := time.Now().UTC()
	s := &internalPtySession{
		info: session.SessionInfo{
			ID:        req.ID,
			Type:      req.Type,
			Command:   req.Command,
			Args:      req.Args,
			Cwd:       req.Cwd,
			Status:    session.SessionRunning,
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
		status:  session.SessionRunning,
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
func (e *Engine) waitProc(s *internalPtySession) {
	if s.cmd != nil {
		_ = s.cmd.Wait()
	}
	s.mu.Lock()
	if s.status != session.SessionKilled {
		s.status = session.SessionExited
		s.info.Status = session.SessionExited
		s.info.UpdatedAt = time.Now().UTC()
	}
	s.mu.Unlock()
	s.closePty()
}

// readPump is the single goroutine draining a session's PTY: it appends output
// to the ring buffer and forwards it to the session.OutputHandler. When the PTY returns
// an error/EOF the process has ended; unless it was Killed we mark it exited.
func (e *Engine) readPump(s *internalPtySession) {
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
		// Answer terminal capability queries (DECRQM) the app blocks on. A TUI that
		// gates its first paint on the reply — a real terminal always sends one —
		// otherwise clears the screen and waits forever on our pass-through PTY.
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
func (e *Engine) Attach(sessionID, viewerID string) (*session.AttachResult, error) {
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
	return &session.AttachResult{SessionID: sessionID, Replay: replay}, nil
}

// Detach removes a viewer. It MUST NOT close the PTY or kill the process — even
// when the last viewer leaves, the process keeps running.
func (e *Engine) Detach(sessionID, viewerID string) error {
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

// HasViewer reports whether viewerID is currently attached to the session. This
// is what gates writes (input / resize / ack): the engine's viewer set is the
// only authority that both goroutines can agree on.
func (e *Engine) HasViewer(sessionID, viewerID string) bool {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.viewers[viewerID]
	return ok
}

// Ack records that a viewer has processed n bytes of output, draining the
// backpressure backlog so the read pump can resume. n is the byte count the
// client parsed (matching len(data) the server metered); over-acking replay is
// harmless (the counter floors at zero).
func (e *Engine) Ack(sessionID string, n int) {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil || n <= 0 {
		return
	}
	s.flow.ack(n)
}

func (e *Engine) Write(sessionID string, data []byte) error {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	s.mu.RLock()
	running := s.status == session.SessionRunning
	p := s.pty
	s.mu.RUnlock()
	if !running || p == nil {
		return fmt.Errorf("session %s is not running", sessionID)
	}
	_, err := p.Write(data)
	return err
}

func (e *Engine) Resize(sessionID string, cols, rows int) error {
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
func (e *Engine) Kill(sessionID string) error {
	e.mu.Lock()
	s := e.sessions[sessionID]
	delete(e.sessions, sessionID)
	e.mu.Unlock()
	if s == nil {
		return nil
	}

	s.mu.Lock()
	s.status = session.SessionKilled
	s.info.Status = session.SessionKilled
	s.info.UpdatedAt = time.Now().UTC()
	cmd := s.cmd
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	s.closePty()
	return nil
}

func (e *Engine) Restart(sessionID string) error {
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

func (e *Engine) HasSession(sessionID string) bool {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status == session.SessionRunning
}

func (e *Engine) Get(sessionID string) (*session.SessionInfo, error) {
	e.mu.RLock()
	s := e.sessions[sessionID]
	e.mu.RUnlock()
	if s == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	info := s.snapshotInfo()
	return &info, nil
}

func (e *Engine) List() ([]session.SessionInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]session.SessionInfo, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, s.snapshotInfo())
	}
	return out, nil
}

func (s *internalPtySession) snapshotInfo() session.SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info := s.info
	info.Status = s.status
	return info
}
