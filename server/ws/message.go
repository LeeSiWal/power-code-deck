package ws

import "encoding/json"

type WSMessage struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// Client -> Server events
const (
	EventTerminalAttach      = "terminal:attach"
	EventTerminalDetach      = "terminal:detach"
	EventTerminalInput       = "terminal:input"
	EventTerminalAck         = "terminal:ack"
	EventTerminalResize      = "terminal:resize"
	EventTerminalPasteSubmit = "terminal:pasteSubmit"
	EventTerminalPasteOnly   = "terminal:pasteOnly"
	EventFileWatch           = "file:watch"
	EventFileUnwatch         = "file:unwatch"
	EventPing                = "ping"

	// Native track — a Claude session driven as a structured stream instead of a
	// terminal. There is no attach/resize/ack here: without a screen there is
	// nothing to size, and nothing to meter for backpressure.
	EventNativeOpen      = "native:open"   // start (or adopt) a session and replay its history
	EventNativeInput     = "native:input"  // a user turn
	EventNativeDecide    = "native:decide" // answer a pending approval
	EventNativeStop      = "native:stop"
	EventNativeInterrupt = "native:interrupt" // stop the turn, keep the session
	EventNativeSetModel  = "native:setModel"  // switch model, resume same conversation
)

// Server -> Client events
const (
	EventTerminalOutput = "terminal:output"
	EventAgentList      = "agent:list"
	EventAgentStatus    = "agent:status"
	EventAgentCreated   = "agent:created"
	EventAgentDestroyed = "agent:destroyed"
	EventAgentActivity  = "agent:activity"
	EventFileChanged    = "file:changed"
	EventFileTree       = "file:tree"
	EventPong           = "pong"
	// Sent to a viewer when another device attaches to the same session — only one
	// device views a session at a time, so the PTY isn't resized by two viewers.
	EventTerminalEvicted = "terminal:evicted"

	// Native track. Unlike the terminal, these go to EVERY device watching the
	// agent: a conversation has no exclusive viewer — two devices can follow the
	// same run, and either can answer a prompt.
	EventNativeEvent    = "native:event"    // one stream-json event, verbatim
	EventNativeApproval = "native:approval" // the agent is blocked, waiting on a human
	EventNativeHistory  = "native:history"  // events so far, on open
	EventNativeState    = "native:state"    // running/stopped + pending approvals
	EventNativeError    = "native:error"    // something failed — say so, never swallow it
)

// Server -> Client events (meta + notifications)
const (
	EventAgentNotification      = "agent:notification"
	EventAgentNotificationClear = "agent:notification:clear"
	EventAgentMeta              = "agent:meta"
	EventAgentMetaStatus        = "agent:meta:status"
	EventAgentMetaProgress      = "agent:meta:progress"
	EventAgentMetaLog           = "agent:meta:log"
)

type TerminalAttachPayload struct {
	AgentID string `json:"agentId"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}

type TerminalInputPayload struct {
	AgentID string `json:"agentId"`
	Data    string `json:"data"`
}

// TerminalAckPayload confirms the viewer has parsed `bytes` of output, draining
// the server's flow-control backlog (ACK-based backpressure). `bytes` is the
// UTF-8 byte count, matching what the server metered when it sent the output.
type TerminalAckPayload struct {
	AgentID string `json:"agentId"`
	Bytes   int    `json:"bytes"`
}

type TerminalResizePayload struct {
	AgentID string `json:"agentId"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}

// TerminalPastePayload carries Prompt Bar text that the server converts into a
// safe paste sequence before writing to the PTY. Mode selects how the text is
// wrapped: "bracketed-paste" (default), "plain-paste", or "typewriter".
type TerminalPastePayload struct {
	AgentID string `json:"agentId"`
	Text    string `json:"text"`
	Mode    string `json:"mode,omitempty"`
}

type TerminalOutputPayload struct {
	AgentID string `json:"agentId"`
	Data    string `json:"data"`
}

type TerminalEvictedPayload struct {
	AgentID string `json:"agentId"`
}

type FileWatchPayload struct {
	AgentID string `json:"agentId"`
	Path    string `json:"path"`
}

type AgentStatusPayload struct {
	AgentID string `json:"agentId"`
	Status  string `json:"status"`
}

type AgentNotificationPayload struct {
	AgentID   string `json:"agentId"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type AgentNotificationClearPayload struct {
	AgentID string `json:"agentId"`
}

type AgentMetaPayload struct {
	AgentID        string `json:"agentId"`
	GitBranch      string `json:"gitBranch,omitempty"`
	GitDirty       bool   `json:"gitDirty"`
	GitAhead       int    `json:"gitAhead"`
	ListeningPorts []int  `json:"listeningPorts,omitempty"`
}

type AgentMetaStatusPayload struct {
	AgentID string `json:"agentId"`
	Key     string `json:"key"`
	Text    string `json:"text"`
	Color   string `json:"color,omitempty"`
}

type AgentMetaProgressPayload struct {
	AgentID string  `json:"agentId"`
	Value   float64 `json:"value"`
	Label   string  `json:"label,omitempty"`
}

type AgentMetaLogPayload struct {
	AgentID   string `json:"agentId"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// --- Native track payloads ---

// NativeOpenPayload starts or adopts a native session. Cwd/Model are only used
// when it isn't already running.
type NativeOpenPayload struct {
	AgentID string `json:"agentId"`
	Cwd     string `json:"cwd"`
	Model   string `json:"model"`
	Resume  string `json:"resume"` // Claude's own session_id, to continue a past run
}

type NativeInputPayload struct {
	AgentID string `json:"agentId"`
	Text    string `json:"text"`
}

// NativeSetModelPayload switches the model mid-conversation: the session restarts
// with the new --model, resuming the same Claude conversation so nothing is lost.
type NativeSetModelPayload struct {
	AgentID string `json:"agentId"`
	Model   string `json:"model"`
}

// NativeDecidePayload answers one approval. Behavior is allow|deny.
//
// UpdatedInput carries an edited tool input (approve-with-changes) and Message the
// reason on deny — Claude reads that reason and adapts, so "not that path, use
// ./tmp" is a far better answer than a bare no.
type NativeDecidePayload struct {
	AgentID      string          `json:"agentId"`
	ID           string          `json:"id"`
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

type NativeStopPayload struct {
	AgentID string `json:"agentId"`
}

type NativeInterruptPayload struct {
	AgentID string `json:"agentId"`
}

// NativeEventPayload wraps one stream-json event for the browser. Event is the
// CLI's raw JSON: the client renders from the same bytes the CLI produced, so a
// field we haven't taught the server about still reaches the UI.
type NativeEventPayload struct {
	AgentID string          `json:"agentId"`
	Event   json.RawMessage `json:"event"`
}

type NativeHistoryPayload struct {
	AgentID string            `json:"agentId"`
	Events  []json.RawMessage `json:"events"`
	Running bool              `json:"running"`
}

// NativeApprovalPayload is one pending "may I?".
type NativeApprovalPayload struct {
	AgentID  string          `json:"agentId"`
	ID       string          `json:"id"`
	ToolName string          `json:"toolName"`
	Input    json.RawMessage `json:"input"`
	AskedAt  string          `json:"askedAt"`
}

type NativeStatePayload struct {
	AgentID string                  `json:"agentId"`
	Running bool                    `json:"running"`
	Pending []NativeApprovalPayload `json:"pending"`
}

// NativeErrorPayload carries a failure to the user instead of dropping it.
//
// The whole point of the native track is not lying about what happened, and the
// first way to break that promise is to swallow "your message never got sent".
type NativeErrorPayload struct {
	AgentID string `json:"agentId"`
	Message string `json:"message"`
}
