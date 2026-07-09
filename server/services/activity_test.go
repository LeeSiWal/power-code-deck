package services

import (
	"testing"
	"time"
)

// newTestWatcher builds a watcher without touching the filesystem or goroutines.
func newTestWatcher() *transcriptWatcher {
	return &transcriptWatcher{
		agentID: "test",
		nodes:   make(map[string]*activityNode),
		tools:   make(map[string]*toolRun),
		subByID: make(map[string]string),
	}
}

func TestEncodeProjectPath(t *testing.T) {
	cases := map[string]string{
		"/home/siwal/code/power-code-deck": "-home-siwal-code-power-code-deck",
		"/home/u/code/agentdeck-go":        "-home-u-code-agentdeck-go",
		"/tmp/newton.e2e_GIM7":             "-tmp-newton-e2e-GIM7",
	}
	for in, want := range cases {
		if got := encodeProjectPath(in); got != want {
			t.Errorf("encodeProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMainToolDetection(t *testing.T) {
	w := newTestWatcher()
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-07-09T10:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`)

	main := w.nodes[mainNodeID]
	if main == nil {
		t.Fatal("main node not created")
	}
	if main.currentToolID != "t1" {
		t.Fatalf("main.currentToolID = %q, want t1", main.currentToolID)
	}
	now := parseTimestamp("2026-07-09T10:00:00Z")
	if s := w.status(main, now); s != "working" {
		t.Fatalf("status = %q, want working", s)
	}
	if tr := w.tools["t1"]; tr == nil || tr.tool != "Bash" || tr.target != "go test ./..." {
		t.Fatalf("tool run not recorded correctly: %+v", tr)
	}

	// tool_result completes it → main becomes thinking (recent activity, no in-flight tool)
	w.processLine(`{"type":"user","isSidechain":false,"timestamp":"2026-07-09T10:00:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`)
	if main.currentToolID != "" {
		t.Fatalf("main.currentToolID = %q, want empty after result", main.currentToolID)
	}
	if tr := w.tools["t1"]; tr == nil || tr.endedAt == 0 {
		t.Fatalf("tool run not marked ended: %+v", tr)
	}
}

func TestSubAgentAndSidechain(t *testing.T) {
	w := newTestWatcher()
	// Main spawns a sub-agent via the Agent tool.
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-07-09T10:00:00Z","message":{"content":[{"type":"tool_use","id":"a1","name":"Agent","input":{"subagent_type":"Explore","description":"find files"}}]}}`)

	subID := w.subByID["a1"]
	if subID == "" {
		t.Fatal("sub-agent node not registered for a1")
	}
	sub := w.nodes[subID]
	if sub == nil || sub.kind != "subagent" || sub.label != "Explore" {
		t.Fatalf("sub-agent node wrong: %+v", sub)
	}
	if w.currentOpenSub() != subID {
		t.Fatalf("currentOpenSub = %q, want %q", w.currentOpenSub(), subID)
	}
	// Main should be busy running the Agent tool.
	if w.nodes[mainNodeID].currentToolID != "a1" {
		t.Fatal("main not marked running the Agent tool")
	}

	// A sidechain tool_use belongs to the running sub-agent, not main.
	w.processLine(`{"type":"assistant","isSidechain":true,"timestamp":"2026-07-09T10:00:02Z","message":{"content":[{"type":"tool_use","id":"s1","name":"Grep","input":{"pattern":"func main"}}]}}`)
	if tr := w.tools["s1"]; tr == nil || tr.node != subID || !tr.sidechain {
		t.Fatalf("sidechain tool not attributed to sub-agent: %+v", tr)
	}
	if sub.currentToolID != "s1" {
		t.Fatalf("sub.currentToolID = %q, want s1", sub.currentToolID)
	}

	// Sub-agent's tool completes.
	w.processLine(`{"type":"user","isSidechain":true,"timestamp":"2026-07-09T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"s1"}]}}`)
	if sub.currentToolID != "" {
		t.Fatal("sub tool should be cleared after its result")
	}

	// The Agent tool result closes the sub-agent node.
	w.processLine(`{"type":"user","isSidechain":false,"timestamp":"2026-07-09T10:00:06Z","message":{"content":[{"type":"tool_result","tool_use_id":"a1"}]}}`)
	if !sub.done {
		t.Fatal("sub-agent should be marked done after Agent tool_result")
	}
	if w.currentOpenSub() != "" {
		t.Fatal("no open sub-agents should remain")
	}
	now := parseTimestamp("2026-07-09T10:00:06Z")
	if s := w.status(sub, now); s != "done" {
		t.Fatalf("sub status = %q, want done", s)
	}
}

func TestPlainStringContentIgnored(t *testing.T) {
	w := newTestWatcher()
	// Ordinary user text has content as a string, not a block array — must not panic.
	w.processLine(`{"type":"user","timestamp":"2026-07-09T10:00:00Z","message":{"content":"hello there"}}`)
	if len(w.nodes) != 0 {
		t.Fatalf("plain string content should create no nodes, got %d", len(w.nodes))
	}
}

func TestStatusIdleTransition(t *testing.T) {
	w := newTestWatcher()
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-07-09T10:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/a/b/main.go"}}]}}`)
	w.processLine(`{"type":"user","isSidechain":false,"timestamp":"2026-07-09T10:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`)
	main := w.nodes[mainNodeID]
	base := parseTimestamp("2026-07-09T10:00:01Z")

	if s := w.status(main, base+int64(5*time.Second/time.Millisecond)); s != "thinking" {
		t.Fatalf("status after 5s = %q, want thinking", s)
	}
	if s := w.status(main, base+int64(30*time.Second/time.Millisecond)); s != "idle" {
		t.Fatalf("status after 30s = %q, want idle", s)
	}
	// Read target should be the basename.
	if tr := w.tools["t1"]; tr == nil || tr.target != "main.go" {
		t.Fatalf("Read target = %+v, want main.go", tr)
	}
}
