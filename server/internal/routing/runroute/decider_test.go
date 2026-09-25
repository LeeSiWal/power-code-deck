package runroute

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/routing"
)

// fakeDecider stands in for the official CLI decider call.
type fakeDecider struct {
	calls  atomic.Int32
	answer func(p routing.Profile, prompt string) (routing.CallOutput, error)
}

func (f *fakeDecider) Call(_ context.Context, p routing.Profile, prompt, _ string) (routing.CallOutput, error) {
	f.calls.Add(1)
	return f.answer(p, prompt)
}

func verdict(v string) func(routing.Profile, string) (routing.CallOutput, error) {
	return func(routing.Profile, string) (routing.CallOutput, error) {
		in, out := 1500, 25
		return routing.CallOutput{Text: v, Usage: &routing.UsageRecord{Scope: "turn", Source: "reported", InputTokens: &in, OutputTokens: &out}}, nil
	}
}

const dispatchCodex = `{"action":"dispatch","profile_id":"codex-fast","reason_code":"VERIFIED_CAPABILITY_MATCH","evidence_refs":["task-spec-1"]}`

// Two services, one MEDIUM profile each: a real tie, so the decider is asked.
func deciderRoutingConfig(verified bool) routing.Config {
	c := routing.DefaultConfig()
	c.Mode = routing.ModeAuto
	c.Strategy = routing.StrategyCommercialLLM
	c.Switching = routing.SwitchPolicy{MaxSwitchesPerRun: 2, MaxAttemptsPerRun: 3, MaxRunMinutes: 30, Stickiness: 0.5}
	q := map[routing.TaskKind]routing.QualityRecord{}
	if verified {
		q[routing.KindDecide] = routing.QualityRecord{Status: routing.QualityVerified, Evidence: "test eval"}
	}
	c.Profiles = []routing.Profile{
		{ID: "claude-fast", Adapter: "claude", Model: "sonnet", Effort: "low", Billing: routing.BillingSubscription, QuotaBucket: "anthropic:me", Tiers: []routing.Tier{routing.Easy, routing.Medium}, Allow: true, Roles: []routing.Role{routing.RoleExecutor, routing.RoleReviewer, routing.RoleDecider}, Quality: q},
		{ID: "codex-fast", Adapter: "codex", Model: "gpt-5.6-luna", Effort: "medium", Billing: routing.BillingSubscription, QuotaBucket: "openai:me", Tiers: []routing.Tier{routing.Easy, routing.Medium}, Allow: true},
	}
	return c
}

func (h *harness) startRun(key, goal string) orchestration.Run {
	run, err := h.runs.Create(key, h.repo, goal, "codex")
	if err != nil {
		h.t.Fatal(err)
	}
	return run
}

func TestDeciderAdvisoryThenApplied(t *testing.T) {
	for _, verified := range []bool{false, true} {
		h := newHarness(t, deciderRoutingConfig(verified), allCLIs())
		fd := &fakeDecider{answer: verdict(dispatchCodex)}
		h.coord.decider = fd
		run := h.startRun("d1", "fix app.txt")
		h.queue("claude", &step{edit: fix})
		h.queue("codex", &step{edit: fix})
		res, err := h.coord.Start(context.Background(), run.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		d := res.Decision
		if fd.calls.Load() != 1 || d.Decider == nil || !d.Decider.Consulted || d.Decider.Verdict == nil {
			t.Fatalf("verified=%v decider %+v", verified, d.Decider)
		}
		if verified && (res.Launched != "codex-fast" || !d.Decider.Applied || d.Source != "commercial_llm") {
			t.Fatalf("verified decider not applied: %+v", res)
		}
		if !verified && (d.Decider.Applied || !strings.Contains(d.Decider.Note, "advisory")) {
			t.Fatalf("unverified decider changed the choice: %+v", d.Decider)
		}
		h.waitPhase(run.ID, routing.PhaseSucceeded)
		v, _ := h.coord.View(run.ID)
		var dec routing.RoleUsage
		for _, r := range v.Usage {
			if r.Role == "decision" {
				dec = r
			}
		}
		if dec.Calls != 1 || dec.Input != 1500 {
			t.Fatalf("decision usage %+v", v.Usage)
		}
		// Recursion guard: the decider call never became an executor launch.
		if len(h.sc.launch) != 1 {
			t.Fatalf("launches %v", h.sc.launch)
		}
	}
}

// Test 16: manual, profile pin and single-service runs make no decider call.
func TestNoDeciderCallWhenNotNeeded(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	fd := &fakeDecider{answer: verdict(dispatchCodex)}
	h.coord.decider = fd
	run := h.startRun("m1", "fix app.txt")
	h.queue("claude", &step{edit: fix})
	if _, err := h.coord.Start(context.Background(), run.ID, "claude-fast"); err != nil {
		t.Fatal(err)
	}
	h.waitPhase(run.ID, routing.PhaseSucceeded)
	run2 := h.startRun("m2", "fix app.txt")
	h.coord.Settings(run2.ID, routing.ModeAuto, "codex-fast", "")
	h.queue("codex", &step{edit: fix})
	res, err := h.coord.Start(context.Background(), run2.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	h.waitPhase(run2.ID, routing.PhaseSucceeded)
	if fd.calls.Load() != 0 || res.Decision.Decider == nil || res.Decision.Decider.Skip != routing.SkipManual {
		t.Fatalf("calls=%d skip=%+v", fd.calls.Load(), res.Decision.Decider)
	}
	// Claude-only host: a single executor, no call, launch directly.
	only := map[string]string{"claude --version": "2.1.239", "claude auth status": `{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty"}`}
	h2 := newHarness(t, deciderRoutingConfig(true), only)
	h2.coord.decider = fd
	run3, _ := h2.runs.Create("s1", h2.repo, "fix app.txt", "claude")
	h2.queue("claude", &step{edit: fix})
	res, err = h2.coord.Start(context.Background(), run3.ID, "")
	if err != nil || res.Launched != "claude-fast" || res.Decision.Decider.Skip != routing.SkipSingleCandidate || fd.calls.Load() != 0 {
		t.Fatalf("single service: %+v %v", res.Decision.Decider, err)
	}
	h2.waitPhase(run3.ID, routing.PhaseSucceeded)
}

// Test 17: commercial Shadow needs per-Run approval and stays in budget.
func TestCommercialShadowApprovalAndBudget(t *testing.T) {
	cfg := deciderRoutingConfig(true)
	cfg.Mode = routing.ModeShadow
	cfg.Decider.ShadowMaxCallsPerDay = 1
	h := newHarness(t, cfg, allCLIs())
	fd := &fakeDecider{answer: verdict(dispatchCodex)}
	h.coord.decider = fd
	run := h.startRun("sh1", "fix app.txt")
	h.queue("codex", &step{edit: fix})
	res, _ := h.coord.Start(context.Background(), run.ID, "")
	if fd.calls.Load() != 0 || res.Decision.Decider.Skip != routing.SkipShadowNotAllowed || res.Launched != "legacy:codex" {
		t.Fatalf("unapproved shadow: %+v", res)
	}
	h.waitPhase(run.ID, routing.PhaseSucceeded)
	for i, key := range []string{"sh2", "sh3"} {
		r := h.startRun(key, "fix app.txt please "+key)
		if _, err := h.coord.SetOptions(r.ID, routing.RunOptions{CommercialShadow: true}); err != nil {
			t.Fatal(err)
		}
		h.queue("codex", &step{edit: fix})
		res, _ := h.coord.Start(context.Background(), r.ID, "")
		h.waitPhase(r.ID, routing.PhaseSucceeded)
		if i == 0 && (fd.calls.Load() != 1 || res.Decision.Decider.Applied || res.Launched != "legacy:codex") {
			t.Fatalf("approved shadow: calls=%d %+v", fd.calls.Load(), res)
		}
		if i == 1 && (fd.calls.Load() != 1 || res.Decision.Decider.Skip != routing.SkipShadowBudget) {
			t.Fatalf("budget: calls=%d %+v", fd.calls.Load(), res.Decision.Decider)
		}
	}
}

// Test 14: cached verdicts are reused and cleared by a refresh.
func TestVerdictCacheAndInvalidation(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	fd := &fakeDecider{answer: verdict(dispatchCodex)}
	h.coord.decider = fd
	for i, key := range []string{"c1", "c2", "c3"} {
		if i == 2 {
			h.coord.Refresh()
		}
		run := h.startRun(key, "fix app.txt")
		h.queue("codex", &step{edit: fix})
		res, err := h.coord.Start(context.Background(), run.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		h.waitPhase(run.ID, routing.PhaseSucceeded)
		if i == 1 && res.Decision.Decider.Skip != routing.SkipCached {
			t.Fatalf("cache not used: %+v", res.Decision.Decider)
		}
	}
	if fd.calls.Load() != 2 {
		t.Fatalf("calls %d (want: first + after refresh)", fd.calls.Load())
	}
}

// Tests 12, 13: a decider 429 cools the shared bucket (the executor on the same
// account too) without recording an executor failure.
func TestDeciderRateLimitSharesBucket(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	h.coord.decider = &fakeDecider{answer: func(routing.Profile, string) (routing.CallOutput, error) {
		return routing.CallOutput{}, &routing.CallError{Class: "rate_limited", Err: errors.New("429")}
	}}
	run := h.startRun("rl", "fix app.txt")
	h.queue("codex", &step{edit: fix})
	res, err := h.coord.Start(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Launched != "codex-fast" || !strings.Contains(res.Decision.Reason, "recomputed") {
		t.Fatalf("claude bucket should be cooled and rules recomputed: %+v", res)
	}
	tl := h.waitPhase(run.ID, routing.PhaseSucceeded)
	if len(tl.Attempts) != 1 || tl.Attempts[0].Class != routing.NoFailure {
		t.Fatalf("decider failure leaked into executor records: %+v", tl.Attempts)
	}
}

// Test 15: a cancel or a settings change while the decider is thinking wins.
func TestLateDeciderResultIgnored(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	var run orchestration.Run
	h.coord.decider = &fakeDecider{answer: func(p routing.Profile, prompt string) (routing.CallOutput, error) {
		h.worker.Cancel(run.ID)
		return verdict(dispatchCodex)(p, prompt)
	}}
	run = h.startRun("late", "fix app.txt")
	if _, err := h.coord.Start(context.Background(), run.ID, ""); err == nil {
		t.Fatal("started after cancel")
	}
	if len(h.sc.launch) != 0 {
		t.Fatal("launched after cancel")
	}
	var run2 orchestration.Run
	h.coord.decider = &fakeDecider{answer: func(p routing.Profile, prompt string) (routing.CallOutput, error) {
		h.coord.Settings(run2.ID, routing.ModeManual, "", "")
		return verdict(dispatchCodex)(p, prompt)
	}}
	run2 = h.startRun("late2", "fix app.txt in a different way")
	if _, err := h.coord.Start(context.Background(), run2.ID, ""); !errors.Is(err, routing.ErrStaleEpoch) {
		t.Fatalf("stale decision applied: %v", err)
	}
	if len(h.sc.launch) != 0 {
		t.Fatal("launched on a stale decision")
	}
}

// Test 18 end to end: reviewer comes from the policy, never a hidden provider;
// no allowed reviewer → visible review_blocked, not success, not escalation.
func TestReviewerThroughPolicy(t *testing.T) {
	cfg := deciderRoutingConfig(true)
	h := newHarness(t, cfg, allCLIs())
	var reviewers atomic.Int32
	h.worker.SetRoleResolver(func(role string, run orchestration.Run, provider string) (orchestration.Factory, string, error) {
		choice, err := h.coord.ResolveRole(role, provider)
		if err != nil {
			return nil, "", err
		}
		if choice.Profile.Adapter == routing.AdapterAntigravity {
			t.Errorf("antigravity chosen as hidden %s", role)
		}
		return func(id, cwd string) (providers.Execution, error) {
			reviewers.Add(1)
			return &fakeExec{id: id, cwd: cwd, provider: providers.ID(choice.Profile.Adapter), s: &step{text: `{"verdict":"pass","summary":"ok"}`}, sc: &script{}}, nil
		}, choice.Label, nil
	})
	run := h.startRun("rv", "fix app.txt")
	h.queue("codex", &step{edit: fix})
	if _, err := h.coord.Start(context.Background(), run.ID, "codex-fast"); err != nil {
		t.Fatal(err)
	}
	tl := h.waitPhase(run.ID, routing.PhaseSucceeded)
	if reviewers.Load() != 1 || !strings.Contains(tl.Attempts[0].Report.Reviewer, "claude-fast") {
		t.Fatalf("reviewer %q", tl.Attempts[0].Report.Reviewer)
	}
	// Only Antigravity signed in, Codex Run: nothing allowed may review.
	h2 := newHarness(t, cfg, map[string]string{"codex --version": "0.154.0", "codex login status": "Logged in using ChatGPT", "agy --version": "1.2.11", "agy --help": "--print --output-format", "agy models": "gemini-3.8-flash-medium\tx"})
	cfg2 := h2.coord.cfg
	cfg2.Profiles[1].Roles = []routing.Role{routing.RoleExecutor} // codex may execute but not review
	h2.coord.cfg = cfg2
	h2.worker.SetRoleResolver(func(role string, run orchestration.Run, provider string) (orchestration.Factory, string, error) {
		choice, err := h2.coord.ResolveRole(role, provider)
		if err != nil {
			return nil, "", err
		}
		return nil, choice.Label, errors.New("unexpected reviewer " + choice.Profile.ID)
	})
	run2, _ := h2.runs.Create("rv2", h2.repo, "fix app.txt", "codex")
	h2.queue("codex", &step{edit: fix})
	if _, err := h2.coord.Start(context.Background(), run2.ID, "codex-fast"); err != nil {
		t.Fatal(err)
	}
	tl = h2.waitPhase(run2.ID, routing.PhaseWaitUser)
	if tl.Attempts[0].Class != routing.ReviewBlocked || len(tl.Attempts) != 1 {
		t.Fatalf("attempt %+v", tl.Attempts[0])
	}
	r, _ := h2.runs.Get(run2.ID)
	if r.State == "succeeded" {
		t.Fatal("run succeeded without its required review")
	}
}
