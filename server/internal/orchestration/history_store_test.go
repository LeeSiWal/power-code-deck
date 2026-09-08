package orchestration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"powercodedeck/internal/orchestration/taskgraph"
)

func TestTaskHistoryPagesRetriedAttemptsAndScopesCursors(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := createTest(t, s, "history")
	plan := TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{{ID: "task", Provider: "codex", Prompt: "task"}}}
	if err := s.SavePlan(r.ID, plan); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 12; i++ {
		tasks, err := s.ClaimTasks(r.ID)
		if err != nil || len(tasks) != 1 {
			t.Fatal(tasks, err)
		}
		id := tasks[0].AttemptID
		ids = append(ids, id)
		if err := s.AddPlannedArtifact(id, "check_log:test", "/private/hidden-workspace/evidence", ""); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishPlannedAttempt(id, true, "provider done"); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordPlannedCheck(id, "tests", false, "test failed"); err != nil {
			t.Fatal(err)
		}
		if err := s.RetryPlannedTask(r.ID, "task"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.TaskAttempts(r.ID, "task", "")
	if err != nil || len(first.Attempts) != 10 || first.NextCursor != ids[2] {
		t.Fatal(first, err)
	}
	for i, e := range first.Attempts {
		if e.ID != ids[11-i] || e.State != "failed" || len(e.Checks) != 1 || e.Checks[0].Passed || len(e.Artifacts) != 1 {
			t.Fatal(e)
		}
	}
	second, err := s.TaskAttempts(r.ID, "task", first.NextCursor)
	if err != nil || len(second.Attempts) != 2 || second.NextCursor != "" || second.Attempts[0].ID != ids[1] || second.Attempts[1].ID != ids[0] {
		t.Fatal(second, err)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "hidden-workspace") {
		t.Fatal("host path exposed", string(raw))
	}
	other := createTest(t, s, "other-history")
	if err := s.SavePlan(other.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskAttempts(other.ID, "task", ids[0]); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign cursor accepted", err)
	}
	if _, err := s.TaskAttempts(r.ID, "missing", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unknown task accepted", err)
	}
	if _, err := s.TaskAttempts(r.ID, "task", "unknown"); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown cursor accepted", err)
	}
	current, _ := s.GetPlan(r.ID)
	if current.Tasks[0].AttemptID != "" || current.Tasks[0].State != "pending" {
		t.Fatal("history read changed retry state", current)
	}
}
