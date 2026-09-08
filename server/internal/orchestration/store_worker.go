package orchestration

import "strings"

type Artifact struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	BaseCommit string `json:"baseCommit"`
}

func (s *Store) RequireChecks(run string, names ...string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = changed(tx.Exec(`UPDATE v2_runs SET state=state WHERE id=? AND state IN ('queued','failed','interrupted')`, run)); err != nil {
		return err
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return ErrInvalid
		}
		if _, err = tx.Exec(`INSERT INTO v2_requirements(run_id,name) VALUES(?,?) ON CONFLICT DO NOTHING`, run, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddArtifact(execution, kind, path, base string) error {
	return changed(s.db.Exec(`INSERT INTO v2_artifacts(execution_id,kind,path,base_commit) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM v2_executions WHERE id=? AND state='running')`, execution, kind, path, base, execution))
}
