package orchestration

import (
	"crypto/rand"
	"database/sql"
	"strings"
)

const integrationSchema = `
CREATE TABLE IF NOT EXISTS v2_integrations(id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES v2_runs(id),state TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',ordinal INTEGER NOT NULL UNIQUE);
CREATE UNIQUE INDEX IF NOT EXISTS v2_integration_active ON v2_integrations(run_id) WHERE state='running';
CREATE TABLE IF NOT EXISTS v2_integration_requirements(attempt_id TEXT NOT NULL REFERENCES v2_integrations(id),name TEXT NOT NULL,PRIMARY KEY(attempt_id,name));
CREATE TABLE IF NOT EXISTS v2_integration_checks(attempt_id TEXT NOT NULL REFERENCES v2_integrations(id),name TEXT NOT NULL,passed INTEGER NOT NULL,detail TEXT NOT NULL,PRIMARY KEY(attempt_id,name));
CREATE TABLE IF NOT EXISTS v2_integration_artifacts(attempt_id TEXT NOT NULL REFERENCES v2_integrations(id),kind TEXT NOT NULL,path TEXT NOT NULL,base_commit TEXT NOT NULL,PRIMARY KEY(attempt_id,kind));
`

// BeginIntegration is internal: every verified task must have a durable result.
// Retries allocate a fresh attempt and cannot inherit passing evidence.
func (s *Store) BeginIntegration(run string, checks []CheckSpec) (string, error) {
	if len(checks) == 0 {
		return "", ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='integrating' WHERE id=? AND state IN ('awaiting_integration','integration_failed') AND base_commit!='' AND EXISTS(SELECT 1 FROM v2_task_plans WHERE run_id=v2_runs.id) AND NOT EXISTS(SELECT 1 FROM v2_plan_tasks t WHERE t.run_id=v2_runs.id AND (t.state!='succeeded' OR NOT EXISTS(SELECT 1 FROM v2_plan_artifacts a WHERE a.attempt_id=t.active_attempt AND a.kind='result_commit' AND a.base_commit!='')))`, run)); err != nil {
		return "", err
	}
	id := "integration_" + rand.Text()
	if _, err = tx.Exec(`INSERT INTO v2_integrations(id,run_id,state,ordinal) VALUES(?,?,'running',(SELECT COALESCE(MAX(ordinal),0)+1 FROM v2_integrations))`, id, run); err != nil {
		return "", err
	}
	names := []string{"diff_check", "review"}
	for _, check := range checks {
		if strings.TrimSpace(check.Name) == "" || check.Name == "diff_check" || check.Name == "review" {
			return "", ErrInvalid
		}
		names = append(names, check.Name)
	}
	for _, name := range names {
		if _, err = tx.Exec(`INSERT INTO v2_integration_requirements(attempt_id,name) VALUES(?,?)`, id, name); err != nil {
			return "", err
		}
	}
	return id, tx.Commit()
}

func (s *Store) AddIntegrationArtifact(id, kind, path, base string) error {
	return changed(s.db.Exec(`INSERT INTO v2_integration_artifacts(attempt_id,kind,path,base_commit) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM v2_integrations i JOIN v2_runs r ON r.id=i.run_id WHERE i.id=? AND i.state='running' AND r.state='integrating')`, id, kind, path, base, id))
}

func failIntegration(tx *sql.Tx, id, detail string) error {
	if err := changed(tx.Exec(`UPDATE v2_integrations SET state='failed',detail=? WHERE id=? AND state='running'`, detail, id)); err != nil {
		return err
	}
	return changed(tx.Exec(`UPDATE v2_runs SET state='integration_failed' WHERE id=(SELECT run_id FROM v2_integrations WHERE id=?) AND state='integrating'`, id))
}
func (s *Store) FailIntegration(id, detail string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = failIntegration(tx, id, detail); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordIntegrationCheck(id, name string, passed bool, detail string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`INSERT INTO v2_integration_checks(attempt_id,name,passed,detail) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM v2_integrations i JOIN v2_runs r ON r.id=i.run_id JOIN v2_integration_requirements q ON q.attempt_id=i.id WHERE i.id=? AND i.state='running' AND r.state='integrating' AND q.name=?) ON CONFLICT DO NOTHING`, id, name, passed, detail, id, name)); err != nil {
		return err
	}
	if !passed {
		if err = failIntegration(tx, id, detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CompleteIntegration is never an HTTP endpoint. Checks alone are insufficient:
// a retained result plus every frozen check are required in the same attempt.
func (s *Store) CompleteIntegration(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_integrations SET state='succeeded',detail='integrated result verified; source branch unchanged' WHERE id=? AND state='running' AND EXISTS(SELECT 1 FROM v2_integration_artifacts WHERE attempt_id=v2_integrations.id AND kind='result_commit' AND base_commit!='') AND EXISTS(SELECT 1 FROM v2_integration_requirements WHERE attempt_id=v2_integrations.id) AND NOT EXISTS(SELECT 1 FROM v2_integration_requirements q WHERE q.attempt_id=v2_integrations.id AND NOT EXISTS(SELECT 1 FROM v2_integration_checks c WHERE c.attempt_id=q.attempt_id AND c.name=q.name AND c.passed=1))`, id)); err != nil {
		return err
	}
	if err = changed(tx.Exec(`UPDATE v2_runs SET state='succeeded' WHERE id=(SELECT run_id FROM v2_integrations WHERE id=?) AND state='integrating' AND NOT EXISTS(SELECT 1 FROM v2_plan_tasks WHERE run_id=v2_runs.id AND state!='succeeded')`, id)); err != nil {
		return err
	}
	return tx.Commit()
}

func readIntegrations(tx *sql.Tx, run string) ([]Execution, error) {
	out := []Execution{}
	rows, err := tx.Query(`SELECT id,state,detail FROM v2_integrations WHERE run_id=? ORDER BY ordinal`, run)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		e := Execution{Artifacts: []Artifact{}, Checks: []Check{}}
		if err := rows.Scan(&e.ID, &e.State, &e.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		e := &out[i]
		rows, err := tx.Query(`SELECT kind,path,base_commit FROM v2_integration_artifacts WHERE attempt_id=? ORDER BY kind`, e.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a Artifact
			if err := rows.Scan(&a.Kind, &a.Path, &a.BaseCommit); err != nil {
				rows.Close()
				return nil, err
			}
			e.Artifacts = append(e.Artifacts, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		rows, err = tx.Query(`SELECT name,passed,detail FROM v2_integration_checks WHERE attempt_id=? ORDER BY name`, e.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c Check
			if err := rows.Scan(&c.Name, &c.Passed, &c.Detail); err != nil {
				rows.Close()
				return nil, err
			}
			e.Checks = append(e.Checks, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
