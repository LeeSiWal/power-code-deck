package services

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"powercodedeck/db"
	"powercodedeck/internal/providers"
)

// Only auto sessions are recorded; unreported usage stays out of the sums (and
// counts as unreported, not zero); idle gaps land in their cache bucket.
func TestAutoUsageRecordsTurns(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	s := &AgentService{db: database, engine: &recordingEngine{}}
	a, _ := s.Create(CreateAgentRequest{Preset: AutoPreset, Name: "자동", WorkingDir: t.TempDir()})
	if _, err := s.Bind(a.ID, BindRequest{Preset: "claude-code", Command: "claude", NativeModel: "claude-sonnet-5", NativeEffort: "low", AutoProfile: "claude-sonnet5-low"}); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.Create(CreateAgentRequest{Preset: "claude-code", Name: "c", Command: "claude", WorkingDir: t.TempDir()})
	u := NewAutoUsage(database)

	u.Note(a.ID, TurnMeta{Switch: "start", IdleSeconds: -1})
	u.Observe(a.ID, nativeResultEventWithUsage(&StreamUsage{InputTokens: 100, OutputTokens: 20, CacheCreationInputTokens: 50}))
	u.Note(a.ID, TurnMeta{Switch: "model", IdleSeconds: 30})
	u.Observe(a.ID, nativeResultEventWithUsage(&StreamUsage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 90}))
	u.Note(a.ID, TurnMeta{Switch: "tool", HandoffTokens: 1200, IdleSeconds: 400})
	u.Observe(a.ID, nativeResultEvent()) // nothing reported
	u.Observe(a.ID, nativeTextEvent("assistant", "not a turn end"))
	u.Observe(plain.ID, nativeResultEventWithUsage(&StreamUsage{InputTokens: 999}))

	sum, err := u.Summary(a.ID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Turns != 3 || len(sum.Models) != 1 || sum.Models[0].Reported != 2 || sum.Models[0].Input != 110 || sum.Models[0].CacheRead != 90 {
		t.Fatalf("summary %+v", sum)
	}
	if sum.ModelSwitches != 1 || sum.ToolSwitches != 1 || sum.HandoffTokens != 1200 {
		t.Fatalf("switches %+v", sum)
	}
	if b := sum.Idle[0]; b.Turns != 1 || b.CacheRead != 90 || b.InputTotal != 100 {
		t.Fatalf("<1분 bucket %+v", b)
	}
	if b := sum.Idle[2]; b.Turns != 1 || b.Reported != 0 {
		t.Fatalf("5–10분 bucket %+v", b)
	}
	all, _ := u.Summary("", time.Now().Add(-time.Hour))
	if all.Turns != 3 {
		t.Fatalf("plain sessions must not be recorded: %d", all.Turns)
	}
	if err := s.Delete(a.ID); err == nil {
		if n, _ := u.Summary(a.ID, time.Time{}); n.Turns != 0 {
			t.Fatal("turns survived their session")
		}
	}
}

// Team member calls are not conductor turns: they stay out of the turn counts,
// per-model rows and handoff tokens, and are reported as their own total.
func TestAutoUsageSeparatesTeamCalls(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	s := &AgentService{db: database, engine: &recordingEngine{}}
	a, _ := s.Create(CreateAgentRequest{Preset: AutoPreset, Name: "자동", WorkingDir: t.TempDir()})
	if _, err := s.Bind(a.ID, BindRequest{Preset: "claude-code", Command: "claude", NativeModel: "claude-opus-5-5", AutoProfile: "claude-opus"}); err != nil {
		t.Fatal(err)
	}
	u := NewAutoUsage(database)
	u.Note(a.ID, TurnMeta{Switch: "start", IdleSeconds: -1})
	u.Observe(a.ID, nativeResultEventWithUsage(&StreamUsage{InputTokens: 100, OutputTokens: 20}))
	haiku := TeamMember{ID: "claude-haiku", Adapter: "claude", Model: "claude-haiku-5-5"}
	u.RecordTeam(a.ID, haiku, 500, &providers.Usage{InputTokens: 40, OutputTokens: 10, CacheCreationInputTokens: 5})
	u.RecordTeam(a.ID, TeamMember{ID: "codex", Adapter: "codex"}, 300, nil)

	for _, sum := range []UsageSummary{mustSummary(t, u, a.ID, time.Time{}), mustSummary(t, u, "", time.Now().Add(-time.Hour))} {
		if sum.Turns != 1 || len(sum.Models) != 1 || sum.HandoffTokens != 0 {
			t.Fatalf("team rows leaked into turn stats: %+v", sum)
		}
		tm := sum.Team
		if tm.Calls != 2 || tm.Reported != 1 || tm.Unknown != 1 || tm.Input != 40 || tm.Output != 10 || tm.CacheCreation != 5 {
			t.Fatalf("team total %+v", tm)
		}
	}
}

func mustSummary(t *testing.T, u *AutoUsage, agentID string, since time.Time) UsageSummary {
	t.Helper()
	sum, err := u.Summary(agentID, since)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}
