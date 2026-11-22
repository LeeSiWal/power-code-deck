package services

import (
	"database/sql"
	"testing"

	"powercodedeck/db"
)

func teamTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestDefaultTeamSettings(t *testing.T) {
	s := DefaultTeamSettings()
	if len(s.Members) != 4 {
		t.Fatalf("members = %d", len(s.Members))
	}
	want := map[string]bool{"codex": true, "claude-sonnet": true, "claude-haiku": true, "claude-fable": false}
	for _, m := range s.Members {
		if m.Enabled != want[m.ID] {
			t.Errorf("%s enabled = %v", m.ID, m.Enabled)
		}
		if m.ID == "claude-fable" && m.Model != "claude-fable-5-2" {
			t.Errorf("fable model = %q", m.Model)
		}
	}
	if s.ResultMaxChars != 6000 || s.MaxCallsPerTurn != 3 || s.SessionTokenCap != 300000 || s.TimeoutSec != 180 {
		t.Fatalf("limits: %+v", s)
	}
}

func TestLoadTeamSettingsFallbacks(t *testing.T) {
	database := teamTestDB(t)
	if _, err := database.Exec("INSERT INTO app_config(key,value) VALUES('team_settings', ?)",
		`{"members":[{"id":"bogus"}],"maxCallsPerTurn":0}`); err != nil {
		t.Fatal(err)
	}
	s := LoadTeamSettings(database)
	if len(s.Members) != 4 || s.MaxCallsPerTurn != 3 {
		t.Fatalf("got %+v", s)
	}
	s.Members[3].Enabled = true
	s.TimeoutSec = 60
	if err := SaveTeamSettings(database, s); err != nil {
		t.Fatal(err)
	}
	got := LoadTeamSettings(database)
	if !got.Members[3].Enabled || got.TimeoutSec != 60 {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestTeamModeRoundTrip(t *testing.T) {
	database := teamTestDB(t)
	s := &AgentService{db: database}
	a, err := s.Create(CreateAgentRequest{Preset: AutoPreset, Name: "t", Command: "claude", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if s.TeamMode(a.ID) {
		t.Fatal("new agent has team mode on")
	}
	if err := s.SetTeamMode(a.ID, true); err != nil || !s.TeamMode(a.ID) {
		t.Fatalf("set on: %v", err)
	}
	if err := s.SetTeamSession(a.ID, "codex", "t1"); err != nil {
		t.Fatal(err)
	}
	if got := s.TeamSessions(a.ID)["codex"]; got != "t1" {
		t.Fatalf("session = %q", got)
	}
}

// TimeoutSec is clamped to TeamMaxTimeoutSec: conductors are spawned with a
// fixed CLI tool timeout derived from it, so a live Settings change can never
// outlast what the CLI will wait for.
func TestTeamSettingsClampTimeout(t *testing.T) {
	s := DefaultTeamSettings()
	s.TimeoutSec = 3600
	if got := NormalizeTeamSettings(s).TimeoutSec; got != 600 || TeamMaxTimeoutSec != 600 {
		t.Fatalf("clamped = %d (max %d), want 600", got, TeamMaxTimeoutSec)
	}
	if TeamToolTimeoutSec != 660 {
		t.Fatalf("tool timeout = %d, want 660", TeamToolTimeoutSec)
	}
}
