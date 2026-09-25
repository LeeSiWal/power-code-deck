package runroute

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/routing"
)

// Choice is the profile picked for a new chat session.
type Choice struct {
	Decision routing.Decision `json:"decision"`
	Profile  *routing.Profile `json:"profile,omitempty"`
}

// Choose picks an executor profile for a chat session's first request. It runs
// the same gates as a Run in Auto mode (install, auth, billing, policy, tier)
// but never launches anything and never calls a paid decider: a rule tie keeps
// the rule order. The user asked for "자동" explicitly, so the config's default
// mode does not apply here.
func (c *Coordinator) Choose(ctx context.Context, goal string) (Choice, error) {
	if strings.TrimSpace(goal) == "" {
		return Choice{}, fmt.Errorf("%w: empty request", orchestration.ErrInvalid)
	}
	cfg, statuses := c.config(ctx)
	d := routing.Decide(ctx, routing.DecideInput{Config: cfg, Statuses: statuses, Policy: c.policy, Health: c.health, Router: c.router,
		Task: routing.DescribeTask(goal, routing.KindCode, nil), Mode: routing.ModeAuto})
	if d.Selected == "" {
		return Choice{Decision: d}, ErrNoProfile
	}
	p, ok := cfg.ProfileByID(d.Selected)
	if !ok {
		return Choice{Decision: d}, ErrNoProfile
	}
	return Choice{Decision: d, Profile: &p}, nil
}

// TurnIdleDowngrade is how long after the last turn a session may move DOWN to a
// cheaper model, or to another tool. Switching makes the new model re-read the
// conversation (caches are per model); past the prompt cache's lifetime (~5 min)
// staying would re-read it at full price anyway, so the move is then free.
// PCD_AUTO_IDLE_SECONDS overrides it (tuning, and end-to-end checks).
var TurnIdleDowngrade = idleFromEnv(5 * time.Minute)

func idleFromEnv(d time.Duration) time.Duration {
	if n, err := strconv.Atoi(os.Getenv("PCD_AUTO_IDLE_SECONDS")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return d
}

// TurnChoice is the profile a session's next turn should use, with the one it
// is on now (nil if that profile no longer exists in the config).
type TurnChoice struct {
	Choice
	Current *routing.Profile `json:"current,omitempty"`
}

// ChooseTurn re-routes a later message of an auto session, pinned to the tool
// the session already runs (moving between tools is a later stage). The current
// profile gets the configured stickiness, so near-ties stay put.
// noLocal leaves local-model profiles out (the user rejected a local answer).
func (c *Coordinator) ChooseTurn(ctx context.Context, goal, adapter, current string, noLocal bool) (TurnChoice, error) {
	if strings.TrimSpace(goal) == "" {
		return TurnChoice{}, fmt.Errorf("%w: empty request", orchestration.ErrInvalid)
	}
	full, statuses := c.config(ctx)
	cfg := full
	if noLocal {
		cfg = withoutLocal(full)
	}
	d := routing.Decide(ctx, routing.DecideInput{Config: cfg, Statuses: statuses, Policy: c.policy, Health: c.health, Router: c.router,
		Task: routing.DescribeTask(goal, routing.KindCode, nil), Mode: routing.ModeAuto, Current: current, PinAdapter: adapter})
	tc := TurnChoice{Choice: Choice{Decision: d}}
	// The current profile may be a local one that noLocal just left out.
	if cur, ok := full.ProfileByID(current); ok {
		tc.Current = &cur
	}
	if d.Selected == "" {
		return tc, ErrNoProfile
	}
	p, ok := cfg.ProfileByID(d.Selected)
	if !ok {
		return tc, ErrNoProfile
	}
	tc.Profile = &p
	return tc, nil
}

// TurnSwitch decides whether to move to next before this turn: up whenever the
// request needs a higher tier, down only once the session has been idle past
// TurnIdleDowngrade, never sideways between profiles of the same tier.
func TurnSwitch(cur *routing.Profile, next routing.Profile, idle time.Duration) (bool, string) {
	switch {
	case cur == nil:
		return true, "current_unknown"
	case next.ID == cur.ID:
		return false, "same"
	case next.MaxTier() > cur.MaxTier():
		return true, "harder"
	case next.MaxTier() == cur.MaxTier():
		return false, "same_tier"
	case idle >= TurnIdleDowngrade:
		return true, "idle_downgrade"
	}
	return false, "downgrade_deferred"
}

// withoutLocal drops local-model profiles (local adapter or local Codex).
func withoutLocal(cfg routing.Config) routing.Config {
	kept := make([]routing.Profile, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		if p.Adapter != routing.AdapterLocal && !p.IsLocalCodex() {
			kept = append(kept, p)
		}
	}
	cfg.Profiles = kept
	return cfg
}
