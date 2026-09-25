package orchestration

import (
	"errors"
	"path/filepath"
	"testing"

	"powercodedeck/internal/orchestration/taskgraph"
)

func TestIntegrationCompletionRequiresCurrentResultAndEveryFrozenCheck(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := createTest(t, s, "integration")
	if err := s.SavePlan(r.ID, TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{{ID: "a", Prompt: "a", Provider: "codex"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindBase(r.ID, "base"); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ClaimTasks(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0]
	if err := s.FinishPlannedAttempt(task.AttemptID, true, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPlannedArtifact(task.AttemptID, "result_commit", "", "result"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tests", "review"} {
		if err := s.RecordPlannedCheck(task.AttemptID, name, true, "pass"); err != nil {
			t.Fatal(err)
		}
	}
	checks := []CheckSpec{{Name: "project_test"}, {Name: "second_test"}}
	if _, err := s.BeginIntegration(r.ID, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty checks accepted", err)
	}
	id, err := s.BeginIntegration(r.ID, checks)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginIntegration(r.ID, checks); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate active attempt", err)
	}
	if err := s.CompleteIntegration(id); !errors.Is(err, ErrConflict) {
		t.Fatal("missing evidence completed", err)
	}
	for _, name := range []string{"diff_check", "project_test", "review"} {
		if err := s.RecordIntegrationCheck(id, name, true, "pass"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordIntegrationCheck(id, "unknown", true, "pass"); !errors.Is(err, ErrConflict) {
		t.Fatal("unconfigured check accepted", err)
	}
	if err := s.RecordIntegrationCheck(id, "review", false, "overwrite"); !errors.Is(err, ErrConflict) {
		t.Fatal("check overwritten", err)
	}
	if err := s.CompleteIntegration(id); !errors.Is(err, ErrConflict) {
		t.Fatal("missing second test accepted", err)
	}
	if err := s.RecordIntegrationCheck(id, "second_test", true, "pass"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIntegration(id); !errors.Is(err, ErrConflict) {
		t.Fatal("missing result accepted", err)
	}
	if err := s.AddIntegrationArtifact(id, "result_commit", "", "combined"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIntegration(id); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if got.State != "succeeded" {
		t.Fatal(got)
	}
	if err := s.FailIntegration(id, "late failure"); !errors.Is(err, ErrConflict) {
		t.Fatal("completed result overwritten", err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(r.ID)
	if got.State != "succeeded" {
		t.Fatal("recovery changed verified result")
	}
}
