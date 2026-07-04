package services

import (
	"fmt"
	"sync"
	"time"
)

// TmuxSessionEngine is the current SessionEngine implementation. It keeps using
// tmux + a PTY under the hood, but confines all of that behind the interface so
// the rest of PowerCodeDeck no longer talks to tmux/PTY directly.
//
// Crucially it distinguishes viewers from the process:
//   - a `tmux attach` PTY is opened when the first viewer attaches and torn down
//     when the last viewer detaches (Detach) — the tmux SESSION keeps running;
//   - the tmux session (the real process) is only ended by Kill.
//
// A later InternalPtySessionEngine (creack/pty or go-pty/ConPTY) or a
// RemoteSessionEngineClient (talking to pcd-sessiond) can replace this type
// without any caller change.
type TmuxSessionEngine struct {
	tmux *TmuxService
	pty  *PtyService

	mu       sync.Mutex
	sessions map[string]*tmuxEntry
	onOutput OutputHandler
}

type tmuxEntry struct {
	info     SessionInfo
	req      CreateSessionRequest
	tmuxName string
	viewers  map[string]bool // viewerID set
	attached bool            // a `tmux attach` PTY is currently streaming
}

func NewTmuxSessionEngine() *TmuxSessionEngine {
	return &TmuxSessionEngine{
		tmux:     NewTmuxService(),
		pty:      NewPtyService(),
		sessions: make(map[string]*tmuxEntry),
	}
}

func (e *TmuxSessionEngine) SetOutputHandler(h OutputHandler) {
	e.mu.Lock()
	e.onOutput = h
	e.mu.Unlock()
}

func (e *TmuxSessionEngine) outputHandler() OutputHandler {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.onOutput
}

func (e *TmuxSessionEngine) Create(req CreateSessionRequest) (*SessionInfo, error) {
	name := e.tmux.GenerateSessionName(req.ID)
	if err := e.tmux.CreateSession(name, req.Cwd, req.Command, req.Args); err != nil {
		return nil, fmt.Errorf("failed to create tmux session: %w", err)
	}

	now := time.Now().UTC()
	info := SessionInfo{
		ID:        req.ID,
		Type:      req.Type,
		Command:   req.Command,
		Args:      req.Args,
		Cwd:       req.Cwd,
		Status:    SessionRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}

	e.mu.Lock()
	e.sessions[req.ID] = &tmuxEntry{
		info:     info,
		req:      req,
		tmuxName: name,
		viewers:  make(map[string]bool),
	}
	e.mu.Unlock()

	return &info, nil
}

// Attach registers a viewer and, if this is the first viewer, opens the
// streaming `tmux attach` PTY. It never (re)starts the underlying process.
func (e *TmuxSessionEngine) Attach(sessionID, viewerID string) (*AttachResult, error) {
	e.mu.Lock()
	entry := e.sessions[sessionID]
	if entry == nil {
		// The engine's in-memory map was lost (e.g. server restart) but the
		// tmux session may still be alive — rebuild a minimal entry so viewers
		// can re-attach to the surviving session.
		name := e.tmux.GenerateSessionName(sessionID)
		if !e.tmux.HasSession(name) {
			e.mu.Unlock()
			return nil, fmt.Errorf("session %s is not running", sessionID)
		}
		entry = &tmuxEntry{
			info:     SessionInfo{ID: sessionID, Status: SessionRunning, CreatedAt: time.Now().UTC()},
			tmuxName: name,
			viewers:  make(map[string]bool),
		}
		e.sessions[sessionID] = entry
	}
	entry.viewers[viewerID] = true
	needAttach := !entry.attached
	name := entry.tmuxName
	e.mu.Unlock()

	if needAttach && !e.pty.HasSession(sessionID) {
		if _, err := e.pty.AttachTmux(sessionID, name, 80, 24); err != nil {
			return nil, err
		}
		handler := e.outputHandler()
		go e.pty.ReadPump(sessionID, func(data []byte) {
			if handler != nil {
				handler(sessionID, data)
			}
		})
	}
	e.mu.Lock()
	entry.attached = true
	e.mu.Unlock()

	// tmux redraws the current pane on attach, so the viewer gets the screen
	// without an explicit replay. A future in-process engine returns its ring
	// buffer here instead.
	return &AttachResult{SessionID: sessionID, Replay: nil}, nil
}

// Detach removes a viewer. When the last viewer leaves it tears down the
// `tmux attach` PTY only — the tmux session (and its process) keeps running.
// It MUST NOT kill the session.
func (e *TmuxSessionEngine) Detach(sessionID, viewerID string) error {
	e.mu.Lock()
	entry := e.sessions[sessionID]
	if entry == nil {
		e.mu.Unlock()
		return nil
	}
	delete(entry.viewers, viewerID)
	last := len(entry.viewers) == 0
	if last {
		entry.attached = false
	}
	e.mu.Unlock()

	if last {
		// Closes the `tmux attach` client PTY only — NOT `tmux kill-session`.
		e.pty.Close(sessionID)
	}
	return nil
}

func (e *TmuxSessionEngine) Write(sessionID string, data []byte) error {
	if e.pty.HasSession(sessionID) {
		e.pty.Write(sessionID, data)
		return nil
	}
	// No active viewer PTY — deliver through tmux so REST /send still works
	// without a browser attached.
	return e.tmux.SendKeysRaw(e.nameFor(sessionID), string(data))
}

func (e *TmuxSessionEngine) Resize(sessionID string, cols, rows int) error {
	e.pty.Resize(sessionID, uint16(cols), uint16(rows))
	return nil
}

// Kill terminates the underlying tmux session (the real process) and drops all
// engine state for it. This is the ONLY path that ends the process.
func (e *TmuxSessionEngine) Kill(sessionID string) error {
	name := e.nameFor(sessionID)
	e.mu.Lock()
	delete(e.sessions, sessionID)
	e.mu.Unlock()

	e.pty.Close(sessionID)          // tear down attach PTY if any
	return e.tmux.KillSession(name) // end the actual session
}

func (e *TmuxSessionEngine) Restart(sessionID string) error {
	e.mu.Lock()
	entry := e.sessions[sessionID]
	e.mu.Unlock()
	if entry == nil || entry.req.Command == "" {
		return fmt.Errorf("session %s cannot be restarted (unknown command)", sessionID)
	}
	req := entry.req
	_ = e.Kill(sessionID)
	_, err := e.Create(req)
	return err
}

func (e *TmuxSessionEngine) HasSession(sessionID string) bool {
	return e.tmux.HasSession(e.nameFor(sessionID))
}

func (e *TmuxSessionEngine) Get(sessionID string) (*SessionInfo, error) {
	e.mu.Lock()
	entry := e.sessions[sessionID]
	e.mu.Unlock()
	if entry == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	info := entry.info
	if e.HasSession(sessionID) {
		info.Status = SessionRunning
	} else if info.Status == SessionRunning {
		info.Status = SessionStopped
	}
	return &info, nil
}

func (e *TmuxSessionEngine) List() ([]SessionInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]SessionInfo, 0, len(e.sessions))
	for _, entry := range e.sessions {
		out = append(out, entry.info)
	}
	return out, nil
}

// nameFor returns the tmux session name for a session id, preferring a tracked
// entry and falling back to the deterministic `pcd-<id>` naming.
func (e *TmuxSessionEngine) nameFor(sessionID string) string {
	e.mu.Lock()
	entry := e.sessions[sessionID]
	e.mu.Unlock()
	if entry != nil && entry.tmuxName != "" {
		return entry.tmuxName
	}
	return e.tmux.GenerateSessionName(sessionID)
}
