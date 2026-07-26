package services

import (
	"encoding/json"
	"testing"
	"time"
)

// askOnce drives one permission request through the broker's ask handler and reports
// whether the server answered it by policy. A request that gets surfaced to a human
// blocks in Ask until someone answers, so a short done channel is what distinguishes
// "auto-decided" from "waiting on a person" without hanging the test.
func askOnce(t *testing.T, s *NativeService, id, sessionID, tool, input string) (PermissionDecision, bool) {
	t.Helper()
	done := make(chan struct{})
	timer := time.AfterFunc(300*time.Millisecond, func() { close(done) })
	defer timer.Stop()

	d, err := s.broker.Ask(PermissionRequest{
		ID:        id,
		SessionID: sessionID,
		ToolName:  tool,
		Input:     json.RawMessage(input),
	}, done)
	if err != nil {
		return PermissionDecision{}, false // never answered — it went to a human
	}
	return d, true
}

// A mode/model switch goes through restart(), which deletes the session from the map
// BEFORE startSession puts the replacement back. autoDecision looks the session up in
// that same map to read its permission mode, and a miss falls through to "ask a human".
//
// So every permission request that lands inside the restart window escapes the mode
// policy entirely. In 전체 허용 (bypassPermissions) that is doubly bad: the user is not
// watching for approval cards, nobody answers, and the bridge's HTTP call has no
// timeout — so the tool call hangs forever and the session looks frozen.
//
// This pins both reported symptoms: "전체 허용인데 멈춤" and "전환 직후 동작이 이상".
func TestBypassPolicySurvivesRestartWindow(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")

	asked := 0
	s.SetHandlers(nil, func(PermissionRequest) { asked++ })

	s.mu.Lock()
	s.sessions["agent-1"] = &nativeSession{id: "agent-1", mode: BypassMode, cwd: "/tmp/proj"}
	s.mu.Unlock()

	// Baseline: with the session in the map, 전체 허용 answers without a human.
	if d, ok := askOnce(t, s, "req-live", "agent-1", "Bash", `{"command":"rm -rf /tmp/x"}`); !ok || d.Behavior != "allow" {
		t.Fatalf("bypass mode should auto-allow while running: decision=%+v answered=%v", d, ok)
	}
	if asked != 0 {
		t.Fatalf("bypass mode asked a human %d time(s) while running", asked)
	}

	// Reproduce the restart window exactly as restart() creates it: the session is
	// gone from the map while the old driver stops and the new one starts.
	s.mu.Lock()
	delete(s.sessions, "agent-1")
	s.mu.Unlock()

	d, ok := askOnce(t, s, "req-restarting", "agent-1", "Bash", `{"command":"rm -rf /tmp/x"}`)
	if !ok {
		t.Fatal("request during the restart window was never auto-decided — it went to a human and would hang the tool call (전체 허용인데 멈춤)")
	}
	if d.Behavior != "allow" {
		t.Fatalf("decision during the restart window = %q, want allow (session is in 전체 허용)", d.Behavior)
	}
	if asked != 0 {
		t.Fatalf("restart window surfaced %d approval card(s) that 전체 허용 should have swallowed (전환 직후 동작이 이상)", asked)
	}
}

// The same hole swallows "auto" (안전 검사) mode: a safe call that the policy would
// have approved silently turns into an approval card if it lands mid-restart.
func TestAutoPolicySurvivesRestartWindow(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")

	asked := 0
	s.SetHandlers(nil, func(PermissionRequest) { asked++ })

	s.mu.Lock()
	s.sessions["agent-2"] = &nativeSession{id: "agent-2", mode: AutoMode, cwd: "/tmp/proj"}
	s.mu.Unlock()

	// Read is unconditionally safe, so auto mode answers it without a human.
	if d, ok := askOnce(t, s, "auto-live", "agent-2", "Read", `{"file_path":"/tmp/proj/a.txt"}`); !ok || d.Behavior != "allow" {
		t.Fatalf("auto mode should auto-allow a safe call while running: decision=%+v answered=%v", d, ok)
	}

	s.mu.Lock()
	delete(s.sessions, "agent-2")
	s.mu.Unlock()

	if _, ok := askOnce(t, s, "auto-restarting", "agent-2", "Read", `{"file_path":"/tmp/proj/a.txt"}`); !ok {
		t.Fatal("safe call during the restart window was surfaced to a human instead of auto-allowed")
	}
	if asked != 0 {
		t.Fatalf("restart window surfaced %d approval card(s) that auto mode should have swallowed", asked)
	}
}

// A session the server has never heard of must still reach a human — the restart-window
// fix must not turn "unknown session" into a blanket allow. This is the guard that keeps
// the fix from widening the permission gate.
func TestUnknownSessionStillAsksAHuman(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")

	asked := 0
	s.SetHandlers(nil, func(PermissionRequest) { asked++ })

	if _, ok := askOnce(t, s, "ghost", "never-existed", "Bash", `{"command":"rm -rf /"}`); ok {
		t.Fatal("an unknown session was auto-decided — the permission gate would be open to any session id")
	}
	if asked != 1 {
		t.Fatalf("unknown session raised %d approval cards, want exactly 1", asked)
	}
}
