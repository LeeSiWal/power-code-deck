package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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
	t.Setenv("PCD_MCP_TOOLS", "")
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

// pipeMCP runs serveMCP on pipes so a test can interleave frames and read
// replies as they arrive. Returns a writer for frames, a reply channel and a
// stop func that closes stdin and waits for serveMCP to return.
func pipeMCP(t *testing.T) (func(string), <-chan map[string]any, func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		serveMCP(inR, outW)
		outW.Close()
		close(done)
	}()
	replies := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				replies <- m
			}
		}
		close(replies)
	}()
	write := func(line string) {
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Errorf("write frame: %v", err)
		}
	}
	stop := func() {
		inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("serveMCP did not return after stdin closed")
		}
	}
	return write, replies, stop
}

// A slow team call must not block an approve call on the same server: the CLI
// may need a permission answer while a member is still working.
func TestMCPSlowTeamCallDoesNotBlockApprove(t *testing.T) {
	release := make(chan struct{})
	team := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Write([]byte(`{"status":"done"}`))
	}))
	defer team.Close()
	defer close(release)
	approve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"behavior":"allow"}`))
	}))
	defer approve.Close()
	t.Setenv("PCD_MCP_TOOLS", "approve,team")
	t.Setenv("PCD_TEAM_URL", team.URL)
	t.Setenv("PCD_APPROVE_URL", approve.URL)

	write, replies, stop := pipeMCP(t)
	write(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask_member","arguments":{"member":"codex","task":"x"}}}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{}}}}`)
	select {
	case m := <-replies:
		if id, _ := m["id"].(float64); id != 2 {
			t.Fatalf("first reply = %v, want the approve (id 2)", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approve blocked behind the slow team call")
	}
	release <- struct{}{}
	select {
	case m := <-replies:
		if id, _ := m["id"].(float64); id != 1 {
			t.Fatalf("second reply = %v, want the team call (id 1)", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("team call never answered after release")
	}
	stop()
}

// notifications/cancelled for the in-flight call must close its HTTP request,
// so the server handler's context ends and TeamService stops the member.
func TestMCPCancelledClosesTeamRequest(t *testing.T) {
	arrived := make(chan struct{})
	closed := make(chan struct{})
	team := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body) // like the real handler; the server watches the conn only after the body
		close(arrived)
		<-r.Context().Done()
		close(closed)
	}))
	defer team.Close()
	t.Setenv("PCD_MCP_TOOLS", "approve,team")
	t.Setenv("PCD_TEAM_URL", team.URL)

	write, replies, stop := pipeMCP(t)
	write(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ask_member","arguments":{"member":"codex","task":"x"}}}`)
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("team request never reached the server")
	}
	write(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5,"reason":"interrupted"}}`)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled call did not close its HTTP request")
	}
	stop()
	// A cancelled request gets no response (MCP: the receiver should not reply).
	for m := range replies {
		t.Fatalf("unexpected reply after cancel: %v", m)
	}
}
