// Package ossbridge lets Codex use a local model server that only speaks the
// OpenAI Chat Completions API (e.g. mlx_lm.server). Codex 0.157+ talks only the
// Responses API (`wire_api = "chat"` is no longer supported), so the bridge
// turns each Responses request into a Chat Completions request and the
// streamed chat answer back into Responses events.
package ossbridge

import (
	"encoding/json"
	"strings"
)

// responsesRequest is the part of a Responses API request the bridge uses.
type responsesRequest struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions"`
	Input             json.RawMessage   `json:"input"`
	Tools             []json.RawMessage `json:"tools"`
	ToolChoice        json.RawMessage   `json:"tool_choice"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls"`
	Stream            bool              `json:"stream"`
	MaxOutputTokens   *int              `json:"max_output_tokens"`
	Temperature       *float64          `json:"temperature"`
	TopP              *float64          `json:"top_p"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type chatRequest struct {
	Model             string        `json:"model"`
	Messages          []chatMessage `json:"messages"`
	Tools             []chatTool    `json:"tools,omitempty"`
	ToolChoice        any           `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool         `json:"parallel_tool_calls,omitempty"`
	Stream            bool          `json:"stream"`
	StreamOptions     any           `json:"stream_options,omitempty"`
	MaxTokens         *int          `json:"max_tokens,omitempty"`
	Temperature       *float64      `json:"temperature,omitempty"`
	TopP              *float64      `json:"top_p,omitempty"`
}

// inputItem is one entry of a Responses `input` array.
type inputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"` // custom_tool_call
	Output    json.RawMessage `json:"output"`
}

// localTools is what a local model gets: running commands and editing files.
// MCP/plugin/goal/image tools and hosted tools (web_search) are dropped — a
// small model wastes turns on them (seen: it probed a nonexistent MCP server
// before editing), and namespaced app groups would only fill its context.
var localTools = map[string]bool{"exec_command": true, "write_stdin": true, "apply_patch": true}

// customToolParams is the single-string schema a freeform (custom) tool gets on
// the chat side; the model's {"input": …} is turned back into custom input.
var customToolParams = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"The complete raw text for this tool."}},"required":["input"]}`)

// A small model cannot reliably write Codex's apply_patch grammar (measured:
// "invalid hunk" in most attempts), but handles exact string replacement. So
// instead of apply_patch the model gets edit_file/create_file, and the bridge
// writes the patch (patch.go). Codex still applies it with its own tool.
var editTools = []chatTool{
	fnTool("edit_file", "Edit an existing file by replacing one exact block of whole lines. Read the file first. old_string must be copied exactly from the file, including indentation, and must be one or more complete lines; new_string replaces it (empty deletes it). To add lines at the end of the file, leave old_string empty. Only say the edit is done after this tool reports success.",
		`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the working directory"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}`),
	fnTool("create_file", "Create a new file with the given content. Fails if the file already exists; use edit_file to change existing files.",
		`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
}

func fnTool(name, desc, params string) chatTool {
	var t chatTool
	t.Type = "function"
	t.Function.Name, t.Function.Description, t.Function.Parameters = name, desc, json.RawMessage(params)
	return t
}

// toChat converts a Responses request, and reports which tool names are
// freeform (custom) so their calls can be converted back.
func toChat(in responsesRequest, model string) (chatRequest, map[string]bool) {
	custom := map[string]bool{}
	out := chatRequest{Model: model, Stream: true, StreamOptions: map[string]bool{"include_usage": true},
		ParallelToolCalls: in.ParallelToolCalls, MaxTokens: in.MaxOutputTokens, Temperature: in.Temperature, TopP: in.TopP}
	if strings.TrimSpace(in.Instructions) != "" {
		out.Messages = append(out.Messages, chatMessage{Role: "system", Content: in.Instructions})
	}
	out.Messages = mergeSystem(append(out.Messages, inputMessages(in.Input)...))
	for _, raw := range in.Tools {
		var t struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(raw, &t) != nil || !localTools[t.Name] || (t.Type != "function" && t.Type != "custom") {
			continue
		}
		var ct chatTool
		ct.Type = "function"
		ct.Function.Name, ct.Function.Description, ct.Function.Parameters = t.Name, t.Description, t.Parameters
		if t.Type == "custom" {
			custom[t.Name] = true
			if t.Name == "apply_patch" {
				out.Tools = append(out.Tools, editTools...)
				continue
			}
			ct.Function.Parameters = customToolParams
		}
		out.Tools = append(out.Tools, ct)
	}
	if len(out.Tools) > 0 {
		var choice any = "auto"
		if len(in.ToolChoice) > 0 {
			var s string
			if json.Unmarshal(in.ToolChoice, &s) == nil && (s == "auto" || s == "none" || s == "required") {
				choice = s
			}
		}
		out.ToolChoice = choice
	} else {
		out.ParallelToolCalls = nil
	}
	return out, custom
}

func inputMessages(raw json.RawMessage) []chatMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []chatMessage{{Role: "user", Content: text}}
	}
	var items []inputItem
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var msgs []chatMessage
	for _, it := range items {
		switch it.Type {
		case "message", "":
			role := it.Role
			if role == "developer" {
				role = "system"
			}
			if role == "" {
				role = "user"
			}
			msgs = append(msgs, chatMessage{Role: role, Content: contentText(it.Content)})
		case "function_call", "custom_tool_call":
			tc := chatToolCall{ID: it.CallID, Type: "function"}
			tc.Function.Name, tc.Function.Arguments = it.Name, it.Arguments
			if it.Type == "custom_tool_call" {
				// Replay the model's own edit_file/create_file call when the bridge
				// made this patch; otherwise show the raw input.
				if name, args, ok := recallCall(it.CallID); ok {
					tc.Function.Name, tc.Function.Arguments = name, args
				} else {
					b, _ := json.Marshal(map[string]string{"input": it.Input})
					tc.Function.Arguments = string(b)
				}
			}
			// Parallel calls of one turn belong to one assistant message.
			if n := len(msgs); n > 0 && msgs[n-1].Role == "assistant" && len(msgs[n-1].ToolCalls) > 0 {
				msgs[n-1].ToolCalls = append(msgs[n-1].ToolCalls, tc)
			} else {
				msgs = append(msgs, chatMessage{Role: "assistant", Content: "", ToolCalls: []chatToolCall{tc}})
			}
		case "function_call_output", "custom_tool_call_output":
			msgs = append(msgs, chatMessage{Role: "tool", ToolCallID: it.CallID, Content: contentText(it.Output)})
		}
		// reasoning items carry encrypted content only the cloud can read: dropped.
	}
	return msgs
}

// contentText flattens Responses content (a string or typed parts) to text.
// Images are replaced by a marker: the local server is text-only.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return string(raw)
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p.Text)
		case "input_image":
			b.WriteString("\n[이미지는 로컬 모델에 전달되지 않았습니다]")
		}
	}
	return b.String()
}

// mergeSystem folds every system message into the first one: chat templates
// (Qwen's among them) expect a single leading system turn.
func mergeSystem(msgs []chatMessage) []chatMessage {
	var sys []string
	var rest []chatMessage
	for _, m := range msgs {
		if m.Role == "system" {
			if s, _ := m.Content.(string); strings.TrimSpace(s) != "" {
				sys = append(sys, s)
			}
			continue
		}
		rest = append(rest, m)
	}
	if len(sys) == 0 {
		return rest
	}
	return append([]chatMessage{{Role: "system", Content: strings.Join(sys, "\n\n")}}, rest...)
}
