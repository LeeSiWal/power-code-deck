package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
)

func taskRepairFixture(t *testing.T) (*Store, Run, *Worker, string) {
	t.Helper()
	tasks := []taskgraph.Task{{ID: "a", Prompt: "a", Provider: "codex"}, {ID: "b", Prompt: "b", Provider: "codex"}, {ID: "join", Prompt: "join", Provider: "codex", DependsOn: []string{"a", "b"}}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(cwd, prompt string) error {
		if prompt == "join" {
			content, err := os.ReadFile(filepath.Join(cwd, "tracked.txt"))
			if err != nil || string(content) != "a+b\n" {
				return errors.New("provider did not receive resolved dependencies")
			}
			return os.WriteFile(filepath.Join(cwd, "joined.txt"), []byte("implementation\n"), 0600)
		}
		return os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte(prompt+"\n"), 0600)
	})
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[2].State != taskgraph.Failed {
		t.Fatal(p, err)
	}
	return s, r, w, p.Tasks[2].AttemptID
}

func TestTaskRepairRunsImplementationAndFinalVerification(t *testing.T) {
	s, r, w, first := taskRepairFixture(t)
	original, err := w.ReadArtifact(r.ID, first, "conflicts")
	if err != nil {
		t.Fatal(err)
	}
	request := repairRequest(t, w, r.ID, first, "a+b\n")
	if err := w.StartTaskResolution(r.ID, "join", request); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[2].State != taskgraph.Succeeded || p.Tasks[2].AttemptID == first || len(p.Tasks[2].Checks) != 2 {
		t.Fatal(p, err)
	}
	id := p.Tasks[2].AttemptID
	patch, err := w.ReadArtifact(r.ID, id, "changes.patch")
	if err != nil || !strings.Contains(string(patch), "+a+b") || !strings.Contains(string(patch), "+implementation") {
		t.Fatal("repair omitted from review evidence", string(patch), err)
	}
	input, err := s.Artifact(r.ID, id, "input_commit")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Artifact(r.ID, id, "result_commit")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := git(context.Background(), r.Path, "rev-parse", result.BaseCommit+"^")
	if err != nil || strings.TrimSpace(parent) != input.BaseCommit {
		t.Fatal("incremental result parent changed", parent, err)
	}
	old, err := w.ReadArtifact(r.ID, first, "conflicts")
	if err != nil || string(old) != string(original) {
		t.Fatal("old evidence changed", err)
	}
	if err := w.StartTaskResolution(r.ID, "join", request); !errors.Is(err, ErrConflict) {
		t.Fatal("stale repair accepted", err)
	}
	// Task-local input repairs are not silently applied to the final graph.
	final := failedIntegration(t, w, r.ID)
	finalRequest := repairRequest(t, w, r.ID, final, "a+b\n")
	finalID, err := w.StartConflictResolution(r.ID, finalRequest)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, err := s.Get(r.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal(got, err)
	}
	artifact, err := s.Artifact(r.ID, finalID, "result_commit")
	if err != nil {
		t.Fatal(err)
	}
	text, err := git(context.Background(), r.Path, "show", artifact.BaseCommit+":joined.txt")
	if err != nil || text != "implementation\n" {
		t.Fatal(text, err)
	}
	status, err := git(context.Background(), r.Path, "status", "--porcelain")
	if err != nil || status != "" {
		t.Fatal("source changed", status, err)
	}
}

func TestTaskRepairRejectsInvalidOrRetriedAttempts(t *testing.T) {
	s, r, w, first := taskRepairFixture(t)
	request := repairRequest(t, w, r.ID, first, "a+b\n")
	if err := w.StartTaskResolution(r.ID, "a", request); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign task accepted", err)
	}
	bad := request
	bad.Fingerprint = "forged"
	if err := w.StartTaskResolution(r.ID, "join", bad); err == nil {
		t.Fatal("forged fingerprint accepted")
	}
	bad = request
	bad.Files = []ResolvedFile{{Path: "../outside", Content: "edit"}}
	if err := w.StartTaskResolution(r.ID, "join", bad); err == nil {
		t.Fatal("path traversal accepted")
	}
	history, err := s.TaskAttempts(r.ID, "join", "")
	if err != nil || len(history.Attempts) != 1 {
		t.Fatal("invalid request allocated attempt", history, err)
	}
	if err := s.RetryPlannedTask(r.ID, "join"); err != nil {
		t.Fatal(err)
	}
	if err := w.StartTaskResolution(r.ID, "join", request); !errors.Is(err, ErrConflict) {
		t.Fatal("normal retry did not invalidate repair", err)
	}
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[2].State != taskgraph.Pending {
		t.Fatal(p, err)
	}
}

func TestTaskRepairRequiresIndependentReview(t *testing.T) {
	s, r, w, first := taskRepairFixture(t)
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"fail","summary":"repair loses behavior"}`, stopped: make(chan struct{})}, nil
	})
	if err := w.StartTaskResolution(r.ID, "join", repairRequest(t, w, r.ID, first, "a+b\n")); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[2].State != taskgraph.Failed {
		t.Fatal(p, err)
	}
	if _, err := s.Artifact(r.ID, p.Tasks[2].AttemptID, "result_commit"); err == nil {
		t.Fatal("unreviewed repair published")
	}
}

func TestTaskRepairCancellationAndCapacity(t *testing.T) {
	s, r, w, first := taskRepairFixture(t)
	entered, stopped := make(chan struct{}), make(chan struct{})
	w.factories["codex"] = func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, provider: providers.Codex, block: true, entered: entered, stopped: stopped}, nil
	}
	request := repairRequest(t, w, r.ID, first, "a+b\n")
	if err := w.StartTaskResolution(r.ID, "join", request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	if err := w.StartTaskResolution(r.ID, "join", request); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate admitted", err)
	}
	if err := w.StartPlan(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("shared slot bypassed", err)
	}
	if err := w.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	select {
	case <-stopped:
	default:
		t.Fatal("provider still running")
	}
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[2].State != taskgraph.Canceled {
		t.Fatal(p, err)
	}
	if err := s.FinishPlannedAttempt(p.Tasks[2].AttemptID, true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late success accepted", err)
	}
}

func TestTaskRepairReplaysEarlierInputResolutions(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Prompt: "a", Provider: "codex"}, {ID: "b", Prompt: "b", Provider: "codex"}, {ID: "c", Prompt: "c", Provider: "codex"}, {ID: "join", Prompt: "join", Provider: "codex", DependsOn: []string{"a", "b", "c"}}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(cwd, prompt string) error {
		if prompt == "join" {
			text, err := os.ReadFile(filepath.Join(cwd, "tracked.txt"))
			if err != nil || string(text) != "a+b+c\n" {
				return errors.New("incomplete resolved input")
			}
			return os.WriteFile(filepath.Join(cwd, "joined.txt"), []byte("done\n"), 0600)
		}
		return os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte(prompt+"\n"), 0600)
	})
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	for i, content := range []string{"a+b\n", "a+b+c\n"} {
		p, err := s.GetPlan(r.ID)
		if err != nil || p.Tasks[3].State != taskgraph.Failed {
			t.Fatal("later conflict was skipped", p, err)
		}
		attempt := p.Tasks[3].AttemptID
		if _, err := s.Artifact(r.ID, attempt, "input_commit"); err == nil {
			t.Fatal("implementation started before all conflicts resolved", i)
		}
		if err := w.StartTaskResolution(r.ID, "join", repairRequest(t, w, r.ID, attempt, content)); err != nil {
			t.Fatal(err)
		}
		waitWorker(t, w)
	}
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[3].State != taskgraph.Succeeded {
		t.Fatal(p, err)
	}
	history, err := s.TaskAttempts(r.ID, "join", "")
	if err != nil || len(history.Attempts) != 3 || history.Attempts[1].State != "failed" || history.Attempts[2].State != "failed" {
		t.Fatal(history, err)
	}
}
