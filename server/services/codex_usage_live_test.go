package services

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Discovery test, not a pass/fail assertion of a known schema. The Codex
// app-server's turn/completed payload is not published, and the driver discarded
// it entirely (handleNotification only read `id` on turn/started), so nobody has
// ever looked. The Local Intelligence savings measurement needs to know whether
// the Codex path can report cloud token usage at all — and "we don't know" is not
// an acceptable input to that decision.
//
// Opt-in because it spends the user's real Codex quota.
//
//	PCD_LIVE_CODEX=1 PCD_LIVE_CODEX_CWD=$PWD go test ./services -run TestCodexTurnCompletedPayload -v -count=1
func TestCodexTurnCompletedPayload(t *testing.T) {
	if os.Getenv("PCD_LIVE_CODEX") != "1" {
		t.Skip("set PCD_LIVE_CODEX=1 to inspect a real Codex turn/completed payload")
	}
	cwd := os.Getenv("PCD_LIVE_CODEX_CWD")
	if cwd == "" {
		cwd = "."
	}

	captured := make(chan json.RawMessage, 4)
	codexTurnObserver = func(turn json.RawMessage) {
		select {
		case captured <- append(json.RawMessage(nil), turn...):
		default:
		}
	}
	t.Cleanup(func() { codexTurnObserver = nil })

	broker := NewPermissionBroker()
	broker.SetAskHandler(func(req PermissionRequest) {
		broker.Resolve(req.ID, PermissionDecision{Behavior: "deny", Message: "usage probe is read-only"})
	})
	d := NewCodexDriver(CodexConfig{SessionID: "usage-probe", Cwd: cwd, Mode: "plan", Broker: broker})
	if err := d.Start(); err != nil {
		t.Fatalf("start real Codex app-server: %v", err)
	}
	defer d.Stop()
	if err := d.Send("Do not use tools. Reply with exactly PCD_CODEX_USAGE_OK"); err != nil {
		t.Fatalf("send: %v", err)
	}

	timer := time.NewTimer(120 * time.Second)
	defer timer.Stop()
	var result *StreamEvent
	for result == nil {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Codex app-server exited before the turn completed")
			}
			if ev.Type == StreamTypeResult {
				result = ev
			}
		case <-timer.C:
			t.Fatal("timed out waiting for turn/completed")
		}
	}

	select {
	case turn := <-captured:
		t.Logf("PCD_CODEX_TURN_PAYLOAD %s", string(turn))
	default:
		t.Log("PCD_CODEX_TURN_PAYLOAD (none captured)")
	}
	if result.Usage == nil {
		t.Log("PCD_CODEX_USAGE_VERDICT: turn/completed reported NO usage — " +
			"the Codex path cannot measure cloud consumption; measure with Claude instead")
		return
	}
	t.Logf("PCD_CODEX_USAGE_VERDICT: usage reported %+v", *result.Usage)
}
