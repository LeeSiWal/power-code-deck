package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// askServer must DENY when it cannot reach the deck. Allowing on error would run
// an unapproved tool because a pipe broke — the failure mode that turns a
// permission system into decoration.
func TestAskServerDeniesWhenUnreachable(t *testing.T) {
	t.Setenv("PCD_APPROVE_URL", "http://127.0.0.1:1/nope") // nothing listens on port 1
	got := askServer(approveRequest{ToolName: "Write"})
	if got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny when the deck is unreachable", got.Behavior)
	}
	if got.Message == "" {
		t.Fatal("deny must carry a message — Claude reads it and adapts")
	}
}

func TestAskServerDeniesWithoutConfig(t *testing.T) {
	os.Unsetenv("PCD_APPROVE_URL")
	if got := askServer(approveRequest{ToolName: "Bash"}); got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny with no bridge configured", got.Behavior)
	}
}

// A server answer that isn't allow/deny must not be trusted through.
func TestAskServerRejectsUnknownBehavior(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"behavior":"maybe"}`))
	}))
	defer srv.Close()
	t.Setenv("PCD_APPROVE_URL", srv.URL)
	if got := askServer(approveRequest{ToolName: "Bash"}); got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny for an unknown decision", got.Behavior)
	}
}

// The happy path: the deck's allow (with edited input) is passed through intact.
func TestAskServerPassesAllowThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PCD-Approve-Token") != "secret-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var req approveRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.ToolName != "Write" || req.SessionID != "s1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"behavior":"allow","updatedInput":{"file_path":"/tmp/edited"}}`))
	}))
	defer srv.Close()
	t.Setenv("PCD_APPROVE_URL", srv.URL)
	t.Setenv("PCD_APPROVE_TOKEN", "secret-1")

	got := askServer(approveRequest{SessionID: "s1", ToolName: "Write", Input: json.RawMessage(`{"file_path":"/tmp/x"}`)})
	if got.Behavior != "allow" {
		t.Fatalf("behavior = %q", got.Behavior)
	}
	// approve-with-changes: the user's edit must survive to the CLI.
	if string(got.UpdatedInput) != `{"file_path":"/tmp/edited"}` {
		t.Fatalf("updatedInput = %s", got.UpdatedInput)
	}
}

// The deck refusing the ask (bad token) must deny, not allow.
func TestAskServerDeniesOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token", http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("PCD_APPROVE_URL", srv.URL)
	if got := askServer(approveRequest{ToolName: "Bash"}); got.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny on HTTP error", got.Behavior)
	}
}

// runMCP feeds JSON-RPC lines to the stdio loop and returns the decoded replies.
func runMCP(t *testing.T, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	serveMCP(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out)
	var replies []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad reply %q: %v", l, err)
		}
		replies = append(replies, m)
	}
	return replies
}

func toolNames(t *testing.T) ([]string, int) {
	t.Helper()
	var out bytes.Buffer
	serveMCP(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("tools/list reply %q: %v", out.String(), err)
	}
	var names []string
	for _, tool := range resp.Result.Tools {
		if n := len([]rune(tool.Description)); n > 300 {
			t.Errorf("%s description is %d chars, want ≤300", tool.Name, n)
		}
		names = append(names, tool.Name)
	}
	return names, len(bytes.TrimSpace(out.Bytes()))
}

func TestMCPToolsListByEnv(t *testing.T) {
	os.Unsetenv("PCD_MCP_TOOLS")
	if got, _ := toolNames(t); strings.Join(got, ",") != "approve" {
		t.Fatalf("unset: %v", got)
	}
	t.Setenv("PCD_MCP_TOOLS", "approve,team")
	got, size := toolNames(t)
	if strings.Join(got, ",") != "approve,ask_member,handoff_to_agy" {
		t.Fatalf("approve,team: %v", got)
	}
	t.Logf("tools/list size for approve,team: %d bytes", size)
	t.Setenv("PCD_MCP_TOOLS", "team")
	if got, _ := toolNames(t); strings.Join(got, ",") != "ask_member,handoff_to_agy" {
		t.Fatalf("team: %v", got)
	}
	// A tool that is not enabled cannot be called either.
	replies := runMCP(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{}}}}`)
	if len(replies) != 1 || replies[0]["error"] == nil {
		t.Fatalf("approve with team only: %v", replies)
	}
}

func TestMCPAskMemberForwards(t *testing.T) {
	var got struct {
		SessionID string          `json:"sessionId"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	var token string
	body := `{"status":"done","member":"codex","text":"요약"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("X-PCD-Approve-Token")
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	t.Setenv("PCD_MCP_TOOLS", "approve,team")
	t.Setenv("PCD_TEAM_URL", srv.URL)
	t.Setenv("PCD_APPROVE_TOKEN", "tok-1")
	t.Setenv("PCD_APPROVE_SESSION", "s1")

	replies := runMCP(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"ask_member","arguments":{"member":"codex","task":"읽어줘","files":["a.go"]}}}`)
	if token != "tok-1" || got.SessionID != "s1" || got.Tool != "ask_member" {
		t.Fatalf("forwarded token=%q req=%+v", token, got)
	}
	var args struct {
		Member string   `json:"member"`
		Task   string   `json:"task"`
		Files  []string `json:"files"`
	}
	json.Unmarshal(got.Arguments, &args)
	if args.Member != "codex" || args.Task != "읽어줘" || len(args.Files) != 1 {
		t.Fatalf("arguments = %s", got.Arguments)
	}
	if len(replies) != 1 {
		t.Fatalf("replies = %v", replies)
	}
	content := replies[0]["result"].(map[string]any)["content"].([]any)
	if text := content[0].(map[string]any)["text"]; text != body {
		t.Fatalf("text = %v, want server body", text)
	}
}

// An unreachable deck must produce an error result, never hang or pretend.
func TestMCPAskMemberUnreachable(t *testing.T) {
	t.Setenv("PCD_MCP_TOOLS", "approve,team")
	t.Setenv("PCD_TEAM_URL", "http://127.0.0.1:1/nope")
	replies := runMCP(t, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"handoff_to_agy","arguments":{"task":"x"}}}`)
	res := replies[0]["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("result = %v, want isError", res)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var r struct{ Status, Reason string }
	if err := json.Unmarshal([]byte(text), &r); err != nil || r.Status != "failed" || r.Reason == "" {
		t.Fatalf("text = %q", text)
	}
}
