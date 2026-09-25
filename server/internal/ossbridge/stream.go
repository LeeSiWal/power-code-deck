package ossbridge

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
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

// chatResult is one complete local-model answer.
type chatResult struct {
	text   string
	calls  []chatCall
	usage  map[string]any
	finish string
	raw    string // bounded copy of the upstream stream, to explain an empty answer
}

type chatCall struct{ id, name, args string }

// collect reads a whole Chat Completions stream. The bridge answers Codex only
// once the local answer is complete: local turns take seconds, and a complete
// answer can be checked (text-written tool calls, an announced-but-not-made
// call) before Codex acts on it.
func collect(upstream io.Reader) (chatResult, error) {
	var res chatResult
	var text, raw strings.Builder
	type acc struct {
		id, name string
		args     strings.Builder
	}
	byIndex := map[int]*acc{}
	var order []int
	sc := bufio.NewScanner(upstream)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
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
			res.usage = map[string]any{"input_tokens": c.Usage.PromptTokens, "output_tokens": c.Usage.CompletionTokens,
				"total_tokens": c.Usage.PromptTokens + c.Usage.CompletionTokens}
			if c.Usage.Details != nil {
				res.usage["input_tokens_details"] = map[string]any{"cached_tokens": c.Usage.Details.CachedTokens}
			}
		}
		for _, ch := range c.Choices {
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				res.finish = *ch.FinishReason
			}
			text.WriteString(ch.Delta.Content)
			for _, tc := range ch.Delta.ToolCalls {
				a, ok := byIndex[tc.Index]
				if !ok {
					a = &acc{}
					byIndex[tc.Index] = a
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	res.text, res.raw = text.String(), raw.String()
	for _, i := range order {
		a := byIndex[i]
		res.calls = append(res.calls, chatCall{id: a.id, name: a.name, args: a.args.String()})
	}
	return res, nil
}

// recoverCalls turns tool calls the model wrote as text into real calls.
func (r *chatResult) recoverCalls(offered map[string]bool) {
	if len(r.calls) > 0 {
		return
	}
	for _, c := range recoverToolCalls(r.text, offered) {
		r.calls = append(r.calls, chatCall{name: c.name, args: c.args})
	}
	if len(r.calls) > 0 {
		log.Printf("ossbridge: recovered %d tool call(s) the model wrote as text", len(r.calls))
	}
}

// promiseRe matches an answer that ends by announcing a next step instead of
// taking it (a "~겠습니다/~겠어요/~게요" ending, or a present "~합니다" — a finished
// answer says "~했습니다/~되었습니다" — e.g. "다시 시도하겠습니다.", "바꾸겠습니다",
// "추가해 드릴게요"; "Let me …", "I'll …").
var promiseRe = regexp.MustCompile(`(?i)(겠습니다|겠어요|게요|[가-힣]합니다|let me\b|i'?ll\b|i will\b)[^\n]{0,40}\s*$`)

// announcedOnly reports an answer that promises an action but calls no tool —
// a small model's common way of ending a turn with nothing done (measured: 2
// of 3 runs of "테스트 파일에 123좀 붙여줄래?" ended on "다시 시도하겠습니다").
func (r chatResult) announcedOnly() bool {
	return len(r.calls) == 0 && promiseRe.MatchString(strings.TrimSpace(r.text))
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

// emit writes one complete answer as a Responses stream: a message item for
// the text, then one item per tool call, then response.completed.
func emit(e *emitter, model string, info turnInfo, res chatResult) {
	respID := "resp_" + rand.Text()[:16]
	e.event("response.created", map[string]any{"response": map[string]any{"id": respID, "object": "response", "status": "in_progress", "model": model, "output": []any{}}})
	e.event("response.in_progress", map[string]any{"response": map[string]any{"id": respID, "object": "response", "status": "in_progress", "model": model, "output": []any{}}})
	var output []any
	idx := 0
	text := res.text
	if strings.TrimSpace(text) == "" && len(res.calls) == 0 {
		log.Printf("ossbridge: local model returned no text and no tool call (finish=%s); upstream sent: %.2000s", res.finish, res.raw)
		// Say so instead of ending the turn silently, which reads as "done".
		text = "(로컬 모델이 빈 답을 보냈습니다. 다시 시도하거나 유료 모델로 보내 주세요.)"
		if res.finish == "length" {
			text = "(로컬 모델 답변이 길이 제한에 걸려 비었습니다. 다시 시도하거나 유료 모델로 보내 주세요.)"
		}
	}
	if strings.TrimSpace(text) != "" {
		id := "msg_" + rand.Text()[:16]
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		e.event("response.output_item.added", map[string]any{"output_index": idx, "item": map[string]any{"id": id, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
		e.event("response.content_part.added", map[string]any{"item_id": id, "output_index": idx, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		e.event("response.output_text.delta", map[string]any{"item_id": id, "output_index": idx, "content_index": 0, "delta": text})
		e.event("response.output_text.done", map[string]any{"item_id": id, "output_index": idx, "content_index": 0, "text": text})
		e.event("response.content_part.done", map[string]any{"item_id": id, "output_index": idx, "content_index": 0, "part": part})
		item := map[string]any{"id": id, "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
		e.event("response.output_item.done", map[string]any{"output_index": idx, "item": item})
		output = append(output, item)
		idx++
	}
	for _, c := range res.calls {
		itemID, callID := "fc_"+rand.Text()[:16], c.id
		if callID == "" {
			callID = "call_" + rand.Text()[:16]
		}
		args := c.args
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		var item map[string]any
		switch {
		case isEditTool(c.name):
			// edit_file/create_file → a patch Codex's apply_patch applies. A bad call
			// still goes out as a patch Codex rejects, so the model sees the error.
			patch, err := patchFor(c.name, args, info.cwd)
			if err != nil {
				patch = errorPatch(err)
			}
			rememberCall(callID, c.name, args)
			item = map[string]any{"id": itemID, "type": "custom_tool_call", "status": "completed", "call_id": callID, "name": "apply_patch", "input": patch}
		case info.custom[c.name]:
			// Another freeform tool: Codex expects the raw text back.
			input := args
			var wrapped struct {
				Input *string `json:"input"`
			}
			if json.Unmarshal([]byte(args), &wrapped) == nil && wrapped.Input != nil {
				input = *wrapped.Input
			}
			item = map[string]any{"id": itemID, "type": "custom_tool_call", "status": "completed", "call_id": callID, "name": c.name, "input": input}
		default:
			item = map[string]any{"id": itemID, "type": "function_call", "status": "completed", "call_id": callID, "name": c.name, "arguments": args}
		}
		added := map[string]any{}
		for k, v := range item {
			added[k] = v
		}
		added["status"] = "in_progress"
		e.event("response.output_item.added", map[string]any{"output_index": idx, "item": added})
		if item["type"] == "function_call" {
			e.event("response.function_call_arguments.delta", map[string]any{"item_id": itemID, "output_index": idx, "delta": args})
			e.event("response.function_call_arguments.done", map[string]any{"item_id": itemID, "output_index": idx, "arguments": args})
		}
		e.event("response.output_item.done", map[string]any{"output_index": idx, "item": item})
		output = append(output, item)
		idx++
	}
	final := map[string]any{"id": respID, "object": "response", "status": "completed", "model": model, "output": output}
	if res.usage != nil {
		final["usage"] = res.usage
	}
	e.event("response.completed", map[string]any{"response": final})
}

// relay converts one upstream answer (collect → recover → emit); the bridge
// adds the announced-only retry between collect and emit.
func relay(upstream io.Reader, e *emitter, model string, info turnInfo) error {
	res, err := collect(upstream)
	if err != nil {
		return err
	}
	res.recoverCalls(info.offered)
	emit(e, model, info, res)
	return nil
}

func isEditTool(name string) bool { return name == "edit_file" || name == "create_file" }

type textCall struct{ name, args string }

var (
	fencedCallRe = regexp.MustCompile("(?s)```[A-Za-z]*\\s*\\n\\s*([A-Za-z_][A-Za-z0-9_]*)\\s*\\n\\s*(\\{.*?\\})\\s*```")
	tagCallRe    = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)
)

// recoverToolCalls finds tool calls written as text, for offered tools only,
// with arguments that are a JSON object.
func recoverToolCalls(text string, offered map[string]bool) []textCall {
	var out []textCall
	for _, m := range fencedCallRe.FindAllStringSubmatch(text, 8) {
		if offered[m[1]] && json.Valid([]byte(m[2])) {
			out = append(out, textCall{m[1], m[2]})
		}
	}
	for _, m := range tagCallRe.FindAllStringSubmatch(text, 8) {
		var c struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal([]byte(m[1]), &c) != nil || !offered[c.Name] {
			continue
		}
		args := strings.TrimSpace(string(c.Arguments))
		var asString string
		if json.Unmarshal(c.Arguments, &asString) == nil {
			args = asString // arguments given as a JSON string
		}
		if json.Valid([]byte(args)) && strings.HasPrefix(args, "{") {
			out = append(out, textCall{c.Name, args})
		}
	}
	return out
}
