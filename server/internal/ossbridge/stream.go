package ossbridge

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
)

// chatChunk is one streamed Chat Completions chunk.
type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		Details          *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// emitter writes Responses API server-sent events.
type emitter struct {
	w     io.Writer
	flush func()
	seq   int
	// completed keeps the final response object (for non-streaming callers).
	completed map[string]any
}

func (e *emitter) event(name string, data map[string]any) {
	if name == "response.completed" {
		e.completed, _ = data["response"].(map[string]any)
	}
	data["type"] = name
	data["sequence_number"] = e.seq
	e.seq++
	b, _ := json.Marshal(data)
	fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", name, b)
	if e.flush != nil {
		e.flush()
	}
}

type toolAcc struct {
	itemID, callID, name string
	args                 strings.Builder
	outputIndex          int
}

// relay reads a Chat Completions SSE stream and writes the equivalent
// Responses stream: a message item for text, a function_call item per tool
// call, and response.completed with usage. It returns an error only when the
// upstream stream breaks; the caller then reports response.failed.
func relay(upstream io.Reader, e *emitter, model string, custom map[string]bool) error {
	respID := "resp_" + rand.Text()[:16]
	e.event("response.created", map[string]any{"response": map[string]any{"id": respID, "object": "response", "status": "in_progress", "model": model, "output": []any{}}})
	e.event("response.in_progress", map[string]any{"response": map[string]any{"id": respID, "object": "response", "status": "in_progress", "model": model, "output": []any{}}})

	var output []map[string]any
	nextIndex := 0
	var msgID string
	var msgIndex int
	var text strings.Builder
	tools := map[int]*toolAcc{}
	var usage map[string]any

	closeMessage := func() {
		if msgID == "" {
			return
		}
		part := map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}
		e.event("response.output_text.done", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "text": text.String()})
		e.event("response.content_part.done", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "part": part})
		item := map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
		e.event("response.output_item.done", map[string]any{"output_index": msgIndex, "item": item})
		output = append(output, item)
		msgID = ""
	}

	sc := bufio.NewScanner(upstream)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var raw strings.Builder // kept (bounded) to explain an empty answer
	finish := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if raw.Len() < 4000 && line != "" {
			raw.WriteString(line + "\n")
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var c chatChunk
		if json.Unmarshal([]byte(payload), &c) != nil {
			continue
		}
		if c.Usage != nil {
			usage = map[string]any{"input_tokens": c.Usage.PromptTokens, "output_tokens": c.Usage.CompletionTokens,
				"total_tokens": c.Usage.PromptTokens + c.Usage.CompletionTokens}
			if c.Usage.Details != nil {
				usage["input_tokens_details"] = map[string]any{"cached_tokens": c.Usage.Details.CachedTokens}
			}
		}
		for _, ch := range c.Choices {
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finish = *ch.FinishReason
			}
			if d := ch.Delta.Content; d != "" {
				if msgID == "" {
					msgID, msgIndex = "msg_"+rand.Text()[:16], nextIndex
					nextIndex++
					text.Reset()
					e.event("response.output_item.added", map[string]any{"output_index": msgIndex,
						"item": map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
					e.event("response.content_part.added", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0,
						"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
				}
				text.WriteString(d)
				e.event("response.output_text.delta", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "delta": d})
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc, ok := tools[tc.Index]
				if !ok {
					closeMessage() // text before a tool call is its own item
					callID := tc.ID
					if callID == "" {
						callID = "call_" + rand.Text()[:16]
					}
					acc = &toolAcc{itemID: "fc_" + rand.Text()[:16], callID: callID, name: tc.Function.Name, outputIndex: nextIndex}
					nextIndex++
					tools[tc.Index] = acc
					added := map[string]any{"id": acc.itemID, "type": "function_call", "status": "in_progress", "call_id": acc.callID, "name": acc.name, "arguments": ""}
					if custom[acc.name] || isEditTool(acc.name) {
						added = map[string]any{"id": acc.itemID, "type": "custom_tool_call", "status": "in_progress", "call_id": acc.callID, "name": customName(acc.name), "input": ""}
					}
					e.event("response.output_item.added", map[string]any{"output_index": acc.outputIndex, "item": added})
				}
				if acc.name == "" && tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if a := tc.Function.Arguments; a != "" {
					acc.args.WriteString(a)
					if !custom[acc.name] && !isEditTool(acc.name) { // custom input is only known once the JSON is complete
						e.event("response.function_call_arguments.delta", map[string]any{"item_id": acc.itemID, "output_index": acc.outputIndex, "delta": a})
					}
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if msgID == "" && len(tools) == 0 && len(output) == 0 {
		log.Printf("ossbridge: local model returned no text and no tool call (finish=%s); upstream sent: %.2000s", finish, raw.String())
		// Say so instead of ending the turn silently, which reads as "done".
		note := "(로컬 모델이 빈 답을 보냈습니다. 다시 시도하거나 유료 모델로 보내 주세요.)"
		if finish == "length" {
			note = "(로컬 모델 답변이 길이 제한에 걸려 비었습니다. 다시 시도하거나 유료 모델로 보내 주세요.)"
		}
		msgID, msgIndex = "msg_"+rand.Text()[:16], nextIndex
		nextIndex++
		text.Reset()
		text.WriteString(note)
		e.event("response.output_item.added", map[string]any{"output_index": msgIndex,
			"item": map[string]any{"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
		e.event("response.output_text.delta", map[string]any{"item_id": msgID, "output_index": msgIndex, "content_index": 0, "delta": note})
	}
	closeMessage()
	idx := make([]int, 0, len(tools))
	for i := range tools {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		acc := tools[i]
		args := acc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		var item map[string]any
		if isEditTool(acc.name) {
			// edit_file/create_file → a patch Codex's apply_patch applies. A bad call
			// still goes out as a patch Codex rejects, so the model sees the error.
			patch, err := patchFor(acc.name, args)
			if err != nil {
				patch = errorPatch(err)
			}
			rememberCall(acc.callID, acc.name, args)
			item = map[string]any{"id": acc.itemID, "type": "custom_tool_call", "status": "completed", "call_id": acc.callID, "name": "apply_patch", "input": patch}
		} else if custom[acc.name] {
			// A freeform tool (apply_patch): Codex expects the raw text back.
			input := args
			var wrapped struct {
				Input *string `json:"input"`
			}
			if json.Unmarshal([]byte(args), &wrapped) == nil && wrapped.Input != nil {
				input = *wrapped.Input
			}
			item = map[string]any{"id": acc.itemID, "type": "custom_tool_call", "status": "completed", "call_id": acc.callID, "name": acc.name, "input": input}
		} else {
			e.event("response.function_call_arguments.done", map[string]any{"item_id": acc.itemID, "output_index": acc.outputIndex, "arguments": args})
			item = map[string]any{"id": acc.itemID, "type": "function_call", "status": "completed", "call_id": acc.callID, "name": acc.name, "arguments": args}
		}
		e.event("response.output_item.done", map[string]any{"output_index": acc.outputIndex, "item": item})
		output = append(output, item)
	}
	sort.SliceStable(output, func(a, b int) bool { return outputIndexOf(output[a], tools) < outputIndexOf(output[b], tools) })
	final := map[string]any{"id": respID, "object": "response", "status": "completed", "model": model, "output": output}
	if usage != nil {
		final["usage"] = usage
	}
	e.event("response.completed", map[string]any{"response": final})
	return nil
}

// outputIndexOf orders the final output like the stream announced it.
func outputIndexOf(item map[string]any, tools map[int]*toolAcc) int {
	if item["type"] == "function_call" || item["type"] == "custom_tool_call" {
		for _, t := range tools {
			if t.itemID == item["id"] {
				return t.outputIndex
			}
		}
	}
	return -1
}

func isEditTool(name string) bool { return name == "edit_file" || name == "create_file" }

func customName(name string) string {
	if isEditTool(name) {
		return "apply_patch"
	}
	return name
}
