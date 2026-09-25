package orchestration

import (
	"database/sql"
	"encoding/json"
)

type DraftHistory struct {
	Drafts     []PlanDraft `json:"drafts"`
	NextCursor string      `json:"nextCursor"`
}

// DraftHistory uses a Run-scoped cursor so newly generated drafts cannot shift
// subsequent pages. Workspace paths are deliberately excluded from the result.
func (s *Store) DraftHistory(run, before string) (DraftHistory, error) {
	out := DraftHistory{Drafts: []PlanDraft{}}
	if len(before) > 128 {
		return out, ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM v2_runs WHERE id=?`, run).Scan(&exists); err != nil {
		return out, err
	}
	var boundary int64 = 9223372036854775807
	if before != "" {
		if err := tx.QueryRow(`SELECT ordinal FROM v2_plan_drafts WHERE run_id=? AND id=?`, run, before).Scan(&boundary); err == sql.ErrNoRows {
			return out, ErrInvalid
		} else if err != nil {
			return out, err
		}
	}
	rows, err := tx.Query(`SELECT id,state,detail,plan FROM v2_plan_drafts WHERE run_id=? AND ordinal<? ORDER BY ordinal DESC LIMIT 11`, run, boundary)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d PlanDraft
		var raw string
		if err := rows.Scan(&d.ID, &d.State, &d.Detail, &raw); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Drafts) == 10 {
			out.NextCursor = out.Drafts[9].ID
			break
		}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &d.Plan); err != nil {
				rows.Close()
				return out, err
			}
		}
		out.Drafts = append(out.Drafts, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
