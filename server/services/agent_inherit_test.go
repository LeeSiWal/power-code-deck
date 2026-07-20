package services

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	"powercodedeck/db"
)

// TestInheritedNativeConfig verifies a new session picks up the model + permission
// mode from the last relevant session: same project first, then anywhere, then the
// defaults when there's nothing to inherit.
func TestInheritedNativeConfig(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	s := &AgentService{db: database}

	seed := func(id, dir, model, mode, createdAt string) {
		_, err := database.Exec(
			`INSERT INTO agents (id, preset, name, tmux_session, working_dir, command, native_model, native_mode, created_at)
			 VALUES (?, 'claude', 'n', ?, ?, 'claude', ?, ?, ?)`,
			id, "pcd-"+id, dir, model, mode, createdAt,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Nothing yet → defaults (empty).
	if m, md := s.inheritedNativeConfig("/proj/a", "claude"); m != "" || md != "" {
		t.Fatalf("expected empty defaults, got %q/%q", m, md)
	}

	// A session in another project sets a choice → used as the global fallback.
	seed("a1", "/proj/b", "claude-opus-4-8", "bypassPermissions", "2026-07-20T10:00:00Z")
	if m, md := s.inheritedNativeConfig("/proj/a", "claude"); m != "claude-opus-4-8" || md != "bypassPermissions" {
		t.Fatalf("global fallback failed: got %q/%q", m, md)
	}

	// A newer session in THIS project wins over the global one.
	seed("a2", "/proj/a", "claude-sonnet-5", "plan", "2026-07-20T11:00:00Z")
	if m, md := s.inheritedNativeConfig("/proj/a", "claude"); m != "claude-sonnet-5" || md != "plan" {
		t.Fatalf("same-project priority failed: got %q/%q", m, md)
	}

	// An even newer session in this project, but only a model set → that model, and
	// its (empty) mode, are what a new session inherits (most recent wins as a unit).
	seed("a3", "/proj/a", "claude-fable-5", "", "2026-07-20T12:00:00Z")
	if m, md := s.inheritedNativeConfig("/proj/a", "claude"); m != "claude-fable-5" || md != "" {
		t.Fatalf("most-recent-in-project failed: got %q/%q", m, md)
	}

	// A Codex choice never leaks a Claude model into a new Codex session.
	_, err = database.Exec(
		`INSERT INTO agents (id, preset, name, tmux_session, working_dir, command, native_model, native_mode, created_at)
		 VALUES ('c1', 'codex-cli', 'n', 'pcd-c1', '/proj/a', 'codex', 'gpt-5.4', 'acceptEdits', '2026-07-20T13:00:00Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if m, md := s.inheritedNativeConfig("/proj/a", "codex"); m != "gpt-5.4" || md != "acceptEdits" {
		t.Fatalf("codex inheritance failed: got %q/%q", m, md)
	}
}
