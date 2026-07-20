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
	pushSvc        *services.PushService
	native         *services.NativeService
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

// detachSurface drops one UI surface's claim on an agent, and only releases the
// engine viewer when the last surface on this connection lets go.
//
// This is what keeps the dashboard's thumbnails from unmounting the terminal's
// viewer out from under it: they share one socket, hence one viewerID.
func (h *Hub) detachSurface(c *Client, agentID string) {
	if agentID == "" {
		return
	}
	if c.attachCount != nil && c.attachCount[agentID] > 0 {
		c.attachCount[agentID]--
		if c.attachCount[agentID] > 0 {
			return // another surface on this connection is still watching
		}
		delete(c.attachCount, agentID)
	}
	h.engine.Detach(agentID, c.viewerID)
	if c.watchingAgent == agentID {
		c.watchingAgent = ""
	}
}

// newViewerID returns a random id identifying a single WebSocket connection as a
// SessionEngine viewer.
func newViewerID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// mayWrite reports whether this client is allowed to affect the given agent's
// session — input, paste, resize and flow-control acks all go through here.
//
// The rule is "you must be an attached viewer of THAT session", and the engine's
// viewer set is the authority. Two holes this closes:
//   - a client watching agent A could write to agent B just by naming it in the
//     payload (the id came from the client and nothing checked it);
//   - an evicted-but-still-open tab kept writing (its emulator answers DA1/DSR
//     queries on its own), because eviction detaches it engine-side while its own
//     watchingAgent field — owned by its goroutine — still names the session.
//
// Attach/detach are deliberately NOT gated: attach is how you become a viewer,
// and detach must always be allowed to release one.
func (h *Hub) mayWrite(c *Client, agentID string) bool {
	if agentID == "" || h.engine == nil {
		return false
	}
	return h.engine.HasViewer(agentID, c.viewerID)
}

// SetNativeService wires the native (non-terminal) track and starts fanning its
// events out to the devices.
//
// Note the difference from the terminal: native events go to EVERY client
// watching the agent. A terminal has one exclusive viewer because a PTY has one
// size; a conversation has no such constraint — a phone and an iPad can follow the
// same run, and either can answer a prompt.
func (h *Hub) SetNativeService(n *services.NativeService) {
	h.native = n
	n.SetHandlers(
		func(agentID string, ev *services.StreamEvent) {
			h.BroadcastToAgent(agentID, EventNativeEvent, NativeEventPayload{AgentID: agentID, Event: ev.Raw})
			// A turn's `result` marks the agent going idle — a "come back" moment for a
			// user who stepped away. Push it (the service worker suppresses it when the
			// app is focused, so it only interrupts when you're actually elsewhere).
			if ev.Type == services.StreamTypeResult {
				h.pushNotify(agentID, "task_complete", "작업 완료", h.agentName(agentID))
			}
		},
		func(req services.PermissionRequest) {
			h.BroadcastToAgent(req.SessionID, EventNativeApproval, NativeApprovalPayload{
				AgentID:  req.SessionID,
				ID:       req.ID,
				ToolName: req.ToolName,
				Input:    req.Input,
				AskedAt:  req.AskedAt.Format(time.RFC3339),
			})
			// A blocked agent goes nowhere until a human answers — the single most
			// worth-interrupting notification, so it's the reason push exists here.
			h.pushNotify(req.SessionID, "permission_request",
				"승인 필요", h.agentName(req.SessionID)+" · "+req.ToolName+" 실행 대기 중")
		},
	)
}

// SetPushService wires the Web Push fan-out. Must be called before SetNativeService
// so the native handlers above can reach it.
func (h *Hub) SetPushService(p *services.PushService) { h.pushSvc = p }

// pushNotify records the event for the Logs/notification history and delivers a Web
// Push to every subscribed device. reason is the stable notification kind (also the
// notifications-table reason), used as the collapse tag so repeats replace rather
// than stack. Best-effort and non-blocking — this runs on the event hot path.
func (h *Hub) pushNotify(agentID, reason, title, body string) {
	if h.notifSvc != nil {
		_, _ = h.notifSvc.Create(agentID, reason, body)
	}
	if h.pushSvc == nil || !h.pushSvc.Enabled() {
		return
	}
	h.pushSvc.Notify(services.PushMessage{
		Title: title,
		Body:  body,
		Tag:   reason + "-" + agentID,
		URL:   "/agents/" + agentID,
	})
}

// agentName is the display name for a notification, falling back to a generic label.
func (h *Hub) agentName(agentID string) string {
	if a, err := h.agentSvc.Get(agentID); err == nil && a.Name != "" {
		return a.Name
	}
	return "에이전트"
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
		agentID := payload.AgentID
		if agentID == "" {
			agentID = c.watchingAgent
		}
		h.detachSurface(c, agentID)

	case EventTerminalInput:
		var payload TerminalInputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if !h.mayWrite(c, payload.AgentID) {
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
		if !h.mayWrite(c, payload.AgentID) {
			return
		}
		h.engine.Ack(payload.AgentID, payload.Bytes)

	case EventTerminalResize:
		var payload TerminalResizePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if !h.mayWrite(c, payload.AgentID) {
			return
		}
		h.engine.Resize(payload.AgentID, int(payload.Cols), int(payload.Rows))

	case EventTerminalPasteSubmit:
		var payload TerminalPastePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if !h.mayWrite(c, payload.AgentID) {
			return
		}
		h.engine.Write(payload.AgentID, []byte(buildPasteData(payload.Text, payload.Mode, true)))

	case EventTerminalPasteOnly:
		var payload TerminalPastePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if !h.mayWrite(c, payload.AgentID) {
			return
		}
		h.engine.Write(payload.AgentID, []byte(buildPasteData(payload.Text, payload.Mode, false)))

	case EventNativeOpen:
		var payload NativeOpenPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		// Opening is what makes this client a watcher, so it must come before the
		// history replay — otherwise events racing the reply would be lost.
		c.watchingAgent = payload.AgentID
		if !h.native.Running(payload.AgentID) {
			if err := h.native.Start(payload.AgentID, payload.Driver, payload.Cwd, payload.Model, payload.Resume, payload.Mode); err != nil {
				log.Printf("native: start %s failed: %v", payload.AgentID, err)
				c.sendEvent(EventNativeError, NativeErrorPayload{
					AgentID: payload.AgentID,
					Message: "세션을 시작하지 못했습니다: " + err.Error(),
				})
				c.sendEvent(EventNativeState, NativeStatePayload{AgentID: payload.AgentID, Running: false})
				return
			}
		}
		h.sendNativeHistory(c, payload.AgentID)

	case EventNativeInput:
		var payload NativeInputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		if err := h.native.Send(payload.AgentID, payload.Text); err != nil {
			// Never swallow this: the user typed something and it went nowhere.
			// Silence here is exactly the failure that makes an agent UI untrustworthy.
			log.Printf("native: send to %s failed: %v", payload.AgentID, err)
			c.sendEvent(EventNativeError, NativeErrorPayload{
				AgentID: payload.AgentID,
				Message: "메시지를 전달하지 못했습니다: " + err.Error(),
			})
			c.sendEvent(EventNativeState, NativeStatePayload{
				AgentID: payload.AgentID, Running: h.native.Running(payload.AgentID),
			})
		}

	case EventNativeSetModel:
		var payload NativeSetModelPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		if err := h.native.SetModel(payload.AgentID, payload.Model); err != nil {
			c.sendEvent(EventNativeError, NativeErrorPayload{
				AgentID: payload.AgentID,
				Message: "모델 전환 실패: " + err.Error(),
			})
		}

	case EventNativeSetMode:
		var payload NativeSetModePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		if err := h.native.SetMode(payload.AgentID, payload.Mode); err != nil {
			c.sendEvent(EventNativeError, NativeErrorPayload{
				AgentID: payload.AgentID,
				Message: "모드 전환 실패: " + err.Error(),
			})
		}

	case EventNativeDecide:
		var payload NativeDecidePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		if payload.Behavior != "allow" && payload.Behavior != "deny" {
			return // never guess a decision the user didn't make
		}
		h.native.Decide(payload.ID, services.PermissionDecision{
			Behavior:     payload.Behavior,
			UpdatedInput: payload.UpdatedInput,
			Message:      payload.Message,
		})

	case EventNativeInterrupt:
		var payload NativeInterruptPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		if err := h.native.Interrupt(payload.AgentID); err != nil {
			// Say so: a 중단 tap that quietly did nothing is worse than no button.
			c.sendEvent(EventNativeError, NativeErrorPayload{
				AgentID: payload.AgentID,
				Message: "중단하지 못했습니다: " + err.Error(),
			})
		}

	case EventNativeStop:
		var payload NativeStopPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		h.native.Stop(payload.AgentID)

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
	// agentSvc is nil only in tests that exercise the viewer bookkeeping itself.
	if h.agentSvc != nil {
		if _, err := h.agentSvc.Get(payload.AgentID); err != nil {
			log.Printf("Agent not found: %s", payload.AgentID)
			return
		}
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
		delete(c.attachCount, c.watchingAgent)
	}
	c.watchingAgent = payload.AgentID
	if c.attachCount == nil {
		c.attachCount = make(map[string]int)
	}
	c.attachCount[payload.AgentID]++

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

// sendNativeHistory replays the conversation to one client: the events so far,
// plus anything the agent is currently blocked on.
//
// This is the native track's whole answer to terminal replay — no serializer, no
// ring buffer, no DEC-mode reconstruction. The events ARE the state, so "replay"
// is just sending them again.
//
// Pending approvals matter as much as the history: a device that connects while
// the agent waits on a human must show the prompt, or the user sees a frozen agent
// with no way to unblock it.
func (h *Hub) sendNativeHistory(c *Client, agentID string) {
	if h.native == nil {
		return
	}
	evs := h.native.History(agentID)
	raw := make([]json.RawMessage, 0, len(evs))
	for _, ev := range evs {
		raw = append(raw, ev.Raw)
	}
	model, mode := h.native.Config(agentID)
	c.sendEvent(EventNativeHistory, NativeHistoryPayload{
		AgentID: agentID, Events: raw, Running: h.native.Running(agentID),
		Model: model, Mode: mode,
	})

	pending := h.native.Pending(agentID)
	out := make([]NativeApprovalPayload, 0, len(pending))
	for _, p := range pending {
		out = append(out, NativeApprovalPayload{
			AgentID: agentID, ID: p.ID, ToolName: p.ToolName,
			Input: p.Input, AskedAt: p.AskedAt.Format(time.RFC3339),
		})
	}
	c.sendEvent(EventNativeState, NativeStatePayload{
		AgentID: agentID, Running: h.native.Running(agentID), Pending: out,
	})
}
