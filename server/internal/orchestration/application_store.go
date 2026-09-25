package orchestration

import (
	"crypto/rand"
	"database/sql"
)

const applicationSchema = `
CREATE TABLE IF NOT EXISTS v2_result_applications(id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES v2_runs(id),integration_id TEXT NOT NULL REFERENCES v2_integrations(id),branch TEXT NOT NULL,base_commit TEXT NOT NULL,result_commit TEXT NOT NULL,state TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '',ordinal INTEGER NOT NULL UNIQUE);
CREATE UNIQUE INDEX IF NOT EXISTS v2_application_pending ON v2_result_applications(run_id) WHERE state IN ('applying','needs_attention','applied');
`

// ApplyTarget is the exact tuple reviewed by the user. No arbitrary revision or
// repository path is accepted from a browser.
type ApplyTarget struct {
	IntegrationID string `json:"integrationId"`
	Branch        string `json:"branch"`
	BaseCommit    string `json:"baseCommit"`
	ResultCommit  string `json:"resultCommit"`
}
type Application struct {
	ApplyTarget
	ID     string `json:"id"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

func (s *Store) verifiedTarget(run string) (ApplyTarget, error) {
	var target ApplyTarget
	err := s.db.QueryRow(`SELECT i.id,r.base_commit,a.base_commit FROM v2_runs r JOIN v2_integrations i ON i.run_id=r.id JOIN v2_integration_artifacts a ON a.attempt_id=i.id AND a.kind='result_commit' WHERE r.id=? AND r.state='succeeded' AND i.state='succeeded' ORDER BY i.ordinal DESC LIMIT 1`, run).Scan(&target.IntegrationID, &target.BaseCommit, &target.ResultCommit)
	return target, err
}

func readApplications(tx *sql.Tx, run string) ([]Application, error) {
	out := []Application{}
	rows, err := tx.Query(`SELECT id,integration_id,branch,base_commit,result_commit,state,detail FROM v2_result_applications WHERE run_id=? ORDER BY ordinal`, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Application
		if err := rows.Scan(&a.ID, &a.IntegrationID, &a.Branch, &a.BaseCommit, &a.ResultCommit, &a.State, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) application(run string) (Application, error) {
	var a Application
	err := s.db.QueryRow(`SELECT id,integration_id,branch,base_commit,result_commit,state,detail FROM v2_result_applications WHERE run_id=? AND state IN ('applying','needs_attention','applied') ORDER BY ordinal DESC LIMIT 1`, run).Scan(&a.ID, &a.IntegrationID, &a.Branch, &a.BaseCommit, &a.ResultCommit, &a.State, &a.Detail)
	return a, err
}

func (s *Store) beginApplication(run string, target ApplyTarget) (Application, error) {
	a := Application{ApplyTarget: target, ID: "apply_" + rand.Text(), State: "applying"}
	err := changed(s.db.Exec(`INSERT INTO v2_result_applications(id,run_id,integration_id,branch,base_commit,result_commit,state,ordinal) SELECT ?,?,?,?,?,?,'applying',(SELECT COALESCE(MAX(ordinal),0)+1 FROM v2_result_applications) WHERE EXISTS(SELECT 1 FROM v2_runs r JOIN v2_integrations i ON i.run_id=r.id JOIN v2_integration_artifacts a ON a.attempt_id=i.id WHERE r.id=? AND r.state='succeeded' AND r.base_commit=? AND i.id=? AND i.state='succeeded' AND a.kind='result_commit' AND a.base_commit=?) AND NOT EXISTS(SELECT 1 FROM v2_result_applications WHERE run_id=? AND state IN ('applying','needs_attention','applied'))`, a.ID, run, target.IntegrationID, target.Branch, target.BaseCommit, target.ResultCommit, run, target.BaseCommit, target.IntegrationID, target.ResultCommit, run))
	return a, err
}

func (s *Store) finishApplication(id, state, detail string) error {
	if state != "applied" && state != "failed" && state != "needs_attention" {
		return ErrInvalid
	}
	return changed(s.db.Exec(`UPDATE v2_result_applications SET state=?,detail=? WHERE id=? AND state IN ('applying','needs_attention')`, state, detail, id))
}
