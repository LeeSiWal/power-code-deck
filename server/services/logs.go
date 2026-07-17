package services

import (
	"database/sql"
	"time"
)

// insertAgentLog appends one activity-log row for an agent. It's best-effort:
// logging must never break the operation that triggered it, so errors are
// ignored. created_at is stored WITHOUT a trailing 'Z' (naive UTC) because the
// Logs page appends 'Z' before parsing — matching that avoids "Invalid Date".
func insertAgentLog(db *sql.DB, agentID, data string) {
	if db == nil || agentID == "" || data == "" {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, _ = db.Exec("INSERT INTO logs (agent_id, data, created_at) VALUES (?, ?, ?)", agentID, data, now)
}
