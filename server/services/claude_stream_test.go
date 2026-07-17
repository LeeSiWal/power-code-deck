package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fixtures are REAL lines captured from claude 2.1.212 running
// `claude -p --output-format stream-json --verbose` (trimmed of the noisy
// cost/usage fields we don't type). If the CLI changes shape, these are what
// should fail first.
const (
	fixtureInit = `{"type":"system","subtype":"init","cwd":"/tmp/probe","session_id":"c42a5737-9da1-4d9e-b0b3-e95441440fd6","tools":["Task","Bash","Read","Write"],"mcp_servers":[{"name":"pcd","status":"connected"},{"name":"claude.ai Gmail","status":"pending"}],"model":"claude-opus-4-8[1m]","permissionMode":"default","apiKeySource":"none","claude_code_version":"2.1.212","capabilities":["interrupt_receipt_v1","msg_lifecycle_v1"],"uuid":"ac12182e-5f54-4594-a8fc-ec153f6b77fc"}`

	fixtureAssistantText = `{"type":"assistant","message":{"model":"claude-opus-4-8","id":"msg_011Cd7Gne7Nk1ae5GuApBsNo","type":"message","role":"assistant","content":[{"type":"text","text":"PROBE_OK"}],"stop_reason":null},"parent_tool_use_id":null,"session_id":"c42a5737","uuid":"6d421820"}`

	fixtureAssistantToolUse = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_012MYY","name":"Write","input":{"file_path":"/tmp/probe/probe_written.txt","content":"HELLO\n"}}]},"parent_tool_use_id":null,"session_id":"c42a5737"}`

	fixtureToolResultErr = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_012MYY","is_error":true,"content":"PowerCodeDeck user declined (probe)."}]},"session_id":"c42a5737"}`

	fixtureRateLimit = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1784284200,"rateLimitType":"five_hour"},"uuid":"d00d6da6","session_id":"c42a5737"}`

	fixtureResultDenied = `{"type":"result","subtype":"success","is_error":false,"duration_ms":1303,"num_turns":1,"result":"The Write call was declined","session_id":"c42a5737","total_cost_usd":0.087,"permission_denials":[{"tool_name":"Write","tool_use_id":"toolu_012MYY","tool_input":{"file_path":"/tmp/probe/denied.txt","content":"HELLO\n"}}],"terminal_reason":"completed"}`
)

func TestParseInitEvent(t *testing.T) {
	ev, err := ParseStreamEvent([]byte(fixtureInit))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != StreamTypeSystem || ev.Subtype != "init" {
		t.Fatalf("type = %s/%s", ev.Type, ev.Subtype)
	}
	if ev.SessionID != "c42a5737-9da1-4d9e-b0b3-e95441440fd6" {
		t.Fatalf("session id = %q", ev.SessionID)
	}
	if ev.Model != "claude-opus-4-8[1m]" || ev.Version != "2.1.212" {
		t.Fatalf("model=%q version=%q", ev.Model, ev.Version)
	}
	// capabilities is how the CLI says what it can do — feature-detect on this,
	// never on a version string.
	if len(ev.Capabilities) != 2 || ev.Capabilities[0] != "interrupt_receipt_v1" {
		t.Fatalf("capabilities = %v", ev.Capabilities)
	}
	// Our permission bridge shows up here; "connected" is the proof it loaded.
	var pcd *MCPServerInfo
	for i := range ev.MCPServers {
		if ev.MCPServers[i].Name == "pcd" {
			pcd = &ev.MCPServers[i]
		}
	}
	if pcd == nil || pcd.Status != "connected" {
		t.Fatalf("pcd mcp server = %+v", pcd)
	}
}

func TestParseAssistantText(t *testing.T) {
	ev, err := ParseStreamEvent([]byte(fixtureAssistantText))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Message == nil || len(ev.Message.Content) != 1 {
		t.Fatalf("message = %+v", ev.Message)
	}
	b := ev.Message.Content[0]
	if b.Type != "text" || b.Text != "PROBE_OK" {
		t.Fatalf("block = %+v", b)
	}
	// null parent_tool_use_id means the main conversation (not a subagent).
	if ev.ParentToolUseID != nil {
		t.Fatalf("parent = %v, want nil for main conversation", *ev.ParentToolUseID)
	}
}

func TestParseToolUseAndResult(t *testing.T) {
	ev, err := ParseStreamEvent([]byte(fixtureAssistantToolUse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := ev.Message.Content[0]
	if b.Type != "tool_use" || b.Name != "Write" || b.ID != "toolu_012MYY" {
		t.Fatalf("tool_use = %+v", b)
	}
	var input map[string]any
	if err := json.Unmarshal(b.Input, &input); err != nil {
		t.Fatalf("tool input: %v", err)
	}
	if input["file_path"] != "/tmp/probe/probe_written.txt" {
		t.Fatalf("input = %v", input)
	}

	// The result of that call comes back on a `user` event — that's how a native
	// UI learns the tool finished (and whether it failed).
	ev2, err := ParseStreamEvent([]byte(fixtureToolResultErr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rb := ev2.Message.Content[0]
	if rb.Type != "tool_result" || rb.ToolUseID != "toolu_012MYY" || !rb.IsError {
		t.Fatalf("tool_result = %+v", rb)
	}
	if !strings.Contains(string(rb.Content), "declined") {
		t.Fatalf("content = %s", rb.Content)
	}
}

// Events we don't model must not break the stream — the CLI adds fields and event
// types over time, and a strict decoder would turn every update into an outage.
func TestParseUnknownEventIsHarmless(t *testing.T) {
	ev, err := ParseStreamEvent([]byte(fixtureRateLimit))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != StreamTypeRateLimit {
		t.Fatalf("type = %s", ev.Type)
	}
	if len(ev.Raw) == 0 {
		t.Fatal("raw bytes must be kept so callers can reach untyped fields")
	}
}

func TestParseResultWithDenial(t *testing.T) {
	ev, err := ParseStreamEvent([]byte(fixtureResultDenied))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != StreamTypeResult || ev.Subtype != "success" {
		t.Fatalf("type = %s/%s", ev.Type, ev.Subtype)
	}
	// A denied tool still ends in subtype=success — "success" describes the turn,
	// NOT whether the work happened. A UI that keys a green check off this lies.
	if len(ev.PermissionDenial) != 1 || ev.PermissionDenial[0].ToolName != "Write" {
		t.Fatalf("denials = %+v", ev.PermissionDenial)
	}
}

func TestUserInputFrame(t *testing.T) {
	b, err := json.Marshal(NewUserText("hello"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// This exact shape was accepted by the real CLI on stdin.
	want := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`
	if string(b) != want {
		t.Fatalf("frame =\n%s\nwant\n%s", b, want)
	}
}
