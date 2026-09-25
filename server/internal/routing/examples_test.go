package routing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The documented routing.json examples must parse and behave as described in
// docs/architecture/multimodel-routing.md.
func TestDocumentedExamples(t *testing.T) {
	pol := mustPolicy(t)
	load := func(name string) Config {
		f, err := os.Open(filepath.Join("..", "..", "..", "docs", "examples", "routing", name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c, err := ParseConfig(f)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return c
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "docs", "examples", "routing", "*.json"))
	if len(files) < 6 {
		t.Fatalf("examples missing: %v", files)
	}
	for _, f := range files {
		load(filepath.Base(f))
	}
	codex := ready(AdapterCodex, BillingSubscription)
	claude := ready(AdapterClaude, BillingSubscription)
	agy := ready(AdapterAntigravity, Unknown)
	decide := func(c Config, goal string, st map[string]AdapterStatus) Decision {
		return Decide(context.Background(), DecideInput{Config: c, Statuses: st, Policy: pol, Task: DescribeTask(goal, KindCode, nil), Mode: ModeAuto})
	}
	if d := decide(load("codex-only.json"), "refactor the scheduler architecture", statuses(codex)); d.Selected != "codex-sol-xhigh" {
		t.Fatalf("codex-only hard: %+v", d.Selected)
	}
	if d := decide(load("claude-only-single-profile.json"), "fix typo", statuses(claude)); d.Selected != "claude-opus-high" || d.Source != "only_candidate" {
		t.Fatalf("single profile: %+v", d)
	}
	c := load("codex-plus-claude.json")
	if d := decide(c, "refactor the scheduler architecture", statuses(codex, claude)); d.Selected != "codex-sol-xhigh" {
		t.Fatalf("two subscriptions hard: %+v", d.Selected)
	}
	for _, cand := range decide(c, "x", statuses(codex, claude)).Candidates {
		if cand.Profile.ID == "claude-fable-max" && !hasExcl(cand, RExtraUsageRisk) {
			t.Fatal("fable profile must be gated for extra-usage risk")
		}
	}
	if d := decide(load("google-only-gated.json"), "fix the bug", statuses(agy)); d.Selected != "" {
		t.Fatalf("google-only must not auto-run: %+v", d)
	}
	if d := decide(load("all-with-local.json"), "fix the bug", map[string]AdapterStatus{}); d.Selected != "" {
		t.Fatal("nothing installed must select nothing")
	}
	lo := load("local-only.json")
	st := map[string]AdapterStatus{"local:this-host": ready("local:this-host", BillingLocal)}
	if cs := Filter(lo, st, pol, nil, Need{Kind: KindText}); len(cs[0].Excluded) != 0 {
		t.Fatalf("local text manual: %+v", cs[0].Excluded)
	}
	if cs := Filter(lo, st, pol, nil, Need{Kind: KindCode}); !hasExcl(cs[0], RNeedsTools) {
		t.Fatal("local-only cannot take code tasks")
	}
}

func hasExcl(c Candidate, r Reason) bool {
	for _, e := range c.Excluded {
		if e.Reason == r {
			return true
		}
	}
	return false
}

// The commercial_llm examples cover all eight Claude/Codex/Local combinations
// (the "none" case is any example on a host with nothing installed).
func TestDeciderExamplesEightCombinations(t *testing.T) {
	pol := mustPolicy(t)
	load := func(name string) Config {
		f, err := os.Open(filepath.Join("..", "..", "..", "docs", "examples", "routing", name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c, err := ParseConfig(f)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return c
	}
	cases := []struct {
		file                 string
		claude, codex, local bool
		consult              bool
		skip                 SkipReason
		deciders             int
		reviewer             string
	}{
		{"decider-all.json", false, false, false, false, SkipNoCandidates, 0, ""},
		{"decider-claude-only.json", true, false, false, false, SkipRuleClear, 1, AdapterClaude},
		{"decider-codex-only.json", false, true, false, false, SkipRuleClear, 1, AdapterCodex},
		{"decider-claude-codex.json", true, true, false, true, "", 1, AdapterCodex},
		{"decider-local-only.json", false, false, true, false, SkipNoCandidates, 0, ""},
		{"decider-claude-local.json", true, false, true, false, SkipRuleClear, 1, AdapterClaude},
		{"decider-codex-local.json", false, true, true, false, SkipRuleClear, 1, AdapterCodex},
		{"decider-all.json", true, true, true, true, "", 1, AdapterCodex},
	}
	for _, tc := range cases {
		cfg := load(tc.file)
		st := map[string]AdapterStatus{}
		if tc.claude {
			st[AdapterClaude] = ready(AdapterClaude, BillingSubscription)
		}
		if tc.codex {
			st[AdapterCodex] = ready(AdapterCodex, BillingSubscription)
		}
		if tc.local {
			st["local:lan-gpu"] = ready("local:lan-gpu", BillingLocal)
		}
		d := Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Task: DescribeTask(mediumGoal, KindCode, nil), Mode: ModeAuto})
		deciders, _ := SelectDeciders(cfg, st, pol, nil, nil)
		consult, skip := PlanConsult(ConsultInput{Strategy: cfg.EffectiveStrategy(), Mode: ModeAuto, Distinct: Distinct(d.Candidates), RuleTie: len(d.RuleTie) > 1, Deciders: deciders})
		if consult != tc.consult || skip != tc.skip || len(deciders) != tc.deciders {
			t.Fatalf("%s %v/%v/%v: consult=%v skip=%s deciders=%v", tc.file, tc.claude, tc.codex, tc.local, consult, skip, deciders)
		}
		aux, _, err := ResolveAux(RoleReviewer, cfg, st, pol, nil, AdapterClaude)
		if (tc.reviewer == "") != (err != nil) || (err == nil && aux.Profile.Adapter != tc.reviewer) {
			t.Fatalf("%s reviewer %+v %v", tc.file, aux.Profile.ID, err)
		}
	}
}
