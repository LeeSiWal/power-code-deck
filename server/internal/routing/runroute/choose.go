package runroute

import (
	"context"
	"fmt"
	"strings"

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
