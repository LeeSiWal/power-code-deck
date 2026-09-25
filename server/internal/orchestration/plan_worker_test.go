package orchestration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
)

type plannedFake struct {
	*fakeExecution
	edit func(string, string) error
}

func TestPlanWorkerCancelAndSharedRunSlot(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(string, string) error { return nil })
	entered, stopped := make(chan struct{}), make(chan struct{})
	w.factories["codex"] = func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, provider: providers.Codex, block: true, entered: entered, stopped: stopped}, nil
	}
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	if _, err := w.Start(r.ID); err != ErrConflict {
		t.Fatal("legacy execution entered occupied slot", err)
	}
	if err := w.StartPlan(r.ID); err != ErrConflict {
		t.Fatal("duplicate scheduler", err)
	}
	p, _ := s.GetPlan(r.ID)
	attempt := p.Tasks[0].AttemptID
	if err := w.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	select {
	case <-stopped:
	default:
		t.Fatal("provider not stopped")
	}
	p, _ = s.GetPlan(r.ID)
	got, _ := s.Get(r.ID)
	if got.State != "canceled" || p.Tasks[0].State != taskgraph.Canceled {
		t.Fatal(got, p)
	}
	if err := s.FinishPlannedAttempt(attempt, true, "late"); err != ErrConflict {
		t.Fatal("late result accepted", err)
	}
	if err := s.AddPlannedArtifact(attempt, "result_commit", "", "forged"); err != ErrConflict {
		t.Fatal("late artifact accepted", err)
	}
}

func TestPlanWorkerRequiresChecksAndReviewer(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(string, string) error { return nil })
	reviewer := w.reviewer
	w.SetReviewer(nil)
	if err := w.StartPlan(r.ID); err == nil {
		t.Fatal("missing reviewer accepted")
	}
	w.SetReviewer(reviewer)
	if err := os.Remove(filepath.Join(r.Path, checkManifest)); err != nil {
		t.Fatal(err)
	}
	if err := w.StartPlan(r.ID); err == nil {
		t.Fatal("missing checks accepted")
	}
	p, _ := s.GetPlan(r.ID)
	if p.Tasks[0].AttemptID != "" {
		t.Fatal("preflight failure claimed a task")
	}
}

func TestPlanWorkerRejectsReviewMutation(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(cwd, _ string) error {
		return os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("edit\n"), 0600)
	})
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"checked"}`, reviewChange: "unreviewed edit\n", stopped: make(chan struct{})}, nil
	})
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, _ := s.GetPlan(r.ID)
	if p.Tasks[0].State != taskgraph.Failed || !strings.Contains(p.Tasks[0].Detail, "reviewer changed") {
		t.Fatal(p)
	}
	if _, err := s.Artifact(r.ID, p.Tasks[0].AttemptID, "result_commit"); err == nil {
		t.Fatal("mutated review result published")
	}
}

func (e *plannedFake) Send(prompt string) error { return e.edit(e.cwd, prompt) }

func planWorkerFixture(t *testing.T, checks string, tasks []taskgraph.Task, edit func(string, string) error) (*Store, Run, *Worker) {
	t.Helper()
	repo := testRepo(t)
	addCheckManifest(t, repo, checks)
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r, err := s.Create("plan", repo, "planned change", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(r.ID, TaskPlan{Tasks: tasks, Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	factory := func(id, cwd string) (providers.Execution, error) {
		return &plannedFake{fakeExecution: &fakeExecution{id: id, cwd: cwd, provider: providers.Codex, stopped: make(chan struct{})}, edit: edit}, nil
	}
	w, err := NewWorker(s, t.TempDir(), map[string]Factory{"codex": factory})
	if err != nil {
		t.Fatal(err)
	}
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"checked"}`, stopped: make(chan struct{})}, nil
	})
	return s, r, w
}

func TestPlanWorkerDiamondTransfersNewFilesOnce(t *testing.T) {
	tasks := []taskgraph.Task{
		{ID: "root", Provider: "codex", Prompt: "root"},
		{ID: "left", Provider: "codex", Prompt: "left", DependsOn: []string{"root"}},
		{ID: "right", Provider: "codex", Prompt: "right", DependsOn: []string{"root"}},
		{ID: "join", Provider: "codex", Prompt: "join", DependsOn: []string{"left", "right"}},
	}
	edit := func(cwd, prompt string) error {
		required := []string{}
		if prompt != "root" {
			required = append(required, "root")
		}
		if prompt == "join" {
			required = append(required, "left", "right")
		}
		for _, name := range required {
			data, err := os.ReadFile(filepath.Join(cwd, name+".txt"))
			if err != nil || string(data) != name+"\n" {
				return fmt.Errorf("missing dependency %s: %v", name, err)
			}
		}
		return os.WriteFile(filepath.Join(cwd, prompt+".txt"), []byte(prompt+"\n"), 0600)
	}
	s, r, w := planWorkerFixture(t, "pass", tasks, edit)
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, _ := s.Get(r.ID)
	if got.State != "awaiting_integration" {
		p, _ := s.GetPlan(r.ID)
		t.Fatalf("%s: %+v", got.State, p)
	}
	p, _ := s.GetPlan(r.ID)
	for _, task := range p.Tasks {
		if task.State != taskgraph.Succeeded {
			t.Fatal(task)
		}
		patch, err := w.ReadArtifact(r.ID, task.AttemptID, "changes.patch")
		if err != nil || !strings.Contains(string(patch), "+"+task.ID) {
			t.Fatal(string(patch), err)
		}
		if task.ID != "root" && strings.Contains(string(patch), "diff --git a/root.txt") {
			t.Fatal("ancestor repeated in incremental result")
		}
		a, err := s.Artifact(r.ID, task.AttemptID, "result_commit")
		if err != nil {
			t.Fatal(err)
		}
		ref, err := git(context.Background(), r.Path, "rev-parse", "refs/powercodedeck/attempts/"+task.AttemptID)
		if err != nil || strings.TrimSpace(ref) != a.BaseCommit {
			t.Fatal("result not retained", err)
		}
		if _, err := w.ReadArtifact("different-run", task.AttemptID, "changes.patch"); err == nil {
			t.Fatal("cross-run artifact exposed")
		}
	}
	status, err := git(context.Background(), r.Path, "status", "--porcelain")
	if err != nil || status != "" {
		t.Fatal("source changed", status, err)
	}
	head, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
	if strings.TrimSpace(head) != got.BaseCommit {
		t.Fatal("source HEAD moved")
	}
	integration, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, _ = s.Get(r.ID)
	if got.State != "succeeded" {
		p, _ := s.GetPlan(r.ID)
		t.Fatal(got, p.Integrations)
	}
	patch, err := w.ReadArtifact(r.ID, integration, "changes.patch")
	if err != nil || strings.Count(string(patch), "diff --git a/root.txt b/root.txt") != 1 {
		t.Fatal("final integration repeated diamond ancestor", string(patch), err)
	}
}

func TestPlanWorkerConflictingDependenciesStopBeforeProvider(t *testing.T) {
	tasks := []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}, {ID: "b", Provider: "codex", Prompt: "b"}, {ID: "join", Provider: "codex", Prompt: "join", DependsOn: []string{"a", "b"}}}
	s, r, w := planWorkerFixture(t, "pass", tasks, func(cwd, prompt string) error {
		if prompt == "join" {
			return fmt.Errorf("join provider must not start")
		}
		return os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte(prompt+"\n"), 0600)
	})
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, _ := s.GetPlan(r.ID)
	last := p.Tasks[2]
	if last.State != taskgraph.Failed || !strings.Contains(last.Detail, "dependency integration failed") {
		t.Fatalf("%+v", p)
	}
	old := last.AttemptID
	report, err := w.ReadArtifact(r.ID, old, "conflicts")
	if err != nil || !strings.Contains(string(report), "tracked.txt") {
		t.Fatal("task conflict evidence missing", string(report), err)
	}
	if err := s.RetryPlannedTask(r.ID, "join"); err != nil {
		t.Fatal(err)
	}
	if err := w.StartPlan(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	p, _ = s.GetPlan(r.ID)
	if p.Tasks[2].AttemptID == old || p.Tasks[0].AttemptID == "" {
		t.Fatal("retry reused attempt")
	}
	history, err := s.TaskAttempts(r.ID, "join", "")
	if err != nil || len(history.Attempts) != 2 || history.Attempts[1].ID != old {
		t.Fatal(history, err)
	}
	oldReport, err := w.ReadArtifact(r.ID, old, "conflicts")
	if err != nil || string(oldReport) != string(report) {
		t.Fatal("retry changed conflict evidence", err)
	}
}

func TestPlanWorkerVerificationFailureBlocksDependents(t *testing.T) {
	for _, mode := range []string{"fail", "mutate"} {
		t.Run(mode, func(t *testing.T) {
			tasks := []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}, {ID: "b", Provider: "codex", Prompt: "b", DependsOn: []string{"a"}}}
			s, r, w := planWorkerFixture(t, mode, tasks, func(cwd, prompt string) error {
				return os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("edit\n"), 0600)
			})
			if err := w.StartPlan(r.ID); err != nil {
				t.Fatal(err)
			}
			waitWorker(t, w)
			p, _ := s.GetPlan(r.ID)
			if p.Tasks[0].State != taskgraph.Failed || p.Tasks[1].AttemptID != "" {
				t.Fatalf("%+v", p)
			}
			if _, err := s.Artifact(r.ID, p.Tasks[0].AttemptID, "result_commit"); err == nil {
				t.Fatal("failed changes published")
			}
		})
	}
}
