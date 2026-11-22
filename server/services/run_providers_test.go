package services

import (
	"powercodedeck/internal/providers"
	"strings"
	"testing"
	"time"
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

func TestNewRoleResumePassesResumeID(t *testing.T) {
	p := &RunProviders{Broker: NewPermissionBroker(), Tokens: NewApproveTokenStore(), ApproveURL: "http://127.0.0.1:1/x", SelfPath: "/test/pcd"}
	// Drivers are wrapped by the adapter, so assert on the constructed driver
	// config (no fake CLI is spawned).
	d, err := p.roleDriver(providers.Claude, "m1", t.TempDir(), "", "", "plan", "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(d.(*ClaudeDriver).buildArgs(), " "); !strings.Contains(args, "--resume conv-1") {
		t.Fatalf("claude args = %s", args)
	}
	d, err = p.roleDriver(providers.Codex, "m2", t.TempDir(), "", "", "plan", "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.(*CodexDriver).cfg.ResumeID; got != "conv-1" {
		t.Fatalf("codex resume = %q", got)
	}
	// Public entry point works and NewRole stays fresh.
	e, err := p.NewRoleResume(providers.Codex, "m3", t.TempDir(), "", "", "plan", "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	e.Stop()
	d, _ = p.roleDriver(providers.Claude, "m4", t.TempDir(), "", "", "", "")
	if strings.Contains(strings.Join(d.(*ClaudeDriver).buildArgs(), " "), "--resume") {
		t.Fatal("fresh role resumed")
	}
}

func TestDenyingBrokerAnswersImmediately(t *testing.T) {
	b := NewDenyingBroker("read-only member")
	type res struct {
		d   PermissionDecision
		err error
	}
	ch := make(chan res, 1)
	go func() {
		d, err := b.Ask(PermissionRequest{ID: "t1", SessionID: "s"}, nil)
		ch <- res{d, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil || r.d.Behavior != "deny" || r.d.Message != "read-only member" {
			t.Fatalf("%+v", r)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Ask did not return within 100ms")
	}
	if len(b.Pending("")) != 0 {
		t.Fatal("pending left behind")
	}
}
