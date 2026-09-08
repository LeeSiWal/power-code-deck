package orchestration

import (
	"crypto/rand"
	"encoding/json"
)

const plannerSchema = `
CREATE TABLE IF NOT EXISTS v2_plan_drafts(id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES v2_runs(id),state TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',plan TEXT NOT NULL DEFAULT '',workspace TEXT NOT NULL DEFAULT '',ordinal INTEGER NOT NULL UNIQUE);
CREATE UNIQUE INDEX IF NOT EXISTS v2_draft_active ON v2_plan_drafts(run_id) WHERE state='running';
`

type PlanDraft struct {
	ID     string    `json:"id"`
	State  string    `json:"state"`
	Detail string    `json:"detail"`
	Plan   *TaskPlan `json:"plan"`
}

func (s *Store) beginDraft(run, base string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if base == "" {
		return "", ErrInvalid
	}
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='planning',base_commit=? WHERE id=? AND state='queued' AND (base_commit='' OR base_commit=?) AND NOT EXISTS(SELECT 1 FROM v2_task_plans WHERE run_id=?) AND NOT EXISTS(SELECT 1 FROM v2_executions e JOIN v2_tasks t ON t.id=e.task_id WHERE t.run_id=?)`, base, run, base, run, run)); err != nil {
		return "", err
	}
	id := "draft_" + rand.Text()
	if _, err = tx.Exec(`INSERT INTO v2_plan_drafts(id,run_id,state,ordinal) VALUES(?,?,'running',(SELECT COALESCE(MAX(ordinal),0)+1 FROM v2_plan_drafts))`, id, run); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func (s *Store) finishDraft(id string, plan *TaskPlan, detail string) error {
	data := ""
	state := "failed"
	if plan != nil {
		encoded, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		data = string(encoded)
		state = "succeeded"
	}
	if len(detail) > 8192 {
		detail = detail[:8192]
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_plan_drafts SET state=?,plan=?,detail=? WHERE id=? AND state='running'`, state, data, detail, id)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='queued' WHERE id=(SELECT run_id FROM v2_plan_drafts WHERE id=?) AND state='planning'`, id)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) GetPlanDraft(run string) (PlanDraft, error) {
	var d PlanDraft
	var raw string
	err := s.db.QueryRow(`SELECT id,state,detail,plan FROM v2_plan_drafts WHERE run_id=? ORDER BY ordinal DESC LIMIT 1`, run).Scan(&d.ID, &d.State, &d.Detail, &raw)
	if err != nil {
		return d, err
	}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &d.Plan)
	}
	return d, err
}
