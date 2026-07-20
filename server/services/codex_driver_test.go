package services

import (
	"encoding/json"
	"testing"
)

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
