package ws

import (
	"encoding/json"
	"testing"

	"powercodedeck/services"
)

// gateEngine is a SessionEngine that records the writes it was asked to perform
// and lets a test declare who is attached where.
type gateEngine struct {
	viewers map[string]string // sessionID -> attached viewerID
	writes  []string          // sessionID of each accepted write
	acks    []string
	resizes []string
}

func (e *gateEngine) HasViewer(sessionID, viewerID string) bool {
	return e.viewers[sessionID] == viewerID
}
func (e *gateEngine) Write(sessionID string, data []byte) error {
	e.writes = append(e.writes, sessionID)
	return nil
}
func (e *gateEngine) Ack(sessionID string, n int) { e.acks = append(e.acks, sessionID) }
func (e *gateEngine) Resize(sessionID string, cols, rows int) error {
	e.resizes = append(e.resizes, sessionID)
	return nil
}

// Unused by these tests.
func (e *gateEngine) Create(services.CreateSessionRequest) (*services.SessionInfo, error) {
	return nil, nil
}
func (e *gateEngine) Attach(string, string) (*services.AttachResult, error) { return nil, nil }
func (e *gateEngine) Detach(string, string) error                          { return nil }
func (e *gateEngine) Kill(string) error                                    { return nil }
func (e *gateEngine) Restart(string) error                                 { return nil }
func (e *gateEngine) HasSession(string) bool                               { return true }
func (e *gateEngine) Get(string) (*services.SessionInfo, error)            { return nil, nil }
func (e *gateEngine) List() ([]services.SessionInfo, error)                { return nil, nil }
func (e *gateEngine) SetOutputHandler(services.OutputHandler)              {}

func msg(t *testing.T, event string, payload any) WSMessage {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return WSMessage{Event: event, Payload: b}
}

// A client may only write to a session it is actually attached to. Naming
// someone else's agent id in the payload must not reach the PTY.
func TestWriteToOtherAgentIsRejected(t *testing.T) {
	eng := &gateEngine{viewers: map[string]string{"agent-A": "viewer-1"}}
	h := &Hub{engine: eng}
	c := &Client{viewerID: "viewer-1", watchingAgent: "agent-A"}

	h.handleMessage(c, msg(t, EventTerminalInput, TerminalInputPayload{AgentID: "agent-B", Data: "rm -rf /\r"}))
	h.handleMessage(c, msg(t, EventTerminalPasteSubmit, TerminalPastePayload{AgentID: "agent-B", Text: "x"}))
	h.handleMessage(c, msg(t, EventTerminalResize, TerminalResizePayload{AgentID: "agent-B", Cols: 1, Rows: 1}))
	h.handleMessage(c, msg(t, EventTerminalAck, TerminalAckPayload{AgentID: "agent-B", Bytes: 999}))

	if len(eng.writes) != 0 || len(eng.resizes) != 0 || len(eng.acks) != 0 {
		t.Fatalf("writes to a non-attached agent leaked through: writes=%v resizes=%v acks=%v",
			eng.writes, eng.resizes, eng.acks)
	}
}

// The session it IS attached to still works — the gate must not break normal use.
func TestWriteToOwnAgentIsAllowed(t *testing.T) {
	eng := &gateEngine{viewers: map[string]string{"agent-A": "viewer-1"}}
	h := &Hub{engine: eng}
	c := &Client{viewerID: "viewer-1", watchingAgent: "agent-A"}

	h.handleMessage(c, msg(t, EventTerminalInput, TerminalInputPayload{AgentID: "agent-A", Data: "ls\r"}))
	h.handleMessage(c, msg(t, EventTerminalResize, TerminalResizePayload{AgentID: "agent-A", Cols: 80, Rows: 24}))
	h.handleMessage(c, msg(t, EventTerminalAck, TerminalAckPayload{AgentID: "agent-A", Bytes: 16384}))

	if len(eng.writes) != 1 || len(eng.resizes) != 1 || len(eng.acks) != 1 {
		t.Fatalf("attached viewer was blocked: writes=%v resizes=%v acks=%v", eng.writes, eng.resizes, eng.acks)
	}
}

// An evicted viewer must go silent. Eviction detaches it engine-side, but its own
// watchingAgent still names the session (that field belongs to its goroutine) —
// so gating on the client's own state would let its emulator keep answering
// DA1/DSR into someone else's live session.
func TestEvictedViewerCannotWrite(t *testing.T) {
	eng := &gateEngine{viewers: map[string]string{"agent-A": "viewer-2"}} // viewer-2 took over
	h := &Hub{engine: eng}
	evicted := &Client{viewerID: "viewer-1", watchingAgent: "agent-A"} // still thinks it's watching

	h.handleMessage(evicted, msg(t, EventTerminalInput, TerminalInputPayload{AgentID: "agent-A", Data: "\x1b[?1;2c"}))
	h.handleMessage(evicted, msg(t, EventTerminalAck, TerminalAckPayload{AgentID: "agent-A", Bytes: 16384}))

	if len(eng.writes) != 0 || len(eng.acks) != 0 {
		t.Fatalf("evicted viewer still reached the PTY: writes=%v acks=%v", eng.writes, eng.acks)
	}
}
