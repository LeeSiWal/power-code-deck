package services

import "encoding/json"

// Wire types for Claude Code's stream-json protocol (`claude -p
// --input-format stream-json --output-format stream-json --verbose`).
//
// These mirror what the CLI ACTUALLY emits — captured from claude 2.1.212 rather
// than copied from docs, because the docs don't publish the CLI wire format and
// the third-party write-ups of it are wrong in at least one load-bearing way (they
// describe a `control_request` permission channel that this CLI never sends; see
// claude_permission.go for what really happens).
//
// Only the fields we use are typed. The protocol carries a lot more (costs,
// per-model usage, rate-limit windows) and adds fields over time, so unknown
// fields are ignored rather than rejected — a strict decoder would turn every CLI
// update into an outage.

// StreamEventType values observed on the wire, in the order a simple turn emits
// them: system/init → assistant → rate_limit_event → assistant(tool_use) →
// user(tool_result) → assistant → result.
const (
	StreamTypeSystem    = "system"
	StreamTypeAssistant = "assistant"
	StreamTypeUser      = "user"
	StreamTypeResult    = "result"
	StreamTypeRateLimit = "rate_limit_event"
	StreamTypeStream    = "stream_event" // only with --include-partial-messages
)

// StreamEvent is one line of the CLI's stdout. Raw keeps the original bytes so a
// caller can reach fields we haven't typed yet without a protocol change here.
type StreamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	SessionID string `json:"session_id"`
	UUID      string `json:"uuid"`

	// system/init
	Model          string          `json:"model"`
	Cwd            string          `json:"cwd"`
	Tools          []string        `json:"tools"`
	PermissionMode string          `json:"permissionMode"`
	Capabilities   []string        `json:"capabilities"`
	MCPServers     []MCPServerInfo `json:"mcp_servers"`
	Version        string          `json:"claude_code_version"`

	// assistant / user
	Message *StreamMessage `json:"message"`
	// Set on a subagent's messages to the id of the Task tool call that spawned
	// it; nil/"" for the main conversation.
	ParentToolUseID *string `json:"parent_tool_use_id"`

	// result
	IsError          bool               `json:"is_error"`
	Result           string             `json:"result"`
	NumTurns         int                `json:"num_turns"`
	DurationMS       int64              `json:"duration_ms"`
	TotalCostUSD     float64            `json:"total_cost_usd"`
	PermissionDenial []PermissionDenial `json:"permission_denials"`
	TerminalReason   string             `json:"terminal_reason"`

	Raw json.RawMessage `json:"-"`
}

type MCPServerInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"` // connected | pending | failed
}

// PermissionDenial records a tool call the user (or the default-deny path)
// refused. The CLI reports these in the result event even when nothing asked.
type PermissionDenial struct {
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input"`
}

type StreamMessage struct {
	ID      string         `json:"id"`
	Role    string         `json:"role"`
	Model   string         `json:"model"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a piece of a message: text, a tool call, or a tool's result.
// tool_result blocks arrive on `user` events (the CLI feeds results back as if
// the user said them) — that's how a native UI learns a tool finished.
//
// This type is used for BOTH directions, so every field is omitempty: an outgoing
// user turn must be exactly {"type":"text","text":"…"}. Sending the zero values of
// the inbound-only fields (id, name, input, tool_use_id, …) would hand the CLI a
// text block that also claims to be an empty tool call.
type ContentBlock struct {
	Type string `json:"type"` // text | tool_use | tool_result | thinking

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or array of blocks
}

// UserInput is what we write to the CLI's stdin. Shape observed working:
//
//	{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
type UserInput struct {
	Type    string           `json:"type"`
	Message UserInputMessage `json:"message"`
}

type UserInputMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewUserText builds the stdin frame for a plain user turn.
func NewUserText(text string) UserInput {
	return UserInput{
		Type: "user",
		Message: UserInputMessage{
			Role:    "user",
			Content: []ContentBlock{{Type: "text", Text: text}},
		},
	}
}

// ParseStreamEvent decodes one stdout line, keeping the raw bytes.
func ParseStreamEvent(line []byte) (*StreamEvent, error) {
	var ev StreamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, err
	}
	ev.Raw = append(json.RawMessage(nil), line...)
	return &ev, nil
}
