package orchestration

import "database/sql"

type TaskHistory struct {
	Attempts   []Execution `json:"attempts"`
	NextCursor string      `json:"nextCursor"`
}

func readPlannedExecution(tx *sql.Tx, id string) (Execution, error) {
	e := Execution{ID: id, Artifacts: []Artifact{}, Checks: []Check{}}
	if err := tx.QueryRow(`SELECT state,detail FROM v2_plan_attempts WHERE id=?`, id).Scan(&e.State, &e.Detail); err != nil {
		return e, err
	}
	rows, err := tx.Query(`SELECT kind,path,base_commit FROM v2_plan_artifacts WHERE attempt_id=? ORDER BY kind`, id)
	if err != nil {
		return e, err
	}
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.Kind, &a.Path, &a.BaseCommit); err != nil {
			rows.Close()
			return e, err
		}
		e.Artifacts = append(e.Artifacts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return e, err
	}
	rows, err = tx.Query(`SELECT name,passed,detail FROM v2_plan_checks WHERE attempt_id=? ORDER BY name`, id)
	if err != nil {
		return e, err
	}
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.Name, &c.Passed, &c.Detail); err != nil {
			rows.Close()
			return e, err
		}
		e.Checks = append(e.Checks, c)
	}
	err = rows.Err()
	rows.Close()
	return e, err
}

// TaskAttempts pages immutable attempt IDs, newest first. Cursor membership is
// checked against both Run and Task; no host paths are included in JSON.
func (s *Store) TaskAttempts(run, task, before string) (TaskHistory, error) {
	out := TaskHistory{Attempts: []Execution{}}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM v2_plan_tasks WHERE run_id=? AND task_id=?`, run, task).Scan(&exists); err != nil {
		return out, err
	}
	var boundary int64 = 9223372036854775807
	if before != "" {
		if err := tx.QueryRow(`SELECT rowid FROM v2_plan_attempts WHERE id=? AND run_id=? AND task_id=?`, before, run, task).Scan(&boundary); err == sql.ErrNoRows {
			return out, ErrInvalid
		} else if err != nil {
			return out, err
		}
	}
	rows, err := tx.Query(`SELECT id FROM v2_plan_attempts WHERE run_id=? AND task_id=? AND rowid<? ORDER BY rowid DESC LIMIT 11`, run, task, boundary)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > 10 {
		ids = ids[:10]
		out.NextCursor = ids[9]
	}
	for _, id := range ids {
		e, err := readPlannedExecution(tx, id)
		if err != nil {
			return out, err
		}
		out.Attempts = append(out.Attempts, e)
	}
	return out, tx.Commit()
}
