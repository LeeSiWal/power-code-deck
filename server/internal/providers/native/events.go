package native

import (
	"encoding/json"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/legacywire"
)

func clone(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }

func normalize(wire *legacywire.StreamEvent) providers.Event {
	event := providers.Event{Kind: providers.Other, ConversationID: wire.SessionID, Model: wire.Model}
	if wire.ParentToolUseID != nil {
		event.ParentToolCallID = *wire.ParentToolUseID
	}
	switch wire.Type {
	case legacywire.StreamTypeSystem:
		if wire.Subtype == "init" {
			event.Kind = providers.Ready
		}
	case legacywire.StreamTypeAssistant, legacywire.StreamTypeUser:
		event.Kind = providers.Message
		if wire.Message == nil {
			break
		}
		event.Role, event.MessageID = wire.Message.Role, wire.Message.ID
		if wire.Message.Model != "" {
			event.Model = wire.Message.Model
		}
		for _, b := range wire.Message.Content {
			block := providers.Block{Kind: providers.UnknownBlock}
			switch b.Type {
			case "text":
				block.Kind, block.Text = providers.Text, b.Text
			case "tool_use":
				block.Kind, block.ToolCallID, block.ToolName, block.Input = providers.ToolCall, b.ID, b.Name, clone(b.Input)
			case "tool_result":
				block.Kind, block.ToolCallID, block.Output, block.IsError = providers.ToolResult, b.ToolUseID, clone(b.Content), b.IsError
			}
			event.Blocks = append(event.Blocks, block)
		}
	case legacywire.StreamTypeResult:
		event.Kind = providers.TurnFinished
		result := &providers.Outcome{Status: providers.CompletionUnknown, IsError: wire.IsError, Text: wire.Result, Reason: wire.TerminalReason}
		if wire.IsError {
			result.Status = providers.CompletionFailed
		} else if wire.Subtype == "success" {
			result.Status = providers.CompletionSuccess
		}
		if wire.TerminalReason == "interrupted" || wire.TerminalReason == "cancelled" {
			result.Status = providers.CompletionInterrupted
		}
		if result.Reason == "" {
			result.Reason = wire.Subtype
		}
		if wire.Usage != nil {
			u := wire.Usage
			result.Usage = &providers.Usage{Scope: providers.TurnUsage, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
				CacheCreationInputTokens: u.CacheCreationInputTokens, CacheReadInputTokens: u.CacheReadInputTokens}
		}
		// The legacy float alone cannot distinguish missing from reported zero.
		// Read presence from the preserved source; never invent unknown costs.
		var reported struct {
			Cost *float64 `json:"total_cost_usd"`
		}
		if json.Unmarshal(wire.Raw, &reported) == nil {
			result.CostUSD = reported.Cost
		}
		for _, denial := range wire.PermissionDenial {
			result.Denials = append(result.Denials, providers.DeniedTool{ToolCallID: denial.ToolUseID, ToolName: denial.ToolName, Input: clone(denial.ToolInput)})
		}
		event.Outcome = result
	}
	return event
}
