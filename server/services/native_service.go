package services

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// nativeTextEvent builds a synthetic user/assistant StreamEvent with its Raw JSON
// filled in — the fan-out and history replay both send ev.Raw, so a server-made
// event MUST carry the wire JSON the client's foldEvents expects, not just the
// struct fields. role is "user" or "assistant".
func nativeTextEvent(role, text string) *StreamEvent {
	raw, _ := json.Marshal(map[string]any{
		"type": role,
		"message": map[string]any{
			"role":    role,
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	})
	return &StreamEvent{
		Type:    role,
		Message: &StreamMessage{Role: role, Content: []ContentBlock{{Type: "text", Text: text}}},
		Raw:     raw,
	}
}

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

	// onSessionID records Claude's own conversation id when a session announces it,
	// so a later open can --resume instead of starting from nothing. Injected
	// rather than reaching for the DB here: this service owns processes, not rows.
	onSessionID func(agentID, claudeSessionID string)
	// resumeIDFor supplies the last known conversation id for an agent.
	resumeIDFor func(agentID string) string

	// saveConfig / loadConfig persist the chosen model + permission mode per agent,
	// so a restart or another device resumes with the same choices rather than
	// snapping back to defaults. Injected for the same reason as the resume id: this
	// service owns processes, not rows.
	saveConfig func(agentID, model, mode string)
	loadConfig func(agentID string) (model, mode string)
}

type nativeSession struct {
	id     string
	driver *ClaudeDriver
	cwd    string
	model  string // remembered so a mode switch keeps the model, and vice-versa
	mode   string // permission mode: "" | acceptEdits | plan | bypassPermissions
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

// SetPersistence wires where the resume id is stored and read from.
func (s *NativeService) SetPersistence(save func(agentID, claudeSessionID string), load func(agentID string) string) {
	s.mu.Lock()
	s.onSessionID = save
	s.resumeIDFor = load
	s.mu.Unlock()
}

// SetConfigPersistence wires where a session's model + permission mode are stored.
func (s *NativeService) SetConfigPersistence(save func(agentID, model, mode string), load func(agentID string) (string, string)) {
	s.mu.Lock()
	s.saveConfig = save
	s.loadConfig = load
	s.mu.Unlock()
}

// Config returns a running session's current model + permission mode, so a client
// opening the page can display the choices the session is actually using (which may
// have been set on another device) rather than its own last local guess.
func (s *NativeService) Config(sessionID string) (model, mode string) {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess != nil {
		return sess.model, sess.mode
	}
	// Not running: fall back to what's persisted, so a cold open still shows the
	// remembered choices.
	s.mu.RLock()
	load := s.loadConfig
	s.mu.RUnlock()
	if load != nil {
		return load(sessionID)
	}
	return "", ""
}

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
func (s *NativeService) Start(sessionID, cwd, model, resumeID, mode string) error {
	return s.startSession(sessionID, cwd, model, resumeID, mode, true)
}

// startSession is the shared launch path. fromDB distinguishes a fresh open (where
// the caller's model/mode are only hints and the session's saved choices win) from
// a restart (where the caller passes the exact model/mode to use, so the DB must
// not override them — e.g. switching model must keep the current default mode, not
// resurrect an old persisted one).
func (s *NativeService) startSession(sessionID, cwd, model, resumeID, mode string, fromDB bool) error {
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; ok {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// Continue the last conversation unless the caller asked for a specific one.
	// Without this every open starts a blank session, which on a phone looks like
	// the deck forgot everything you just discussed.
	if resumeID == "" {
		s.mu.RLock()
		load := s.resumeIDFor
		s.mu.RUnlock()
		if load != nil {
			resumeID = load(sessionID)
		}
	}

	// On a fresh open the session's remembered model + mode win over the client's
	// local hint, so every device resumes with the same choices. (A restart passes
	// exact values and skips this.)
	if fromDB {
		s.mu.RLock()
		load := s.loadConfig
		s.mu.RUnlock()
		if load != nil {
			savedModel, savedMode := load(sessionID)
			if savedModel != "" {
				model = savedModel
			}
			if savedMode != "" {
				mode = savedMode
			}
		}
	}

	token, err := s.tokens.Issue(sessionID)
	if err != nil {
		return err
	}

	d := NewClaudeDriver(ClaudeConfig{
		SessionID:      sessionID,
		Cwd:            cwd,
		Model:          model,
		PermissionMode: mode,
		ResumeID:       resumeID,
		ApproveURL:     s.baseURL + "/internal/native/approve",
		ApproveToken:   token,
		SelfPath:       s.selfBin,
	})
	if err := d.Start(); err != nil {
		// A stale resume id (its transcript was deleted, or the CLI rejects it)
		// must not lock the agent out of ever starting. Drop it and try fresh
		// once, rather than failing every open from here on.
		if resumeID != "" {
			d = NewClaudeDriver(ClaudeConfig{
				SessionID: sessionID, Cwd: cwd, Model: model, PermissionMode: mode,
				ApproveURL: s.baseURL + "/internal/native/approve", ApproveToken: token,
				SelfPath: s.selfBin,
			})
			if err2 := d.Start(); err2 != nil {
				s.tokens.Revoke(sessionID)
				return err
			}
		} else {
			s.tokens.Revoke(sessionID)
			return err
		}
	}

	sess := &nativeSession{id: sessionID, driver: d, cwd: cwd, model: model, mode: mode}
	// A resumed session's PRIOR conversation is not re-emitted as events by
	// `claude --resume` — it just continues. So seed history from the transcript on
	// disk, or the chat opens blank until the next reply. (Only when we actually
	// resumed a real id, and only useful before the first client renders history.)
	if resumeID != "" {
		seedNativeHistory(sess, cwd, resumeID)
	}
	s.mu.Lock()
	s.sessions[sessionID] = sess
	save := s.saveConfig
	s.mu.Unlock()

	// Remember the choices this session actually launched with, so the next open —
	// on this device or another — resumes with them.
	if save != nil {
		save(sessionID, model, mode)
	}

	go s.pump(sess)
	return nil
}

// seedNativeHistory loads a resumed conversation's earlier user/assistant turns
// from its transcript into the session history, so the chat shows them at once.
func seedNativeHistory(sess *nativeSession, cwd, sid string) {
	msgs, err := ReadSession(cwd, sid)
	if err != nil || len(msgs) == 0 {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for _, m := range msgs {
		if m.Text == "" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		sess.history = append(sess.history, nativeTextEvent(m.Role, m.Text))
	}
	if len(sess.history) > maxNativeHistory {
		sess.history = sess.history[len(sess.history)-maxNativeHistory:]
	}
}

// pump forwards the driver's events and cleans up when the process exits.
func (s *NativeService) pump(sess *nativeSession) {
	for ev := range sess.driver.Events() {
		if ev.Type == StreamTypeSystem && ev.Subtype == "init" && ev.SessionID != "" {
			s.mu.RLock()
			save := s.onSessionID
			s.mu.RUnlock()
			if save != nil {
				save(sess.id, ev.SessionID)
			}
		}
		s.emit(sess, ev)
	}

	// The process is gone: release anything waiting on a human for it, or the
	// bridge's HTTP call (and its goroutine) would hang forever.
	s.broker.CancelSession(sess.id)
	s.tokens.Revoke(sess.id)
	s.mu.Lock()
	// Only clear the map slot if it still points at THIS session — a model switch
	// replaces the driver, so this old pump's exit must not evict the new session.
	if s.sessions[sess.id] == sess {
		delete(s.sessions, sess.id)
	}
	s.mu.Unlock()
}

// restart replaces a running session's driver with a new one — same conversation
// (resume), new model + permission mode — so a model or mode switch loses nothing.
// The new system/init flows to watchers as a fresh banner. Shared by SetModel and
// SetMode; each keeps the OTHER setting from the session it's replacing.
func (s *NativeService) restart(sessionID, model, mode string) error {
	s.mu.Lock()
	old := s.sessions[sessionID]
	if old != nil {
		delete(s.sessions, sessionID) // remove now so Start() below won't no-op
	}
	s.mu.Unlock()
	if old == nil {
		return fmt.Errorf("native session %s is not running", sessionID)
	}
	resumeID := old.driver.ClaudeSessionID()
	cwd := old.cwd
	old.driver.Stop() // its pump exits; the pump guard keeps it from evicting the new one
	return s.startSession(sessionID, cwd, model, resumeID, mode, false)
}

// SetModel switches model, keeping the current permission mode.
func (s *NativeService) SetModel(sessionID, model string) error {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	mode := ""
	if sess != nil {
		mode = sess.mode
	}
	return s.restart(sessionID, model, mode)
}

// SetMode switches the permission mode (the TUI's Shift+Tab), keeping the model.
func (s *NativeService) SetMode(sessionID, mode string) error {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	model := ""
	if sess != nil {
		model = sess.model
	}
	return s.restart(sessionID, model, mode)
}

// emit records an event in the session's history (bounded) and fans it out to
// every watcher — the single path both live driver events and server-synthesized
// turns go through, so history order and what's on screen never disagree.
func (s *NativeService) emit(sess *nativeSession, ev *StreamEvent) {
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

// Send delivers a user turn to a running session.
func (s *NativeService) Send(sessionID, text string) error {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess == nil {
		return fmt.Errorf("native session %s is not running", sessionID)
	}
	if err := sess.driver.Send(text); err != nil {
		return err
	}
	// Record the user turn in history NOW — at its real position, before the reply
	// arrives — instead of relying on the CLI to echo it back (that echo can land
	// after the assistant's response, flipping the order). This is also what keeps
	// the user's half of the conversation across a reconnect.
	s.emit(sess, nativeTextEvent("user", text))
	return nil
}

// Interrupt stops the turn a session is in the middle of.
func (s *NativeService) Interrupt(sessionID string) error {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess == nil {
		return fmt.Errorf("native session %s is not running", sessionID)
	}
	return sess.driver.Interrupt()
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
