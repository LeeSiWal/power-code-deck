package orchestration

import (
	"crypto/rand"
	"powercodedeck/internal/orchestration/taskgraph"
)

// Reserve the failed task and a new attempt atomically. An intervening normal
// retry, cancellation or attempt replacement invalidates the submitted repair.
func (s *Store) claimTaskResolution(run, task, source string) (PlannedTask, error) {
	out := PlannedTask{}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := changed(tx.Exec(`UPDATE v2_runs SET state='plan_running' WHERE id=? AND state IN ('planned','plan_running')`, run)); err != nil {
		return out, err
	}
	p, err := readPlan(tx, run)
	if err != nil {
		return out, err
	}
	states := map[string]taskgraph.State{}
	for _, t := range p.Tasks {
		states[t.ID] = t.State
		if t.ID == task {
			out = t
		}
		if t.State == taskgraph.Running || t.State == taskgraph.Verifying {
			return out, ErrConflict
		}
	}
	if out.ID == "" || out.State != taskgraph.Failed || out.AttemptID != source {
		return out, ErrConflict
	}
	for _, dep := range out.DependsOn {
		if states[dep] != taskgraph.Succeeded {
			return out, ErrConflict
		}
	}
	id := "attempt_" + rand.Text()
	if err := changed(tx.Exec(`UPDATE v2_plan_tasks SET state='running',active_attempt=? WHERE run_id=? AND task_id=? AND state='failed' AND active_attempt=?`, id, run, task, source)); err != nil {
		return out, err
	}
	if _, err := tx.Exec(`INSERT INTO v2_plan_attempts(id,run_id,task_id,state) VALUES(?,?,?,'running')`, id, run, task); err != nil {
		return out, err
	}
	out.AttemptID = id
	out.State = taskgraph.Running
	out.Artifacts = []Artifact{}
	out.Checks = []Check{}
	out.Detail = ""
	return out, tx.Commit()
}
