package routing

import (
	"context"
	"crypto/rand"
	"sort"
	"strings"
	"time"
)

// Decision is the structured record of one routing choice. It is persisted
// and shown to the user; there is no free-form model reasoning in it.
type Decision struct {
	ID                string         `json:"id"`
	Mode              Mode           `json:"mode"`
	Stage             string         `json:"stage"`
	Kind              TaskKind       `json:"kind"`
	RuleTier          Tier           `json:"ruleTier"`
	RuleWhy           []string       `json:"ruleWhy,omitempty"`
	Router            *RouterVerdict `json:"router,omitempty"`
	RouterPair        *RouterPair    `json:"routerPair,omitempty"`
	RouterError       string         `json:"routerError,omitempty"`
	Selected          string         `json:"selected,omitempty"`
	Source            string         `json:"source"` // manual | only_candidate | rule | routellm | sticky | none
	Reason            string         `json:"reason"`
	Candidates        []Candidate    `json:"candidates"`
	ConfigFingerprint string         `json:"configFingerprint"`
	CreatedAt         time.Time      `json:"createdAt"`
	// RuleTie lists distinct candidates that share the top rule score — the
	// only situation in which a decider call can add information.
	RuleTie []string `json:"ruleTie,omitempty"`
	// Decider is the commercial_llm strategy record (skip or call).
	Decider *DeciderRecord `json:"decider,omitempty"`
	// Strategy that settled this decision.
	Strategy   Strategy `json:"strategy,omitempty"`
	DurationMS int64    `json:"durationMs"`
}

type DecideInput struct {
	Config   Config
	Statuses map[string]AdapterStatus
	Policy   PolicyEvidence
	Health   *Health
	Router   *RouteLLMClient
	Task     Task
	Mode     Mode
	// Current is the profile of the previous attempt, if any (stickiness).
	Current string
	// Manual is the user's explicit profile choice (Manual mode, or a one-off
	// override). It is honored or refused, never substituted.
	Manual        string
	PinProfile    string
	PinAdapter    string
	ContextTokens int
}

// Decide never launches anything. In Shadow mode the caller records the
// decision and runs the existing provider unchanged.
func Decide(ctx context.Context, in DecideInput) Decision {
	start := time.Now()
	d := Decision{ID: "rd_" + rand.Text(), Mode: in.Mode, Stage: in.Task.Stage, Kind: in.Task.Kind, ConfigFingerprint: in.Config.Fingerprint(), CreatedAt: start.UTC()}
	defer func() { d.DurationMS = time.Since(start).Milliseconds() }()
	d.RuleTier, d.RuleWhy = RuleTier(in.Task)

	if in.Manual != "" {
		d.Candidates = Filter(in.Config, in.Statuses, in.Policy, in.Health, Need{Kind: in.Task.Kind, ContextTokens: in.ContextTokens, PinProfile: in.PinProfile, PinAdapter: in.PinAdapter, Role: RoleExecutor})
		for _, c := range d.Candidates {
			if c.Profile.ID != in.Manual {
				continue
			}
			if c.Eligible() {
				d.Selected, d.Source, d.Reason = c.Profile.ID, "manual", "user-selected profile"
				for _, w := range c.Warnings {
					d.Reason += "; warning: " + string(w.Reason)
				}
				if c.Profile.MaxTier() != TierUnset && c.Profile.MaxTier() < d.RuleTier {
					d.Reason += "; below rule floor " + d.RuleTier.String() + " (user choice honored)"
				}
			} else {
				d.Source, d.Reason = "none", "selected profile is unavailable: "+string(c.Excluded[0].Reason)
			}
			return d
		}
		d.Source, d.Reason = "none", "unknown profile "+in.Manual
		return d
	}

	need := Need{Kind: in.Task.Kind, MinTier: d.RuleTier, ContextTokens: in.ContextTokens, Automatic: true, PinProfile: in.PinProfile, PinAdapter: in.PinAdapter, Role: RoleExecutor}
	d.Candidates = Filter(in.Config, in.Statuses, in.Policy, in.Health, need)
	// Aliases of one executor count once.
	eligible := Distinct(d.Candidates)
	switch len(eligible) {
	case 0:
		// No silent downgrade: the user sees why and chooses.
		d.Source, d.Reason = "none", "no allowed profile satisfies "+d.RuleTier.String()+" for "+string(in.Task.Kind)
		return d
	case 1:
		d.Selected, d.Source, d.Reason = eligible[0].Profile.ID, "only_candidate", "single eligible profile; router not consulted"
		return d
	}

	stick := in.Config.Switching.Stickiness
	score := func(c Candidate) float64 {
		s := -float64(c.Profile.MaxTier() - d.RuleTier) // lowest adequate tier first
		if c.Profile.ID == in.Current {
			s += stick
		}
		if c.Profile.Quality[in.Task.Kind].Status == QualityVerified {
			s += 0.25
		}
		return s
	}
	sort.SliceStable(eligible, func(i, j int) bool { return score(eligible[i]) > score(eligible[j]) })
	best := eligible[0]
	for _, c := range eligible[1:] {
		if score(c) >= score(best)-1e-9 && c.Profile.Identity() != best.Profile.Identity() {
			d.RuleTie = append(d.RuleTie, c.Profile.ID)
		}
	}
	if len(d.RuleTie) > 0 {
		d.RuleTie = append([]string{best.Profile.ID}, d.RuleTie...)
	}
	d.Selected, d.Source, d.Reason = best.Profile.ID, "rule", "lowest adequate tier ("+best.Profile.MaxTier().String()+") for floor "+d.RuleTier.String()
	if best.Profile.ID == in.Current && len(eligible) > 1 && score(eligible[1]) > score(best)-stick {
		d.Source, d.Reason = "sticky", "kept current profile (switch cost outweighs tier difference)"
	}

	// RouteLLM may raise within a configured pair whose weak side is the rule
	// choice. It is skipped for evidence-based escalation, whose floor already
	// reflects an observed failure.
	if in.Router == nil || in.Task.Stage == "escalation" || in.Config.EffectiveStrategy() != StrategyRouteLLM {
		return d
	}
	ok := map[string]bool{}
	for _, c := range eligible {
		ok[c.Profile.ID] = true
	}
	for _, pair := range in.Config.RouteLLM.Pairs {
		if pair.Weak != d.Selected || !ok[pair.Strong] || (len(pair.Kinds) > 0 && !kindIn(pair.Kinds, in.Task.Kind)) {
			continue
		}
		p := pair
		d.RouterPair = &p
		v, err := in.Router.Score(ctx, RouterText(in.Task, 4096), pair, d.ConfigFingerprint)
		if err != nil {
			d.RouterError = err.Error()
			d.Reason += "; router unavailable, rule choice kept"
			return d
		}
		d.Router = &v
		if !strings.HasPrefix(pair.Calibration, "evaluated:") {
			// Advisory only: an unevaluated checkpoint/pair never changes what
			// runs. Measured on 2026-09-25 the stock BERT checkpoint separated
			// hard from easy coding tasks no better than chance (AUC 0.458).
			d.Reason += "; RouteLLM verdict recorded as advisory (pair not evaluated): " + v.Choice
			return d
		}
		if v.Choice == "strong" {
			d.Selected, d.Source = pair.Strong, "routellm"
			d.Reason = "RouteLLM predicts the stronger profile is needed (" + pair.Calibration + ")"
		} else {
			d.Source = "routellm"
			d.Reason = "RouteLLM predicts the rule choice is sufficient (" + pair.Calibration + ")"
		}
		return d
	}
	return d
}

func kindIn(ks []TaskKind, k TaskKind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}
