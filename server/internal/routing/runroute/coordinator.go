// Package runroute connects routing policy to v2 Runs. It decides a profile,
// records the decision, builds handoff evidence, launches through the worker,
// and classifies each finished attempt. It never bypasses the worker's single
// slot, its checks, or its approval wiring.
package runroute

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/routing"
)

// Worker is the subset of orchestration.Worker the coordinator uses.
type Worker interface {
	StartWith(runID string, l orchestration.Launch) (string, error)
	Interrupt(runID string) error
	Busy() bool
	ReadArtifact(run, execution, kind string) ([]byte, error)
}

type Runs interface {
	Get(id string) (orchestration.Run, error)
}

var ErrRoutingOff = errors.New("routing is off for this run")
var ErrNoProfile = errors.New("no allowed profile")

type Coordinator struct {
	mu       sync.Mutex
	cfg      routing.Config
	cfgErr   error
	store    *routing.Store
	runs     Runs
	worker   Worker
	prober   *routing.Prober
	policy   routing.PolicyEvidence
	health   *routing.Health
	router   *routing.RouteLLMClient
	now      func() time.Time
	probeAge time.Duration
	// async runs automatic continuations; tests replace it to run inline.
	async func(func())
	// timings holds routing/handoff durations until the attempt reports.
	timings  sync.Map
	decider  routing.DeciderCaller
	verdicts verdictCache
	// judged caches local tier-judge answers by request text for a minute: one
	// route-turn asks with and without a tool pin, and retries repeat a goal.
	judgedMu sync.Mutex
	judged   map[string]judgedTier
}

type judgedTier struct {
	tier routing.Tier
	exp  time.Time
}

type Options struct {
	Config    routing.Config
	ConfigErr error
	Store     *routing.Store
	Runs      Runs
	Worker    Worker
	Prober    *routing.Prober
	Policy    routing.PolicyEvidence
	Decider   routing.DeciderCaller // nil = commercial_llm strategy cannot consult
}

func New(o Options) (*Coordinator, error) {
	router, err := routing.NewRouteLLMClient(o.Config.RouteLLM)
	if err != nil {
		return nil, err
	}
	return &Coordinator{cfg: o.Config, cfgErr: o.ConfigErr, store: o.Store, runs: o.Runs, worker: o.Worker, prober: o.Prober, policy: o.Policy,
		health: routing.NewHealth(), router: router, now: time.Now, probeAge: 60 * time.Second, async: func(f func()) { go f() }, decider: o.Decider}, nil
}

// Config returns the effective configuration including discovered defaults.
func (c *Coordinator) config(ctx context.Context) (routing.Config, map[string]routing.AdapterStatus) {
	statuses := c.prober.Probe(ctx, c.probeAge)
	m := map[string]routing.AdapterStatus{}
	for _, s := range statuses {
		m[s.AdapterID] = s
	}
	return c.cfg.WithDiscoveredDefaults(statuses), m
}

// Refresh re-probes CLIs and drops cached router answers (after login/logout,
// CLI update, or a billing/policy change).
func (c *Coordinator) Refresh() {
	c.prober.Invalidate()
	c.router.Invalidate()
	c.verdicts.clear()
}

type ProfileView struct {
	Profile    routing.Profile                `json:"profile"`
	Manual     []routing.Exclusion            `json:"manualExcluded"`
	Warnings   []routing.Exclusion            `json:"manualWarnings"`
	Automatic  []routing.Exclusion            `json:"automaticExcluded"`
	Capability routing.Capabilities           `json:"capabilities"`
	Policy     map[string]routing.Observation `json:"policy"`
}

type Snapshot struct {
	Mode        routing.Mode            `json:"mode"`
	ConfigError string                  `json:"configError,omitempty"`
	Fingerprint string                  `json:"fingerprint"`
	Spend       routing.SpendPolicy     `json:"spend"`
	Switching   routing.SwitchPolicy    `json:"switching"`
	Adapters    []routing.AdapterStatus `json:"adapters"`
	Profiles    []ProfileView           `json:"profiles"`
	Tiers       map[string][]string     `json:"tiers"`
	RouteLLM    map[string]any          `json:"routellm"`
	Policy      routing.PolicyEvidence  `json:"policy"`
	Decider     DeciderView             `json:"decider"`
	// TierJudge is the local difficulty judge, when configured.
	TierJudge *routing.TierJudgeConfig `json:"tierJudge,omitempty"`
}

// Snapshot is the support matrix the UI renders.
func (c *Coordinator) Snapshot(ctx context.Context) Snapshot {
	cfg, st := c.config(ctx)
	s := Snapshot{Mode: cfg.Mode, Fingerprint: cfg.Fingerprint(), Spend: cfg.Spend, Switching: cfg.Switching, Policy: c.policy, Tiers: map[string][]string{}, RouteLLM: map[string]any{"enabled": cfg.RouteLLM.Enabled}, TierJudge: cfg.TierJudge}
	if c.cfgErr != nil {
		s.ConfigError = c.cfgErr.Error()
	}
	for _, a := range c.prober.Probe(ctx, c.probeAge) {
		if a.Version != "" {
			if validated, err := c.store.NoteVersion(a.AdapterID, a.Version); err == nil {
				a.ValidatedVersion = validated
				a.NeedsRevalidation = validated != a.Version
			}
		}
		s.Adapters = append(s.Adapters, a)
	}
	s.Decider = c.deciderView(ctx)
	manual := routing.Filter(cfg, st, c.policy, c.health, routing.Need{Kind: routing.KindCode})
	auto := routing.Filter(cfg, st, c.policy, c.health, routing.Need{Kind: routing.KindCode, Automatic: true})
	for i, p := range cfg.Profiles {
		v := ProfileView{Profile: p, Manual: manual[i].Excluded, Warnings: manual[i].Warnings, Automatic: auto[i].Excluded, Capability: p.Effective(), Policy: map[string]routing.Observation{}}
		for _, sc := range []routing.Scope{routing.ScopePersonalInteractive, routing.ScopePersonalAutomatic, routing.ScopeUnattended, routing.ScopeHostedMultiUser} {
			v.Policy[string(sc)] = c.policy.For(p.Adapter, sc)
		}
		s.Profiles = append(s.Profiles, v)
		for _, t := range p.Tiers {
			s.Tiers[t.String()] = append(s.Tiers[t.String()], p.ID)
		}
	}
	if cfg.RouteLLM.Enabled {
		s.RouteLLM["router"] = cfg.RouteLLM.Router
		s.RouteLLM["pairs"] = cfg.RouteLLM.Pairs
		hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if h, err := c.router.Health(hctx); err == nil {
			s.RouteLLM["health"] = h
		} else {
			s.RouteLLM["error"] = err.Error()
		}
	}
	return s
}

func (c *Coordinator) mode(st routing.RunState) routing.Mode {
	if st.Mode != "" {
		return st.Mode
	}
	return c.cfg.Mode
}

// Settings sets a Run's routing mode and pins.
func (c *Coordinator) Settings(runID string, mode routing.Mode, pinProfile, pinAdapter string) (routing.RunState, error) {
	if _, err := c.runs.Get(runID); err != nil {
		return routing.RunState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Settings(runID, mode, pinProfile, pinAdapter)
}

// Preview computes (but does not record) the decision for the Run's next attempt.
func (c *Coordinator) Preview(ctx context.Context, runID, manual string) (routing.Decision, error) {
	run, err := c.runs.Get(runID)
	if err != nil {
		return routing.Decision{}, err
	}
	st, err := c.store.Ensure(runID, c.cfg.Mode)
	if err != nil {
		return routing.Decision{}, err
	}
	in, err := c.decideInput(ctx, run, st, manual)
	if err != nil {
		return routing.Decision{}, err
	}
	d := routing.Decide(ctx, in)
	c.applyStrategy(ctx, run, st, c.mode(st), in, &d, startOpts{manual: manual, preview: true})
	return d, nil
}

func (c *Coordinator) decideInput(ctx context.Context, run orchestration.Run, st routing.RunState, manual string) (routing.DecideInput, error) {
	cfg, statuses := c.config(ctx)
	tl, err := c.store.Timeline(run.ID)
	if err != nil {
		return routing.DecideInput{}, err
	}
	var prior []routing.PriorAttempt
	for _, a := range tl.Attempts {
		if a.FinishedAt == "" {
			continue
		}
		p, _ := cfg.ProfileByID(a.ProfileID)
		pa := routing.PriorAttempt{ExecutionID: a.ExecutionID, ProfileID: a.ProfileID, Tier: p.MaxTier(), Class: a.Class}
		if a.Report != nil {
			pa.FailedChecks = a.Report.FailedChecks
			pa.Summary = routing.Summarize(strings.Join(a.Report.FailedChecks, ", ")+" "+a.Report.Detail, 300)
		}
		prior = append(prior, pa)
	}
	mode := c.mode(st)
	if manual == "" && st.PendingProfile != "" {
		manual = st.PendingProfile
	}
	if manual == "" && mode == routing.ModeManual {
		return routing.DecideInput{}, fmt.Errorf("%w: manual mode needs a profile", orchestration.ErrInvalid)
	}
	decideMode := mode
	if manual != "" {
		decideMode = routing.ModeManual
	}
	return routing.DecideInput{Config: cfg, Statuses: statuses, Policy: c.policy, Health: c.health, Router: c.router,
		Task: routing.DescribeTask(run.Prompt, routing.KindCode, prior), Mode: decideMode, Current: st.CurrentProfile,
		Manual: manual, PinProfile: st.PinProfile, PinAdapter: st.PinAdapter}, nil
}

type StartResult struct {
	ExecutionID string           `json:"executionId,omitempty"`
	Decision    routing.Decision `json:"decision"`
	State       routing.RunState `json:"state"`
	Launched    string           `json:"launchedProfile,omitempty"`
}

// Start is a user-initiated attempt (HTTP). manual names a profile or is empty.
func (c *Coordinator) Start(ctx context.Context, runID, manual string) (StartResult, error) {
	return c.start(ctx, runID, startOpts{manual: manual})
}

// Reevaluate starts an attempt and asks the decider even when the skip policy
// would not (explicit user request). Gates and budgets still apply.
func (c *Coordinator) Reevaluate(ctx context.Context, runID string) (StartResult, error) {
	return c.start(ctx, runID, startOpts{reevaluate: true})
}

var autoFrom = map[routing.Phase]bool{routing.PhaseCheckpointed: true}
var userFrom = map[routing.Phase]bool{routing.PhaseIdle: true, routing.PhaseCheckpointed: true, routing.PhaseFailed: true, routing.PhaseWaitUser: true,
	routing.PhaseWaitPolicy: true, routing.PhaseWaitBilling: true, routing.PhaseBlockedEnv: true, routing.PhaseReconcile: true}

type startOpts struct {
	manual     string
	auto       bool // automatic continuation (escalation/failover/requested switch)
	reevaluate bool // user explicitly asked the decider to re-evaluate
	failover   bool // previous attempt hit an availability failure
	preview    bool // compute the decision without any decider call
}

// precheck validates that a new attempt may start. Caller holds c.mu.
func (c *Coordinator) precheck(runID string, auto bool) (orchestration.Run, routing.RunState, routing.Mode, error) {
	run, err := c.runs.Get(runID)
	if err != nil {
		return run, routing.RunState{}, "", err
	}
	if run.State == "canceled" || run.State == "succeeded" {
		return run, routing.RunState{}, "", fmt.Errorf("%w: run is %s", orchestration.ErrConflict, run.State)
	}
	st, err := c.store.Ensure(runID, c.cfg.Mode)
	if err != nil {
		return run, st, "", err
	}
	mode := c.mode(st)
	if mode == routing.ModeOff {
		return run, st, mode, ErrRoutingOff
	}
	allowed := userFrom
	if auto {
		allowed = autoFrom
	}
	if !allowed[st.Phase] {
		return run, st, mode, fmt.Errorf("%w: routing phase %s does not accept a new attempt", orchestration.ErrConflict, st.Phase)
	}
	if c.worker.Busy() {
		return run, st, mode, fmt.Errorf("%w: another attempt owns the worker", orchestration.ErrConflict)
	}
	return run, st, mode, nil
}

func (c *Coordinator) start(ctx context.Context, runID string, o startOpts) (StartResult, error) {
	manual, auto := o.manual, o.auto
	c.mu.Lock()
	run, st, mode, err := c.precheck(runID, auto)
	c.mu.Unlock()
	if err != nil {
		return StartResult{State: st}, err
	}
	// Deciding runs without the coordinator lock: a decider call can take up
	// to its timeout. Everything that happens meanwhile (settings, cancel,
	// another start) bumps the epoch and the fence below discards this result.
	started := c.now()
	in, err := c.decideInput(ctx, run, st, manual)
	if err != nil {
		return StartResult{State: st}, err
	}
	d := routing.Decide(ctx, in)
	if c.applyStrategy(ctx, run, st, mode, in, &d, o) {
		// A decider failure revealed a limit/login problem on a shared bucket:
		// re-run the rules with that knowledge instead of launching into it.
		rec, strategy := d.Decider, d.Strategy
		d = routing.Decide(ctx, in)
		d.Decider, d.Strategy = rec, strategy
		d.Reason += "; recomputed after the decider call changed provider health"
	}
	routingMS := time.Since(started).Milliseconds()

	c.mu.Lock()
	defer c.mu.Unlock()
	if st, err = c.store.Transition(runID, st.Epoch, routing.PhaseRouting, causeFor(auto, manual), d.Reason, ""); err != nil {
		return StartResult{Decision: d}, err
	}
	if err := c.store.SaveDecision(runID, st.Epoch, d); err != nil {
		return StartResult{Decision: d, State: st}, err
	}
	if run2, err := c.runs.Get(runID); err != nil || run2.State == "canceled" {
		st, _ = c.store.Transition(runID, st.Epoch, routing.PhaseCanceled, "user_cancel", "run canceled while routing", "")
		return StartResult{Decision: d, State: st}, fmt.Errorf("%w: run was canceled", orchestration.ErrConflict)
	}

	cfg, _ := c.config(ctx)
	var profile routing.Profile
	if mode == routing.ModeShadow && manual == "" && st.PendingProfile == "" {
		// Shadow: record what routing would do; run exactly what the user asked.
		profile = routing.Profile{ID: "legacy:" + run.Provider, Adapter: run.Provider}
	} else if d.Selected == "" {
		wait := waitingPhase(d)
		st, _ = c.store.Transition(runID, st.Epoch, wait, "no_candidate", d.Reason, "")
		return StartResult{Decision: d, State: st}, fmt.Errorf("%w: %s", ErrNoProfile, d.Reason)
	} else {
		profile, _ = cfg.ProfileByID(d.Selected)
	}

	launch := orchestration.Launch{ProfileID: profile.ID, Provider: profile.Adapter, Model: profile.Model, Effort: profile.Effort, DecisionID: d.ID}
	tl, _ := c.store.Timeline(runID)
	var last *routing.AttemptRecord
	for i := len(tl.Attempts) - 1; i >= 0; i-- {
		if tl.Attempts[i].Report != nil && tl.Attempts[i].Report.Workspace != "" {
			last = &tl.Attempts[i]
			break
		}
	}
	cont := routing.PlanContinuation(tl.Bindings, profile, "", "", last != nil)
	handoffStart := c.now()
	if last != nil {
		if _, err := c.worker.ReadArtifact(runID, last.ExecutionID, "checkpoint.json"); err == nil {
			launch.InheritFrom = last.ExecutionID
		}
		bundle := c.bundle(run, last, profile)
		budget := 24 * 1024
		if ct := profile.Effective().ContextTokens; ct > 0 {
			// ~3 bytes/token, and at most a quarter of the window for handoff so
			// instructions, tool schemas and output keep their room.
			budget = ct * 3 / 4
		}
		text, err := bundle.Render(budget)
		if err != nil {
			st, _ = c.store.Transition(runID, st.Epoch, routing.PhaseWaitUser, "context_overflow", err.Error(), "")
			return StartResult{Decision: d, State: st}, err
		}
		launch.Handoff = text
		if st, err = c.store.Transition(runID, st.Epoch, routing.PhaseHandoff, "handoff", string(cont.Kind)+": "+cont.Reason, last.ExecutionID); err != nil {
			return StartResult{Decision: d, State: st}, err
		}
	}
	handoffMS := time.Since(handoffStart).Milliseconds()
	if !strings.HasPrefix(profile.ID, "legacy:") {
		if err := c.recheck(ctx, profile, d); err != nil {
			st, _ = c.store.Transition(runID, st.Epoch, routing.PhaseWaitUser, "state_changed", err.Error(), "")
			return StartResult{Decision: d, State: st}, err
		}
	}
	exec, err := c.worker.StartWith(runID, launch)
	if err != nil {
		st, _ = c.store.Transition(runID, st.Epoch, routing.PhaseFailed, "launch_refused", err.Error(), "")
		return StartResult{Decision: d, State: st}, err
	}
	if err := c.store.BeginAttempt(runID, st.Epoch, exec, profile, d.ID, launch.InheritFrom, cont); err != nil {
		// The worker already owns the slot; record the conflict loudly rather
		// than start anything else.
		log.Printf("routing: run %s attempt %s not recorded: %v", runID, exec, err)
	}
	c.timings.Store(exec, routing.Timings{RoutingMS: routingMS, HandoffMS: handoffMS})
	st, err = c.store.Transition(runID, st.Epoch, routing.PhaseRunning, "launched", profile.ID, exec)
	return StartResult{ExecutionID: exec, Decision: d, State: st, Launched: profile.ID}, err
}

func causeFor(auto bool, manual string) string {
	switch {
	case auto:
		return "automatic"
	case manual != "":
		return "user_selected"
	}
	return "user_start"
}

// waitingPhase maps the dominant exclusion to the phase the user must resolve.
func waitingPhase(d routing.Decision) routing.Phase {
	counts := map[routing.Phase]int{}
	for _, cand := range d.Candidates {
		for _, e := range cand.Excluded {
			switch e.Reason {
			case routing.RPolicyReview, routing.RPolicyBlocked:
				counts[routing.PhaseWaitPolicy]++
			case routing.RBillingUnknown, routing.RBillingNotAllowed, routing.RExtraUsageRisk, routing.RInheritedBillingEnv:
				counts[routing.PhaseWaitBilling]++
			case routing.RNotInstalled, routing.RWrongBinary, routing.REndpointUnavailable:
				counts[routing.PhaseBlockedEnv]++
			}
		}
	}
	best, n := routing.PhaseWaitUser, 0
	for _, p := range []routing.Phase{routing.PhaseBlockedEnv, routing.PhaseWaitBilling, routing.PhaseWaitPolicy} {
		if counts[p] > n {
			best, n = p, counts[p]
		}
	}
	return best
}

func (c *Coordinator) bundle(run orchestration.Run, last *routing.AttemptRecord, to routing.Profile) routing.Bundle {
	task := routing.DescribeTask(run.Prompt, routing.KindCode, nil)
	b := routing.Bundle{Version: 1, RunID: run.ID, FromExecution: last.ExecutionID, FromProfile: last.ProfileID, ToProfile: to.ID, CreatedAt: c.now().UTC(),
		Goal: run.Prompt, Constraints: task.Constraints, Stage: "handoff", BaseCommit: run.BaseCommit,
		Permissions: "Same as the previous attempt: edit files inside this workspace only. Switching models grants no new tools, paths, network destinations or spending."}
	if raw, err := c.worker.ReadArtifact(run.ID, last.ExecutionID, "checkpoint.json"); err == nil {
		var cp orchestration.Checkpoint
		if json.Unmarshal(raw, &cp) == nil {
			for _, f := range cp.Files {
				b.Manifest = append(b.Manifest, routing.FileEntry{Path: f.Path, Status: f.Status, SHA256: f.SHA256, Size: f.Size, Sensitive: f.Sensitive})
			}
			b.Excluded = cp.Excluded
		}
	}
	for _, e := range run.Executions {
		if e.ID != last.ExecutionID {
			continue
		}
		for _, ch := range e.Checks {
			b.Checks = append(b.Checks, routing.CheckResult{Name: ch.Name, Passed: ch.Passed, Detail: ch.Detail})
		}
	}
	var failed []string
	for _, ch := range b.Checks {
		if !ch.Passed {
			failed = append(failed, ch.Name)
		}
	}
	if last.Report != nil {
		b.PriorAnswer = last.Report.Answer
		b.FailedApproach = append(b.FailedApproach, fmt.Sprintf("%s (%s) ended as %s", last.ProfileID, last.ExecutionID, nonEmpty(string(last.Class), "unknown")))
	}
	if len(failed) > 0 {
		b.NextAction = "Make these failing host checks pass without breaking the others: " + strings.Join(failed, ", ") + ". Re-read the files you change; do not assume the previous model's description is accurate."
	} else {
		b.NextAction = "Continue the user goal from the current workspace state. Verify what is already done before changing it."
	}
	return b
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Interrupt asks for the current attempt to stop at once so a pending switch
// can apply. The next attempt starts only after quiescence is verified.
func (c *Coordinator) SwitchNow(runID, profile string) (routing.RunState, error) {
	c.mu.Lock()
	st, err := c.store.RequestSwitch(runID, profile, "now")
	c.mu.Unlock()
	if err != nil {
		return st, err
	}
	if err := c.worker.Interrupt(runID); err != nil {
		return st, fmt.Errorf("%w: no running attempt to interrupt; the switch applies to the next start", orchestration.ErrConflict)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, _ := c.store.Get(runID)
	if cur.Phase == routing.PhaseRunning {
		cur, _ = c.store.Transition(runID, cur.Epoch, routing.PhaseQuiescing, "switch_now", "user asked to switch to "+profile, cur.CurrentExecution)
	}
	return cur, nil
}

func (c *Coordinator) SwitchAtBoundary(runID, profile string) (routing.RunState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.RequestSwitch(runID, profile, "boundary")
}

// OnAttempt is the worker observer. It runs after the worker slot is free.
func (c *Coordinator) OnAttempt(r orchestration.AttemptResult) {
	if !r.Routed {
		return
	}
	cfg, _ := c.config(context.Background())
	c.mu.Lock()
	report := toReport(r)
	if t, ok := c.timings.LoadAndDelete(r.ExecutionID); ok {
		tm := t.(routing.Timings)
		report.Timings.RoutingMS, report.Timings.HandoffMS = tm.RoutingMS, tm.HandoffMS
	}
	st, err := c.store.Get(r.RunID)
	if err != nil {
		c.mu.Unlock()
		log.Printf("routing: attempt %s for unknown run state: %v", r.ExecutionID, err)
		return
	}
	report.Epoch = st.Epoch
	if st.CurrentExecution != r.ExecutionID {
		// Late or foreign result: record nothing, change nothing.
		c.mu.Unlock()
		log.Printf("routing: ignoring stale result %s (current %s)", r.ExecutionID, st.CurrentExecution)
		return
	}
	class := routing.Classify(report)
	switchNow := st.PendingWhen == "now" && st.PendingProfile != "" && r.ProviderStatus != "success"
	if switchNow && report.QuiesceVerified && !report.Canceled {
		// The interruption was requested; the outcome is known to be "stopped",
		// and the checkpoint below records what it left behind.
		class = routing.NoFailure
	}
	profile, _ := cfg.ProfileByID(r.Launch.ProfileID)
	switch class {
	case routing.AvailabilityFail:
		wait := routing.RetryAfter(report.Detail)
		if wait == 0 {
			wait = 5 * time.Minute
		}
		c.markHealth(profile, routing.RateLimited, c.now().Add(wait), routing.Summarize(report.Detail, 200))
	case routing.AuthFailure:
		c.markHealth(profile, routing.AuthExpired, time.Time{}, "sign in again through the official CLI")
		c.prober.Invalidate()
	case routing.EntitlementFail:
		c.markHealth(profile, routing.ModelNotEntitled, time.Time{}, routing.Summarize(report.Detail, 200))
	case routing.NoFailure, routing.QualityFailure:
		c.health.Clear(profile)
	}
	budget := routing.Budget{Switches: st.Switches, Attempts: st.Attempts, Pinned: st.PinProfile != "" || st.PinAdapter != ""}
	if t, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil {
		budget.StartedAt = t
	}
	act := routing.NextAction(c.mode(st), cfg.Switching, budget, class, c.now())
	if switchNow && class == routing.NoFailure && !report.RunSucceeded {
		act = routing.Action{Kind: routing.ActEscalate, Reason: "user requested an immediate switch to " + st.PendingProfile}
	}
	if err := c.store.FinishAttempt(report, class, act); err != nil {
		c.mu.Unlock()
		return // duplicate delivery
	}
	if r.ConversationID != "" {
		_ = c.store.PutBinding(routing.Binding{RunID: r.RunID, Adapter: r.Launch.Provider, AccountRef: profile.AccountRef, Workspace: r.Workspace, NativeID: r.ConversationID, ProfileID: r.Launch.ProfileID, ExecutionID: r.ExecutionID,
			WorkspaceRev: r.BaseCommit, Checkpoint: r.ExecutionID,
			// Each attempt runs in its own disposable worktree, so a native
			// session is never resumed across attempts; the handoff carries state.
			Resumable: false})
	}
	// running → quiescing → checkpointed | reconcile
	if st.Phase == routing.PhaseRunning {
		st, _ = c.store.Transition(r.RunID, st.Epoch, routing.PhaseQuiescing, "attempt_finished", r.ProviderStatus, r.ExecutionID)
	}
	if !report.QuiesceVerified {
		c.store.Transition(r.RunID, st.Epoch, routing.PhaseReconcile, "quiesce_unverified", report.QuiesceDetail, r.ExecutionID)
		c.mu.Unlock()
		return
	}
	st, _ = c.store.Transition(r.RunID, st.Epoch, routing.PhaseCheckpointed, "checkpointed", "", r.ExecutionID)
	var next routing.Phase
	switch {
	case report.Canceled:
		next = routing.PhaseCanceled
	case report.RunSucceeded:
		next = routing.PhaseSucceeded
	case act.Kind == routing.ActReconcile:
		next = routing.PhaseReconcile
	case act.Kind == routing.ActStop && class == routing.EnvironmentFail:
		next = routing.PhaseBlockedEnv
	case act.Kind == routing.ActStop:
		next = routing.PhaseFailed
	case act.Kind == routing.ActWaitUser:
		next = routing.PhaseWaitUser
	}
	if next != "" {
		c.store.Transition(r.RunID, st.Epoch, next, string(class), act.Reason, r.ExecutionID)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	// Escalate/failover/requested switch: a new attempt from the checkpointed
	// phase only. start() re-validates the Run state and every gate.
	c.async(func() {
		if _, err := c.start(context.Background(), r.RunID, startOpts{auto: true, failover: class == routing.AvailabilityFail}); err != nil {
			log.Printf("routing: automatic continuation for %s stopped: %v", r.RunID, err)
			c.mu.Lock()
			if cur, gerr := c.store.Get(r.RunID); gerr == nil && cur.Phase == routing.PhaseCheckpointed {
				c.store.Transition(r.RunID, cur.Epoch, routing.PhaseWaitUser, "continuation_failed", err.Error(), "")
			}
			c.mu.Unlock()
		}
	})
}

func toReport(r orchestration.AttemptResult) routing.AttemptReport {
	rep := routing.AttemptReport{RunID: r.RunID, ExecutionID: r.ExecutionID, ProfileID: r.Launch.ProfileID, Adapter: r.Launch.Provider,
		Canceled: r.RunState == "canceled", ProviderStatus: r.ProviderStatus, Detail: routing.Summarize(r.Diagnostics, 4000), Denials: r.Denials,
		RunSucceeded: r.RunState == "succeeded", EnvironmentErr: r.Stage == "prepare", QuiesceVerified: r.QuiesceVerified, QuiesceDetail: r.QuiesceDetail,
		ObservedModel: r.ObservedModel, Timings: routing.Timings{ExecMS: r.ExecMS, VerifyMS: r.VerifyMS}, Answer: routing.Summarize(r.Answer, 2000), Workspace: r.Workspace}
	for _, ch := range r.Checks {
		if ch.Name == "review" && !ch.Passed && strings.Contains(ch.Detail, "reviewer_unavailable:") {
			rep.ReviewUnavailable = true
		}
		if ch.Passed {
			rep.PassedChecks = append(rep.PassedChecks, ch.Name)
		} else {
			rep.FailedChecks = append(rep.FailedChecks, ch.Name)
		}
	}
	rep.Reviewer, rep.ReviewerModel = r.ReviewerLabel, r.ReviewerModel
	if u := r.ReviewUsage; u != nil {
		in, out := u.InputTokens, u.OutputTokens
		rep.ReviewUsage = &routing.UsageRecord{Scope: string(u.Scope), Source: "reported", InputTokens: &in, OutputTokens: &out}
	}
	if u := r.Usage; u != nil {
		ptr := func(v int) *int { return &v }
		rec := &routing.UsageRecord{Scope: string(u.Scope), Source: "reported", InputTokens: ptr(u.InputTokens), OutputTokens: ptr(u.OutputTokens),
			CacheRead: ptr(u.CacheReadInputTokens), CacheCreation: ptr(u.CacheCreationInputTokens), ThinkingTokens: u.ThinkingTokens, TotalTokens: u.TotalTokens,
			Local: r.Launch.Provider == routing.AdapterLocal}
		if rec.Scope == "" {
			rec.Scope = string(providers.TurnUsage)
		}
		rep.Usage = rec
	}
	return rep
}

// Timeline returns the Run's routing history.
func (c *Coordinator) Timeline(runID string) (routing.Timeline, error) {
	if _, err := c.runs.Get(runID); err != nil {
		return routing.Timeline{}, err
	}
	return c.store.Timeline(runID)
}

// MarkValidated records that the adapter was re-verified at its current version
// and drops cached router answers computed before.
func (c *Coordinator) MarkValidated(adapter string) error {
	c.router.Invalidate()
	c.verdicts.clear()
	return c.store.MarkValidated(adapter)
}

func (c *Coordinator) Forget(runID string) error { return c.store.DeleteRun(runID) }

// IsNotFound helps handlers map errors.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
