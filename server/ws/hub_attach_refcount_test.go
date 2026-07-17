package ws

import (
	"testing"

	"powercodedeck/services"
)

// viewerEngine tracks the engine's viewer set the way the real one does, so these
// tests exercise the actual attach/detach bookkeeping rather than a mock's idea of it.
type viewerEngine struct {
	gateEngine
	attached map[string]map[string]bool // sessionID -> viewerID -> true
}

func newViewerEngine() *viewerEngine {
	return &viewerEngine{attached: map[string]map[string]bool{}}
}

func (e *viewerEngine) Attach(sessionID, viewerID string) (*services.AttachResult, error) {
	if e.attached[sessionID] == nil {
		e.attached[sessionID] = map[string]bool{}
	}
	e.attached[sessionID][viewerID] = true
	return &services.AttachResult{SessionID: sessionID}, nil
}

func (e *viewerEngine) Detach(sessionID, viewerID string) error {
	delete(e.attached[sessionID], viewerID)
	return nil
}

func (e *viewerEngine) HasViewer(sessionID, viewerID string) bool {
	return e.attached[sessionID][viewerID]
}

// REGRESSION: the browser has ONE WebSocket, but several surfaces attach through
// it — the terminal view and the dashboard's thumbnails. They therefore share one
// viewerID. A thumbnail unmounting used to detach that shared viewer, and once
// writes were gated on being an attached viewer, every keystroke after that was
// silently discarded: the user typed and nothing happened.
//
// The engine viewer must survive until the LAST surface on the connection lets go.
func TestThumbnailDetachDoesNotSilenceTheTerminal(t *testing.T) {
	eng := newViewerEngine()
	h := &Hub{engine: eng}
	c := &Client{viewerID: "v1", send: make(chan []byte, 8)}

	// The dashboard thumbnail attaches, then the terminal view attaches.
	h.handleTerminalAttach(c, TerminalAttachPayload{AgentID: "a1", Cols: 40, Rows: 10})
	h.handleTerminalAttach(c, TerminalAttachPayload{AgentID: "a1", Cols: 80, Rows: 24})

	// The thumbnail unmounts.
	h.detachSurface(c, "a1")

	if !eng.HasViewer("a1", "v1") {
		t.Fatal("the terminal's viewer was dropped when the thumbnail detached")
	}
	if !h.mayWrite(c, "a1") {
		t.Fatal("typing would be silently discarded after a thumbnail unmounted")
	}

	// The terminal view leaves too — now the viewer really should go.
	h.detachSurface(c, "a1")
	if eng.HasViewer("a1", "v1") {
		t.Fatal("viewer still attached after the last surface detached")
	}
	if h.mayWrite(c, "a1") {
		t.Fatal("a fully detached client must not be able to write")
	}
}

// Detaching something never attached must not underflow the count or wrongly
// leave a viewer attached.
func TestDetachUnknownAgentIsSafe(t *testing.T) {
	eng := newViewerEngine()
	h := &Hub{engine: eng}
	c := &Client{viewerID: "v1", send: make(chan []byte, 8)}

	h.detachSurface(c, "ghost")
	h.handleTerminalAttach(c, TerminalAttachPayload{AgentID: "a1", Cols: 80, Rows: 24})
	h.detachSurface(c, "a1")
	h.detachSurface(c, "a1") // extra detach — must stay released, not go negative

	if eng.HasViewer("a1", "v1") {
		t.Fatal("viewer leaked after detach")
	}
	h.handleTerminalAttach(c, TerminalAttachPayload{AgentID: "a1", Cols: 80, Rows: 24})
	if !h.mayWrite(c, "a1") {
		t.Fatal("re-attaching after extra detaches left the client unable to write")
	}
}

// Switching agents releases the old one immediately: its surfaces are gone with
// the page, and leaving a stale viewer would keep flow control metering for a
// screen nobody is looking at.
func TestSwitchingAgentReleasesThePrevious(t *testing.T) {
	eng := newViewerEngine()
	h := &Hub{engine: eng}
	c := &Client{viewerID: "v1", send: make(chan []byte, 8)}

	h.handleTerminalAttach(c, TerminalAttachPayload{AgentID: "a1", Cols: 80, Rows: 24})
	h.handleTerminalAttach(c, TerminalAttachPayload{AgentID: "a2", Cols: 80, Rows: 24})

	if eng.HasViewer("a1", "v1") {
		t.Fatal("old agent still has this viewer after switching")
	}
	if !h.mayWrite(c, "a2") {
		t.Fatal("cannot write to the agent we just switched to")
	}
	if h.mayWrite(c, "a1") {
		t.Fatal("can still write to the agent we left")
	}
}
