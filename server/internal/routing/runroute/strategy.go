package runroute

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/routing"
)

// verdictCache reuses a valid decider verdict for the same question. Any
// health, auth, config, validation or policy change clears it.
type verdictCache struct {
	mu sync.Mutex
	m  map[string]cachedDecision
}

type cachedDecision struct {
	v       routing.Verdict
	decider string
	exp     time.Time
}

func (vc *verdictCache) get(k string, now time.Time) (cachedDecision, bool) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	e, ok := vc.m[k]
	if !ok || now.After(e.exp) {
		return cachedDecision{}, false
	}
	return e, true
}

func (vc *verdictCache) put(k string, e cachedDecision) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	if vc.m == nil || len(vc.m) > 256 {
		vc.m = map[string]cachedDecision{}
	}
	vc.m[k] = e
}

func (vc *verdictCache) clear() {
	vc.mu.Lock()
	vc.m = nil
	vc.mu.Unlock()
}

// markHealth records a provider condition and drops every cached decision.
func (c *Coordinator) markHealth(p routing.Profile, st routing.Status, until time.Time, detail string) {
	c.health.Mark(p, st, until, detail)
	c.verdicts.clear()
	c.router.Invalidate()
}

func (c *Coordinator) strategyFor(runID string) (routing.Strategy, routing.RunOptions) {
	opts, _ := c.store.Options(runID)
	if opts.Strategy != "" {
		return opts.Strategy, opts
	}
	cfg, _ := c.currentConfig()
	return cfg.EffectiveStrategy(), opts
}

// applyStrategy runs the commercial_llm strategy when it is in effect: skip
// policy first, then at most one bounded consult, then code-side validation.
// The decider can only pick among candidates the rules already allowed.
// It reports whether a decider call changed provider health (429, expired
// login), in which case the caller recomputes the rule decision.
func (c *Coordinator) applyStrategy(ctx context.Context, run orchestration.Run, st routing.RunState, mode routing.Mode, in routing.DecideInput, d *routing.Decision, o startOpts) (healthChanged bool) {
	strategy, opts := c.strategyFor(run.ID)
	d.Strategy = strategy
	if strategy != routing.StrategyCommercialLLM {
		return false
	}
	cfg := in.Config
	distinct := routing.Distinct(d.Candidates)
	stats, _ := c.store.DeciderStats()
	deciders, _ := routing.SelectDeciders(cfg, in.Statuses, c.policy, c.health, stats)
	key := c.cacheKey(cfg, in, distinct)
	cached, hit := c.verdicts.get(key, c.now())
	shadowLeft := 0
	if mode == routing.ModeShadow {
		used, _ := c.store.ShadowCallsSince(c.now().Add(-24 * time.Hour))
		shadowLeft = cfg.Decider.ShadowMaxCallsPerDay - used
	}
	consult, skip := routing.PlanConsult(routing.ConsultInput{Strategy: strategy, Mode: mode, Manual: in.Manual != "" || in.PinProfile != "",
		Distinct: distinct, RuleTie: len(d.RuleTie) > 1, Stage: in.Task.Stage, Failover: o.failover, Forced: o.reevaluate,
		Cached: hit, Deciders: deciders, ShadowApproved: opts.CommercialShadow, ShadowLeft: shadowLeft})
	rec := routing.DeciderRecord{Skip: skip}
	deciderID := ""
	switch {
	case consult && o.preview:
		ids := make([]string, 0, len(deciders))
		for _, p := range deciders {
			ids = append(ids, p.ID)
		}
		rec.Note = "a decider call would be made with " + strings.Join(ids, ", ") + " (not made in preview)"
	case consult:
		var eligible []routing.Candidate
		for _, cand := range distinct {
			eligible = append(eligible, cand)
		}
		budget := map[string]int{"switchesLeft": cfg.Switching.MaxSwitchesPerRun - st.Switches, "attemptsLeft": cfg.Switching.MaxAttemptsPerRun - st.Attempts}
		req := routing.BuildDecisionRequest("dq_"+rand.Text(), run.ID, st.Epoch, in.Task, d.RuleTier, st.CurrentProfile, eligible, budget, nil)
		rec = routing.Consult(ctx, cfg.Decider, deciders, req, c.decider, c.moreEvidence(run))
		_ = c.store.SaveDeciderCalls(run.ID, d.ID, mode, rec)
		for _, call := range rec.Calls {
			p, _ := cfg.ProfileByID(call.ProfileID)
			switch call.ErrorClass {
			case "rate_limited":
				// Shared quota: the executor profiles in this bucket are limited too.
				c.markHealth(p, routing.RateLimited, c.now().Add(5*time.Minute), "observed on a decider call")
				healthChanged = true
			case "auth":
				c.markHealth(p, routing.AuthExpired, time.Time{}, "decider call reported an authentication failure")
				healthChanged = true
				c.prober.Invalidate()
			}
			if call.Valid {
				deciderID = call.ProfileID
			}
		}
		if rec.Verdict != nil && (rec.Verdict.Action == routing.ActDispatch || rec.Verdict.Action == routing.ActKeepCurrent) {
			c.verdicts.put(key, cachedDecision{v: *rec.Verdict, decider: deciderID, exp: c.now().Add(time.Duration(cfg.Decider.Limits().CacheMinutes) * time.Minute)})
		}
	case skip == routing.SkipCached:
		v := cached.v
		rec.Verdict, deciderID = &v, cached.decider
	}
	defer func() { d.Decider = &rec }()
	if rec.Verdict == nil {
		if rec.Consulted {
			d.Reason += "; decider gave no usable verdict, rule choice kept"
		}
		return
	}
	v := rec.Verdict
	if v.Action != routing.ActDispatch && v.Action != routing.ActKeepCurrent {
		d.Reason += "; decider " + v.Action + " (" + v.ReasonCode + "), rule choice kept"
		return
	}
	target := ""
	for _, cand := range distinct {
		if cand.Profile.ID == v.ProfileID {
			target = cand.Profile.ID
		}
	}
	if target == "" {
		rec.Note = "verdict names a profile that is no longer eligible"
		return
	}
	dp, _ := cfg.ProfileByID(deciderID)
	switch {
	case mode != routing.ModeAuto:
		rec.Note = "recorded only (" + string(mode) + " mode)"
	case dp.Quality[routing.KindDecide].Status != routing.QualityVerified:
		rec.Note = "advisory: decider quality for routing is unverified; rule choice kept"
	default:
		d.Selected, d.Source, rec.Applied = target, "commercial_llm", true
		d.Reason = "decider " + deciderID + " chose " + target + " (" + v.ReasonCode + ")"
	}
	return
}

func (c *Coordinator) cacheKey(cfg routing.Config, in routing.DecideInput, cands []routing.Candidate) string {
	ids := make([]string, 0, len(cands))
	for _, cand := range cands {
		ids = append(ids, cand.Profile.ID)
	}
	b, _ := json.Marshal([]any{cfg.Fingerprint(), ids, in.Task.Goal, in.Task.Stage, in.Current, len(in.Task.Prior)})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// moreEvidence answers need_context from records PCD already holds; it never
// explores the repository.
func (c *Coordinator) moreEvidence(run orchestration.Run) func([]string) []routing.EvidenceItem {
	return func(needs []string) []routing.EvidenceItem {
		tl, _ := c.store.Timeline(run.ID)
		var last *routing.AttemptRecord
		if n := len(tl.Attempts); n > 0 {
			last = &tl.Attempts[n-1]
		}
		var out []routing.EvidenceItem
		for _, need := range needs {
			switch need {
			case "prior_failure_detail":
				if last != nil && last.Report != nil {
					out = append(out, routing.EvidenceItem{Ref: "failure-1", Kind: need, Text: routing.Summarize(last.Report.Detail, 1500)})
				}
			case "check_logs":
				for _, e := range run.Executions {
					for _, ch := range e.Checks {
						if !ch.Passed && len(out) < 6 {
							out = append(out, routing.EvidenceItem{Ref: "check-" + ch.Name, Kind: need, Text: routing.Summarize(ch.Detail, 1500)})
						}
					}
				}
			case "changed_files":
				if last != nil {
					if raw, err := c.worker.ReadArtifact(run.ID, last.ExecutionID, "checkpoint.json"); err == nil {
						var cp orchestration.Checkpoint
						if json.Unmarshal(raw, &cp) == nil {
							var paths []string
							for _, f := range cp.Files {
								paths = append(paths, f.Status+" "+f.Path)
							}
							out = append(out, routing.EvidenceItem{Ref: "files-1", Kind: need, Text: routing.Summarize(strings.Join(paths, "\n"), 1500)})
						}
					}
				}
			}
		}
		return out
	}
}

// recheck re-evaluates the chosen profile right before launch: auth, quota,
// endpoint or policy may have changed while deciding.
func (c *Coordinator) recheck(ctx context.Context, p routing.Profile, d routing.Decision) error {
	cfg, statuses := c.config(ctx)
	need := routing.Need{Kind: routing.KindCode, Role: routing.RoleExecutor, Automatic: d.Source != "manual"}
	for _, cand := range routing.Filter(cfg, statuses, c.policy, c.health, need) {
		if cand.Profile.ID != p.ID {
			continue
		}
		if !cand.Eligible() {
			return fmt.Errorf("%w: %s changed state before launch: %s", ErrNoProfile, p.ID, cand.Excluded[0].Reason)
		}
		return nil
	}
	return fmt.Errorf("%w: %s is no longer configured", ErrNoProfile, p.ID)
}

// ResolveRole picks the reviewer/planner profile for a Run (see
// routing.ResolveAux). It launches nothing itself.
func (c *Coordinator) ResolveRole(role string, provider string) (routing.AuxChoice, error) {
	cfg, statuses := c.config(context.Background())
	choice, _, err := routing.ResolveAux(routing.Role(role), cfg, statuses, c.policy, c.health, provider)
	return choice, err
}

// RunView is the Run routing timeline plus per-Run options and usage by role.
type RunView struct {
	routing.Timeline
	Strategy        routing.Strategy    `json:"strategy"`
	Options         routing.RunOptions  `json:"options"`
	Usage           []routing.RoleUsage `json:"usage"`
	CommercialUsage routing.RoleUsage   `json:"commercialUsage"`
}

func (c *Coordinator) View(runID string) (RunView, error) {
	tl, err := c.Timeline(runID)
	if err != nil {
		return RunView{}, err
	}
	strategy, opts := c.strategyFor(runID)
	roles, total := routing.UsageByRole(tl)
	if roles == nil {
		roles = []routing.RoleUsage{}
	}
	return RunView{Timeline: tl, Strategy: strategy, Options: opts, Usage: roles, CommercialUsage: total}, nil
}

// SetOptions changes a Run's strategy and commercial-Shadow approval. Spend
// opt-ins and profiles are not settable here.
func (c *Coordinator) SetOptions(runID string, o routing.RunOptions) (RunView, error) {
	if _, err := c.runs.Get(runID); err != nil {
		return RunView{}, err
	}
	c.mu.Lock()
	if _, err := c.store.Ensure(runID, c.modeDefault()); err != nil {
		c.mu.Unlock()
		return RunView{}, err
	}
	err := c.store.SetOptions(runID, o)
	c.mu.Unlock()
	if err != nil {
		return RunView{}, fmt.Errorf("%w: %v", orchestration.ErrInvalid, err)
	}
	return c.View(runID)
}

// DeciderView is the Settings summary of the commercial_llm strategy.
type DeciderView struct {
	Strategy   routing.Strategy                `json:"strategy"`
	Limits     routing.DeciderConfig           `json:"limits"`
	Ordered    []string                        `json:"ordered"`
	Candidates []routing.Candidate             `json:"candidates"`
	Stats      map[string]routing.DeciderStats `json:"stats"`
	ShadowUsed int                             `json:"shadowCallsLast24h"`
	Callable   bool                            `json:"callable"`
	Roles      map[string]map[string]string    `json:"roles"` // role → run provider → label or "unavailable: …"
}

func (c *Coordinator) deciderView(ctx context.Context) DeciderView {
	cfg, statuses := c.config(ctx)
	stats, _ := c.store.DeciderStats()
	if stats == nil {
		stats = map[string]routing.DeciderStats{}
	}
	ordered, all := routing.SelectDeciders(cfg, statuses, c.policy, c.health, stats)
	v := DeciderView{Strategy: cfg.EffectiveStrategy(), Limits: cfg.Decider.Limits(), Candidates: all, Stats: stats, Callable: c.decider != nil, Roles: map[string]map[string]string{}}
	for _, p := range ordered {
		v.Ordered = append(v.Ordered, p.ID)
	}
	v.ShadowUsed, _ = c.store.ShadowCallsSince(c.now().Add(-24 * time.Hour))
	for _, role := range []routing.Role{routing.RoleReviewer, routing.RolePlanner} {
		m := map[string]string{}
		for _, provider := range []string{routing.AdapterClaude, routing.AdapterCodex, routing.AdapterAntigravity} {
			choice, _, err := routing.ResolveAux(role, cfg, statuses, c.policy, c.health, provider)
			if err != nil {
				m[provider] = "unavailable: " + err.Error()
			} else {
				m[provider] = choice.Label
			}
		}
		v.Roles[string(role)] = m
	}
	return v
}
