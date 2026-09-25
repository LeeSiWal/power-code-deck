package ossbridge

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"powercodedeck/internal/routing"
)

func TestToChatConvertsHistoryAndTools(t *testing.T) {
	in := responsesRequest{Model: "alias", Instructions: "base rules",
		Input: json.RawMessage(`[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev rules"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"README 읽어줘"}]},
			{"type":"reasoning","encrypted_content":"x"},
			{"type":"function_call","call_id":"c1","name":"exec_command","arguments":"{\"cmd\":\"cat README.md\"}"},
			{"type":"function_call","call_id":"c2","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
			{"type":"function_call_output","call_id":"c1","output":"# Title"},
			{"type":"function_call_output","call_id":"c2","output":[{"type":"input_text","text":"README.md"}]}
		]`),
		Tools: []json.RawMessage{
			json.RawMessage(`{"type":"function","name":"exec_command","description":"run","parameters":{"type":"object"}}`),
			json.RawMessage(`{"type":"namespace","name":"mcp__apps"}`),
			json.RawMessage(`{"type":"web_search"}`),
		}}
	out, _ := toChat(in, "mlx-model")
	if out.Model != "mlx-model" || !out.Stream || len(out.Tools) != 1 || out.Tools[0].Function.Name != "exec_command" {
		t.Fatalf("header/tools: %+v", out)
	}
	roles := []string{}
	for _, m := range out.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,tool" {
		t.Fatalf("roles %v", roles)
	}
	if sys := out.Messages[0].Content.(string); !strings.Contains(sys, "base rules") || !strings.Contains(sys, "dev rules") {
		t.Fatalf("system %q", sys)
	}
	if calls := out.Messages[2].ToolCalls; len(calls) != 2 || calls[1].ID != "c2" {
		t.Fatalf("parallel calls not merged: %+v", calls)
	}
	if out.Messages[4].ToolCallID != "c2" || out.Messages[4].Content != "README.md" {
		t.Fatalf("tool output %+v", out.Messages[4])
	}
}

type sse struct {
	name string
	data map[string]any
}

func readSSE(t *testing.T, r io.Reader) []sse {
	var out []sse
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var name string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var d map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d); err != nil {
				t.Fatalf("bad data: %v", err)
			}
			out = append(out, sse{name, d})
		}
	}
	return out
}

const chatStream = `data: {"choices":[{"delta":{"content":"읽어볼게요."}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"exec_command","arguments":"{\"cmd\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"cat README.md\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":15,"prompt_tokens_details":{"cached_tokens":100}}}

data: [DONE]

`

func TestRelayTextThenToolCall(t *testing.T) {
	var b strings.Builder
	if err := relay(strings.NewReader(chatStream), &emitter{w: &b}, "m", nil); err != nil {
		t.Fatal(err)
	}
	evs := readSSE(t, strings.NewReader(b.String()))
	names := []string{}
	for _, e := range evs {
		names = append(names, e.name)
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"response.created", "response.output_text.delta", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.completed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
	final := evs[len(evs)-1].data["response"].(map[string]any)
	out := final["output"].([]any)
	if len(out) != 2 {
		t.Fatalf("output %v", out)
	}
	msg, call := out[0].(map[string]any), out[1].(map[string]any)
	if msg["type"] != "message" || call["type"] != "function_call" || call["call_id"] != "call_a" || call["arguments"] != `{"cmd":"cat README.md"}` {
		t.Fatalf("items %v %v", msg, call)
	}
	u := final["usage"].(map[string]any)
	if u["input_tokens"].(float64) != 120 || u["output_tokens"].(float64) != 15 {
		t.Fatalf("usage %v", u)
	}
}

func TestBridgeEndToEndWithFakeUpstream(t *testing.T) {
	var got chatRequest
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chatStream)
	}))
	defer up.Close()
	br := &Bridge{Client: routing.NewLocalClient(), Endpoints: func() map[string]routing.LocalEndpoint {
		return map[string]routing.LocalEndpoint{"mac": {ID: "mac", URL: up.URL, Kind: "openai", Model: "qwen-mlx"}}
	}}
	srv := httptest.NewServer(br)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/mac/v1/responses", "application/json", strings.NewReader(`{"model":"alias","stream":true,"input":"hi","tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	evs := readSSE(t, resp.Body)
	if resp.StatusCode != 200 || len(evs) == 0 || evs[len(evs)-1].name != "response.completed" {
		t.Fatalf("status %d events %d", resp.StatusCode, len(evs))
	}
	if got.Model != "qwen-mlx" || len(got.Messages) != 1 || got.Messages[0].Content != "hi" || len(got.Tools) != 1 {
		t.Fatalf("upstream got %+v", got)
	}
	if r, _ := http.Post(srv.URL+"/nope/v1/responses", "application/json", strings.NewReader(`{}`)); r.StatusCode != 404 {
		t.Fatalf("unknown endpoint status %d", r.StatusCode)
	}
}

// apply_patch reaches the model as edit_file/create_file; an edit_file call
// comes back as an apply_patch custom_tool_call, and the history later shows
// the model its own edit_file call again.
func TestEditFileBecomesApplyPatch(t *testing.T) {
	tools := []json.RawMessage{json.RawMessage(`{"type":"custom","name":"apply_patch","description":"Edit files.","format":{"type":"grammar"}}`),
		json.RawMessage(`{"type":"function","name":"read_mcp_resource","parameters":{}}`)}
	out, custom := toChat(responsesRequest{Input: json.RawMessage(`"hi"`), Tools: tools}, "m")
	names := []string{}
	for _, t := range out.Tools {
		names = append(names, t.Function.Name)
	}
	if !custom["apply_patch"] || strings.Join(names, ",") != "edit_file,create_file" {
		t.Fatalf("tools %v", names)
	}
	args := `{"path":"README.md","old_string":"버전: 0.5.0\n","new_string":"버전: 0.6.0\n"}`
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{
		map[string]any{"index": 0, "id": "e1", "function": map[string]any{"name": "edit_file", "arguments": args}}}}}}})
	var b strings.Builder
	if err := relay(strings.NewReader("data: "+string(chunk)+"\n\ndata: [DONE]\n\n"), &emitter{w: &b}, "m", custom); err != nil {
		t.Fatal(err)
	}
	evs := readSSE(t, strings.NewReader(b.String()))
	item := evs[len(evs)-1].data["response"].(map[string]any)["output"].([]any)[0].(map[string]any)
	want := "*** Begin Patch\n*** Update File: README.md\n@@\n-버전: 0.5.0\n+버전: 0.6.0\n*** End Patch"
	if item["type"] != "custom_tool_call" || item["name"] != "apply_patch" || item["input"] != want {
		t.Fatalf("item %v", item)
	}
	hist, _ := toChat(responsesRequest{Input: json.RawMessage(`[{"type":"custom_tool_call","call_id":"e1","name":"apply_patch","input":"x"},{"type":"custom_tool_call_output","call_id":"e1","output":"Success"}]`), Tools: tools}, "m")
	if c := hist.Messages[0].ToolCalls[0]; c.Function.Name != "edit_file" || c.Function.Arguments != args {
		t.Fatalf("history call %+v", c)
	}
}

func TestPatchFor(t *testing.T) {
	p, err := patchFor("create_file", `{"path":"a/b.txt","content":"one\ntwo\n"}`)
	if err != nil || p != "*** Begin Patch\n*** Add File: a/b.txt\n+one\n+two\n*** End Patch" {
		t.Fatalf("create %q %v", p, err)
	}
	if p, err := patchFor("edit_file", `{"path":"x","old_string":"","new_string":"y\n"}`); err != nil || p != "*** Begin Patch\n*** Update File: x\n@@\n+y\n*** End of File\n*** End Patch" {
		t.Fatalf("append %q %v", p, err)
	}
	if _, err := patchFor("edit_file", `{"old_string":"a","new_string":"b"}`); err == nil {
		t.Fatal("missing path accepted")
	}
}
