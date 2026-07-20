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

// logs_fts is an external-content FTS5 index, so it only stays in sync with the
// logs table through triggers. Without these the FTS MATCH search returns nothing
// even when rows exist. logs are append-only; the delete trigger keeps search
// consistent when an agent (and its logs, via ON DELETE CASCADE) is removed.
const logsFtsTriggerMigration = `
CREATE TRIGGER IF NOT EXISTS logs_ai AFTER INSERT ON logs BEGIN
  INSERT INTO logs_fts(rowid, data) VALUES (new.id, new.data);
END;
CREATE TRIGGER IF NOT EXISTS logs_ad AFTER DELETE ON logs BEGIN
  INSERT INTO logs_fts(logs_fts, rowid, data) VALUES('delete', old.id, old.data);
END;
`

// The native track resumes a conversation with `claude --resume <session_id>`,
// where the id is Claude's own (from system/init) — not our agent id. It has to
// outlive the process, so it lives on the agent row. Non-fatal if it already
// exists, like the other ALTERs here.
const nativeSessionMigration = `
ALTER TABLE agents ADD COLUMN claude_session_id TEXT DEFAULT '';
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

// Web Push: browser push subscriptions (one row per installed PWA / browser), and
// a tiny key/value store for server-wide config — used to persist the VAPID keypair
// so every device subscribes against the same application-server identity across
// restarts. Subscriptions are keyed by their endpoint URL (unique per browser).
const pushMigration = `
CREATE TABLE IF NOT EXISTS push_subscriptions (
    endpoint TEXT PRIMARY KEY,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS app_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// FTS5 creation may fail on some builds, non-fatal
	db.Exec(ftsMigration)
	// Keep the FTS index in sync with the logs table (no-op / non-fatal if FTS
	// isn't available on this build).
	db.Exec(logsFtsTriggerMigration)

	// Native resume id — may already exist, non-fatal
	db.Exec(nativeSessionMigration)

	// Color columns — may already exist, non-fatal
	for _, stmt := range []string{
		"ALTER TABLE agents ADD COLUMN color_hue INTEGER DEFAULT 220",
		"ALTER TABLE agents ADD COLUMN color_name TEXT DEFAULT 'blue'",
		// Native-chat model + permission mode, remembered per session so a restart
		// or another device resumes with the same choices.
		"ALTER TABLE agents ADD COLUMN native_model TEXT DEFAULT ''",
		"ALTER TABLE agents ADD COLUMN native_mode TEXT DEFAULT ''",
	} {
		db.Exec(stmt)
	}

	// Notifications table
	db.Exec(notificationMigration)

	// Handoff tokens table
	db.Exec(handoffMigration)

	// Web Push subscriptions + app_config KV
	db.Exec(pushMigration)

	return nil
}
