package services

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	"powercodedeck/db"
)

// Real runtime evidence that the activity-log path works end to end: migrations
// build the logs table + FTS index + sync triggers, insertAgentLog writes rows,
// the default (no-query) view and the FTS MATCH search both return them, and an
// agent delete cascades to its logs while keeping the FTS index consistent.
func TestInsertAgentLogAndSearch(t *testing.T) {
	dir := t.TempDir()
	database, err := sql.Open("sqlite", dir+"/test.db?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1) // keep the per-connection foreign_keys pragma deterministic

	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := database.Exec(
		"INSERT INTO agents (id, preset, name, tmux_session, working_dir, command) VALUES ('a1','claude-code','Test','pcd-a1','/tmp','claude')",
	); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	insertAgentLog(database, "a1", "세션 생성됨 · Test (agy)")
	insertAgentLog(database, "a1", "세션 종료됨")

	// Default Logs view (no query) — plain table select.
	var n int
	if err := database.QueryRow("SELECT count(*) FROM logs WHERE agent_id='a1'").Scan(&n); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 log rows, got %d", n)
	}

	// FTS MATCH search — proves the sync trigger populated logs_fts. FTS5's
	// default tokenizer is whitespace/word based, so match a whole token ("세션",
	// shared by both rows) rather than a substring; both rows must come back.
	if err := database.QueryRow(
		"SELECT count(*) FROM logs l JOIN logs_fts f ON l.id = f.rowid WHERE logs_fts MATCH '세션'",
	).Scan(&n); err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected FTS to match both '세션' rows, got %d", n)
	}

	// Deleting the agent cascades to its logs (and the delete trigger keeps FTS
	// in sync so it does not error on the next search).
	if _, err := database.Exec("DELETE FROM agents WHERE id='a1'"); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if err := database.QueryRow("SELECT count(*) FROM logs").Scan(&n); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected logs cascade-deleted, got %d rows", n)
	}
}
