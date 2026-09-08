package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/providers"
)

const validDraft = `{"concurrency":2,"tasks":[{"id":"api","prompt":"implement API","provider":"codex","dependsOn":[]},{"id":"ui","prompt":"connect UI","provider":"codex","dependsOn":["api"]}]}`

func plannerFixture(t *testing.T) (*Store, Run, *Worker) {
	t.Helper()
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r, err := s.Create("planner", testRepo(t), "add API and UI", "codex")
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorker(s, t.TempDir(), map[string]Factory{"codex": func(string, string) (providers.Execution, error) {
		return nil, errors.New("implementation must not start during planning")
	}})
	if err != nil {
		t.Fatal(err)
	}
	w.SetPlanner(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: validDraft, stopped: make(chan struct{})}, nil
	})
	return s, r, w
}

func TestPlanningProducesEditableDraftWithoutDispatch(t *testing.T) {
	s, r, w := plannerFixture(t)
	id, err := w.StartPlanning(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	d, err := s.GetPlanDraft(r.ID)
	if err != nil || d.ID != id || d.State != "succeeded" || d.Plan == nil {
		t.Fatal(d, err)
	}
	run, _ := s.Get(r.ID)
	if run.State != "queued" || len(run.Executions) != 0 || run.BaseCommit == "" {
		t.Fatal(run)
	}
	if _, err := s.GetPlan(r.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("draft automatically froze plan", err)
	}
	status, err := git(context.Background(), r.Path, "status", "--porcelain")
	if err != nil || status != "" {
		t.Fatal("source changed", status, err)
	}
	var workspace string
	if err := s.db.QueryRow(`SELECT workspace FROM v2_plan_drafts WHERE id=?`, id).Scan(&workspace); err != nil || workspace == r.Path {
		t.Fatal(workspace, err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "tracked.txt")); err != nil || string(data) != "original\n" {
		t.Fatal("planner did not receive source snapshot", err)
	}
	d.Plan.Tasks[1].Prompt = "edited acceptance criteria"
	if err := s.SavePlan(r.ID, *d.Plan); err != nil {
		t.Fatal(err)
	}
	plan, _ := s.GetPlan(r.ID)
	if plan.Tasks[1].Prompt != "edited acceptance criteria" || plan.Tasks[0].AttemptID != "" {
		t.Fatal(plan)
	}
	if _, err := w.StartPlanning(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("saved plan regenerated", err)
	}
	original, _ := s.GetPlanDraft(r.ID)
	if original.Plan.Tasks[1].Prompt == d.Plan.Tasks[1].Prompt {
		t.Fatal("editing overwrote generated evidence")
	}
}

func TestPlannerRejectsInvalidOutputAndSourceMutation(t *testing.T) {
	for _, mode := range []string{"invalid", "mutation", "identity"} {
		t.Run(mode, func(t *testing.T) {
			s, r, w := plannerFixture(t)
			w.SetPlanner(func(id, cwd string) (providers.Execution, error) {
				e := &fakeExecution{id: id, cwd: cwd, response: validDraft, stopped: make(chan struct{})}
				switch mode {
				case "invalid":
					e.response = "not JSON"
				case "mutation":
					e.reviewChange = "unexpected edit\n"
				case "identity":
					e.id = "wrong"
				}
				return e, nil
			})
			if _, err := w.StartPlanning(r.ID); err != nil {
				t.Fatal(err)
			}
			waitWorker(t, w)
			d, err := s.GetPlanDraft(r.ID)
			if err != nil || d.State != "failed" || d.Plan != nil {
				t.Fatal(d, err)
			}
			got, _ := s.Get(r.ID)
			if got.State != "queued" {
				t.Fatal("manual editing/retry unavailable", got)
			}
			data, _ := os.ReadFile(filepath.Join(r.Path, "tracked.txt"))
			if string(data) != "original\n" {
				t.Fatal("source changed")
			}
		})
	}
}

func TestDecodePlanRejectsUnsafeGraphAndForgedState(t *testing.T) {
	available := map[string]Factory{"codex": func(string, string) (providers.Execution, error) { return nil, nil }}
	invalid := []string{
		`{}`, validDraft + `{}`, "```json\n" + validDraft + "\n```",
		strings.Replace(validDraft, `"concurrency":2`, `"concurrency":65`, 1),
		strings.Replace(validDraft, `"concurrency":2`, `"concurrency":1.5`, 1),
		strings.Replace(validDraft, `"dependsOn":[]`, `"dependsOn":["ui"]`, 1),
		strings.Replace(validDraft, `"dependsOn":[]`, `"dependsOn":["missing"]`, 1),
		strings.Replace(validDraft, `"concurrency":2`, `"state":"succeeded","concurrency":2`, 1),
		strings.Replace(validDraft, `"id":"api"`, `"state":"succeeded","id":"api"`, 1),
		strings.Replace(validDraft, `"provider":"codex"`, `"provider":"antigravity"`, 1),
		strings.Repeat(" ", 512*1024+1),
	}
	for i, raw := range invalid {
		if _, err := decodePlan(raw, available); err == nil {
			t.Fatalf("invalid plan %d accepted", i)
		}
	}
	if _, err := decodePlan(validDraft, available); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningCancellationAndRecovery(t *testing.T) {
	s, r, w := plannerFixture(t)
	entered, stopped := make(chan struct{}), make(chan struct{})
	w.SetPlanner(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, block: true, entered: entered, stopped: stopped}, nil
	})
	id, err := w.StartPlanning(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("planner did not start")
	}
	if _, err := w.Start(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("single worker entered occupied slot", err)
	}
	if err := s.SavePlan(r.ID, testPlan()); !errors.Is(err, ErrConflict) {
		t.Fatal("plan saved during generation", err)
	}
	if err := w.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	select {
	case <-stopped:
	default:
		t.Fatal("planner not stopped")
	}
	if err := s.finishDraft(id, &TaskPlan{}, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late plan accepted", err)
	}
	d, _ := s.GetPlanDraft(r.ID)
	if d.State != "canceled" {
		t.Fatal(d)
	}

	s2, r2, w2 := plannerFixture(t)
	base, _ := git(context.Background(), r2.Path, "rev-parse", "HEAD")
	prior, err := s2.beginDraft(r2.ID, strings.TrimSpace(base))
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Recover(); err != nil {
		t.Fatal(err)
	}
	d, _ = s2.GetPlanDraft(r2.ID)
	if d.State != "interrupted" {
		t.Fatal(d)
	}
	next, err := w2.StartPlanning(r2.ID)
	if err != nil || next == prior {
		t.Fatal(next, err)
	}
	waitWorker(t, w2)
	if err := s2.finishDraft(prior, nil, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal("old draft changed", err)
	}
}

func TestPlanningPinsRevisionAndRejectsDirtySource(t *testing.T) {
	_, r, w := plannerFixture(t)
	if err := os.WriteFile(filepath.Join(r.Path, "local.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartPlanning(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("dirty source accepted", err)
	}
	if err := os.Remove(filepath.Join(r.Path, "local.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartPlanning(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	if _, err := git(context.Background(), r.Path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "advance"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.StartPlanning(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("pinned draft regenerated against new source", err)
	}
}
