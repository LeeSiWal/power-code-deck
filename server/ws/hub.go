package ws

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
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
	activity       *services.ActivityManager
	controlRoom    *services.ControlRoomService
	rules          *services.ApprovalRuleStore
	allowedOrigins map[string]bool
	upgrader       websocket.Upgrader

	statusMu   sync.Mutex
	lastStatus map[string]string // last agent:status we broadcast, for change detection

	metaMu       sync.Mutex
	lastMetaPoll time.Time
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
	// A faster, cheap loop that watches session aliveness and pushes status changes.
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			h.pollStatus()
		}
	}()
}

// pollStatus watches every agent's true aliveness and broadcasts agent:status on any
// change, so the dashboard, control room, and terminal header all reflect a session
// starting or dying without a manual refresh. Previously agent:status was only sent
// on an explicit restart, so a session that died — or a native session that came up —
// left every surface showing the stale state (the "stopped while working" bug).
//
// agentSvc.List() already reconciles the real status (PTY OR native session); we diff
// it against what we last broadcast and emit only transitions.
func (h *Hub) pollStatus() {
	agents, err := h.agentSvc.List()
	if err != nil {
		return
	}
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	if h.lastStatus == nil {
		h.lastStatus = make(map[string]string)
	}
	live := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		live[a.ID] = struct{}{}
		prev, seen := h.lastStatus[a.ID]
		h.lastStatus[a.ID] = a.Status
		// Emit only a real transition. The first time we see an agent we just record
		// its status (the client already has it from its REST load), so a reconnect
		// doesn't replay a burst of no-op status events.
		if seen && prev != a.Status {
			h.BroadcastAll(EventAgentStatus, AgentStatusPayload{AgentID: a.ID, Status: a.Status})
			h.NoteAgentChange(a.ID)
		}
	}
	for id := range h.lastStatus {
		if _, ok := live[id]; !ok {
			delete(h.lastStatus, id)
		}
	}
	// Companion shells have no DB row, so surface an exited process directly to
	// each attached panel instead of leaving it as a silent frozen terminal.
	h.clients.Range(func(key, _ interface{}) bool {
		client := key.(*Client)
		for _, sessionID := range client.shells() {
			if !h.engine.HasSession(sessionID) {
				agentID := strings.TrimPrefix(sessionID, "shell:")
				client.sendEvent(EventShellState, ShellStatePayload{AgentID: agentID, Running: false})
				client.removeShell(sessionID)
			}
		}
		return true
	})
}

func (h *Hub) pollMeta() {
	h.metaMu.Lock()
	h.lastMetaPoll = time.Now()
	h.metaMu.Unlock()

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

// pollMetaSoon은 새 클라이언트가 붙었을 때 메타를 즉시 한 번 밀어준다.
//
// agent:meta의 유일한 발신 경로가 10초 티커라, 이게 없으면 새로 연 화면은 git 브랜치와
// 포트가 최대 10초간 빈 채로 렌더된다. 대시보드에서는 다른 정보가 먼저 채워져 가려져
// 있었지만, 통합 관제실은 타일에 메타를 직접 얹기 때문에 공백이 그대로 보인다.
//
// 여러 기기가 동시에 붙을 때 git·포트 스캔이 반복되지 않도록 3초 디바운스를 건다.
// 건너뛰어도 정규 티커가 곧 처리하므로 공백은 최대 3초로 제한된다.
func (h *Hub) pollMetaSoon() {
	h.metaMu.Lock()
	if time.Since(h.lastMetaPoll) < 3*time.Second {
		h.metaMu.Unlock()
		return
	}
	// 검사와 스탬프를 같은 임계구역에서 처리한다 — 둘을 나눠 잡으면 두 고루틴이
	// 모두 검사를 통과해 스캔이 중복된다. 무거운 pollMeta 본문은 락 밖에서 돈다.
	h.lastMetaPoll = time.Now()
	h.metaMu.Unlock()
	h.pollMeta()
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
		// Persistent browser id — lets the server tie a WS connection AND a push
		// subscription to the same device, so "active device" survives reconnects.
		deviceID: r.URL.Query().Get("device"),
	}
	h.clients.Store(client, true)

	// 새 화면이 붙었다 — 10초 티커를 기다리지 말고 메타를 지금 밀어준다.
	go h.pollMetaSoon()

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
	for _, sessionID := range c.shells() {
		h.engine.Detach(sessionID, c.viewerID)
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
				h.pushNotify(agentID, "task_complete", "작업 완료", h.agentName(agentID), "chat", "latest")
			}
		},
		func(req services.PermissionRequest) {
			// BroadcastAll (not per-agent): the Control Room approval feed isn't a
			// viewer of any single session, so a per-agent broadcast would never
			// reach it. Session-scoped surfaces filter by agentId on the client.
			//
			// CanRemember·RememberTarget: 판정은 서버에만 두고 클라이언트로 실어 보낸다.
			// 권한 판정이 두 곳에 살면 어긋나고, 어긋나는 쪽은 항상 사용자에게
			// 거짓말을 하는 쪽이다. SetNativeService 안의 콜백이라 h.native는 nil이
			// 아니지만 SessionCwd가 "" 을 돌려줄 수 있다 — cwd가 없으면 프로젝트를
			// 특정할 수 없으므로 CanRemember=false로 둔다.
			cwd := h.native.SessionCwd(req.SessionID)
			canRemember, rememberTarget := services.CanRememberCall(req.ToolName, req.Input, cwd)
			h.BroadcastAll(EventNativeApproval, NativeApprovalPayload{
				AgentID:        req.SessionID,
				ID:             req.ID,
				ToolName:       req.ToolName,
				Input:          req.Input,
				AskedAt:        req.AskedAt.Format(time.RFC3339),
				CanRemember:    canRemember,
				RememberTarget: rememberTarget,
			})
			h.NoteAgentChange(req.SessionID)
			// A blocked agent goes nowhere until a human answers — the single most
			// worth-interrupting notification, so it's the reason push exists here.
			h.pushNotify(req.SessionID, "permission_request",
				"승인 필요", h.agentName(req.SessionID)+" · "+req.ToolName+" 실행 대기 중", "approval", req.ID)
		},
	)
}

func (h *Hub) SetActivityManager(activity *services.ActivityManager) {
	h.activity = activity
}

// SetApprovalRules wires the allowlist so a "항상 허용" decision can persist.
func (h *Hub) SetApprovalRules(store *services.ApprovalRuleStore) {
	h.rules = store
}

// SetPushService wires the Web Push fan-out. Must be called before SetNativeService
// so the native handlers above can reach it.
func (h *Hub) SetPushService(p *services.PushService) { h.pushSvc = p }

// SetControlRoom wires the multi-session overview aggregator so lifecycle,
// approval and notification changes mark the affected session dirty for the next
// summary batch.
func (h *Hub) SetControlRoom(c *services.ControlRoomService) { h.controlRoom = c }

// NoteAgentChange marks an agent dirty in the Control Room. Handlers that already
// hold the hub (create/destroy/restart) call this so a tile updates promptly
// instead of waiting for the slow correction pass. Empty id is a no-op.
func (h *Hub) NoteAgentChange(agentID string) {
	if h.controlRoom != nil {
		h.controlRoom.MarkDirty(agentID)
	}
}

// pushNotify records the event for the Logs/notification history and delivers a Web
// Push to every subscribed device. reason is the stable notification kind (also the
// notifications-table reason), used as the collapse tag so repeats replace rather
// than stack. Best-effort and non-blocking — this runs on the event hot path.
func (h *Hub) pushNotify(agentID, reason, title, body, refType, refID string) {
	if h.notifSvc != nil {
		_, _ = h.notifSvc.CreateWithRef(agentID, reason, body, refType, refID)
	}
	// Broadcast the in-app signal too. This event was defined but never emitted, so
	// the notification bell/badge and any foreground toast had nothing to react to —
	// only the Web Push (which iOS delivers SILENTLY while the PWA is focused) fired.
	// Emitting it lets the app show a banner when it's open, filling exactly that gap.
	h.BroadcastAll(EventAgentNotification, AgentNotificationPayload{
		AgentID:   agentID,
		Reason:    reason,
		Message:   body,
		Timestamp: time.Now().Format(time.RFC3339),
		RefType:   refType,
		RefID:     refID,
	})
	// A new notification changes the tile's unread badges — refresh its summary.
	h.NoteAgentChange(agentID)
	if h.pushSvc == nil || !h.pushSvc.Enabled() {
		return
	}
	// Skip the push when the target device is looking at the app RIGHT NOW: it already
	// received the in-app toast from the broadcast above, and pushing as well is the
	// double buzz. Keyed on the DEVICE rather than the agent on purpose — the toaster
	// is mounted app-wide and shows every agent's notification, so being foregrounded
	// anywhere in the deck is enough for the alert to land.
	if dev := h.pushSvc.ActiveDevice(agentID); dev != "" && h.deviceForeground(dev) {
		log.Printf("push: skip %q tag=%s-%s → device is foregrounded (in-app toast covers it)", title, reason, agentID)
		return
	}
	// Aim the push at ONLY the session's active device — the one you're using — so the
	// same alert no longer lights up every device at once. No active device on record
	// means no push (deliberately no broadcast fallback).
	h.pushSvc.NotifyAgent(agentID, services.PushMessage{
		Title: title,
		Body:  body,
		Tag:   reason + "-" + agentID,
		URL:   "/agents/" + agentID,
	})
}

// deviceForeground reports whether any live connection from this device says the
// browser is visible. Any one is enough: a device may hold several tabs, and if even
// one is on screen the toast is seen.
//
// A device with no live connection is NOT foregrounded — so a dropped socket
// re-enables push immediately rather than silencing it.
func (h *Hub) deviceForeground(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	found := false
	h.clients.Range(func(key, _ interface{}) bool {
		c := key.(*Client)
		if c.deviceID == deviceID && c.isForeground() {
			found = true
			return false
		}
		return true
	})
	return found
}

// NotifyStalled fires a "stalled" notification when the Control Room detects a
// running session has gone quiet past the idle threshold. The transition (fire once
// per stalled episode, not every batch) is owned by ControlRoomService; this is just
// the delivery path, reusing pushNotify so it records + pushes like the others.
func (h *Hub) NotifyStalled(agentID string) {
	h.pushNotify(agentID, "stalled", "무응답 세션", h.agentName(agentID)+" · 오랫동안 반응이 없습니다", "chat", "latest")
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
	case EventClientVisibility:
		// The browser reporting its own visibilitychange. Used only to suppress a
		// redundant push while the in-app toast is visible (see pushNotify).
		var payload ClientVisibilityPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		c.setForeground(payload.Visible)

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

	case EventShellAttach:
		var payload TerminalAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		h.handleShellAttach(c, payload)

	case EventShellDetach:
		var payload TerminalAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		sessionID := shellSessionID(payload.AgentID)
		h.engine.Detach(sessionID, c.viewerID)
		c.removeShell(sessionID)

	case EventShellInput:
		var payload TerminalInputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		sessionID := shellSessionID(payload.AgentID)
		if !h.mayWrite(c, sessionID) {
			return
		}
		h.engine.Write(sessionID, []byte(payload.Data))

	case EventShellAck:
		var payload TerminalAckPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		sessionID := shellSessionID(payload.AgentID)
		if !h.mayWrite(c, sessionID) {
			return
		}
		h.engine.Ack(sessionID, payload.Bytes)

	case EventShellResize:
		var payload TerminalResizePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		sessionID := shellSessionID(payload.AgentID)
		if !h.mayWrite(c, sessionID) {
			return
		}
		h.engine.Resize(sessionID, int(payload.Cols), int(payload.Rows))

	case EventShellKill:
		var payload TerminalAttachPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		sessionID := shellSessionID(payload.AgentID)
		if !h.mayWrite(c, sessionID) {
			return
		}
		h.engine.Kill(sessionID)
		c.removeShell(sessionID)
		c.sendEvent(EventShellState, ShellStatePayload{AgentID: payload.AgentID, Running: false})

	case EventNativeOpen:
		var payload NativeOpenPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		// Opening is what makes this client a watcher, so it must come before the
		// history replay — otherwise events racing the reply would be lost.
		c.watchingAgent = payload.AgentID
		// This device now owns the session: it becomes the exclusive viewer and the
		// sole push target. Evict other DEVICES watching this agent (a different tab on
		// the same device may keep following) so the previous device drops to standby.
		if c.deviceID != "" {
			h.clients.Range(func(key, _ interface{}) bool {
				other, ok := key.(*Client)
				if !ok || other == c {
					return true
				}
				if other.watchingAgent == payload.AgentID && other.deviceID != "" && other.deviceID != c.deviceID {
					other.sendEvent(EventNativeEvicted, NativeEvictedPayload{AgentID: payload.AgentID})
				}
				return true
			})
		}
		if h.pushSvc != nil {
			h.pushSvc.SetActiveDevice(payload.AgentID, c.deviceID)
		}
		driver, cwd, err := h.nativeLaunchIdentity(payload.AgentID)
		if err != nil {
			log.Printf("native: reject open %s: %v", payload.AgentID, err)
			c.sendEvent(EventNativeError, NativeErrorPayload{AgentID: payload.AgentID, Message: "세션 정보를 확인하지 못했습니다: " + err.Error()})
			c.sendEvent(EventNativeState, NativeStatePayload{AgentID: payload.AgentID, Running: false})
			return
		}
		if !h.native.Running(payload.AgentID) {
			// Repository identity, driver kind and resume identity are server-owned.
			// Model/mode/effort remain client hints only; NativeService replaces them
			// with persisted values when present.
			if err := h.native.Start(payload.AgentID, driver, cwd, payload.Model, "", payload.Mode, payload.Effort); err != nil {
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
		h.sendActivitySnapshot(c, payload.AgentID)

	case EventNativeInput:
		var payload NativeInputPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		// The device sending a message IS the one in use right now — make it the active
		// device so push follows real-time usage, not just whoever opened last.
		if h.pushSvc != nil {
			h.pushSvc.SetActiveDevice(payload.AgentID, c.deviceID)
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

	case EventNativeSetOptions:
		var payload NativeSetOptionsPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		dropped, err := h.native.SetOptions(payload.AgentID, services.NativeOptions{
			AddDirs:       payload.Options.AddDirs,
			MaxBudgetUSD:  payload.Options.MaxBudgetUSD,
			Autocompact:   payload.Options.Autocompact,
			FallbackModel: payload.Options.FallbackModel,
		})
		// Report the stored result either way. A restart that failed still leaves the
		// options saved, so the deck must show what it will launch with next time
		// rather than the values the user typed.
		h.broadcastNativeOptions(payload.AgentID, dropped)
		if err != nil {
			c.sendEvent(EventNativeError, NativeErrorPayload{
				AgentID: payload.AgentID,
				Message: "세션 옵션 적용 실패: " + err.Error(),
			})
		}

	case EventNativeSetEffort:
		var payload NativeSetEffortPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || h.native == nil {
			return
		}
		if err := h.native.SetEffort(payload.AgentID, payload.Effort); err != nil {
			c.sendEvent(EventNativeError, NativeErrorPayload{
				AgentID: payload.AgentID,
				Message: "Effort 전환 실패: " + err.Error(),
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
		// Decide is a one-shot CAS: Resolve returns false if this request was already
		// answered (by another device). Tell the deciding client which happened, and
		// on a real resolution tell EVERY client so the card disappears everywhere —
		// otherwise a browser that didn't decide keeps showing a dead approval.

		// "항상 허용"이면 규칙으로 남긴다. Resolve가 pending에서 요청을 지우므로
		// 도구 이름과 입력을 결정 전에 스냅샷해 둔다.
		var ruleTool string
		var ruleInput json.RawMessage
		if payload.Remember && payload.Behavior == "allow" && h.rules != nil {
			for _, p := range h.native.Broker().Pending(payload.AgentID) {
				if p.ID == payload.ID {
					ruleTool, ruleInput = p.ToolName, p.Input
					break
				}
			}
		}
		// 다른 기기가 같은 요청을 먼저 처리했다면 Decide는 false를 반환하고,
		// 스냅샷은 사용되지 않으며 규칙이 저장되지 않는다 — 이는 의도적이다.

		ok := h.native.Decide(payload.ID, services.PermissionDecision{
			Behavior:     payload.Behavior,
			UpdatedInput: payload.UpdatedInput,
			Message:      payload.Message,
		})
		result := "already_resolved"
		if ok {
			result = "accepted"
			// 저장 실패는 승인 자체를 실패시키지 않는다 — 부가 기능이 주 기능을
			// 막으면 안 된다. 안전 판정 재확인은 ApprovalRuleStore.Save가 한다.
			if ruleInput != nil {
				if cwd := h.native.SessionCwd(payload.AgentID); cwd != "" {
					if err := h.rules.Save(cwd, ruleTool, ruleInput); err != nil {
						log.Printf("approval rule: not saved: %v", err)
					}
				}
			}
			h.BroadcastAll(EventApprovalResolved, ApprovalResolvedPayload{
				RequestID: payload.ID,
				AgentID:   payload.AgentID,
				Result:    "accepted",
			})
			h.NoteAgentChange(payload.AgentID)
		}
		c.sendEvent(EventNativeDecideResult, NativeDecideResultPayload{
			RequestID: payload.ID,
			AgentID:   payload.AgentID,
			Result:    result,
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

// nativeLaunchIdentity resolves all context-bearing launch metadata from the
// durable agent row. A WebSocket payload is transport input, not authority: using
// its cwd/driver/resume fields could attach an agent id to another repository.
func (h *Hub) nativeLaunchIdentity(agentID string) (driver, cwd string, err error) {
	if h.agentSvc == nil {
		return "", "", fmt.Errorf("agent service unavailable")
	}
	agent, err := h.agentSvc.Get(agentID)
	if err != nil {
		return "", "", fmt.Errorf("agent not found")
	}
	cwd, err = services.ResolveWorkingDir(agent.WorkingDir)
	if err != nil {
		return "", "", err
	}
	switch {
	case agent.Preset == "codex-cli" || agent.Command == "codex":
		driver = "codex"
	case agent.Preset == "claude", agent.Preset == "claude-code", agent.Command == "claude":
		driver = "claude"
	default:
		return "", "", fmt.Errorf("agent is not native-capable")
	}
	return driver, cwd, nil
}

func shellSessionID(agentID string) string { return "shell:" + agentID }

func defaultShell() string {
	if runtime.GOOS == "windows" {
		if shell := os.Getenv("COMSPEC"); shell != "" {
			return shell
		}
		return "cmd.exe"
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func (h *Hub) handleShellAttach(c *Client, payload TerminalAttachPayload) {
	agent, err := h.agentSvc.Get(payload.AgentID)
	if err != nil {
		c.sendEvent(EventShellState, ShellStatePayload{AgentID: payload.AgentID, Message: "에이전트를 찾을 수 없습니다"})
		return
	}
	sessionID := shellSessionID(payload.AgentID)
	fallback := false
	if !h.engine.HasSession(sessionID) {
		cwd := agent.WorkingDir
		if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
			cwd, _ = os.UserHomeDir()
			fallback = true
		}
		if _, err := h.engine.Create(services.CreateSessionRequest{
			ID: sessionID, Type: "shell", Command: defaultShell(), Cwd: cwd,
			Cols: int(payload.Cols), Rows: int(payload.Rows),
		}); err != nil {
			c.sendEvent(EventShellState, ShellStatePayload{
				AgentID: payload.AgentID, Message: "셸을 시작하지 못했습니다: " + err.Error(),
			})
			return
		}
	}
	res, err := h.engine.Attach(sessionID, c.viewerID)
	if err != nil {
		c.sendEvent(EventShellState, ShellStatePayload{AgentID: payload.AgentID, Message: err.Error()})
		return
	}
	c.addShell(sessionID)
	cols, rows := payload.Cols, payload.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	h.engine.Resize(sessionID, int(cols), int(rows))
	message := ""
	if fallback {
		message = "작업 디렉토리가 없어 홈 디렉토리에서 시작했습니다"
	}
	c.sendEvent(EventShellState, ShellStatePayload{AgentID: payload.AgentID, Running: true, Message: message})
	if res != nil && len(res.Replay) > 0 {
		c.sendEvent(EventShellOutput, TerminalOutputPayload{AgentID: payload.AgentID, Data: string(res.Replay)})
	}
	h.sendActivitySnapshot(c, payload.AgentID)
}

// sendActivitySnapshot pushes the current activity to a client that just attached, so
// re-opening a running session shows what the agent is doing immediately instead of
// waiting for the next poll tick.
//
// Called from native:open and shell:attach — deliberately NOT from terminal:attach.
// The gap it closes is at most one 800ms tick, and the terminal track (shell / custom
// commands) has virtually no Claude activity to show, so matching it there would add
// code for no visible gain. If terminal sessions ever need the panel, this same line
// is all it takes.
func (h *Hub) sendActivitySnapshot(c *Client, agentID string) {
	if h.activity == nil {
		return
	}
	if snap, ok := h.activity.Latest(agentID); ok {
		c.sendEvent(EventAgentActivity, snap)
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
	// A full terminal view (claim=true) also makes this the device's active session, so
	// push follows the device you're actually working on. Dashboard snapshot thumbnails
	// attach WITHOUT claim, so previewing many agents never hijacks push ownership.
	if payload.Claim && h.pushSvc != nil {
		h.pushSvc.SetActiveDevice(payload.AgentID, c.deviceID)
	}

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

// broadcastNativeOptions tells every device watching this agent which session options
// are now in force. Sent to all watchers rather than just the one that changed them,
// so a second device doesn't keep showing the settings it last saw.
func (h *Hub) broadcastNativeOptions(agentID string, dropped []string) {
	if h.native == nil {
		return
	}
	h.BroadcastToAgent(agentID, EventNativeOptions, NativeOptionsPayload{
		AgentID: agentID,
		Options: toWireOptions(h.native.Options(agentID)),
		Dropped: dropped,
	})
}

// toWireOptions converts the service's options to the wire shape. Kept explicit rather
// than reusing the service struct so the WS contract can't drift by accident when a
// field is added on the service side.
func toWireOptions(o services.NativeOptions) NativeSessionOptions {
	return NativeSessionOptions{
		AddDirs:       o.AddDirs,
		MaxBudgetUSD:  o.MaxBudgetUSD,
		Autocompact:   o.Autocompact,
		FallbackModel: o.FallbackModel,
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

// BroadcastToShell sends output only to clients attached to the companion shell.
func (h *Hub) BroadcastToShell(agentID string, payload TerminalOutputPayload) {
	h.clients.Range(func(key, _ interface{}) bool {
		client := key.(*Client)
		if client.hasShell(shellSessionID(agentID)) {
			client.sendEvent(EventShellOutput, payload)
		}
		return true
	})
}

// KillCompanionShell enforces the shell's ownership by its agent.
func (h *Hub) KillCompanionShell(agentID string) {
	h.engine.Kill(shellSessionID(agentID))
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
	model, mode, effort := h.native.Config(agentID)
	c.sendEvent(EventNativeHistory, NativeHistoryPayload{
		AgentID: agentID, Events: raw, Running: h.native.Running(agentID),
		Model: model, Mode: mode, Effort: effort,
		Options: toWireOptions(h.native.Options(agentID)),
	})

	pending := h.native.Pending(agentID)
	out := make([]NativeApprovalPayload, 0, len(pending))
	// 재접속 시에도 CanRemember·RememberTarget을 채워야 버튼이 사라지지 않는다.
	cwd := h.native.SessionCwd(agentID)
	for _, p := range pending {
		canRemember, rememberTarget := services.CanRememberCall(p.ToolName, p.Input, cwd)
		out = append(out, NativeApprovalPayload{
			AgentID: agentID, ID: p.ID, ToolName: p.ToolName,
			Input: p.Input, AskedAt: p.AskedAt.Format(time.RFC3339),
			CanRemember: canRemember, RememberTarget: rememberTarget,
		})
	}
	c.sendEvent(EventNativeState, NativeStatePayload{
		AgentID: agentID, Running: h.native.Running(agentID), Pending: out,
	})
}
