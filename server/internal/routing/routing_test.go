package routing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func mustPolicy(t *testing.T) PolicyEvidence {
	t.Helper()
	p, err := LoadPolicyEvidence()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ready builds a status as a probe would report for an installed, signed-in CLI.
func ready(adapter string, billing Status) AdapterStatus {
	now := time.Now()
	return AdapterStatus{AdapterID: adapter, Installation: Observed(Installed, "t", "", now), Auth: Observed(Authenticated, "t", "", now),
		Billing: Observed(billing, "t", "", now), Technical: Observed(Supported, "t", "", now)}
}

func statuses(list ...AdapterStatus) map[string]AdapterStatus {
	m := map[string]AdapterStatus{}
	for _, s := range list {
		m[s.AdapterID] = s
	}
	return m
}

func baseProfiles() []Profile {
	return []Profile{
		{ID: "codex-fast", Adapter: AdapterCodex, Model: "gpt-5.6-luna", Effort: "medium", Billing: BillingSubscription, Tiers: []Tier{Easy, Medium}, Allow: true},
		{ID: "codex-deep", Adapter: AdapterCodex, Model: "gpt-5.6-sol", Effort: "xhigh", Billing: BillingSubscription, Tiers: []Tier{High}, Allow: true},
		{ID: "claude-opus", Adapter: AdapterClaude, Model: "opus", Effort: "high", Billing: BillingSubscription, Tiers: []Tier{High, Ultra}, Allow: true},
		{ID: "agy-flash", Adapter: AdapterAntigravity, Model: "gemini-3.8-flash-medium", Billing: BillingSubscription, Tiers: []Tier{Easy, Medium}, Allow: true},
		{ID: "local-qwen", Adapter: AdapterLocal, EndpointRef: "lan", Model: "qwen", Billing: BillingLocal, Tiers: []Tier{VeryEasy, Easy}, Allow: true},
	}
}

func cfgWith(ps ...Profile) Config {
	c := DefaultConfig()
	c.Mode = ModeAuto
	c.Profiles = ps
	c.LocalEndpoints = []LocalEndpoint{{ID: "lan", URL: "http://127.0.0.1:11434", Kind: "ollama"}}
	return c
}

func eligibleIDs(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		if c.Eligible() {
			out = append(out, c.Profile.ID)
		}
	}
	return out
}

func reasons(cs []Candidate, id string) []Reason {
	for _, c := range cs {
		if c.Profile.ID == id {
			var r []Reason
			for _, e := range c.Excluded {
				r = append(r, e.Reason)
			}
			return r
		}
	}
	return nil
}

func hasReason(cs []Candidate, id string, r Reason) bool {
	for _, x := range reasons(cs, id) {
		if x == r {
			return true
		}
	}
	return false
}

// Test 1: every availability combination of the four executors, including none.
func TestFilterAllAvailabilityCombinations(t *testing.T) {
	pol := mustPolicy(t)
	cfg := cfgWith(baseProfiles()...)
	adapters := []string{AdapterClaude, AdapterCodex, AdapterAntigravity, AdapterLocal}
	for mask := 0; mask < 16; mask++ {
		st := map[string]AdapterStatus{}
		for i, a := range adapters {
			if mask&(1<<i) == 0 {
				continue
			}
			if a == AdapterLocal {
				s := ready("local:lan", BillingLocal)
				st["local:lan"] = s
			} else {
				st[a] = ready(a, BillingSubscription)
			}
		}
		cs := Filter(cfg, st, pol, NewHealth(), Need{Kind: KindCode, Automatic: true})
		got := map[string]bool{}
		for _, id := range eligibleIDs(cs) {
			got[id] = true
		}
		// Antigravity is never automatic (policy review), local never for code.
		if got["agy-flash"] || got["local-qwen"] {
			t.Fatalf("mask %04b: policy-gated or tool-less profile became automatic: %v", mask, got)
		}
		if mask&1 != 0 != got["claude-opus"] {
			t.Fatalf("mask %04b: claude eligibility wrong: %v", mask, got)
		}
		if mask&2 != 0 != got["codex-fast"] {
			t.Fatalf("mask %04b: codex eligibility wrong: %v", mask, got)
		}
		if mask&1 == 0 && !hasReason(cs, "claude-opus", RNotInstalled) {
			t.Fatalf("mask %04b: missing not_installed reason", mask)
		}
		if mask&4 != 0 && !hasReason(cs, "agy-flash", RPolicyReview) {
			t.Fatalf("mask %04b: antigravity must show policy_review_required, got %v", mask, reasons(cs, "agy-flash"))
		}
		if mask&4 != 0 && hasReason(cs, "agy-flash", RNotInstalled) {
			t.Fatalf("mask %04b: installed antigravity reported as not installed", mask)
		}
		if mask&8 != 0 && !hasReason(cs, "local-qwen", RNeedsTools) {
			t.Fatalf("mask %04b: local code task must be refused for missing tools", mask)
		}
	}
}

// Test 6: Antigravity manual use is preserved but visible; automatic is blocked.
func TestAntigravityPolicyGate(t *testing.T) {
	pol := mustPolicy(t)
	cfg := cfgWith(baseProfiles()[3])
	st := statuses(ready(AdapterAntigravity, BillingSubscription))
	if ids := eligibleIDs(Filter(cfg, st, pol, nil, Need{Kind: KindCode, Automatic: true})); len(ids) != 0 {
		t.Fatalf("auto candidates = %v", ids)
	}
	if ids := eligibleIDs(Filter(cfg, st, pol, nil, Need{Kind: KindCode})); len(ids) != 1 {
		t.Fatalf("manual selection should stay available, got %v", ids)
	}
	d := Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask("fix the bug", KindCode, nil), Mode: ModeAuto})
	if d.Selected != "" || d.Source != "none" {
		t.Fatalf("Google-only auto must not run: %+v", d)
	}
	if pol.For(AdapterAntigravity, ScopeHostedMultiUser).Value != PolicyBlocked {
		t.Fatal("hosted multi-user antigravity must be blocked")
	}
}

// Test 2 & 5: Gemini consumer sign-in ending is distinct from a plain login
// failure, and the Gemini path is never executable.
func TestGeminiConsumerAuthDistinct(t *testing.T) {
	pol := mustPolicy(t)
	g := ready(AdapterGemini, Unknown)
	g.Auth = Observed(ConsumerAuthDiscontinued, "G1", "ended", time.Now())
	c := ready(AdapterClaude, BillingSubscription)
	c.Auth = Observed(NotAuthenticated, "t", "", time.Now())
	cfg := cfgWith(Profile{ID: "gem", Adapter: AdapterGemini, Billing: BillingAPIMetered, Tiers: []Tier{Medium}, Allow: true}, baseProfiles()[2])
	cfg.Spend.AllowAPIMetered = true
	cs := Filter(cfg, statuses(g, c), pol, nil, Need{Kind: KindCode})
	if !hasReason(cs, "gem", RConsumerAuthEnded) || hasReason(cs, "gem", RNotAuthenticated) {
		t.Fatalf("gemini reasons = %v", reasons(cs, "gem"))
	}
	if !hasReason(cs, "gem", RNotImplemented) {
		t.Fatal("gemini compatibility path must be marked not implemented")
	}
	if !hasReason(cs, "claude-opus", RNotAuthenticated) || hasReason(cs, "claude-opus", RConsumerAuthEnded) {
		t.Fatalf("claude reasons = %v", reasons(cs, "claude-opus"))
	}
	// Missing Gemini CLI with the enterprise path allowed.
	cs = Filter(cfg, map[string]AdapterStatus{}, pol, nil, Need{Kind: KindCode})
	if !hasReason(cs, "gem", RNotInstalled) {
		t.Fatalf("missing gemini = %v", reasons(cs, "gem"))
	}
}

// Test 3: one real profile serves the run; several tiers on one profile are
// shown as-is and nothing is invented.
func TestSingleProfileAndSharedTiers(t *testing.T) {
	pol := mustPolicy(t)
	only := Profile{ID: "codex-only", Adapter: AdapterCodex, Billing: BillingSubscription, Tiers: []Tier{VeryEasy, Easy, Medium, High, Ultra}, Allow: true}
	cfg := cfgWith(only)
	st := statuses(ready(AdapterCodex, BillingSubscription))
	for _, goal := range []string{"fix typo", "redesign the concurrency architecture and migrate security model across 10 files a.go b.go c.go d.go e.go f.go g.go h.go " + strings.Repeat("x", 4100)} {
		d := Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask(goal, KindCode, nil), Mode: ModeAuto})
		if d.Selected != "codex-only" || d.Source != "only_candidate" || d.Router != nil {
			t.Fatalf("goal %.20q: %+v", goal, d)
		}
	}
	if len(cfg.Profiles) != 1 {
		t.Fatal("profiles were invented")
	}
}

// Test 4: required tier/tools/context unsupported → no silent downgrade.
func TestNoCandidateNoDowngrade(t *testing.T) {
	pol := mustPolicy(t)
	cfg := cfgWith(baseProfiles()[0]) // EASY/MEDIUM only
	st := statuses(ready(AdapterCodex, BillingSubscription))
	d := Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask("Refactor the architecture of the scheduler", KindCode, nil), Mode: ModeAuto})
	if d.Selected != "" || !strings.Contains(d.Reason, "HIGH") {
		t.Fatalf("downgraded: %+v", d)
	}
	if !hasReason(d.Candidates, "codex-fast", RTierNotMapped) {
		t.Fatalf("reasons = %v", reasons(d.Candidates, "codex-fast"))
	}
	small := baseProfiles()[0]
	small.Capabilities = &Capabilities{Tools: true, EditFiles: true, ForceModel: true, ForceEffort: true, ContextTokens: 1000}
	cs := Filter(cfgWith(small), st, pol, nil, Need{Kind: KindCode, ContextTokens: 5000})
	if !hasReason(cs, "codex-fast", RContextTooSmall) {
		t.Fatalf("context reasons = %v", reasons(cs, "codex-fast"))
	}
}

// Test 7 & 9: spend gates.
func TestSpendPolicyGates(t *testing.T) {
	pol := mustPolicy(t)
	st := statuses(ready(AdapterClaude, Unknown))
	mk := func(b Status, extra bool) Config {
		return cfgWith(Profile{ID: "p", Adapter: AdapterClaude, Billing: b, ExtraUsageRisk: extra, Tiers: []Tier{High}, Allow: true})
	}
	cases := []struct {
		billing Status
		extra   bool
		allow   func(*SpendPolicy)
		want    Reason
	}{
		{BillingAPIMetered, false, nil, RBillingNotAllowed},
		{BillingPlanCredits, false, nil, RBillingNotAllowed},
		{BillingPurchasedCredits, false, nil, RBillingNotAllowed},
		{BillingPromoCredits, false, nil, RBillingNotAllowed},
		{Unknown, false, nil, RBillingUnknown},
		{BillingSubscription, true, nil, RExtraUsageRisk},
	}
	for _, c := range cases {
		cs := Filter(mk(c.billing, c.extra), st, pol, nil, Need{Kind: KindCode, Automatic: true})
		if !hasReason(cs, "p", c.want) {
			t.Fatalf("%s extra=%v: reasons %v", c.billing, c.extra, reasons(cs, "p"))
		}
	}
	// An explicit user choice of an unknown-billing or extra-usage path is not
	// blocked, but it carries a visible warning; known metered paths stay blocked.
	for _, c := range []Config{mk(Unknown, false), mk(BillingSubscription, true)} {
		cs := Filter(c, st, pol, nil, Need{Kind: KindCode})
		if len(cs[0].Excluded) != 0 || len(cs[0].Warnings) != 1 {
			t.Fatalf("manual: excluded %v warnings %v", cs[0].Excluded, cs[0].Warnings)
		}
	}
	if cs := Filter(mk(BillingAPIMetered, false), st, pol, nil, Need{Kind: KindCode}); !hasReason(cs, "p", RBillingNotAllowed) {
		t.Fatal("manual metered API must still need the opt-in")
	}
	cfg := mk(BillingPurchasedCredits, false)
	cfg.Spend.AllowPurchasedCredits = true
	if ids := eligibleIDs(Filter(cfg, st, pol, nil, Need{Kind: KindCode, Automatic: true})); len(ids) != 1 {
		t.Fatal("explicit purchased-credit opt-in should allow the profile")
	}
	cfg.Spend.AllowPurchasedCredits = false
	cfg.Spend.AllowPlanCredits = true // a different opt-in must not unlock purchased credits
	if ids := eligibleIDs(Filter(cfg, st, pol, nil, Need{Kind: KindCode, Automatic: true})); len(ids) != 0 {
		t.Fatal("plan-credit opt-in unlocked purchased credits")
	}
	// Inherited API key in the server env blocks a subscription profile.
	s := ready(AdapterClaude, BillingSubscription)
	s.InheritedEnv = []string{"ANTHROPIC_API_KEY"}
	if !hasReason(Filter(mk(BillingSubscription, false), statuses(s), pol, nil, Need{Kind: KindCode}), "p", RInheritedBillingEnv) {
		t.Fatal("inherited API key not flagged")
	}
	// Auth method drift (profile expects subscription, CLI now reports API key).
	s = ready(AdapterClaude, BillingSubscription)
	s.AuthMethod = "api_key"
	p := mk(BillingSubscription, false)
	p.Profiles[0].AuthMethod = "subscription"
	if !hasReason(Filter(p, statuses(s), pol, nil, Need{Kind: KindCode}), "p", RAuthMethodMismatch) {
		t.Fatal("auth method mismatch not flagged")
	}
	if cfg.Fingerprint() == mk(BillingPurchasedCredits, false).Fingerprint() {
		t.Fatal("config change must change the fingerprint (cache invalidation)")
	}
}

// Test 8: Codex model/effort from model/list; unsupported effort and unlisted
// model are refused.
func TestCodexModelListAndEffort(t *testing.T) {
	models, err := ParseCodexModelList(codexModelListFixture)
	if err != nil || len(models) != 2 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	if models[1].ID != "gpt-5.5" || strings.Join(models[1].Efforts, ",") != "low,medium,high,xhigh" {
		t.Fatalf("parsed %+v", models[1])
	}
	if _, err := ParseCodexModelList(`{"id":2,"error":{"code":-32601,"message":"method not found"}}`); err == nil {
		t.Fatal("unsupported method must be an error")
	}
	if _, err := ParseCodexModelList(`{"id":1,"result":{}}`); err == nil {
		t.Fatal("missing response must be an error")
	}
	pol := mustPolicy(t)
	st := ready(AdapterCodex, BillingSubscription)
	st.Models = models
	cfg := cfgWith(
		Profile{ID: "ok", Adapter: AdapterCodex, Model: "gpt-5.5", Effort: "xhigh", Billing: BillingSubscription, Tiers: []Tier{High}, Allow: true},
		Profile{ID: "bad-effort", Adapter: AdapterCodex, Model: "gpt-5.5", Effort: "ultra", Billing: BillingSubscription, Tiers: []Tier{High}, Allow: true},
		Profile{ID: "unlisted", Adapter: AdapterCodex, Model: "gpt-9", Billing: BillingSubscription, Tiers: []Tier{High}, Allow: true},
	)
	cs := Filter(cfg, statuses(st), pol, nil, Need{Kind: KindCode})
	if !hasReason(cs, "bad-effort", REffortUnsupported) || !hasReason(cs, "unlisted", RModelNotListed) || len(reasons(cs, "ok")) != 0 {
		t.Fatalf("ok=%v bad=%v unlisted=%v", reasons(cs, "ok"), reasons(cs, "bad-effort"), reasons(cs, "unlisted"))
	}
}

const codexModelListFixture = `{"id":1,"result":{"userAgent":"x"}}
{"method":"remoteControl/status/changed","params":{"status":"disabled"}}
{"id":2,"result":{"data":[{"id":"gpt-6-astra","model":"gpt-6-astra","displayName":"GPT-6 Astra","hidden":false,"isDefault":true,"defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"ultra"}],"futureField":1},{"id":"gpt-5.5","model":"gpt-5.5","hidden":false,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"medium"},{"reasoningEffort":"high"},{"reasoningEffort":"xhigh"}]},{"id":"internal","hidden":true}]}}
`

// Tests 22 & 23: quota is shared per bucket; limits stop instead of looping.
func TestHealthBucketsAndBudgets(t *testing.T) {
	pol := mustPolicy(t)
	h := NewHealth()
	ps := baseProfiles()[:2] // same codex account bucket
	h.Mark(ps[0], RateLimited, time.Now().Add(time.Minute), "429 Retry-After: 60")
	cs := Filter(cfgWith(ps...), statuses(ready(AdapterCodex, BillingSubscription)), pol, h, Need{Kind: KindCode})
	if !hasReason(cs, "codex-fast", RRateLimited) || !hasReason(cs, "codex-deep", RRateLimited) {
		t.Fatal("shared quota bucket must cool every model on the account")
	}
	h.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if s, _ := h.Get(ps[1]); s != Unknown {
		t.Fatal("cooldown should expire")
	}
	if RetryAfter("HTTP 429; Retry-After: 120") != 120*time.Second {
		t.Fatal("retry-after not parsed")
	}
	pol2 := SwitchPolicy{MaxSwitchesPerRun: 1, MaxAttemptsPerRun: 3, MaxRunMinutes: 10, AutoEscalate: true}
	now := time.Now()
	if a := NextAction(ModeAuto, pol2, Budget{Switches: 1, Attempts: 1, StartedAt: now}, QualityFailure, now); a.Kind != ActWaitUser {
		t.Fatalf("switch budget: %+v", a)
	}
	if a := NextAction(ModeAuto, pol2, Budget{Attempts: 3, StartedAt: now}, QualityFailure, now); a.Kind != ActWaitUser {
		t.Fatalf("attempt budget: %+v", a)
	}
	if a := NextAction(ModeAuto, pol2, Budget{Attempts: 1, StartedAt: now.Add(-time.Hour)}, QualityFailure, now); a.Kind != ActWaitUser {
		t.Fatalf("time budget: %+v", a)
	}
	if a := NextAction(ModeAuto, pol2, Budget{Attempts: 1, StartedAt: now}, QualityFailure, now); a.Kind != ActEscalate {
		t.Fatalf("escalate: %+v", a)
	}
	if a := NextAction(ModeAuto, pol2, Budget{Attempts: 1, StartedAt: now}, AvailabilityFail, now); a.Kind != ActFailover {
		t.Fatalf("failover: %+v", a)
	}
	if a := NextAction(ModeShadow, pol2, Budget{Attempts: 1, StartedAt: now}, QualityFailure, now); a.Kind != ActWaitUser {
		t.Fatalf("shadow must never continue on its own: %+v", a)
	}
	if a := NextAction(ModeAuto, pol2, Budget{Attempts: 1, StartedAt: now, Pinned: true}, QualityFailure, now); a.Kind != ActWaitUser {
		t.Fatalf("pinned: %+v", a)
	}
}

// Tests 15, 16, 24: classification; cancel/permission/env/unknown never escalate.
func TestClassifyAndNoAutoRestart(t *testing.T) {
	pol := SwitchPolicy{MaxSwitchesPerRun: 5, MaxAttemptsPerRun: 5, MaxRunMinutes: 60, AutoEscalate: true}
	now := time.Now()
	cases := []struct {
		r    AttemptReport
		want FailureClass
		act  ActionKind
	}{
		{AttemptReport{Canceled: true, FailedChecks: []string{"x"}, QuiesceVerified: true}, UserCanceled, ActStop},
		{AttemptReport{ProviderStatus: "success", FailedChecks: []string{"server_test"}, QuiesceVerified: true, Detail: "answer mentions quota and 401"}, QualityFailure, ActEscalate},
		{AttemptReport{ProviderStatus: "success", FailedChecks: []string{"server_test"}, QuiesceVerified: false}, UnknownSideEffect, ActReconcile},
		{AttemptReport{ProviderStatus: "failed", Detail: "HTTP 429 Too Many Requests", QuiesceVerified: true}, AvailabilityFail, ActFailover},
		{AttemptReport{ProviderStatus: "failed", Detail: "Not logged in · Please run /login", QuiesceVerified: true}, AuthFailure, ActWaitUser},
		{AttemptReport{ProviderStatus: "failed", Detail: "model gpt-9 not available for your plan", QuiesceVerified: true}, EntitlementFail, ActWaitUser},
		{AttemptReport{ProviderStatus: "success", Denials: 1, FailedChecks: []string{"diff_check"}, QuiesceVerified: true}, PermissionFail, ActStop},
		{AttemptReport{EnvironmentErr: true, Detail: "exec: \"agy\": executable file not found in $PATH"}, EnvironmentFail, ActStop},
		{AttemptReport{ProviderStatus: "interrupted", QuiesceVerified: true}, UnknownSideEffect, ActReconcile},
		{AttemptReport{RunSucceeded: true, QuiesceVerified: true}, NoFailure, ActNone},
	}
	for i, c := range cases {
		got := Classify(c.r)
		if got != c.want {
			t.Fatalf("case %d: class %q want %q", i, got, c.want)
		}
		if a := NextAction(ModeAuto, pol, Budget{Attempts: 1, StartedAt: now}, got, now); a.Kind != c.act {
			t.Fatalf("case %d: action %q want %q", i, a.Kind, c.act)
		}
	}
}

// Escalation raises the floor above the failed tier; availability does not.
func TestRuleTierEscalationFloor(t *testing.T) {
	task := DescribeTask("add a flag", KindCode, []PriorAttempt{{ExecutionID: "e1", Tier: Medium, Class: QualityFailure}})
	if tier, _ := RuleTier(task); tier != High || task.Stage != "escalation" {
		t.Fatalf("tier=%v stage=%s", tier, task.Stage)
	}
	task = DescribeTask("add a flag", KindCode, []PriorAttempt{{ExecutionID: "e1", Tier: Medium, Class: AvailabilityFail}})
	if tier, _ := RuleTier(task); tier != Medium {
		t.Fatalf("availability failure must not escalate, got %v", tier)
	}
	if tier, _ := RuleTier(DescribeTask("오타 수정", KindCode, nil)); tier != VeryEasy {
		t.Fatalf("korean trivial: %v", tier)
	}
	if tier, _ := RuleTier(DescribeTask("동시성 버그가 있는 상태 머신을 리팩터링해줘", KindCode, nil)); tier != High {
		t.Fatalf("korean hard: %v", tier)
	}
}

// Downgrade after success, stickiness, and pins.
func TestSelectionDowngradeStickinessPins(t *testing.T) {
	pol := mustPolicy(t)
	cfg := cfgWith(baseProfiles()[:3]...)
	st := statuses(ready(AdapterCodex, BillingSubscription), ready(AdapterClaude, BillingSubscription))
	d := Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask("rename the helper in util.go", KindCode, nil), Mode: ModeAuto, Current: "claude-opus"})
	if d.Selected != "codex-fast" {
		t.Fatalf("easy task after a HIGH task should step down: %+v", d.Selected)
	}
	cfg.Switching.Stickiness = 5
	d = Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask("rename the helper in util.go", KindCode, nil), Mode: ModeAuto, Current: "claude-opus"})
	if d.Selected != "claude-opus" || d.Source != "sticky" {
		t.Fatalf("high stickiness should keep the current profile: %+v", d)
	}
	d = Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask("refactor the design", KindCode, nil), Mode: ModeAuto, PinAdapter: AdapterCodex})
	if d.Selected != "codex-deep" {
		t.Fatalf("provider pin: %+v", d.Selected)
	}
	d = Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask("x", KindCode, nil), Mode: ModeManual, Manual: "codex-deep"})
	if d.Selected != "codex-deep" || d.Source != "manual" {
		t.Fatalf("manual: %+v", d)
	}
	d = Decide(context.Background(), DecideInput{Config: cfg, Statuses: statuses(ready(AdapterCodex, BillingSubscription)), Policy: pol, Task: DescribeTask("x", KindCode, nil), Mode: ModeManual, Manual: "claude-opus"})
	if d.Selected != "" || !strings.Contains(d.Reason, "not_installed") {
		t.Fatalf("unavailable manual choice must be refused, not substituted: %+v", d)
	}
}

func TestStateMachine(t *testing.T) {
	path := []Phase{PhaseIdle, PhaseRouting, PhaseRunning, PhaseQuiescing, PhaseCheckpointed, PhaseRouting, PhaseHandoff, PhaseRunning}
	for i := 1; i < len(path); i++ {
		if !CanTransition(path[i-1], path[i]) {
			t.Fatalf("%s → %s rejected", path[i-1], path[i])
		}
	}
	for _, bad := range [][2]Phase{{PhaseQuiescing, PhaseRunning}, {PhaseRunning, PhaseRunning}, {PhaseCanceled, PhaseRouting}, {PhaseSucceeded, PhaseRouting}, {PhaseRunning, PhaseHandoff}} {
		if CanTransition(bad[0], bad[1]) {
			t.Fatalf("%s → %s must be rejected", bad[0], bad[1])
		}
	}
}

func TestConfigValidation(t *testing.T) {
	good := `{"version":1,"mode":"shadow","spend":{},"switching":{"maxSwitchesPerRun":2,"maxAttemptsPerRun":3,"maxRunMinutes":60,"stickiness":0.5},
	 "profiles":[{"id":"a","adapter":"codex","tiers":["EASY"],"allow":true},{"id":"b","adapter":"claude","tiers":["HIGH","ULTRA"],"allow":true}],
	 "routellm":{"enabled":true,"url":"http://127.0.0.1:8765","router":"bert","pairs":[{"weak":"a","strong":"b","threshold":0.5}]}}`
	c, err := ParseConfig(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if c.Profiles[1].MaxTier() != Ultra {
		t.Fatal("tiers not parsed")
	}
	for name, bad := range map[string]string{
		"unknown field": strings.Replace(good, `"spend":{}`, `"spend":{"hardCap":true}`, 1),
		"bad adapter":   strings.Replace(good, `"adapter":"codex"`, `"adapter":"openai-api"`, 1),
		"bad tier":      strings.Replace(good, `"EASY"`, `"TRIVIAL"`, 1),
		"pair unknown":  strings.Replace(good, `"weak":"a"`, `"weak":"zz"`, 1),
		"hard budget":   strings.Replace(good, `"spend":{}`, `"spend":{"budgetKind":"hard"}`, 1),
		"bad threshold": strings.Replace(good, `"threshold":0.5`, `"threshold":1.5`, 1),
		"duplicate":     strings.Replace(good, `"id":"b"`, `"id":"a"`, 1),
	} {
		if _, err := ParseConfig(strings.NewReader(bad)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if d := DefaultConfig(); d.Mode != ModeOff || d.Spend.AllowAPIMetered || d.Spend.AllowUnknownBilling {
		t.Fatal("default must be off and subscription-only")
	}
}

func TestGeneratedDefaultsAreManualOnly(t *testing.T) {
	c := DefaultConfig().WithDiscoveredDefaults([]AdapterStatus{ready(AdapterCodex, BillingSubscription), {AdapterID: AdapterClaude, Installation: Observation{Value: NotInstalled}}})
	if len(c.Profiles) != 1 || c.Profiles[0].Allow || len(c.Profiles[0].Tiers) != 0 || c.Profiles[0].Model != "" {
		t.Fatalf("generated = %+v", c.Profiles)
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	if errors.Is(ErrStaleEpoch, ErrRouterUnavailable) {
		t.Fatal("error identity collision")
	}
}
