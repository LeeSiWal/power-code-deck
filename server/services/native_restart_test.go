package services

import (
	"testing"
	"time"
)

// A model/mode switch (restart) replaces a session's driver: the new driver gets a
// freshly Issued approve token BEFORE the old driver's pump has drained. The old
// pump's cleanup used to Revoke unconditionally, which raced the Issue and deleted
// the NEW token — after which every bridge ask for the session got 403 and every
// gated tool (git commit included) was refused. This pins the fix: a superseded
// pump must not touch the token store or the broker.
func TestOldPumpDoesNotRevokeReplacementToken(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")

	// The session being replaced. Its driver never starts — only its events
	// channel matters, closed to make pump run straight to cleanup.
	oldSess := &nativeSession{id: "agent-1", driver: NewClaudeDriver(ClaudeConfig{SessionID: "agent-1"})}

	// The restart has already happened: the map points at the replacement, and its
	// token is the one the live bridge holds.
	newSess := &nativeSession{id: "agent-1", driver: NewClaudeDriver(ClaudeConfig{SessionID: "agent-1"})}
	tok, err := s.tokens.Issue("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.sessions["agent-1"] = newSess
	s.mu.Unlock()

	// A prompt the NEW session is already waiting on — the old pump must not
	// cancel it either.
	answered := make(chan PermissionDecision, 1)
	go func() {
		d, err := s.broker.Ask(PermissionRequest{ID: "ask-1", SessionID: "agent-1"}, nil)
		if err != nil {
			close(answered)
			return
		}
		answered <- d
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(s.broker.Pending("agent-1")) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("pending ask never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The old driver exits; its pump drains and cleans up.
	close(oldSess.driver.events)
	s.pump(oldSess)

	if !s.tokens.Valid("agent-1", tok) {
		t.Fatal("old pump revoked the replacement session's token — asks would 403")
	}
	if !s.Running("agent-1") {
		t.Fatal("old pump evicted the replacement session")
	}
	if !s.broker.Resolve("ask-1", PermissionDecision{Behavior: "allow"}) {
		t.Fatal("old pump cancelled the replacement session's pending prompt")
	}
	if d, ok := <-answered; !ok || d.Behavior != "allow" {
		t.Fatalf("pending ask did not receive the answer: %+v ok=%v", d, ok)
	}

	// A session that dies while still current DOES clean up after itself.
	s.mu.Lock()
	s.sessions["agent-1"] = oldSess
	s.mu.Unlock()
	s.pump(oldSess) // events channel already closed — cleanup runs again
	if s.tokens.Valid("agent-1", tok) {
		t.Fatal("current session's exit must revoke its token")
	}
	if s.Running("agent-1") {
		t.Fatal("current session's exit must clear the map slot")
	}
}
