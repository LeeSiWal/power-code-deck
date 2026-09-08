// Package history stores opaque chat events without depending on a provider or UI.
package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

const Limit = 2000

type Store struct{ db *sql.DB }

func New(database *sql.DB) *Store { return &Store{db: database} }

// Append commits the displayed event and its explicit resume ID together.
// Foreign-key deletion prevents a late process event from resurrecting an agent.
func (s *Store) Append(agent, provider string, raw json.RawMessage, resume string) error {
	if !json.Valid(raw) {
		return fmt.Errorf("history event is invalid JSON")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO native_history(agent_id,provider,event) VALUES(?,?,?)`, agent, provider, string(raw)); err != nil {
		return err
	}
	if resume != "" {
		if _, err := tx.Exec(`UPDATE agents SET claude_session_id=? WHERE id=?`, resume, agent); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM native_history WHERE agent_id=? AND provider=? AND id NOT IN
		(SELECT id FROM native_history WHERE agent_id=? AND provider=? ORDER BY id DESC LIMIT ?)`, agent, provider, agent, provider, Limit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Load(agent, provider string) ([]json.RawMessage, error) {
	rows, err := s.db.Query(`SELECT event FROM (SELECT id,event FROM native_history WHERE agent_id=? AND provider=? ORDER BY id DESC LIMIT ?) ORDER BY id`, agent, provider, Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []json.RawMessage
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if !json.Valid([]byte(raw)) {
			return nil, fmt.Errorf("stored history event is invalid JSON")
		}
		result = append(result, json.RawMessage(raw))
	}
	return result, rows.Err()
}
