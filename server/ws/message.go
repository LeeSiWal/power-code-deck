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
