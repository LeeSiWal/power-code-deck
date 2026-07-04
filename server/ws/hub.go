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

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  65536,
	WriteBufferSize: 65536,
}

type Hub struct {
	clients     sync.Map
	engine      services.SessionEngine
	watcherSvc  *services.WatcherService
	agentSvc    *services.AgentService
	gitSvc      *services.GitService
	portScanner *services.PortScanner
	notifSvc    *services.NotificationService
}

func NewHub(engine services.SessionEngine, watcherSvc *services.WatcherService, agentSvc *services.AgentService, gitSvc *services.GitService, portScanner *services.PortScanner, notifSvc *services.NotificationService) *Hub {
	h := &Hub{
		engine:      engine,
		watcherSvc:  watcherSvc,
		agentSvc:    agentSvc,
		gitSvc:      gitSvc,
		portScanner: portScanner,
		notifSvc:    notifSvc,
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
		ports := h.portScanner.Poll(agent.ID, agent.TmuxSession)
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
	conn, err := upgrader.Upgrade(w, r, nil)
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
		h.watcherSvc.Watch(payload.AgentID, payload.Path)

	case EventFileUnwatch:
		var payload FileWatchPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.watcherSvc.Unwatch(payload.AgentID)
	}
}

func (h *Hub) handleTerminalAttach(c *Client, payload TerminalAttachPayload) {
	if _, err := h.agentSvc.Get(payload.AgentID); err != nil {
		log.Printf("Agent not found: %s", payload.AgentID)
		return
	}

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

	// Replay scrollback to this viewer only (nil for the tmux engine, which
	// relies on tmux redrawing the pane on attach).
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
