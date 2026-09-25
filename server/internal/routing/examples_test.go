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
