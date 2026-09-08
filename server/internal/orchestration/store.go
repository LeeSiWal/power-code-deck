// Package orchestration owns durable work independently of chat sessions and PTYs.
package orchestration

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrConflict = errors.New("work state or idempotency key conflicts")
var ErrInvalid = errors.New("invalid work request")

type Store struct{ db *sql.DB }
type Run struct {
	ID          string      `json:"id"`
	WorkspaceID string      `json:"workspaceId"`
	Path        string      `json:"path"`
	Prompt      string      `json:"prompt"`
	Provider    string      `json:"provider"`
	State       string      `json:"state"`
	TaskID      string      `json:"taskId"`
	Executions  []Execution `json:"executions"`
}
type Execution struct {
	ID     string  `json:"id"`
	State  string  `json:"state"`
	Detail string  `json:"detail"`
	Checks []Check `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

const schema = `
CREATE TABLE IF NOT EXISTS v2_workspaces(id TEXT PRIMARY KEY,path TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS v2_runs(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL REFERENCES v2_workspaces(id),request_key TEXT NOT NULL UNIQUE,prompt TEXT NOT NULL,provider TEXT NOT NULL,state TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS v2_tasks(id TEXT PRIMARY KEY,run_id TEXT NOT NULL UNIQUE REFERENCES v2_runs(id),state TEXT NOT NULL,active_execution TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS v2_executions(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES v2_tasks(id),state TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',ordinal INTEGER NOT NULL UNIQUE);
CREATE INDEX IF NOT EXISTS v2_executions_task ON v2_executions(task_id,ordinal);
CREATE TABLE IF NOT EXISTS v2_checks(execution_id TEXT NOT NULL REFERENCES v2_executions(id),name TEXT NOT NULL,passed INTEGER NOT NULL,detail TEXT NOT NULL,PRIMARY KEY(execution_id,name));
`

func New(database *sql.DB) (*Store, error) {
	if _, err := database.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: database}, nil
}

// Create reserves one task. It does not start a CLI or mutate the workspace.
func (s *Store) Create(key, path, prompt, provider string) (Run, error) {
	key, prompt = strings.TrimSpace(key), strings.TrimSpace(prompt)
	if key == "" || len(key) > 128 || prompt == "" || len(prompt) > 65536 || (provider != "claude" && provider != "codex" && provider != "antigravity") {
		return Run{}, ErrInvalid
	}
	if !filepath.IsAbs(path) {
		return Run{}, fmt.Errorf("%w: absolute workspace path required", ErrInvalid)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Run{}, fmt.Errorf("%w: workspace unavailable", ErrInvalid)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Run{}, ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	workspace := "ws_" + rand.Text()
	if _, err = tx.Exec(`INSERT INTO v2_workspaces(id,path) VALUES(?,?) ON CONFLICT(path) DO NOTHING`, workspace, path); err != nil {
		return Run{}, err
	}
	if err = tx.QueryRow(`SELECT id FROM v2_workspaces WHERE path=?`, path).Scan(&workspace); err != nil {
		return Run{}, err
	}
	id := "run_" + rand.Text()
	result, err := tx.Exec(`INSERT INTO v2_runs(id,workspace_id,request_key,prompt,provider,state) VALUES(?,?,?,?,?,'queued') ON CONFLICT(request_key) DO NOTHING`, id, workspace, key, prompt, provider)
	if err != nil {
		return Run{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		var savedWorkspace, savedPrompt, savedProvider string
		if err = tx.QueryRow(`SELECT id,workspace_id,prompt,provider FROM v2_runs WHERE request_key=?`, key).Scan(&id, &savedWorkspace, &savedPrompt, &savedProvider); err != nil {
			return Run{}, err
		}
		if savedWorkspace != workspace || savedPrompt != prompt || savedProvider != provider {
			return Run{}, ErrConflict
		}
	} else {
		if _, err = tx.Exec(`INSERT INTO v2_tasks(id,run_id,state) VALUES(?,?,'queued')`, "task_"+rand.Text(), id); err != nil {
			return Run{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Run{}, err
	}
	return s.Get(id)
}

func (s *Store) Get(id string) (Run, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	r := Run{Executions: []Execution{}}
	err = tx.QueryRow(`SELECT r.id,r.workspace_id,w.path,r.prompt,r.provider,r.state,t.id FROM v2_runs r JOIN v2_workspaces w ON w.id=r.workspace_id JOIN v2_tasks t ON t.run_id=r.id WHERE r.id=?`, id).Scan(&r.ID, &r.WorkspaceID, &r.Path, &r.Prompt, &r.Provider, &r.State, &r.TaskID)
	if err != nil {
		return Run{}, err
	}
	rows, err := tx.Query(`SELECT id,state,detail FROM v2_executions WHERE task_id=? ORDER BY ordinal`, r.TaskID)
	if err != nil {
		return Run{}, err
	}
	for rows.Next() {
		var e Execution
		if err = rows.Scan(&e.ID, &e.State, &e.Detail); err != nil {
			rows.Close()
			return Run{}, err
		}
		r.Executions = append(r.Executions, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Run{}, err
	}
	for i := range r.Executions {
		e := &r.Executions[i]
		e.Checks = []Check{}
		rows, err := tx.Query(`SELECT name,passed,detail FROM v2_checks WHERE execution_id=? ORDER BY name`, e.ID)
		if err != nil {
			return Run{}, err
		}
		for rows.Next() {
			var c Check
			if err := rows.Scan(&c.Name, &c.Passed, &c.Detail); err != nil {
				rows.Close()
				return Run{}, err
			}
			e.Checks = append(e.Checks, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Run{}, err
		}
	}
	return r, tx.Commit()
}

func (s *Store) List() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM v2_runs ORDER BY rowid DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// StartAttempt is an internal worker API. A browser cannot assert execution success.
func (s *Store) StartAttempt(run string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='running' WHERE id=? AND state IN ('queued','failed','interrupted')`, run)); err != nil {
		return "", err
	}
	id := "exec_" + rand.Text()
	if err = changed(tx.Exec(`UPDATE v2_tasks SET state='running',active_execution=? WHERE run_id=?`, id, run)); err != nil {
		return "", err
	}
	_, err = tx.Exec(`INSERT INTO v2_executions(id,task_id,state,ordinal) SELECT ?,id,'running',(SELECT COALESCE(MAX(ordinal),0)+1 FROM v2_executions) FROM v2_tasks WHERE run_id=?`, id, run)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (s *Store) FinishAttempt(execution string, success bool, detail string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, next := "failed", "failed"
	if success {
		state, next = "succeeded", "awaiting_checks"
	}
	if err = changed(tx.Exec(`UPDATE v2_executions SET state=?,detail=? WHERE id=? AND state='running'`, state, detail, execution)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_tasks SET state=? WHERE active_execution=? AND state='running'`, next, execution)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_runs SET state=? WHERE id=(SELECT run_id FROM v2_tasks WHERE active_execution=?) AND state='running'`, next, execution)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordCheck(execution, name string, passed bool, detail string) error {
	if strings.TrimSpace(name) == "" {
		return ErrInvalid
	}
	// Checks are immutable evidence per attempt, not a mutable green indicator.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`INSERT INTO v2_checks(execution_id,name,passed,detail) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM v2_tasks WHERE active_execution=? AND state='awaiting_checks') ON CONFLICT(execution_id,name) DO NOTHING`, execution, name, passed, detail, execution)); err != nil {
		return err
	}
	if !passed {
		if err = changed(tx.Exec(`UPDATE v2_tasks SET state='failed' WHERE active_execution=? AND state='awaiting_checks'`, execution)); err != nil {
			return err
		}
		if err = changed(tx.Exec(`UPDATE v2_runs SET state='failed' WHERE id=(SELECT run_id FROM v2_tasks WHERE active_execution=?) AND state='awaiting_checks'`, execution)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Complete(run string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_tasks SET state='succeeded' WHERE run_id=? AND state='awaiting_checks' AND EXISTS(SELECT 1 FROM v2_checks WHERE execution_id=active_execution) AND NOT EXISTS(SELECT 1 FROM v2_checks WHERE execution_id=active_execution AND passed=0)`, run)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='succeeded' WHERE id=? AND state='awaiting_checks'`, run)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Cancel(run string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='canceled' WHERE id=? AND state IN ('queued','running','awaiting_checks','failed','interrupted')`, run)); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE v2_executions SET state='canceled' WHERE task_id=(SELECT id FROM v2_tasks WHERE run_id=?) AND state='running'`, run); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE v2_tasks SET state='canceled' WHERE run_id=?`, run); err != nil {
		return err
	}
	return tx.Commit()
}

// Recover is called once before workers or routes start. It never retries work.
func (s *Store) Recover() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{`UPDATE v2_executions SET state='interrupted',detail='server restarted; outcome unknown' WHERE state='running'`, `UPDATE v2_tasks SET state='interrupted' WHERE state='running'`, `UPDATE v2_runs SET state='interrupted' WHERE state='running'`} {
		if _, err = tx.Exec(query); err != nil {
			return err
		}
	}
	return tx.Commit()
}
