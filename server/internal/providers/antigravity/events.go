package antigravity

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"powercodedeck/internal/providers"
)

// Protocol reference: https://antigravity.google/docs/cli/headless/
// Keep the per-execution decoder separate from process management. Tool DONE
// may be the first update, or follow ACTIVE; emit its call exactly once.
type decoder struct{ tools map[string]bool }

func (d *decoder) parse(line []byte) (providers.Event, error) {
	var wire struct {
		Event          string `json:"event"`
		ConversationID string `json:"conversation_id"`
		Init           *struct {
			Model string `json:"model"`
		} `json:"init"`
		Step *struct {
			ConversationID string `json:"conversation_id"`
			Index          int    `json:"step_index"`
			State          string `json:"state"`
			Type           string `json:"step_type"`
			ToolName       string `json:"tool_name"`
			Text           string `json:"text_delta"`
			Tool           *struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
				Output     json.RawMessage `json:"output"`
				Error      json.RawMessage `json:"error"`
			} `json:"tool_info"`
		} `json:"step_update"`
		Result *struct {
			ConversationID string `json:"conversation_id"`
			Status         string `json:"status"`
			Response       string `json:"response"`
			Error          string `json:"error"`
			DeniedActions  []struct {
				Action string `json:"action"`
			} `json:"denied_actions"`
			Usage *struct {
				Input     int  `json:"input_tokens"`
				Output    int  `json:"output_tokens"`
				Thinking  *int `json:"thinking_tokens"`
				CacheRead int  `json:"cache_read_tokens"`
				Total     *int `json:"total_tokens"`
			} `json:"usage"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &wire); err != nil {
		return providers.Event{}, err
	}
	if wire.Event == "" {
		return providers.Event{}, fmt.Errorf("missing Antigravity event type")
	}
	event := providers.Event{Kind: providers.Other, ConversationID: wire.ConversationID}
	switch wire.Event {
	case "init":
		if wire.Init == nil {
			return event, fmt.Errorf("missing init payload")
		}
		event.Kind = providers.Ready
		event.Model = wire.Init.Model
	case "step_update":
		step := wire.Step
		if step == nil {
			return event, fmt.Errorf("missing step payload")
		}
		event.ConversationID = step.ConversationID
		event.MessageID = strconv.Itoa(step.Index)
		switch step.Type {
		case "agent_response":
			event.Kind = providers.Message
			event.Role = "assistant"
			event.Delta = true
			if step.Text != "" {
				event.Blocks = []providers.Block{{Kind: providers.Text, Text: step.Text}}
			}
		case "tool":
			if step.Tool == nil || (step.State != "ACTIVE" && step.State != "DONE") {
				break
			}
			event.Kind = providers.Message
			event.Role = "tool"
			id := step.ConversationID + ":" + strconv.Itoa(step.Index)
			if d.tools == nil {
				d.tools = make(map[string]bool)
			}
			if !d.tools[id] {
				name := step.ToolName
				if name == "" {
					name = step.Tool.Name
				}
				event.Blocks = append(event.Blocks, providers.Block{Kind: providers.ToolCall, ToolCallID: id, ToolName: name, Input: step.Tool.Parameters})
				d.tools[id] = true
			}
			if step.State == "DONE" {
				output := step.Tool.Output
				hasError := len(step.Tool.Error) > 0 && string(step.Tool.Error) != "null" && string(step.Tool.Error) != `""`
				if len(output) == 0 && hasError {
					output = step.Tool.Error
				}
				event.Blocks = append(event.Blocks, providers.Block{Kind: providers.ToolResult, ToolCallID: id, Output: output, IsError: hasError})
			}
		}
	case "result":
		result := wire.Result
		if result == nil {
			return event, fmt.Errorf("missing result payload")
		}
		event.Kind = providers.TurnFinished
		event.ConversationID = result.ConversationID
		event.Outcome = &providers.Outcome{IsError: result.Status != "SUCCESS" || result.Error != "", Reason: result.Status, Text: result.Response}
		event.Outcome.Status = providers.CompletionFailed
		if !event.Outcome.IsError {
			event.Outcome.Status = providers.CompletionSuccess
		}
		if result.Status == "CANCELED" || result.Status == "INTERRUPTED" {
			event.Outcome.Status = providers.CompletionInterrupted
		}
		if result.Error != "" {
			event.Outcome.Text = result.Error
		}
		var denied []string
		for _, action := range result.DeniedActions {
			if name := strings.TrimSpace(action.Action); name != "" {
				denied = append(denied, name)
			}
		}
		if len(denied) > 0 {
			detail := strings.Join(denied, ", ")
			if len(detail) > 512 {
				detail = detail[:512]
			}
			event.Outcome.Diagnostics = "Antigravity denied actions: " + detail
			// A nonempty answer can legitimately recover from a denied tool.
			// Empty SUCCESS plus explicit denials cannot establish completion.
			if event.Outcome.Status == providers.CompletionSuccess && strings.TrimSpace(result.Response) == "" {
				event.Outcome.Status = providers.CompletionFailed
				event.Outcome.IsError = true
				event.Outcome.Reason = "permission_denied"
				event.Outcome.Text = "Antigravity could not proceed: required tool permissions were denied: " + detail
			}
		}
		if result.Usage != nil {
			u := result.Usage
			event.Outcome.Usage = &providers.Usage{Scope: providers.ConversationUsage, InputTokens: u.Input, OutputTokens: u.Output,
				CacheReadInputTokens: u.CacheRead, ThinkingTokens: u.Thinking, TotalTokens: u.Total}
		}
	}
	return event, nil
}
