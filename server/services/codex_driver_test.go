package services

import (
	"bytes"
	"encoding/json"
	"io"
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
