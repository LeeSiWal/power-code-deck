package orchestration

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
)

// checkGeneratedPlan rejects a draft that would make the live result meaningless:
// an empty or unconnected plan, or one whose prompts never mention what the
// request asked for. It does not judge task decomposition; the planner is free
// to choose any shape that still carries the requested work.
func checkGeneratedPlan(plan *TaskPlan, connected []string, required []string) error {
	if plan == nil {
		return fmt.Errorf("no plan generated")
	}
	if len(plan.Tasks) == 0 {
		return fmt.Errorf("plan has no tasks")
	}
	if plan.Concurrency < 1 {
		return fmt.Errorf("plan concurrency %d below one", plan.Concurrency)
	}
	if _, err := taskgraph.New(plan.Tasks); err != nil {
		return fmt.Errorf("plan graph invalid: %w", err)
	}
	allowed := map[string]bool{}
	for _, name := range connected {
		allowed[name] = true
	}
	var prompts strings.Builder
	for _, task := range plan.Tasks {
		if !allowed[task.Provider] {
			return fmt.Errorf("task %s uses unconnected provider %s", task.ID, task.Provider)
		}
		prompts.WriteString(task.Prompt)
		prompts.WriteString("\n")
	}
	for _, want := range required {
		if !strings.Contains(prompts.String(), want) {
			return fmt.Errorf("no task prompt mentions %q", want)
		}
	}
	return nil
}

func TestCheckGeneratedPlanRejectsUnusablePlans(t *testing.T) {
	connected := []string{"antigravity", "codex"}
	required := []string{"tracked.txt", "derived.txt"}
	ok := &TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{
		{ID: "a", Provider: "antigravity", Prompt: "rewrite tracked.txt"},
		{ID: "b", Provider: "codex", DependsOn: []string{"a"}, Prompt: "create derived.txt from it"},
	}}
	for _, tc := range []struct {
		name string
		plan *TaskPlan
		ok   bool
	}{
		{"valid", ok, true},
		{"nil", nil, false},
		{"empty", &TaskPlan{Concurrency: 1}, false},
		{"zero concurrency", &TaskPlan{Tasks: ok.Tasks}, false},
		{"cycle", &TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{
			{ID: "a", Provider: "antigravity", DependsOn: []string{"b"}, Prompt: "tracked.txt"},
			{ID: "b", Provider: "antigravity", DependsOn: []string{"a"}, Prompt: "derived.txt"},
		}}, false},
		{"unknown dependency", &TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{
			{ID: "a", Provider: "antigravity", DependsOn: []string{"ghost"}, Prompt: "tracked.txt derived.txt"},
		}}, false},
		{"unconnected provider", &TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{
			{ID: "a", Provider: "gemini", Prompt: "tracked.txt derived.txt"},
		}}, false},
		{"request dropped", &TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{
			{ID: "a", Provider: "antigravity", Prompt: "rewrite tracked.txt"},
		}}, false},
	} {
		err := checkGeneratedPlan(tc.plan, connected, required)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: ok=%v err=%v", tc.name, tc.ok, err)
		}
	}
}

// Opt-in: the planner turns a natural-language request into a plan, and that
// generated plan alone drives execution, review and integration. Nothing here
// freezes or edits the plan, so an unusable draft fails the test rather than
// being repaired. This is the coverage TestAntigravityPlanIntegrationLive
// deliberately skipped by starting from a frozen plan.
func TestAntigravityGeneratedPlanLive(t *testing.T) {
	if os.Getenv("PCD_PLANNER_LIVE") != "1" {
		t.Skip("set PCD_PLANNER_LIVE=1 for authenticated automatic planning")
	}
	ctx := context.Background()
	repo := testRepo(t)
	marker := "PCD_PLANNER_" + rand.Text()
	addCheckManifest(t, repo, "verify-plan:"+marker)
	originalHead, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		head, e := git(ctx, repo, "rev-parse", "HEAD")
		if e != nil || head != originalHead {
			t.Error("source HEAD changed", e)
		}
		status, e := git(ctx, repo, "status", "--porcelain")
		if e != nil || status != "" {
			t.Error("source dirtied", status, e)
		}
		data, e := os.ReadFile(filepath.Join(repo, "tracked.txt"))
		if e != nil || string(data) != "original\n" {
			t.Error("source contents changed", e)
		}
	})
	request := "Update tracked.txt to contain exactly " + marker + " followed by a newline. " +
		"Create derived.txt containing that complete line followed by dependent and a newline. " +
		"Modify no other files. Use file tools only, not shell commands. " +
		"Do not commit or install dependencies."
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("live-planner", repo, request, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	implementations, reviews := 0, 0
	worker, err := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		implementations++
		return antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "accept-edits"})
	}})
	if err != nil {
		t.Fatal(err)
	}
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		reviews++
		return antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "plan", Sandbox: true})
	})
	worker.SetPlanner(func(id, cwd string) (providers.Execution, error) {
		return antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "plan", Sandbox: true})
	})
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := worker.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	wait := func(stage string) {
		t.Helper()
		worker.mu.Lock()
		done := worker.done
		worker.mu.Unlock()
		select {
		case <-done:
		case <-time.After(15 * time.Minute):
			t.Fatalf("%s exceeded fifteen minutes", stage)
		}
	}
	logAttempt := func(label, id string, checks []Check) {
		t.Helper()
		for _, check := range checks {
			t.Logf("%s %s passed=%v: %s", label, check.Name, check.Passed, check.Detail)
		}
		if data, e := worker.ReadArtifact(run.ID, id, "review_log"); e == nil {
			if len(data) > 4096 {
				data = data[:4096]
			}
			t.Logf("%s review: %s", label, data)
		}
	}

	// Planning: the model alone produces the plan from the request text.
	if _, err := worker.StartPlanning(run.ID); err != nil {
		t.Fatal(err)
	}
	wait("planning")
	draft, err := store.GetPlanDraft(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.State != "succeeded" {
		t.Fatalf("planner draft %s: %s", draft.State, draft.Detail)
	}
	if err := checkGeneratedPlan(draft.Plan, []string{"antigravity"}, []string{marker, "tracked.txt", "derived.txt"}); err != nil {
		t.Fatalf("generated plan unusable: %v (%+v)", err, draft.Plan)
	}
	for _, task := range draft.Plan.Tasks {
		t.Logf("generated task %s provider=%s dependsOn=%v: %.512s", task.ID, task.Provider, task.DependsOn, task.Prompt)
	}
	t.Logf("generated plan: %d tasks, concurrency %d", len(draft.Plan.Tasks), draft.Plan.Concurrency)
	if implementations != 0 || reviews != 0 {
		t.Fatalf("planning started implementation or review: %d/%d", implementations, reviews)
	}

	// Execution: the generated plan is saved verbatim, with no manual repair.
	if err := store.SavePlan(run.ID, *draft.Plan); err != nil {
		t.Fatal(err)
	}
	if err := worker.StartPlan(run.ID); err != nil {
		t.Fatal(err)
	}
	wait("execution")
	plan, err := store.GetPlan(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan.Tasks {
		logAttempt(task.ID, task.AttemptID, task.Checks)
	}
	got, err := store.Get(run.ID)
	if err != nil || got.State != "awaiting_integration" {
		t.Fatalf("execution state=%s err=%v tasks=%+v", got.State, err, plan.Tasks)
	}
	for _, task := range plan.Tasks {
		if task.State != taskgraph.Succeeded {
			t.Fatal(task)
		}
		input, e := worker.ReadArtifact(run.ID, task.AttemptID, "review_input")
		if e != nil || len(input) == 0 {
			t.Fatal("missing task review input", e)
		}
	}
	if implementations < len(plan.Tasks) || reviews < len(plan.Tasks) {
		t.Fatalf("tasks did not each implement and review: %d/%d for %d tasks", implementations, reviews, len(plan.Tasks))
	}

	// Integration: a separate independent review over the combined result.
	beforeIntegration := reviews
	id, err := worker.StartIntegration(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait("integration")
	plan, err = store.GetPlan(run.ID)
	if err != nil || len(plan.Integrations) != 1 {
		t.Fatal(plan, err)
	}
	integration := plan.Integrations[0]
	logAttempt("integration", id, integration.Checks)
	got, err = store.Get(run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatalf("integration state=%s err=%v detail=%s", got.State, err, integration.Detail)
	}
	if reviews <= beforeIntegration {
		t.Fatalf("integration did not get a fresh review: %d", reviews)
	}
	for _, check := range integration.Checks {
		if !check.Passed {
			t.Fatal(check)
		}
	}
	result, err := store.Artifact(run.ID, id, "result_commit")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := git(ctx, repo, "rev-parse", "refs/powercodedeck/integrations/"+id)
	if err != nil || strings.TrimSpace(ref) != result.BaseCommit {
		t.Fatal("result not retained", err)
	}
	for name, want := range map[string]string{"tracked.txt": marker + "\n", "derived.txt": marker + "\ndependent\n"} {
		content, e := git(ctx, repo, "show", result.BaseCommit+":"+name)
		if e != nil || content != want {
			t.Fatalf("retained %s=%q want=%q err=%v", name, content, want, e)
		}
	}
	patch, err := worker.ReadArtifact(run.ID, id, "changes.patch")
	if err != nil || !strings.Contains(string(patch), "derived.txt") || !strings.Contains(string(patch), "+"+marker) {
		t.Fatal("missing combined patch", err)
	}
}
