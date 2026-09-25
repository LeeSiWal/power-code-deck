package runroute

import (
	"context"
	"fmt"
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
// cheaper model. Switching models makes the new model re-read the conversation
// (caches are per model); past the prompt cache's lifetime (~5 min) staying would
// re-read it at full price anyway, so the downgrade is then free.
const TurnIdleDowngrade = 5 * time.Minute

// TurnChoice is the profile a session's next turn should use, with the one it
// is on now (nil if that profile no longer exists in the config).
type TurnChoice struct {
	Choice
	Current *routing.Profile `json:"current,omitempty"`
}

// ChooseTurn re-routes a later message of an auto session, pinned to the tool
// the session already runs (moving between tools is a later stage). The current
// profile gets the configured stickiness, so near-ties stay put.
func (c *Coordinator) ChooseTurn(ctx context.Context, goal, adapter, current string) (TurnChoice, error) {
	if strings.TrimSpace(goal) == "" {
		return TurnChoice{}, fmt.Errorf("%w: empty request", orchestration.ErrInvalid)
	}
	cfg, statuses := c.config(ctx)
	d := routing.Decide(ctx, routing.DecideInput{Config: cfg, Statuses: statuses, Policy: c.policy, Health: c.health, Router: c.router,
		Task: routing.DescribeTask(goal, routing.KindCode, nil), Mode: routing.ModeAuto, Current: current, PinAdapter: adapter})
	tc := TurnChoice{Choice: Choice{Decision: d}}
	if cur, ok := cfg.ProfileByID(current); ok {
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
