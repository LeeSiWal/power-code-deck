package services

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// The permission channel, as it actually works.
//
// Third-party write-ups describe a `control_request`/`control_response` channel on
// the stream-json pipe. This CLI (2.1.212) does not do that. Observed instead:
//
//   - With no --permission-prompt-tool, a tool needing permission is DENIED
//     outright. Nothing asks. The turn still ends `subtype:"success"`, with the
//     refusal recorded in result.permission_denials. (`--permission-mode manual`
//     does not change this; the CLI reports permissionMode "default" for it.)
//   - --permission-prompt-tool NAMES AN MCP TOOL (the flag is real but hidden from
//     --help). The CLI calls that tool and waits for its result.
//
// The observed contract, captured from a live run:
//
//	tools/call arguments:
//	  {"tool_name":"Write",
//	   "input":{"file_path":"…","content":"…"},
//	   "tool_use_id":"toolu_012MYY…"}
//	  _meta: {"claudecode/toolUseId":"toolu_…","progressToken":2}
//
//	reply — content[0].text is a JSON *string*:
//	  {"behavior":"allow","updatedInput":{…}}
//	  {"behavior":"deny","message":"…"}   ← the message reaches Claude verbatim
//
// Both paths verified end to end: allow → the file was created; deny → the file
// was not created and Claude quoted our message back.

// PermissionRequest is one pending "may I?" waiting on a human.
type PermissionRequest struct {
	ID        string          `json:"id"` // our id (the CLI's tool_use_id)
	SessionID string          `json:"sessionId"`
	ToolName  string          `json:"toolName"`
	Input     json.RawMessage `json:"input"`
	AskedAt   time.Time       `json:"askedAt"`
}

// PermissionDecision is the human's answer.
type PermissionDecision struct {
	Behavior string `json:"behavior"` // allow | deny
	// UpdatedInput lets the user edit the call before it runs (approve-with-changes).
	// Empty means "run it as proposed" — we echo the original input back, because
	// the CLI expects updatedInput to be present on allow.
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	// Message is shown to Claude on deny. Claude reads it and adapts, so "not that
	// path, use ./tmp" is a useful answer, not just "no".
	Message string `json:"message,omitempty"`
}

var ErrPermissionCancelled = errors.New("permission request cancelled")

// PermissionBroker parks tool-permission requests until a human answers.
//
// Waiting is the point, not a problem to engineer around: the user is on a phone
// and may answer in an hour. There is deliberately NO timeout — a request lives
// until it's answered or its session ends. A timeout would auto-deny work the user
// meant to approve, and (worse) the turn would still report success.
type PermissionBroker struct {
	mu      sync.Mutex
	pending map[string]*pendingPermission
	// onAsk is called when a new request arrives, so the hub can push it to the
	// devices. Set once at wiring time.
	onAsk func(PermissionRequest)
}

type pendingPermission struct {
	req    PermissionRequest
	answer chan PermissionDecision
}

func NewPermissionBroker() *PermissionBroker {
	return &PermissionBroker{pending: make(map[string]*pendingPermission)}
}

// SetAskHandler installs the callback that surfaces a request to the user.
func (b *PermissionBroker) SetAskHandler(fn func(PermissionRequest)) {
	b.mu.Lock()
	b.onAsk = fn
	b.mu.Unlock()
}

// Ask registers a request and blocks until Resolve is called for it (or cancel
// fires). It is called from the MCP bridge — i.e. from Claude's own tool call, so
// blocking here is exactly what pauses the agent.
func (b *PermissionBroker) Ask(req PermissionRequest, cancel <-chan struct{}) (PermissionDecision, error) {
	if req.AskedAt.IsZero() {
		req.AskedAt = time.Now().UTC()
	}
	p := &pendingPermission{req: req, answer: make(chan PermissionDecision, 1)}

	b.mu.Lock()
	// A repeat id (the CLI retrying the same tool_use) replaces the old waiter
	// rather than stacking a second prompt for the same call.
	if old, ok := b.pending[req.ID]; ok {
		close(old.answer)
	}
	b.pending[req.ID] = p
	ask := b.onAsk
	b.mu.Unlock()

	if ask != nil {
		ask(req)
	}

	select {
	case d, ok := <-p.answer:
		if !ok {
			return PermissionDecision{}, ErrPermissionCancelled
		}
		return d, nil
	case <-cancel:
		b.forget(req.ID)
		return PermissionDecision{}, ErrPermissionCancelled
	}
}

// Resolve delivers a human's answer. Unknown ids are ignored (a stale tap from a
// device that missed the session ending).
func (b *PermissionBroker) Resolve(id string, d PermissionDecision) bool {
	b.mu.Lock()
	p := b.pending[id]
	delete(b.pending, id)
	b.mu.Unlock()
	if p == nil {
		return false
	}
	p.answer <- d
	return true
}

// Pending lists unanswered requests — what a device that just attached must show.
// Without this, a phone that reconnects would sit in front of a stalled agent with
// no idea it's waiting on a tap.
func (b *PermissionBroker) Pending(sessionID string) []PermissionRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PermissionRequest, 0, len(b.pending))
	for _, p := range b.pending {
		if sessionID == "" || p.req.SessionID == sessionID {
			out = append(out, p.req)
		}
	}
	return out
}

// CancelSession drops every request for a session (its process died).
func (b *PermissionBroker) CancelSession(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, p := range b.pending {
		if p.req.SessionID == sessionID {
			close(p.answer)
			delete(b.pending, id)
		}
	}
}

func (b *PermissionBroker) forget(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}
