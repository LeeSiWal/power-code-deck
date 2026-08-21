package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateLocalProviderTimeoutUpgradesOldDefaultOnce(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	if _, err := database.Exec(`
		CREATE TABLE app_config (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE local_ai_providers (
			name TEXT PRIMARY KEY, type TEXT NOT NULL, base_url TEXT NOT NULL,
			model TEXT NOT NULL, timeout_ms INTEGER NOT NULL DEFAULT 30000,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO local_ai_providers(name,type,base_url,model,timeout_ms)
		VALUES('legacy','ollama','http://local.test','coder',30000);
	`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	var timeout int
	if err := database.QueryRow("SELECT timeout_ms FROM local_ai_providers WHERE name='legacy'").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 180000 {
		t.Fatalf("legacy timeout = %d, want 180000", timeout)
	}

	if _, err := database.Exec("UPDATE local_ai_providers SET timeout_ms=30000 WHERE name='legacy'"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT timeout_ms FROM local_ai_providers WHERE name='legacy'").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 30000 {
		t.Fatalf("explicit timeout was migrated twice: got %d", timeout)
	}
}
