package orchestration

import "strings"

type Artifact struct {
	Kind       string `json:"kind"`
	Path       string `json:"-"`
	BaseCommit string `json:"baseCommit"`
}

// Artifact returns only evidence registered for this exact Run and execution.
// Reading and root-containment checks remain Worker responsibilities.
func (s *Store) Artifact(run, execution, kind string) (Artifact, error) {
	var artifact Artifact
	err := s.db.QueryRow(`SELECT a.kind,a.path,a.base_commit
		FROM v2_artifacts a
		JOIN v2_executions e ON e.id=a.execution_id
		JOIN v2_tasks t ON t.id=e.task_id
		WHERE t.run_id=? AND e.id=? AND a.kind=?
		UNION ALL SELECT a.kind,a.path,a.base_commit FROM v2_plan_artifacts a JOIN v2_plan_attempts e ON e.id=a.attempt_id WHERE e.run_id=? AND e.id=? AND a.kind=?`, run, execution, kind, run, execution, kind).
		Scan(&artifact.Kind, &artifact.Path, &artifact.BaseCommit)
	if err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// BindBase pins the source revision on the first prepared attempt. Retries must
// review the same code; advancing the user's branch requires a new Run/key.
func (s *Store) BindBase(run, commit string) error {
	if strings.TrimSpace(commit) == "" {
		return ErrInvalid
	}
	return changed(s.db.Exec(`UPDATE v2_runs SET base_commit=? WHERE id=? AND state IN ('running','planned','plan_running') AND (base_commit='' OR base_commit=?)`, commit, run, commit))
}

func (s *Store) AddPlannedArtifact(attempt, kind, path, base string) error {
	return changed(s.db.Exec(`INSERT INTO v2_plan_artifacts(attempt_id,kind,path,base_commit) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM v2_plan_tasks WHERE active_attempt=? AND state IN ('running','verifying'))`, attempt, kind, path, base, attempt))
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
	return changed(s.db.Exec(`INSERT INTO v2_artifacts(execution_id,kind,path,base_commit)
		SELECT ?,?,?,? WHERE EXISTS(
			SELECT 1 FROM v2_executions e JOIN v2_tasks t ON t.id=e.task_id
			WHERE e.id=? AND (e.state='running' OR (e.state='succeeded' AND t.active_execution=e.id AND t.state='awaiting_checks'))
		)`, execution, kind, path, base, execution))
}
