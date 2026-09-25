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

// Opt-in: two real implementations, two task reviews, one integration review.
// A frozen test plan isolates execution/integration from automatic planning.
func TestAntigravityPlanIntegrationLive(t *testing.T) {
	if os.Getenv("PCD_PLAN_LIVE") != "1" {
		t.Skip("set PCD_PLAN_LIVE=1 for authenticated planned integration")
	}
	ctx := context.Background()
	repo := testRepo(t)
	marker := "PCD_PLAN_" + rand.Text()
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
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("live-plan", repo, "Update tracked.txt to contain exactly "+marker+" followed by a newline. Create derived.txt containing that complete line followed by dependent and a newline. Modify no other files.", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	restrictions := " Use file tools only, not shell commands. Do not commit, install dependencies, or modify other files. Host verification runs separately."
	tasks := []taskgraph.Task{
		{ID: "seed", Provider: "antigravity", Prompt: "Change tracked.txt to contain exactly " + marker + " followed by a newline." + restrictions},
		{ID: "derive", Provider: "antigravity", DependsOn: []string{"seed"}, Prompt: "Read tracked.txt from the completed dependency. Create derived.txt containing its exact full contents followed by dependent and a newline. Leave tracked.txt unchanged." + restrictions},
	}
	if err := store.SavePlan(run.ID, TaskPlan{Tasks: tasks, Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	implementations := 0
	worker, err := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		implementations++
		if implementations == 2 {
			data, e := os.ReadFile(filepath.Join(cwd, "tracked.txt"))
			if e != nil || string(data) != marker+"\n" {
				return nil, fmt.Errorf("dependency input missing before second provider: %w", e)
			}
		}
		return antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "accept-edits"})
	}})
	if err != nil {
		t.Fatal(err)
	}
	reviews := 0
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		reviews++
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
		case <-time.After(6 * time.Minute):
			t.Fatalf("%s exceeded six minutes", stage)
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
	if err := worker.StartPlan(run.ID); err != nil {
		t.Fatal(err)
	}
	wait("plan")
	plan, err := store.GetPlan(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan.Tasks {
		logAttempt(task.ID, task.AttemptID, task.Checks)
	}
	got, err := store.Get(run.ID)
	if err != nil || got.State != "awaiting_integration" {
		t.Fatalf("plan state=%s err=%v tasks=%+v", got.State, err, plan.Tasks)
	}
	if implementations != 2 || reviews != 2 {
		t.Fatalf("unexpected provider counts %d/%d", implementations, reviews)
	}
	for _, task := range plan.Tasks {
		if task.State != taskgraph.Succeeded {
			t.Fatal(task)
		}
		input, e := worker.ReadArtifact(run.ID, task.AttemptID, "review_input")
		if e != nil || len(input) == 0 {
			t.Fatal("missing task review input", e)
		}
		if task.ID == "derive" {
			patch, e := worker.ReadArtifact(run.ID, task.AttemptID, "changes.patch")
			if e != nil || strings.Contains(string(patch), "diff --git a/tracked.txt") {
				t.Fatal("dependency duplicated in task patch", e)
			}
		}
	}
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
	if reviews != 3 || implementations != 2 {
		t.Fatalf("integration did not get fresh review: %d/%d", implementations, reviews)
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
	if _, err := worker.ReadArtifact(run.ID, id, "review_input"); err != nil {
		t.Fatal("missing integration review input", err)
	}
}

func verifyLivePlanFiles(root, marker string) error {
	data, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	if err != nil {
		return err
	}
	if string(data) != marker+"\n" {
		return fmt.Errorf("wrong seed contents")
	}
	derived, err := os.ReadFile(filepath.Join(root, "derived.txt"))
	if os.IsNotExist(err) {
		return nil
	} // first task; final test requires both files
	if err != nil {
		return err
	}
	if string(derived) != marker+"\ndependent\n" {
		return fmt.Errorf("derived file does not match dependency")
	}
	return nil
}

func TestLivePlanCheckRejectsWrongDependency(t *testing.T) {
	for _, tc := range []struct {
		seed, derived string
		present, ok   bool
	}{
		{"seed\n", "", false, true}, {"seed\n", "seed\ndependent\n", true, true},
		{"original\n", "", false, false}, {"seed\n", "original\ndependent\n", true, false},
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte(tc.seed), 0600); err != nil {
			t.Fatal(err)
		}
		if tc.present {
			if err := os.WriteFile(filepath.Join(root, "derived.txt"), []byte(tc.derived), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := verifyLivePlanFiles(root, "seed"); (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
}
