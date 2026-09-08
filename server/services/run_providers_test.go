package services

import (
	"powercodedeck/internal/providers"
	"testing"
)

func TestRunProviderIsolationAndCleanup(t *testing.T) {
	p := &RunProviders{Broker: NewPermissionBroker(), Tokens: NewApproveTokenStore(), ApproveURL: "http://127.0.0.1:1/internal/runs/approve", SelfPath: "/test/pcd"}
	for _, provider := range []providers.ID{providers.Claude, providers.Codex} {
		id := "exec_" + string(provider)
		e, err := p.New(provider, id, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if e.Identity() != (providers.Identity{ExecutionID: id, Provider: provider}) {
			t.Fatal("incorrect identity")
		}
		answer := p.Broker.InjectPendingForTest(PermissionRequest{ID: id + ":tool", SessionID: id})
		token := p.Tokens.tokens[id]
		if provider == providers.Claude && !p.Tokens.Valid(id, token) {
			t.Fatal("Claude token missing")
		}
		e.Stop()
		e.Stop()
		if token != "" && p.Tokens.Valid(id, token) {
			t.Fatal("token survived stop")
		}
		select {
		case _, ok := <-answer:
			if ok {
				t.Fatal("stop approved pending tool")
			}
		default:
			t.Fatal("stop left approval blocked")
		}
		if len(p.Broker.Pending(id)) != 0 {
			t.Fatal("pending approval survived stop")
		}
	}
	if _, err := p.New(providers.Antigravity, "exec_other", t.TempDir()); err == nil {
		t.Fatal("unsupported provider accepted")
	}
}

func TestRunApprovalDecisionIsScoped(t *testing.T) {
	b := NewPermissionBroker()
	answer := b.InjectPendingForTest(PermissionRequest{ID: "tool", SessionID: "execution-a"})
	if b.ResolveSession("execution-b", "tool", PermissionDecision{Behavior: "allow"}) {
		t.Fatal("another execution approved tool")
	}
	if !b.ResolveSession("execution-a", "tool", PermissionDecision{Behavior: "deny"}) {
		t.Fatal("owner could not decide")
	}
	if decision := <-answer; decision.Behavior != "deny" {
		t.Fatal(decision)
	}
	if b.ResolveSession("execution-a", "tool", PermissionDecision{Behavior: "allow"}) {
		t.Fatal("stale decision accepted")
	}
}
