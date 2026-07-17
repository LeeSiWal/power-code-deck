package services

import (
	"fmt"
	"os"
	"sync"
)

// NativeService owns the live native sessions: it starts a driver per session,
// wires that session's approval bridge, and fans the event stream out to whoever
// is watching (the WS hub).
//
// It is deliberately separate from AgentService/SessionEngine: a native session
// has no PTY, no viewers-with-a-screen, and no replay. What it has is a
// conversation and a queue of questions waiting on a human.
type NativeService struct {
	broker  *PermissionBroker
	tokens  *approveTokens
	baseURL string // e.g. http://127.0.0.1:33033 — where the bridge calls back
	selfBin string // the pcd binary, spawned by claude as its MCP server

	mu       sync.RWMutex
	sessions map[string]*nativeSession

	// onEvent/onApproval are set by the hub at wiring time.
	onEvent    func(sessionID string, ev *StreamEvent)
	onApproval func(PermissionRequest)
}

type nativeSession struct {
	id     string
	driver *ClaudeDriver
	cwd    string
	// history keeps the events already emitted, so a device that connects late (or
	// reconnects from another device) can render the conversation so far. This is
	// the native track's answer to terminal replay — and it needs no serializer,
	// because the events ARE the state.
	mu      sync.RWMutex
	history []*StreamEvent
}

// maxNativeHistory bounds per-session memory. Unlike a terminal ring, dropping the
// oldest events costs only scrollback of the conversation, never correctness: a
// truncated event list still renders, it just starts later.
const maxNativeHistory = 2000

func NewNativeService(baseURL string) *NativeService {
	selfBin, err := os.Executable()
	if err != nil {
		selfBin = "pcd"
	}
	return &NativeService{
		broker:   NewPermissionBroker(),
		tokens:   NewApproveTokenStore(),
		baseURL:  baseURL,
		selfBin:  selfBin,
		sessions: make(map[string]*nativeSession),
	}
}

// Broker/Tokens expose the pieces the HTTP approval endpoint needs.
func (s *NativeService) Broker() *PermissionBroker { return s.broker }
func (s *NativeService) Tokens() ApproveTokenStore { return s.tokens }

// SetHandlers wires the hub's fan-out. Called once at startup.
func (s *NativeService) SetHandlers(onEvent func(string, *StreamEvent), onApproval func(PermissionRequest)) {
	s.mu.Lock()
	s.onEvent = onEvent
	s.onApproval = onApproval
	s.mu.Unlock()
	s.broker.SetAskHandler(func(req PermissionRequest) {
		s.mu.RLock()
		fn := s.onApproval
		s.mu.RUnlock()
		if fn != nil {
			fn(req)
		}
	})
}

// Start launches a native Claude session for an agent. Idempotent: starting one
// that already runs is a no-op, so a second device opening the page doesn't spawn
// a second agent.
func (s *NativeService) Start(sessionID, cwd, model, resumeID string) error {
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; ok {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	token, err := s.tokens.Issue(sessionID)
	if err != nil {
		return err
	}

	d := NewClaudeDriver(ClaudeConfig{
		SessionID:    sessionID,
		Cwd:          cwd,
		Model:        model,
		ResumeID:     resumeID,
		ApproveURL:   s.baseURL + "/internal/native/approve",
		ApproveToken: token,
		SelfPath:     s.selfBin,
	})
	if err := d.Start(); err != nil {
		s.tokens.Revoke(sessionID)
		return err
	}

	sess := &nativeSession{id: sessionID, driver: d, cwd: cwd}
	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	go s.pump(sess)
	return nil
}

// pump forwards the driver's events and cleans up when the process exits.
func (s *NativeService) pump(sess *nativeSession) {
	for ev := range sess.driver.Events() {
		sess.mu.Lock()
		sess.history = append(sess.history, ev)
		if len(sess.history) > maxNativeHistory {
			sess.history = sess.history[len(sess.history)-maxNativeHistory:]
		}
		sess.mu.Unlock()

		s.mu.RLock()
		fn := s.onEvent
		s.mu.RUnlock()
		if fn != nil {
			fn(sess.id, ev)
		}
	}

	// The process is gone: release anything waiting on a human for it, or the
	// bridge's HTTP call (and its goroutine) would hang forever.
	s.broker.CancelSession(sess.id)
	s.tokens.Revoke(sess.id)
	s.mu.Lock()
	delete(s.sessions, sess.id)
	s.mu.Unlock()
}

// Send delivers a user turn to a running session.
func (s *NativeService) Send(sessionID, text string) error {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess == nil {
		return fmt.Errorf("native session %s is not running", sessionID)
	}
	return sess.driver.Send(text)
}

// Decide answers a pending approval.
func (s *NativeService) Decide(id string, d PermissionDecision) bool {
	return s.broker.Resolve(id, d)
}

// History returns the events so far — what a device renders on connect. Combined
// with Pending(), a phone that opens the page mid-run sees both the conversation
// and whatever the agent is stuck waiting on.
func (s *NativeService) History(sessionID string) []*StreamEvent {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess == nil {
		return nil
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	out := make([]*StreamEvent, len(sess.history))
	copy(out, sess.history)
	return out
}

// Pending lists the approvals a session is blocked on.
func (s *NativeService) Pending(sessionID string) []PermissionRequest {
	return s.broker.Pending(sessionID)
}

// Running reports whether a native session is live.
func (s *NativeService) Running(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sessions[sessionID]
	return ok
}

// Stop ends a session.
func (s *NativeService) Stop(sessionID string) {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess != nil {
		sess.driver.Stop()
	}
}
