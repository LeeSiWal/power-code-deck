package db

import (
	"database/sql"
	"errors"
)

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
-- The device that currently "owns" each agent's session: the last one to open or
-- reclaim it. Drives both exclusive viewing (others go to standby) and push
-- targeting (only this device's subscription is notified). One row per agent.
CREATE TABLE IF NOT EXISTS agent_active_device (
    agent_id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
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
		// Effort는 사고 깊이와 토큰 소비량을 함께 정한다. 빈 값은 "아직 고른 적 없음"이고,
		// 그때 서버가 high를 넘긴다 — CLI 기본값(xhigh)을 그대로 두면 라우팅 같은 가벼운
		// 작업까지 최상위 설정으로 돌아 토큰이 필요 이상으로 나간다.
		"ALTER TABLE agents ADD COLUMN native_effort TEXT DEFAULT ''",
		// Session options that are set once and left alone (extra directories, spend
		// cap, auto-compaction window, fallback model). One JSON blob rather than a
		// column each: the next knob then costs a struct field, not a migration.
		"ALTER TABLE agents ADD COLUMN native_options TEXT DEFAULT ''",
	} {
		db.Exec(stmt)
	}

	// Notifications table
	db.Exec(notificationMigration)
	db.Exec("ALTER TABLE notifications ADD COLUMN ref_type TEXT DEFAULT ''")
	db.Exec("ALTER TABLE notifications ADD COLUMN ref_id TEXT DEFAULT ''")

	// Handoff tokens table
	db.Exec(handoffMigration)

	// Web Push subscriptions + app_config KV + active-device table
	db.Exec(pushMigration)
	// Tie each push subscription to the device that registered it, so a notification
	// can be aimed at only the session's active device. Non-fatal if it already exists.
	db.Exec("ALTER TABLE push_subscriptions ADD COLUMN device_id TEXT DEFAULT ''")

	// 승인 허용 목록. 에이전트가 아니라 프로젝트(작업 디렉토리)에 속하므로 외래키를
	// 걸지 않는다 — 세션을 지우고 다시 만들어도 규칙은 살아남아야 한다.
	// UNIQUE가 중복 저장을 막는다(INSERT OR IGNORE와 짝).
	db.Exec(`CREATE TABLE IF NOT EXISTS approval_rules (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		working_dir TEXT NOT NULL,
		tool_name   TEXT NOT NULL,
		target      TEXT NOT NULL DEFAULT '',
		created_at  TEXT DEFAULT (datetime('now')),
		UNIQUE(working_dir, tool_name, target)
	)`)

	// Local Intelligence POC. Provider addresses are runtime data because the model
	// commonly runs on another LAN/Tailscale machine. Traces deliberately contain
	// measurements and state transitions only — never prompts, context, credentials,
	// or provider response bodies.
	db.Exec(`CREATE TABLE IF NOT EXISTS local_ai_providers (
		name       TEXT PRIMARY KEY,
		type       TEXT NOT NULL,
		base_url   TEXT NOT NULL,
		model      TEXT NOT NULL,
		timeout_ms INTEGER NOT NULL DEFAULT 180000,
		enabled    BOOLEAN NOT NULL DEFAULT TRUE,
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err := migrateLocalProviderTimeout(db); err != nil {
		return err
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS intelligence_traces (
		id               TEXT PRIMARY KEY,
		agent_id         TEXT NOT NULL DEFAULT '',
		mode             TEXT NOT NULL,
		status           TEXT NOT NULL,
		provider         TEXT NOT NULL DEFAULT '',
		model            TEXT NOT NULL DEFAULT '',
		raw_tokens       INTEGER NOT NULL DEFAULT 0,
		optimized_tokens INTEGER NOT NULL DEFAULT 0,
		local_tokens     INTEGER NOT NULL DEFAULT 0,
		latency_ms       INTEGER NOT NULL DEFAULT 0,
		error_code       TEXT NOT NULL DEFAULT '',
		fallback         BOOLEAN NOT NULL DEFAULT FALSE,
		events_json      TEXT NOT NULL DEFAULT '[]',
		created_at       TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	db.Exec("CREATE INDEX IF NOT EXISTS idx_intelligence_traces_created ON intelligence_traces(created_at DESC)")

	return nil
}

// The original Local Intelligence POC used 30 seconds for both a network check
// and an entire non-streaming Ollama generation. That is too short for a cold
// 30B model and a repository-sized prompt. Upgrade only the old default once;
// after the marker is written, an explicit user choice of 30000 is preserved.
func migrateLocalProviderTimeout(db *sql.DB) error {
	const marker = "local_intelligence_timeout_v2"
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var value string
	err = tx.QueryRow("SELECT value FROM app_config WHERE key=?", marker).Scan(&value)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec("UPDATE local_ai_providers SET timeout_ms=180000 WHERE timeout_ms=30000"); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO app_config(key,value) VALUES(?,?)", marker, "180000"); err != nil {
		return err
	}
	return tx.Commit()
}
