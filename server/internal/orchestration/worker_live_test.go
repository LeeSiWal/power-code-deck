package orchestration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
)

// Opt-in: needs installed/authenticated agy and consumes model usage. All source
// edits happen in disposable test repositories through the production Worker.
func TestAntigravityRunLive(t *testing.T) {
	if os.Getenv("PCD_RUN_LIVE") != "1" {
		t.Skip("set PCD_RUN_LIVE=1 for authenticated worker integration")
	}
	repo := testRepo(t)
	addCheckManifest(t, repo, "verify-change")
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("live", repo, "Change tracked.txt to contain exactly PCD_RUN_LIVE_OK followed by a newline. Do not change any other file, do not commit, do not install anything. This is a minimal integration test.", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		return antigravity.New(id, antigravity.Config{Cwd: cwd})
	}})
	if err != nil {
		t.Fatal(err)
	}
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "plan", Sandbox: true, JSONSchema: ReviewJSONSchema})
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Close(ctx)
	}()
	execution, err := worker.Start(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	worker.mu.Lock()
	done := worker.done
	worker.mu.Unlock()
	select {
	case <-done:
	case <-time.After(6 * time.Minute):
		t.Fatal("live run exceeded six minutes")
	}
	got, err := store.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range got.Executions[0].Checks {
		t.Logf("%s passed=%v: %s", check.Name, check.Passed, check.Detail)
	}
	if got.State != "succeeded" {
		t.Fatalf("live run state=%s outcome=%s", got.State, got.Executions[0].Detail)
	}
	patch, err := worker.ReadArtifact(run.ID, execution, "changes.patch")
	if err != nil || !strings.Contains(string(patch), "+PCD_RUN_LIVE_OK") {
		t.Fatal("missing verified patch", err)
	}
	original, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil || string(original) != "original\n" {
		t.Fatal("source checkout changed", err)
	}
}
