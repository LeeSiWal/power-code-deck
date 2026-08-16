package services

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in because it uses the user's real Codex login and cloud quota. This is the
// proof test for PowerCodeDeck's driver itself (not merely `codex exec`).
func TestCodexDriverLiveSmoke(t *testing.T) {
	if os.Getenv("PCD_LIVE_CODEX") != "1" {
		t.Skip("set PCD_LIVE_CODEX=1 to run the real Codex app-server smoke test")
	}
	cwd := os.Getenv("PCD_LIVE_CODEX_CWD")
	if cwd == "" {
		cwd = "."
	}
	broker := NewPermissionBroker()
	broker.SetAskHandler(func(req PermissionRequest) {
		broker.Resolve(req.ID, PermissionDecision{Behavior: "deny", Message: "live smoke is read-only"})
	})
	d := NewCodexDriver(CodexConfig{SessionID: "live-smoke", Cwd: cwd, Mode: "plan", Broker: broker})
	if err := d.Start(); err != nil {
		t.Fatalf("start real Codex app-server: %v", err)
	}
	defer d.Stop()
	if err := d.Send("Do not use tools. Reply with exactly PCD_CODEX_DRIVER_OK"); err != nil {
		t.Fatalf("send: %v", err)
	}

	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	seenText, seenResult := false, false
	for !seenResult {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Codex app-server exited before result")
			}
			if ev.Message != nil {
				for _, block := range ev.Message.Content {
					if strings.Contains(block.Text, "PCD_CODEX_DRIVER_OK") {
						seenText = true
					}
				}
			}
			if ev.Type == StreamTypeResult {
				seenResult = true
			}
		case <-timer.C:
			t.Fatal("timed out waiting for real Codex response")
		}
	}
	if !seenText {
		t.Fatal("real Codex response did not contain the smoke marker")
	}
}
