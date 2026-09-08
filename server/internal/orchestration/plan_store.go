package orchestration

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"

	"powercodedeck/internal/orchestration/taskgraph"
)

const planSchema = `
CREATE TABLE IF NOT EXISTS v2_task_plans(run_id TEXT PRIMARY KEY REFERENCES v2_runs(id),spec TEXT NOT NULL,concurrency INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS v2_plan_tasks(run_id TEXT NOT NULL REFERENCES v2_task_plans(run_id),task_id TEXT NOT NULL,state TEXT NOT NULL,active_attempt TEXT NOT NULL DEFAULT '',PRIMARY KEY(run_id,task_id));
CREATE TABLE IF NOT EXISTS v2_plan_attempts(id TEXT PRIMARY KEY,run_id TEXT NOT NULL,task_id TEXT NOT NULL,state TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',FOREIGN KEY(run_id,task_id) REFERENCES v2_plan_tasks(run_id,task_id));
CREATE TABLE IF NOT EXISTS v2_plan_checks(attempt_id TEXT NOT NULL REFERENCES v2_plan_attempts(id),name TEXT NOT NULL,passed INTEGER NOT NULL,detail TEXT NOT NULL,PRIMARY KEY(attempt_id,name));
CREATE TABLE IF NOT EXISTS v2_plan_artifacts(attempt_id TEXT NOT NULL REFERENCES v2_plan_attempts(id),kind TEXT NOT NULL,path TEXT NOT NULL,base_commit TEXT NOT NULL,PRIMARY KEY(attempt_id,kind));
`

type TaskPlan struct {
	Tasks       []taskgraph.Task `json:"tasks"`
	Concurrency int              `json:"concurrency"`
}
type PlannedTask struct {
	taskgraph.Task
	State     taskgraph.State `json:"state"`
	AttemptID string          `json:"attemptId"`
	Detail    string          `json:"detail"`
	Artifacts []Artifact      `json:"artifacts"`
	Checks    []Check         `json:"checks"`
}
type PlanSnapshot struct {
	Concurrency int                 `json:"concurrency"`
	Tasks       []PlannedTask       `json:"tasks"`
	Selection   taskgraph.Selection `json:"selection"`
}

// SavePlan freezes a validated plan before any execution. Identical retries are
// idempotent; changing a saved plan requires a new Run. It never starts a CLI.
func (s *Store) SavePlan(run string, plan TaskPlan) error {
	graph, err := taskgraph.New(plan.Tasks)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err = graph.Select(nil, plan.Concurrency); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	plan.Tasks = append([]taskgraph.Task{}, plan.Tasks...)
	for i := range plan.Tasks {
		if plan.Tasks[i].DependsOn == nil {
			plan.Tasks[i].DependsOn = []string{}
		}
	}
	spec, err := json.Marshal(plan.Tasks)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Take the SQLite writer lock before reading state, also serializing StartAttempt.
	if err = changed(tx.Exec(`UPDATE v2_runs SET state=state WHERE id=?`, run)); err != nil {
		return err
	}
	var saved string
	var concurrency int
	err = tx.QueryRow(`SELECT spec,concurrency FROM v2_task_plans WHERE run_id=?`, run).Scan(&saved, &concurrency)
	if err == nil {
		if saved != string(spec) || concurrency != plan.Concurrency {
			return ErrConflict
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='planned' WHERE id=? AND state='queued' AND NOT EXISTS(SELECT 1 FROM v2_executions e JOIN v2_tasks t ON t.id=e.task_id WHERE t.run_id=?)`, run, run)); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO v2_task_plans(run_id,spec,concurrency) VALUES(?,?,?)`, run, string(spec), plan.Concurrency); err != nil {
		return err
	}
	for _, task := range plan.Tasks {
		if _, err = tx.Exec(`INSERT INTO v2_plan_tasks(run_id,task_id,state) VALUES(?,?,'pending')`, run, task.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readPlan(tx *sql.Tx, run string) (PlanSnapshot, error) {
	p := PlanSnapshot{Tasks: []PlannedTask{}}
	var spec string
	if err := tx.QueryRow(`SELECT spec,concurrency FROM v2_task_plans WHERE run_id=?`, run).Scan(&spec, &p.Concurrency); err != nil {
		return p, err
	}
	var tasks []taskgraph.Task
	if err := json.Unmarshal([]byte(spec), &tasks); err != nil {
		return p, err
	}
	g, err := taskgraph.New(tasks)
	if err != nil {
		return p, err
	}
	states := map[string]taskgraph.State{}
	for _, task := range tasks {
		t := PlannedTask{Task: task}
		if err := tx.QueryRow(`SELECT state,active_attempt FROM v2_plan_tasks WHERE run_id=? AND task_id=?`, run, task.ID).Scan(&t.State, &t.AttemptID); err != nil {
			return p, err
		}
		states[task.ID] = t.State
		t.Artifacts = []Artifact{}
		t.Checks = []Check{}
		if t.AttemptID != "" {
			if err := tx.QueryRow(`SELECT detail FROM v2_plan_attempts WHERE id=?`, t.AttemptID).Scan(&t.Detail); err != nil {
				return p, err
			}
			rows, err := tx.Query(`SELECT kind,path,base_commit FROM v2_plan_artifacts WHERE attempt_id=? ORDER BY kind`, t.AttemptID)
			if err != nil {
				return p, err
			}
			for rows.Next() {
				var a Artifact
				if err := rows.Scan(&a.Kind, &a.Path, &a.BaseCommit); err != nil {
					rows.Close()
					return p, err
				}
				t.Artifacts = append(t.Artifacts, a)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return p, err
			}
			checkRows, err := tx.Query(`SELECT name,passed,detail FROM v2_plan_checks WHERE attempt_id=? ORDER BY name`, t.AttemptID)
			if err != nil {
				return p, err
			}
			for checkRows.Next() {
				var c Check
				if err := checkRows.Scan(&c.Name, &c.Passed, &c.Detail); err != nil {
					checkRows.Close()
					return p, err
				}
				t.Checks = append(t.Checks, c)
			}
			err = checkRows.Err()
			checkRows.Close()
			if err != nil {
				return p, err
			}
		}
		p.Tasks = append(p.Tasks, t)
	}
	p.Selection, err = g.Select(states, p.Concurrency)
	return p, err
}

func (s *Store) GetPlan(run string) (PlanSnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return PlanSnapshot{}, err
	}
	defer tx.Rollback()
	p, err := readPlan(tx, run)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// ClaimTasks is internal scheduler API. Reservations and fresh attempt IDs are
// committed together. A second scheduler cannot claim the same capacity/tasks.
func (s *Store) ClaimTasks(run string) ([]PlannedTask, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='plan_running' WHERE id=? AND state IN ('planned','plan_running')`, run)); err != nil {
		return nil, err
	}
	p, err := readPlan(tx, run)
	if err != nil {
		return nil, err
	}
	out := []PlannedTask{}
	for _, task := range p.Selection.Ready {
		id := "attempt_" + rand.Text()
		if err = changed(tx.Exec(`UPDATE v2_plan_tasks SET state='running',active_attempt=? WHERE run_id=? AND task_id=? AND state='pending'`, id, run, task.ID)); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO v2_plan_attempts(id,run_id,task_id,state) VALUES(?,?,?,'running')`, id, run, task.ID); err != nil {
			return nil, err
		}
		out = append(out, PlannedTask{Task: task, State: taskgraph.Running, AttemptID: id})
	}
	return out, tx.Commit()
}

// FinishPlannedAttempt records provider completion, not verified task success.
func (s *Store) FinishPlannedAttempt(attempt string, success bool, detail string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := "failed"
	if success {
		state = "verifying"
	}
	if err = changed(tx.Exec(`UPDATE v2_plan_attempts SET state=?,detail=? WHERE id=? AND state='running'`, state, detail, attempt)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_plan_tasks SET state=? WHERE active_attempt=? AND state='running'`, state, attempt)); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordPlannedCheck requires independent tests AND review for every task.
// No browser route may submit this evidence or mark a plan successful.
func (s *Store) RecordPlannedCheck(attempt, name string, passed bool, detail string) error {
	if name != "tests" && name != "review" {
		return ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`INSERT INTO v2_plan_checks(attempt_id,name,passed,detail) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM v2_plan_tasks WHERE active_attempt=? AND state='verifying') ON CONFLICT DO NOTHING`, attempt, name, passed, detail, attempt)); err != nil {
		return err
	}
	state := "failed"
	if passed {
		var count int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM v2_plan_checks WHERE attempt_id=? AND passed=1`, attempt).Scan(&count); err != nil {
			return err
		}
		if count < 2 {
			return tx.Commit()
		}
		state = "succeeded"
	}
	if err = changed(tx.Exec(`UPDATE v2_plan_tasks SET state=? WHERE active_attempt=? AND state='verifying'`, state, attempt)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_plan_attempts SET state=? WHERE id=? AND state='verifying'`, state, attempt)); err != nil {
		return err
	}
	if !passed {
		if _, err = tx.Exec(`UPDATE v2_plan_attempts SET detail=? WHERE id=?`, detail, attempt); err != nil {
			return err
		}
	}
	// Verified branches still need downstream patch integration and final checks.
	if _, err = tx.Exec(`UPDATE v2_runs SET state='awaiting_integration' WHERE id=(SELECT run_id FROM v2_plan_attempts WHERE id=?) AND state='plan_running' AND NOT EXISTS(SELECT 1 FROM v2_plan_tasks t WHERE t.run_id=v2_runs.id AND t.state!='succeeded')`, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RetryPlannedTask(run, task string) error {
	return changed(s.db.Exec(`UPDATE v2_plan_tasks SET state='pending',active_attempt='' WHERE run_id=? AND task_id=? AND state='failed' AND EXISTS(SELECT 1 FROM v2_runs WHERE id=? AND state IN ('planned','plan_running'))`, run, task, run))
}
