package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
)

// verifyLiveMergeFile accepts either branch's isolated result or the merged
// result, so one project check can gate the parallel tasks and the repaired
// integration. Anything else fails, so a wrong edit is still caught.
func verifyLiveMergeFile(root, marker string) error {
	data, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	if err != nil {
		return err
	}
	switch string(data) {
	case marker + "-alpha\n", marker + "-bravo\n", marker + "-alpha\n" + marker + "-bravo\n":
		return nil
	}
	return &mergeCheckError{got: string(data)}
}

type mergeCheckError struct{ got string }

func (e *mergeCheckError) Error() string { return "unexpected tracked.txt contents: " + e.got }

func TestLiveMergeCheckAcceptsOnlyBranchOrMergedResults(t *testing.T) {
	for _, tc := range []struct {
		content string
		ok      bool
	}{
		{"seed-alpha\n", true},
		{"seed-bravo\n", true},
		{"seed-alpha\nseed-bravo\n", true},
		{"seed-bravo\nseed-alpha\n", false},
		{"original\n", false},
		{"seed-alpha\nseed-bravo\nextra\n", false},
		{"", false},
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyLiveMergeFile(root, "seed"); (err == nil) != tc.ok {
			t.Fatalf("%q: ok=%v err=%v", tc.content, tc.ok, err)
		}
	}
	if err := verifyLiveMergeFile(t.TempDir(), "seed"); err == nil {
		t.Fatal("a missing file must fail the check")
	}
}

// concurrencyProbe records how many provider executions were alive at once, so
// the test can prove tasks really overlapped rather than assuming they did.
type concurrencyProbe struct {
	mu      sync.Mutex
	live    int
	highest int
}

func (p *concurrencyProbe) wrap(inner providers.Execution) providers.Execution {
	p.mu.Lock()
	p.live++
	if p.live > p.highest {
		p.highest = p.live
	}
	p.mu.Unlock()
	return &probedExecution{Execution: inner, probe: p}
}

func (p *concurrencyProbe) peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.highest
}

type probedExecution struct {
	providers.Execution
	probe *concurrencyProbe
	once  sync.Once
}

func (e *probedExecution) Stop() {
	e.once.Do(func() {
		e.probe.mu.Lock()
		e.probe.live--
		e.probe.mu.Unlock()
	})
	e.Execution.Stop()
}

// Opt-in: two independent tasks edit the same file concurrently, so their
// verified results genuinely conflict at integration. The conflict is then
// repaired with an explicit resolution and re-verified. Earlier live coverage
// used concurrency one and never produced a real conflict.
func TestAntigravityParallelConflictRepairLive(t *testing.T) {
	if os.Getenv("PCD_CONFLICT_LIVE") != "1" {
		t.Skip("set PCD_CONFLICT_LIVE=1 for authenticated parallel conflict repair")
	}
	ctx := context.Background()
	repo := testRepo(t)
	marker := "PCD_MERGE_" + rand.Text()
	addCheckManifest(t, repo, "verify-merge:"+marker)
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
	run, err := store.Create("live-conflict", repo, "Two independent edits to the same file.", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	restrictions := " Use file tools only, not shell commands. Do not commit, install dependencies, or modify other files. Host verification runs separately."
	tasks := []taskgraph.Task{
		{ID: "alpha", Provider: "antigravity", Prompt: "Change tracked.txt to contain exactly " + marker + "-alpha followed by a newline." + restrictions},
		{ID: "bravo", Provider: "antigravity", Prompt: "Change tracked.txt to contain exactly " + marker + "-bravo followed by a newline." + restrictions},
	}
	// No dependency between the tasks, so the scheduler may run both at once.
	if err := store.SavePlan(run.ID, TaskPlan{Tasks: tasks, Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	probe := &concurrencyProbe{}
	implementations := 0
	worker, err := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		implementations++
		inner, err := antigravity.New(id, antigravity.Config{Cwd: cwd, Mode: "accept-edits"})
		if err != nil {
			return nil, err
		}
		return probe.wrap(inner), nil
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
		case <-time.After(15 * time.Minute):
			t.Fatalf("%s exceeded fifteen minutes", stage)
		}
	}
	logAttempt := func(label, id string, checks []Check) {
		t.Helper()
		for _, check := range checks {
			t.Logf("%s %s passed=%v: %s", label, check.Name, check.Passed, check.Detail)
		}
	}

	// Parallel execution: both tasks must pass independently in isolation.
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
		if task.State != taskgraph.Succeeded {
			t.Fatal(task)
		}
	}
	got, err := store.Get(run.ID)
	if err != nil || got.State != "awaiting_integration" {
		t.Fatalf("execution state=%s err=%v tasks=%+v", got.State, err, plan.Tasks)
	}
	if implementations != 2 || reviews != 2 {
		t.Fatalf("unexpected provider counts %d/%d", implementations, reviews)
	}
	if peak := probe.peak(); peak < 2 {
		t.Fatalf("tasks never overlapped; peak concurrency was %d", peak)
	}
	t.Logf("peak concurrent implementations: %d", probe.peak())

	// Integration must detect the real conflict rather than pick a side.
	reviewsBeforeIntegration := reviews
	first, err := worker.StartIntegration(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait("integration")
	got, err = store.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State == "succeeded" {
		t.Fatal("conflicting parallel results integrated without a conflict")
	}
	raw, err := worker.ReadArtifact(run.ID, first, "conflicts")
	if err != nil {
		t.Fatal("missing conflict evidence", err)
	}
	var report ConflictReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 1 || report.Files[0].Path != "tracked.txt" || report.Fingerprint == "" {
		t.Fatalf("unexpected conflict report %+v", report)
	}
	t.Logf("conflict on %s, fingerprint %s", report.Files[0].Path, report.Fingerprint)
	// A conflicted merge is rejected before a reviewer is ever started, so the
	// failed attempt must not have spent a model call.
	if reviews != reviewsBeforeIntegration {
		t.Fatalf("conflicted integration started %d reviews", reviews-reviewsBeforeIntegration)
	}

	// Explicit repair: the merged content is supplied, then re-verified.
	merged := marker + "-alpha\n" + marker + "-bravo\n"
	second, err := worker.StartConflictResolution(run.ID, ResolutionRequest{
		SourceAttempt: first,
		Fingerprint:   report.Fingerprint,
		Files:         []ResolvedFile{{Path: "tracked.txt", Content: merged}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wait("repair")
	plan, err = store.GetPlan(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, integration := range plan.Integrations {
		logAttempt("integration "+integration.ID, integration.ID, integration.Checks)
	}
	got, err = store.Get(run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatalf("repaired integration state=%s err=%v", got.State, err)
	}
	if reviews != reviewsBeforeIntegration+1 {
		t.Fatalf("repair needs exactly one fresh independent review, got %d", reviews-reviewsBeforeIntegration)
	}
	if implementations != 2 {
		t.Fatalf("repair re-ran implementation providers: %d", implementations)
	}
	result, err := store.Artifact(run.ID, second, "result_commit")
	if err != nil {
		t.Fatal(err)
	}
	content, err := git(ctx, repo, "show", result.BaseCommit+":tracked.txt")
	if err != nil || content != merged {
		t.Fatalf("retained tracked.txt=%q want=%q err=%v", content, merged, err)
	}
	ref, err := git(ctx, repo, "rev-parse", "refs/powercodedeck/integrations/"+second)
	if err != nil || strings.TrimSpace(ref) != result.BaseCommit {
		t.Fatal("repaired result not retained", err)
	}
	// The failed attempt's evidence must survive the successful repair.
	if _, err := worker.ReadArtifact(run.ID, first, "conflicts"); err != nil {
		t.Fatal("conflict evidence lost after repair", err)
	}
}
