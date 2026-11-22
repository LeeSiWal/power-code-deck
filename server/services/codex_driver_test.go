package services

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testWriteCloser struct{ io.Writer }

func (testWriteCloser) Close() error { return nil }

func TestCodexThreadParamsMapNativeModes(t *testing.T) {
	tests := []struct {
		mode, approval, sandbox string
	}{
		{"", "on-request", "workspace-write"},
		{"acceptEdits", "on-request", "workspace-write"},
		{"plan", "never", "read-only"},
		{"bypassPermissions", "never", "danger-full-access"},
	}
	for _, tt := range tests {
		d := NewCodexDriver(CodexConfig{Cwd: "/work", Mode: tt.mode, ResumeID: "thread-1"})
		p := d.threadParams(true)
		if p["approvalPolicy"] != tt.approval || p["sandbox"] != tt.sandbox {
			t.Fatalf("mode %q: got approval=%v sandbox=%v", tt.mode, p["approvalPolicy"], p["sandbox"])
		}
		if p["threadId"] != "thread-1" {
			t.Fatalf("resume id missing for mode %q", tt.mode)
		}
	}
}

func TestCodexCallTimesOutAndRemovesPendingRequest(t *testing.T) {
	var writes bytes.Buffer
	d := NewCodexDriver(CodexConfig{})
	d.stdin = testWriteCloser{Writer: &writes}
	d.rpcTimeout = 15 * time.Millisecond

	_, err := d.call("initialize", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected observable timeout, got %v", err)
	}
	if len(d.pending) != 0 {
		t.Fatalf("timed-out request leaked from pending map: %d", len(d.pending))
	}
	if !strings.Contains(writes.String(), `"method":"initialize"`) {
		t.Fatalf("request was not written before timeout: %q", writes.String())
	}
}

func TestCodexItemsNormalizeForNativeChat(t *testing.T) {
	d := NewCodexDriver(CodexConfig{})
	d.emitItem(json.RawMessage(`{
		"id":"cmd-1","type":"commandExecution","command":"go test ./...",
		"cwd":"/work","status":"inProgress","commandActions":[]
	}`), false)
	started := <-d.Events()
	if started.Type != StreamTypeAssistant || started.Message == nil ||
		len(started.Message.Content) != 1 || started.Message.Content[0].Name != "Bash" {
		t.Fatalf("unexpected command start event: %#v", started)
	}

	d.emitItem(json.RawMessage(`{
		"id":"cmd-1","type":"commandExecution","command":"go test ./...",
		"cwd":"/work","status":"completed","aggregatedOutput":"ok","commandActions":[]
	}`), true)
	completed := <-d.Events()
	if completed.Type != StreamTypeUser || completed.Message == nil ||
		len(completed.Message.Content) != 1 ||
		completed.Message.Content[0].ToolUseID != "cmd-1" {
		t.Fatalf("unexpected command completion event: %#v", completed)
	}
}

// fakeCodex points the driver at a shell script that records its argv and env,
// answers initialize + thread/start, then idles until stdin closes. Nothing real
// is spawned, so no Codex quota is used.
func fakeCodex(t *testing.T) (argvFile, envFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile, envFile = dir+"/argv", dir+"/env"
	script := dir + "/codex"
	body := `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done > "` + argvFile + `"
env > "` + envFile + `"
read -r l1
echo '{"jsonrpc":"2.0","id":1,"result":{}}'
read -r l2
read -r l3
echo '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"thr-1","turns":[]},"model":"gpt-test"}}'
cat >/dev/null
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := codexBinary
	codexBinary = func() string { return script }
	t.Cleanup(func() { codexBinary = prev })
	return argvFile, envFile
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func codexInitEvent(t *testing.T, d *CodexDriver) *StreamEvent {
	t.Helper()
	select {
	case ev := <-d.Events():
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no init event")
	}
	return nil
}

func TestCodexStartAddsPcdMCP(t *testing.T) {
	argvFile, envFile := fakeCodex(t)
	d := NewCodexDriver(CodexConfig{
		SessionID: "agent-7", Cwd: t.TempDir(),
		SelfPath: "/opt/pcd", TeamURL: "http://127.0.0.1:33033/internal/native/team", TeamToken: "secret-tok-xyz",
		ToolTimeoutSec: 240,
	})
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer d.Stop()
	argv := readLines(t, argvFile)
	want := []string{
		"-c", `mcp_servers.pcd.command="/opt/pcd"`,
		"-c", `mcp_servers.pcd.args=["mcp-approve"]`,
		"-c", `mcp_servers.pcd.env_vars=["PCD_MCP_TOOLS","PCD_TEAM_URL","PCD_APPROVE_TOKEN","PCD_APPROVE_SESSION"]`,
		"-c", `shell_environment_policy.exclude=["PCD_APPROVE_TOKEN"]`,
		"-c", "mcp_servers.pcd.tool_timeout_sec=240",
		"app-server", "--stdio",
	}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q\nwant %q", argv, want)
	}
	for _, a := range argv {
		if strings.Contains(a, "secret-tok-xyz") {
			t.Fatalf("token in argv: %q", a)
		}
	}
	env := "\n" + strings.Join(readLines(t, envFile), "\n") + "\n"
	for _, kv := range []string{
		"PCD_APPROVE_TOKEN=secret-tok-xyz", "PCD_APPROVE_SESSION=agent-7",
		"PCD_MCP_TOOLS=team", "PCD_TEAM_URL=http://127.0.0.1:33033/internal/native/team",
	} {
		if !strings.Contains(env, "\n"+kv+"\n") {
			t.Fatalf("process env lacks %s", kv)
		}
	}
}

func TestCodexStartWithoutTeamConfigUnchanged(t *testing.T) {
	argvFile, envFile := fakeCodex(t)
	d := NewCodexDriver(CodexConfig{SessionID: "agent-8", Cwd: t.TempDir()})
	if err := d.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	d.Stop()
	if argv := readLines(t, argvFile); strings.Join(argv, " ") != "app-server --stdio" {
		t.Fatalf("argv = %q", argv)
	}
	if env := strings.Join(readLines(t, envFile), "\n"); strings.Contains(env, "PCD_MCP_TOOLS") || strings.Contains(env, "PCD_APPROVE_TOKEN") {
		t.Fatal("plain codex got team env")
	}

	// A partial config (e.g. no token) must not half-configure the server.
	argvFile, _ = fakeCodex(t)
	d2 := NewCodexDriver(CodexConfig{SessionID: "agent-9", Cwd: t.TempDir(), SelfPath: "/opt/pcd", TeamURL: "u"})
	if err := d2.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	d2.Stop()
	if argv := readLines(t, argvFile); strings.Join(argv, " ") != "app-server --stdio" {
		t.Fatalf("partial config argv = %q", argv)
	}

	// oss models keep their bridge args after the pcd ones (buildArgs writes the
	// one-model catalog under the user cache dir, so point that at a temp dir).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d3 := NewCodexDriver(CodexConfig{SelfPath: "/opt/pcd", TeamURL: "u", TeamToken: "t", Model: "oss:http://127.0.0.1:1/v1:qwen"})
	args, err := d3.buildArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, `-c mcp_servers.pcd.command="/opt/pcd"`) || !strings.HasSuffix(joined, "app-server --stdio") {
		t.Fatalf("oss+team args = %q", joined)
	}
	if !strings.Contains(joined, `-c shell_environment_policy.exclude=["PCD_APPROVE_TOKEN"]`) {
		t.Fatalf("team args lack the token shell exclude: %q", joined)
	}
	plainOSS := NewCodexDriver(CodexConfig{Model: "oss:http://127.0.0.1:1/v1:qwen"})
	pargs, err := plainOSS.buildArgs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(pargs, " "), "shell_environment_policy") {
		t.Fatalf("plain oss args got the shell exclude: %q", pargs)
	}
	if strings.Contains(strings.Join(pargs, " "), "mcp_servers.pcd") || !strings.HasSuffix(strings.Join(pargs, " "), "app-server --stdio") {
		t.Fatalf("plain oss args = %q", pargs)
	}
}

// Codex 0.164.0 asks before an MCP tool call through mcpServer/elicitation/request
// with _meta.codex_approval_kind = "mcp_tool_call". Our own team tools are accepted
// without a card; the broker is never consulted.
func TestCodexAutoAcceptsTeamMCPApproval(t *testing.T) {
	var writes bytes.Buffer
	broker := NewPermissionBroker()
	asked := make(chan PermissionRequest, 4)
	broker.SetAskHandler(func(req PermissionRequest) {
		asked <- req
		broker.Resolve(req.ID, PermissionDecision{Behavior: "deny"})
	})
	d := NewCodexDriver(CodexConfig{SessionID: "agent-7", Broker: broker, SelfPath: "/opt/pcd", TeamURL: "u", TeamToken: "t"})
	d.stdin = testWriteCloser{Writer: &writes}

	for i, tool := range []string{"ask_member", "handoff_to_agy"} {
		writes.Reset()
		params := `{"threadId":"thr-1","turnId":"turn-1","serverName":"pcd","mode":"form",
			"message":"Allow pcd to run tool \"` + tool + `\"?","requestedSchema":{"type":"object","properties":{}},
			"_meta":{"codex_approval_kind":"mcp_tool_call","tool_params":{"member":"codex"}}}`
		d.handleRequest(codexRPCMessage{ID: json.RawMessage(strconv.Itoa(40 + i)), Method: "mcpServer/elicitation/request", Params: json.RawMessage(params)})
		var resp struct {
			ID     int `json:"id"`
			Result struct {
				Action string `json:"action"`
			} `json:"result"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(writes.Bytes()), &resp); err != nil {
			t.Fatalf("%s: bad response %q: %v", tool, writes.String(), err)
		}
		if resp.ID != 40+i || resp.Result.Action != "accept" {
			t.Fatalf("%s: response = %s", tool, writes.String())
		}
	}
	select {
	case req := <-asked:
		t.Fatalf("broker was asked: %+v", req)
	default:
	}

	// Another server's tool, or another pcd tool, is not auto-accepted.
	writes.Reset()
	d.handleRequest(codexRPCMessage{ID: json.RawMessage("50"), Method: "mcpServer/elicitation/request", Params: json.RawMessage(
		`{"threadId":"thr-1","serverName":"other","mode":"form","message":"Allow other to run tool \"ask_member\"?",
		"requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call"}}`)})
	if strings.Contains(writes.String(), `"accept"`) {
		t.Fatalf("non-pcd MCP call auto-accepted: %s", writes.String())
	}
	writes.Reset()
	d.handleRequest(codexRPCMessage{ID: json.RawMessage("51"), Method: "mcpServer/elicitation/request", Params: json.RawMessage(
		`{"threadId":"thr-1","serverName":"pcd","mode":"form","message":"Allow pcd to run tool \"approve\"?",
		"requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call"}}`)})
	if strings.Contains(writes.String(), `"accept"`) {
		t.Fatalf("pcd/approve auto-accepted: %s", writes.String())
	}
	// The tool name is read only from a whole prompt of the known shape, not
	// from a team tool name quoted somewhere inside a longer message.
	writes.Reset()
	d.handleRequest(codexRPCMessage{ID: json.RawMessage("52"), Method: "mcpServer/elicitation/request", Params: json.RawMessage(
		`{"threadId":"thr-1","serverName":"pcd","mode":"form","message":"pcd wants something else; asked to run tool \"ask_member\" earlier. Allow?",
		"requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call"}}`)})
	if strings.Contains(writes.String(), `"accept"`) {
		t.Fatalf("unanchored tool name auto-accepted: %s", writes.String())
	}
}

func TestCodexInitReportsRealMCPStatus(t *testing.T) {
	mcpNames := func(d *CodexDriver) []string {
		d.emitSystem("gpt-test")
		ev := <-d.Events()
		var names []string
		for _, s := range ev.MCPServers {
			names = append(names, s.Name+":"+s.Status)
		}
		return names
	}
	if got := mcpNames(NewCodexDriver(CodexConfig{})); len(got) != 0 {
		t.Fatalf("unconfigured codex reported mcp servers %v", got)
	}
	got := mcpNames(NewCodexDriver(CodexConfig{SelfPath: "/opt/pcd", TeamURL: "u", TeamToken: "t"}))
	if len(got) != 1 || got[0] != "pcd:connected" {
		t.Fatalf("configured codex mcp servers = %v", got)
	}

	// End to end through Start with the fake binary.
	fakeCodex(t)
	d := NewCodexDriver(CodexConfig{SessionID: "a", Cwd: t.TempDir(), SelfPath: "/opt/pcd", TeamURL: "u", TeamToken: "t"})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if ev := codexInitEvent(t, d); len(ev.MCPServers) != 1 || ev.MCPServers[0].Name != "pcd" {
		t.Fatalf("init event mcp = %+v", ev.MCPServers)
	}
}
