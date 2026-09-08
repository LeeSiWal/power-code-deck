package orchestration

import (
	"errors"
	"path/filepath"
	"powercodedeck/internal/orchestration/taskgraph"
	"sync"
	"testing"
)

func testPlan() TaskPlan {
	return TaskPlan{Concurrency: 2, Tasks: []taskgraph.Task{
		{ID: "api", Provider: "codex", Prompt: "API"},
		{ID: "ui", Provider: "claude", Prompt: "UI"},
		{ID: "integrate", Provider: "antigravity", Prompt: "Integration", DependsOn: []string{"api", "ui"}},
	}}
}
func TestPlanFreezeAndVerifiedDependencies(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := createTest(t, s, "plan")
	p := testPlan()
	if err := s.SavePlan(r.ID, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(r.ID, p); err != nil {
		t.Fatal("duplicate rejected", err)
	}
	p.Tasks[0].Prompt = "overwrite"
	if err := s.SavePlan(r.ID, p); !errors.Is(err, ErrConflict) {
		t.Fatal("plan overwritten", err)
	}
	if _, err := s.StartAttempt(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("legacy worker accepted plan", err)
	}
	claimed, err := s.ClaimTasks(r.ID)
	if err != nil || len(claimed) != 2 {
		t.Fatal(claimed, err)
	}
	for _, task := range claimed {
		if err := s.FinishPlannedAttempt(task.AttemptID, true, "done"); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordPlannedCheck(task.AttemptID, "tests", true, "passed"); err != nil {
			t.Fatal(err)
		}
	}
	if more, err := s.ClaimTasks(r.ID); err != nil || len(more) != 0 {
		t.Fatal("unreviewed dependency released", more, err)
	}
	for _, task := range claimed {
		if err := s.RecordPlannedCheck(task.AttemptID, "review", true, "passed"); err != nil {
			t.Fatal(err)
		}
	}
	more, err := s.ClaimTasks(r.ID)
	if err != nil || len(more) != 1 || more[0].ID != "integrate" {
		t.Fatal(more, err)
	}
	if err := s.RecordPlannedCheck(more[0].AttemptID, "tests", true, "premature"); !errors.Is(err, ErrConflict) {
		t.Fatal("premature check", err)
	}
	if err := s.FinishPlannedAttempt(more[0].AttemptID, true, "done"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tests", "review"} {
		if err := s.RecordPlannedCheck(more[0].AttemptID, name, true, "passed"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(r.ID)
	if err != nil || got.State != "awaiting_integration" {
		t.Fatal("task success became Run success", got, err)
	}
	if err := s.Complete(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("plan skipped integration", err)
	}
}

func TestFailedPlannedCheckIsImmutableAndRetryHasFreshEvidence(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := createTest(t, s, "failed-plan")
	if err := s.SavePlan(r.ID, testPlan()); err != nil {
		t.Fatal(err)
	}
	claims, err := s.ClaimTasks(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := claims[0]
	if err := s.FinishPlannedAttempt(a.AttemptID, true, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPlannedCheck(a.AttemptID, "tests", false, "broken"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPlannedCheck(a.AttemptID, "tests", true, "overwrite"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := s.RetryPlannedTask(r.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	newClaims, err := s.ClaimTasks(r.ID)
	if err != nil || len(newClaims) != 1 || newClaims[0].AttemptID == a.AttemptID {
		t.Fatal(newClaims, err)
	}
	if err := s.FinishPlannedAttempt(a.AttemptID, true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale attempt", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM v2_plan_checks WHERE attempt_id=?`, newClaims[0].AttemptID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old evidence reused", count, err)
	}
}

func TestConcurrentPlanClaimsAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s := openTest(t, path)
	other := openTest(t, path)
	r := createTest(t, s, "concurrent-plan")
	if err := s.SavePlan(r.ID, testPlan()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan []PlannedTask, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = other
			}
			tasks, err := store.ClaimTasks(r.ID)
			results <- tasks
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]bool{}
	total := 0
	for tasks := range results {
		for _, task := range tasks {
			if ids[task.ID] {
				t.Fatal("duplicate claim")
			}
			ids[task.ID] = true
			total++
		}
	}
	if total != 2 {
		t.Fatal("capacity exceeded", total)
	}
}

func TestPlanRecoveryRetryAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s := openTest(t, path)
	r := createTest(t, s, "recovery-plan")
	if err := s.SavePlan(r.ID, testPlan()); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimTasks(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishPlannedAttempt(claimed[0].AttemptID, true, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPlannedCheck(claimed[0].AttemptID, "tests", true, "passed"); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s = openTest(t, path)
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPlan(r.ID)
	if err != nil || p.Tasks[0].State != taskgraph.Failed || len(p.Selection.Blocked) != 1 {
		t.Fatal(p, err)
	}
	if err := s.RecordPlannedCheck(claimed[0].AttemptID, "review", true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late review", err)
	}
	if err := s.RetryPlannedTask(r.ID, "api"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.ClaimTasks(r.ID)
	if err != nil || len(retry) != 1 || retry[0].AttemptID == claimed[0].AttemptID {
		t.Fatal(retry, err)
	}
	if err := s.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishPlannedAttempt(retry[0].AttemptID, true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("cancel undone", err)
	}
	if _, err := s.ClaimTasks(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("canceled plan claimed", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM v2_plan_checks WHERE attempt_id=?`, claimed[0].AttemptID).Scan(&count); err != nil || count != 1 {
		t.Fatal("evidence lost", count, err)
	}
}
