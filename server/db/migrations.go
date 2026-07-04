package db

import "database/sql"

const schema = `
CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    preset TEXT NOT NULL,
    name TEXT NOT NULL,
    tmux_session TEXT NOT NULL UNIQUE,
    working_dir TEXT NOT NULL,
    command TEXT NOT NULL,
    args TEXT DEFAULT '[]',
    status TEXT DEFAULT 'stopped',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS recent_projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    last_opened_at TEXT DEFAULT (datetime('now')),
    last_agent_preset TEXT,
    open_count INTEGER DEFAULT 1
);

CREATE TABLE IF NOT EXISTS logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_logs_agent_id ON logs(agent_id);
CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(created_at);
CREATE INDEX IF NOT EXISTS idx_recent_projects_last_opened ON recent_projects(last_opened_at DESC);
`

const ftsMigration = `
CREATE VIRTUAL TABLE IF NOT EXISTS logs_fts USING fts5(data, content='logs', content_rowid='id');
`

const colorMigration = `
ALTER TABLE agents ADD COLUMN color_hue INTEGER DEFAULT 220;
ALTER TABLE agents ADD COLUMN color_name TEXT DEFAULT 'blue';
`

const notificationMigration = `
CREATE TABLE IF NOT EXISTS notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    message TEXT NOT NULL,
    read BOOLEAN DEFAULT FALSE,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(agent_id, read);
`

// Session Handoff — one-time tokens for "Continue on Mobile". Only the SHA-256
// hash of the token is stored; the raw token never touches the database.
const handoffMigration = `
CREATE TABLE IF NOT EXISTS handoff_tokens (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL,
    used_at TEXT,
    created_by TEXT,
    client_ip TEXT,
    user_agent TEXT
);
CREATE INDEX IF NOT EXISTS idx_handoff_session ON handoff_tokens(session_id);
CREATE INDEX IF NOT EXISTS idx_handoff_expires ON handoff_tokens(expires_at);
`

func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// FTS5 creation may fail on some builds, non-fatal
	db.Exec(ftsMigration)

	// Color columns — may already exist, non-fatal
	for _, stmt := range []string{
		"ALTER TABLE agents ADD COLUMN color_hue INTEGER DEFAULT 220",
		"ALTER TABLE agents ADD COLUMN color_name TEXT DEFAULT 'blue'",
	} {
		db.Exec(stmt)
	}

	// Notifications table
	db.Exec(notificationMigration)

	// Handoff tokens table
	db.Exec(handoffMigration)

	return nil
}
