package services

import (
	"encoding/json"
	"testing"
	"time"
)

// Ask blocks until a human answers — that block IS what pauses the agent.
func TestBrokerAskBlocksUntilResolved(t *testing.T) {
	b := NewPermissionBroker()
	asked := make(chan PermissionRequest, 1)
	b.SetAskHandler(func(r PermissionRequest) { asked <- r })

	done := make(chan PermissionDecision, 1)
	go func() {
		d, err := b.Ask(PermissionRequest{
			ID: "toolu_1", SessionID: "s1", ToolName: "Write",
			Input: json.RawMessage(`{"file_path":"/tmp/x"}`),
		}, nil)
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		done <- d
	}()

	r := <-asked
	if r.ToolName != "Write" || r.SessionID != "s1" {
		t.Fatalf("asked with %+v", r)
	}

	// Still waiting — nobody has answered.
	select {
	case d := <-done:
		t.Fatalf("Ask returned %+v before anyone answered", d)
	case <-time.After(50 * time.Millisecond):
	}

	if !b.Resolve("toolu_1", PermissionDecision{Behavior: "allow"}) {
		t.Fatal("Resolve found no pending request")
	}
	select {
	case d := <-done:
		if d.Behavior != "allow" {
			t.Fatalf("decision = %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask did not return after Resolve")
	}
}

// A request must survive an arbitrarily long wait: the user is on a phone. There
// is no timeout by design, so this only checks nothing expires it early.
func TestBrokerHasNoTimeout(t *testing.T) {
	b := NewPermissionBroker()
	go b.Ask(PermissionRequest{ID: "toolu_slow", SessionID: "s1", ToolName: "Bash"}, nil)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := b.Pending("s1"); len(got) != 1 {
		t.Fatalf("pending = %v, want the request still waiting", got)
	}
}

// A device that (re)attaches must be able to see what the agent is stuck on —
// otherwise it stares at a frozen agent with no prompt.
func TestBrokerPendingIsScopedBySession(t *testing.T) {
	b := NewPermissionBroker()
	go b.Ask(PermissionRequest{ID: "a", SessionID: "s1", ToolName: "Write"}, nil)
	go b.Ask(PermissionRequest{ID: "b", SessionID: "s2", ToolName: "Bash"}, nil)
	time.Sleep(50 * time.Millisecond)

	if got := b.Pending("s1"); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("s1 pending = %+v", got)
	}
	if got := b.Pending(""); len(got) != 2 {
		t.Fatalf("all pending = %+v", got)
	}
}

// When a session dies its waiters must be released, not leaked — the MCP bridge
// goroutine is blocked in Ask and would otherwise never return.
func TestBrokerCancelSessionReleasesWaiters(t *testing.T) {
	b := NewPermissionBroker()
	errc := make(chan error, 1)
	go func() {
		_, err := b.Ask(PermissionRequest{ID: "a", SessionID: "s1", ToolName: "Write"}, nil)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)

	b.CancelSession("s1")
	select {
	case err := <-errc:
		if err != ErrPermissionCancelled {
			t.Fatalf("err = %v, want ErrPermissionCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not released on session cancel")
	}
	if got := b.Pending("s1"); len(got) != 0 {
		t.Fatalf("pending after cancel = %+v", got)
	}
}

// Answering something that isn't waiting (a stale tap from a device that missed
// the session ending) must be a no-op, not a panic.
func TestBrokerResolveUnknownIsNoop(t *testing.T) {
	b := NewPermissionBroker()
	if b.Resolve("nope", PermissionDecision{Behavior: "allow"}) {
		t.Fatal("Resolve claimed to answer an unknown request")
	}
}
