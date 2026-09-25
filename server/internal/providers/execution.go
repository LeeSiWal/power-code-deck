// Package providers defines execution contracts independent of the native chat
// wire protocol, the legacy agent database, HTTP and WebSocket transport.
package providers

import (
	"context"
	"encoding/json"
)

type ID string

const (
	Claude      ID = "claude"
	Codex       ID = "codex"
	Antigravity ID = "antigravity"
)

// Identity names a single process attempt, not an agent, task or conversation.
// A restart/resume must receive a new ExecutionID even in the same conversation.
type Identity struct {
	ExecutionID string
	Provider    ID
}

// Execution owns one driver lifecycle. Start is called once. Next has one reader;
// EOF means the stream ended, not that the requested work succeeded. Canceling
// Next only cancels the wait; Interrupt asks to stop a turn, Stop ends the process.
// Callers serialize lifecycle changes and create a fresh execution for retries.
type Execution interface {
	Identity() Identity
	Capabilities() Capabilities
	Start() error
	Next(context.Context) (Event, error)
	Send(string) error
	Interrupt() error
	ConversationID() string
	Stop()
	// Mode vocabulary remains provider-specific until policy extraction.
	// An error must be surfaced; it does not itself authorize a restart.
	SetPermissionMode(string) error
}

// Capabilities describe this adapter, not every feature the upstream CLI has.
type Capabilities struct {
	MultiTurn        bool
	ApprovalHandling ApprovalHandling
}
type ApprovalHandling string

const (
	ApplicationBroker ApprovalHandling = "application_broker"
	CLISettings       ApprovalHandling = "cli_settings"
)

type EventKind string

const (
	Ready        EventKind = "ready"
	Message      EventKind = "message"
	TurnFinished EventKind = "turn_finished"
	Other        EventKind = "other"
)

// Event preserves message block ordering and tool call/result correlation.
// Unknown/control wire events remain Other; they never imply completion.
// Sequence is local to this execution's consumed stream, starting at one.
type Event struct {
	Identity         Identity
	Sequence         uint64
	Kind             EventKind
	ConversationID   string
	MessageID        string
	Model            string
	Role             string
	ParentToolCallID string
	Blocks           []Block
	Delta            bool // true for a streamed message fragment
	Outcome          *Outcome
}

type BlockKind string

const (
	Text         BlockKind = "text"
	ToolCall     BlockKind = "tool_call"
	ToolResult   BlockKind = "tool_result"
	UnknownBlock BlockKind = "unknown"
)

type Block struct {
	Kind       BlockKind
	Text       string
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
	Output     json.RawMessage
	IsError    bool
}

type CompletionStatus string

const (
	CompletionUnknown     CompletionStatus = "unknown"
	CompletionSuccess     CompletionStatus = "success"
	CompletionFailed      CompletionStatus = "failed"
	CompletionInterrupted CompletionStatus = "interrupted"
)

// Outcome is the provider/process result, never a substitute for task checks.
type Outcome struct {
	Status      CompletionStatus
	IsError     bool
	Text        string
	Reason      string
	Diagnostics string // bounded provider stderr, including soft-denial notices
	Usage       *Usage
	// nil means the source did not report cost; zero can be a reported value.
	CostUSD *float64
	Denials []DeniedTool
}

type UsageScope string

const (
	TurnUsage         UsageScope = "turn"
	ConversationUsage UsageScope = "conversation"
)

// Counts retain provider-reported semantics. Cache counts must not be added to
// InputTokens without knowing whether that provider includes them already.
type Usage struct {
	Scope                    UsageScope
	ThinkingTokens           *int
	TotalTokens              *int
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

type DeniedTool struct {
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
}
