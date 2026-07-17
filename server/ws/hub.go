package ws

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"powercodedeck/services"

	"github.com/gorilla/websocket"
)

// buildPasteData converts Prompt Bar text into the byte sequence written to the
// PTY. The Prompt Bar is the single place multi-line / IME text enters the
// terminal, so wrapping happens here (one place) rather than per-client.
//
//	bracketed-paste (default): ESC[200~ <text> ESC[201~  — the app treats it as a
//	                           single paste, so embedded newlines never submit.
//	plain-paste / typewriter:  raw text, no wrapping.
//
// When submit is true a trailing CR is appended to send the prompt.
func buildPasteData(text, mode string, submit bool) string {
	var b strings.Builder
	switch mode {
	case "plain-paste", "typewriter":
		b.WriteString(text)
	default: // bracketed-paste
		b.WriteString("\x1b[200~")
		b.WriteString(text)
		b.WriteString("\x1b[201~")
	}
	if submit {
		b.WriteString("\r")
	}
	return b.String()
}

// checkOrigin decides whether a WebSocket handshake may proceed. Browsers always
// send an Origin header, so a cross-origin page (the drive-by RCE vector) is
// rejected unless its origin is explicitly allowed. Requests with no Origin
// header are non-browser clients (e.g. a CLI) and are permitted.
func checkOrigin(allowed map[string]bool, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return allowed[strings.ToLower(strings.TrimRight(origin, "/"))]
}

type Hub struct {
	clients        sync.Map
	engine         services.SessionEngine
	watcherSvc     *services.WatcherService
	agentSvc       *services.AgentService
	gitSvc         *services.GitService
	portScanner    *services.PortScanner
	notifSvc       *services.NotificationService
	allowedOrigins map[string]bool
	upgrader       websocket.Upgrader
}

func NewHub(engine services.SessionEngine, watcherSvc *services.WatcherService, agentSvc *services.AgentService, gitSvc *services.GitService, portScanner *services.PortScanner, notifSvc *services.NotificationService, allowedOrigins []string) *Hub {
	origins := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.ToLower(strings.TrimRight(strings.TrimSpace(o), "/")); o != "" {
			origins[o] = true
		}
	}
	h := &Hub{
		engine:         engine,
		watcherSvc:     watcherSvc,
		agentSvc:       agentSvc,
		gitSvc:         gitSvc,
		portScanner:    portScanner,
		notifSvc:       notifSvc,
		allowedOrigins: origins,
	}
	h.upgrader = websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return checkOrigin(h.allowedOrigins, r) },
		ReadBufferSize:  65536,
		WriteBufferSize: 65536,
	}

	watcherSvc.SetOnChange(func(agentID string, change services.FileChange) {
		h.BroadcastToAgent(agentID, EventFileChanged, change)
	})

	return h
}

func (h *Hub) Run() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			h.pollMeta()
		}
	}()
}

func (h *Hub) pollMeta() {
	agents, err := h.agentSvc.List()
	if err != nil {
		return
	}
	for _, agent := range agents {
		if agent.Status != "running" {
			continue
		}
		gitInfo := h.gitSvc.Poll(agent.ID, agent.WorkingDir)
		ports := h.portScanner.Poll(agent.ID)
		payload := AgentMetaPayload{
			AgentID:        agent.ID,
			GitBranch:      gitInfo.Branch,
			GitDirty:       gitInfo.Dirty,
			GitAhead:       gitInfo.Ahead,
			ListeningPorts: ports,
		}
		h.BroadcastAll(EventAgentMeta, payload)
	}
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 256),
		viewerID: newViewerID(),
	}
	h.clients.Store(client, true)

	go client.writePump()
	go client.readPump()
}

func (h *Hub) unregister(c *Client) {
	h.clients.Delete(c)
	close(c.send)

	if c.watchingAgent != "" {
		// A browser closing is a viewer Detach — the session process lives on.
		h.engine.Detach(c.watchingAgent, c.viewerID)
		h.watcherSvc.Unwatch(c.watchingAgent)
	}
}

// newViewerID returns a random id identifying a single WebSocket connection as a
// SessionEngine viewer.
func newViewerID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Hub) handleMessage(c *Client, msg WSMessage) {
	switch msg.Event {
	case EventTerminalAttach:
		var payload TerminalAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.handleTerminalAttach(c, payload)

	case EventTerminalDetach:
		// Detach the viewer only — the session process is NEVER killed here.
		var payload TerminalAttachPayload
		_ = json.Unmarshal(msg.Payload, &payload)
		if payload.AgentID == "" || c.watchingAgent == payload.AgentID {
			if c.watchingAgent != "" {
				h.engine.Detach(c.watchingAgent, c.viewerID)
				c.watchingAgent = ""
			}
		}

	case EventTerminalInput:
		var payload TerminalInputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.engine.Write(payload.AgentID, []byte(payload.Data))

	case EventTerminalAck:
		// Flow-control ACK: the viewer parsed some output, so drain the backlog
		// and let a backpressured read pump resume.
		var payload TerminalAckPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.engine.Ack(payload.AgentID, payload.Bytes)

	case EventTerminalResize:
		var payload TerminalResizePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.engine.Resize(payload.AgentID, int(payload.Cols), int(payload.Rows))

	case EventTerminalPasteSubmit:
		var payload TerminalPastePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.engine.Write(payload.AgentID, []byte(buildPasteData(payload.Text, payload.Mode, true)))

	case EventTerminalPasteOnly:
		var payload TerminalPastePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.engine.Write(payload.AgentID, []byte(buildPasteData(payload.Text, payload.Mode, false)))

	case EventFileWatch:
		var payload FileWatchPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		// The client only knows the agent id; resolve its working dir here so it
		// need not track the (server-side) project path.
		path := payload.Path
		if path == "" && h.agentSvc != nil {
			if wd, err := h.agentSvc.GetWorkingDir(payload.AgentID); err == nil {
				path = wd
			}
		}
		if path != "" {
			h.watcherSvc.Watch(payload.AgentID, path)
		}

	case EventFileUnwatch:
		var payload FileWatchPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.watcherSvc.Unwatch(payload.AgentID)

	case EventPing:
		// Client-initiated liveness probe (sent when a mobile tab returns to the
		// foreground). Echo back so the client can tell a live socket from a dead
		// one that iOS left stuck in the OPEN state.
		c.sendEvent(EventPong, nil)
	}
}

func (h *Hub) handleTerminalAttach(c *Client, payload TerminalAttachPayload) {
	if _, err := h.agentSvc.Get(payload.AgentID); err != nil {
		log.Printf("Agent not found: %s", payload.AgentID)
		return
	}

	// Exclusive viewer: only one device watches a session at a time so two viewers
	// never fight over the PTY size. Evict any OTHER client on this agent — notify
	// it (terminal:evicted) and detach it from the engine. We don't touch the other
	// client's watchingAgent from here (that field is owned by its own goroutine);
	// it clears itself by sending terminal:detach in response to the event.
	h.clients.Range(func(key, _ interface{}) bool {
		other, ok := key.(*Client)
		if !ok || other == c {
			return true
		}
		if other.watchingAgent == payload.AgentID {
			other.sendEvent(EventTerminalEvicted, TerminalEvictedPayload{AgentID: payload.AgentID})
			h.engine.Detach(payload.AgentID, other.viewerID)
		}
		return true
	})

	// Leaving a previous session is a viewer Detach — never a Kill.
	if c.watchingAgent != "" && c.watchingAgent != payload.AgentID {
		h.engine.Detach(c.watchingAgent, c.viewerID)
	}
	c.watchingAgent = payload.AgentID

	res, err := h.engine.Attach(payload.AgentID, c.viewerID)
	if err != nil {
		log.Printf("Failed to attach session %s: %v", payload.AgentID, err)
		return
	}

	// Apply this viewer's terminal size.
	cols := payload.Cols
	rows := payload.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	h.engine.Resize(payload.AgentID, int(cols), int(rows))

	// Replay scrollback to this viewer only (the engine's ring buffer snapshot).
	if res != nil && len(res.Replay) > 0 {
		c.sendEvent(EventTerminalOutput, TerminalOutputPayload{
			AgentID: payload.AgentID,
			Data:    string(res.Replay),
		})
	}
}

func (h *Hub) BroadcastToAgent(agentID string, event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := WSMessage{
		Event:   event,
		Payload: json.RawMessage(data),
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.clients.Range(func(key, value interface{}) bool {
		client := key.(*Client)
		if client.watchingAgent == agentID {
			select {
			case client.send <- msgBytes:
			default:
			}
		}
		return true
	})
}

func (h *Hub) BroadcastAll(event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := WSMessage{
		Event:   event,
		Payload: json.RawMessage(data),
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.clients.Range(func(key, value interface{}) bool {
		client := key.(*Client)
		select {
		case client.send <- msgBytes:
		default:
		}
		return true
	})
}
