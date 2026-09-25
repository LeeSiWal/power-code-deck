package routing

import (
	"fmt"
	"sort"
	"strings"
)

// AuxChoice is the resolved profile for an auxiliary role (reviewer, planner).
type AuxChoice struct {
	Profile Profile
	// SameProvider means the reviewer runs on the executor's provider in a
	// fresh context. It is a separate check, not a cross-vendor review.
	SameProvider bool
	Label        string
}

// ResolveAux picks an auxiliary profile under the same gates as executors:
//  1. a role pin from routing.json, if it passes the automatic gates;
//  2. any profile allowed that role under the automatic gates (a different
//     provider first, for independence);
//  3. the Run's own provider, which the user chose for this Run, under the
//     manual gates (warnings allowed, hard blocks still apply).
//
// Antigravity is therefore reachable only when the user picked it for the
// Run, never as a hidden default. No candidate → error; callers must surface a
// manual/blocked state instead of skipping the role.
func ResolveAux(role Role, cfg Config, statuses map[string]AdapterStatus, policy PolicyEvidence, health *Health, runProvider string) (AuxChoice, []Candidate, error) {
	kind := KindReview
	auto := Filter(cfg, statuses, policy, health, Need{Kind: kind, Automatic: true, Role: role})
	byID := map[string]Candidate{}
	for _, c := range auto {
		byID[c.Profile.ID] = c
	}
	label := func(p Profile, same bool) string {
		l := p.ID + " (" + p.Adapter
		if p.Model != "" {
			l += " " + p.Model
		}
		if same {
			return l + "; same provider as the executor, fresh context — not a cross-vendor review)"
		}
		return l + "; different provider from the executor)"
	}
	if pin := cfg.RolePins[role]; pin != "" {
		c, ok := byID[pin]
		if !ok || !c.Eligible() {
			return AuxChoice{}, auto, fmt.Errorf("pinned %s profile %q is not allowed: %s", role, pin, reasonsOf(c))
		}
		same := c.Profile.Adapter == runProvider
		return AuxChoice{Profile: c.Profile, SameProvider: same, Label: label(c.Profile, same)}, auto, nil
	}
	var eligible []Candidate
	for _, c := range Distinct(auto) {
		eligible = append(eligible, c)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return (eligible[i].Profile.Adapter != runProvider) && (eligible[j].Profile.Adapter == runProvider)
	})
	if len(eligible) > 0 {
		p := eligible[0].Profile
		same := p.Adapter == runProvider
		return AuxChoice{Profile: p, SameProvider: same, Label: label(p, same)}, auto, nil
	}
	if runProvider != "" {
		manual := Filter(cfg, statuses, policy, health, Need{Kind: kind, Role: role, PinAdapter: runProvider})
		for _, c := range Distinct(manual) {
			return AuxChoice{Profile: c.Profile, SameProvider: true, Label: label(c.Profile, true)}, manual, nil
		}
		auto = append(auto, manual...)
	}
	// One line per profile, first (automatic) view only, without the
	// bookkeeping reasons that say nothing about the profile itself.
	var why []string
	seen := map[string]bool{}
	for _, c := range auto {
		if c.Eligible() || seen[c.Profile.ID] {
			continue
		}
		seen[c.Profile.ID] = true
		var rs []string
		for _, e := range c.Excluded {
			if e.Reason != RPinned && e.Reason != RNotAllowlisted {
				rs = append(rs, string(e.Reason))
			}
		}
		if len(rs) > 0 {
			why = append(why, c.Profile.ID+": "+strings.Join(rs, ","))
		}
	}
	if len(why) == 0 {
		why = append(why, "no profile allows the "+string(role)+" role")
	}
	return AuxChoice{}, auto, fmt.Errorf("%s", strings.Join(why, "; "))
}

func reasonsOf(c Candidate) string {
	var rs []string
	for _, e := range c.Excluded {
		rs = append(rs, string(e.Reason))
	}
	if len(rs) == 0 {
		return "not configured"
	}
	return strings.Join(rs, ",")
}
