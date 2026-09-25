package taskgraph

import "testing"

func plan() []Task {
	return []Task{
		{ID: "api", Prompt: "Implement API", Provider: "codex"},
		{ID: "ui", Prompt: "Implement UI", Provider: "claude"},
		{ID: "integration", Prompt: "Integrate changes", Provider: "codex", DependsOn: []string{"api", "ui"}},
		{ID: "review", Prompt: "Review integration", Provider: "antigravity", DependsOn: []string{"integration"}},
	}
}

func TestVerifiedDependenciesAndConcurrency(t *testing.T) {
	g, err := New(plan())
	if err != nil {
		t.Fatal(err)
	}
	first, err := g.Select(nil, 2)
	if err != nil || len(first.Ready) != 2 || first.Ready[0].ID != "api" || first.Complete {
		t.Fatal(first, err)
	}
	states := map[string]State{"api": Succeeded, "ui": Verifying}
	next, err := g.Select(states, 2)
	if err != nil || len(next.Ready) != 0 {
		t.Fatal("unverified dependency released integration", next, err)
	}
	states["ui"] = Succeeded
	next, err = g.Select(states, 2)
	if err != nil || len(next.Ready) != 1 || next.Ready[0].ID != "integration" {
		t.Fatal(next, err)
	}
	states["integration"] = Failed
	next, err = g.Select(states, 2)
	if err != nil || len(next.Blocked) != 1 || next.Blocked[0] != "review" || next.Complete {
		t.Fatal(next, err)
	}
	states["integration"] = Succeeded
	states["review"] = Succeeded
	next, err = g.Select(states, 2)
	if err != nil || !next.Complete {
		t.Fatal(next, err)
	}
}

func TestFailureBlocksDescendantsButIndependentTasksContinue(t *testing.T) {
	g, _ := New(plan())
	next, err := g.Select(map[string]State{"api": Failed}, 1)
	if err != nil || len(next.Blocked) != 2 || len(next.Ready) != 1 || next.Ready[0].ID != "ui" {
		t.Fatal(next, err)
	}
	next, err = g.Select(map[string]State{"api": Running}, 1)
	if err != nil || len(next.Ready) != 0 {
		t.Fatal("concurrency exceeded", next, err)
	}
}

func TestRejectInvalidPlansAndState(t *testing.T) {
	for _, mutate := range []func([]Task) []Task{
		func(p []Task) []Task { return nil },
		func(p []Task) []Task { p[1].ID = p[0].ID; return p },
		func(p []Task) []Task { p[0].Provider = "unknown"; return p },
		func(p []Task) []Task { p[0].Prompt = " "; return p },
		func(p []Task) []Task { p[0].DependsOn = []string{"missing"}; return p },
		func(p []Task) []Task { p[0].DependsOn = []string{"api"}; return p },
		func(p []Task) []Task { p[0].DependsOn = []string{"review"}; return p },
		func(p []Task) []Task { p[2].DependsOn = []string{"api", "api"}; return p },
	} {
		if _, err := New(mutate(plan())); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	g, _ := New(plan())
	if _, err := g.Select(map[string]State{"unknown": Succeeded}, 1); err == nil {
		t.Fatal("unknown task accepted")
	}
	if _, err := g.Select(map[string]State{"api": "made-up"}, 1); err == nil {
		t.Fatal("unknown state accepted")
	}
	if _, err := g.Select(nil, 0); err == nil {
		t.Fatal("invalid concurrency accepted")
	}
}

func TestGraphOwnsValidatedInput(t *testing.T) {
	p := plan()
	g, _ := New(p)
	p[2].DependsOn[0] = "missing"
	states := map[string]State{"api": Succeeded, "ui": Succeeded}
	s, _ := g.Select(states, 1)
	s.Ready[0].DependsOn[0] = "missing"
	s, err := g.Select(states, 1)
	if err != nil || len(s.Ready) != 1 || s.Ready[0].DependsOn[0] != "api" {
		t.Fatal("validated graph mutated", s, err)
	}
}
