package services

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
	"powercodedeck/db"
)

// An auto session starts no process (nil engine proves it), and binds exactly once.
func TestAutoSessionCreateAndBind(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	s := &AgentService{db: database}
	a, err := s.Create(CreateAgentRequest{Preset: AutoPreset, Name: "자동 - p", Command: "claude", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "stopped" || a.Command != "" {
		t.Fatalf("auto session started or kept a command: %+v", a)
	}
	if _, err := s.Restart(a.ID); err != nil {
		t.Fatalf("restart of an unbound session: %v", err)
	}
	b, err := s.Bind(a.ID, BindRequest{Preset: "antigravity", Command: "agy"})
	if err != nil || b.Preset != "antigravity" || b.Command != "agy" || b.ID != a.ID {
		t.Fatalf("bind: %+v %v", b, err)
	}
	if got, _ := s.Get(a.ID); got.Preset != "antigravity" || got.Name != "자동 - p" {
		t.Fatalf("stored row: %+v", got)
	}
	if _, err := s.Bind(a.ID, BindRequest{Preset: "antigravity", Command: "agy"}); !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("second bind: %v", err)
	}
}

// recordingEngine records Create; other SessionEngine methods are unused here.
type recordingEngine struct {
	SessionEngine
	created []CreateSessionRequest
}

func (e *recordingEngine) HasSession(id string) bool {
	for _, c := range e.created {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (e *recordingEngine) Kill(id string) error { return nil }

func (e *recordingEngine) Create(req CreateSessionRequest) (*SessionInfo, error) {
	e.created = append(e.created, req)
	return &SessionInfo{}, nil
}

// Binding to Claude starts its process like Create would and applies the routed
// model/effort over what the project's last session would pass on.
func TestAutoSessionBindClaudeAppliesRoutedConfig(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	eng := &recordingEngine{}
	s := &AgentService{db: database, engine: eng}
	dir := t.TempDir()
	if _, err := database.Exec(`INSERT INTO agents (id, preset, name, tmux_session, working_dir, command, native_model, native_mode, native_effort, created_at)
		VALUES ('old', 'claude-code', 'n', 'pcd-old', ?, 'claude', 'claude-opus-5-5', 'plan', 'high', '2026-09-25 10:00:00')`, dir); err != nil {
		t.Fatal(err)
	}
	a, err := s.Create(CreateAgentRequest{Preset: AutoPreset, Name: "자동", WorkingDir: dir})
	if err != nil || len(eng.created) != 0 {
		t.Fatalf("create: %v, processes=%d", err, len(eng.created))
	}
	b, err := s.Bind(a.ID, BindRequest{Preset: "claude-code", Command: "claude", NativeModel: "claude-sonnet-5", NativeEffort: "low"})
	if err != nil || b.Status != "running" || len(eng.created) != 1 || eng.created[0].Command != "claude" || eng.created[0].ID != a.ID {
		t.Fatalf("bind: %+v %v %+v", b, err, eng.created)
	}
	if m, mode, e := s.NativeConfig(a.ID); m != "claude-sonnet-5" || mode != "plan" || e != "low" {
		t.Fatalf("native config %q %q %q", m, mode, e)
	}
}

// Moving an auto session Claude → Codex → Claude keeps each tool's own
// conversation: Codex starts fresh, and Claude resumes where it left off, with
// the turn count at which it left recorded for the delta handoff.
func TestAutoSessionSwitchToolKeepsEachConversation(t *testing.T) {
	database, err := sql.Open("sqlite", t.TempDir()+"/agents.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	eng := &recordingEngine{}
	s := &AgentService{db: database, engine: eng}
	a, _ := s.Create(CreateAgentRequest{Preset: AutoPreset, Name: "자동", WorkingDir: t.TempDir()})
	if _, err := s.Bind(a.ID, BindRequest{Preset: "claude-code", Command: "claude", NativeModel: "claude-sonnet-5", NativeEffort: "low", AutoProfile: "claude-sonnet5-low"}); err != nil {
		t.Fatal(err)
	}
	s.SetClaudeSessionID(a.ID, "conv-claude")
	b, err := s.SwitchTool(a.ID, BindRequest{Preset: "codex-cli", Command: "codex", NativeModel: "gpt-5.6-sol", NativeEffort: "xhigh", AutoProfile: "codex-sol-xhigh"}, 3)
	if err != nil || b.Preset != "codex-cli" || b.AutoProfile != "codex-sol-xhigh" {
		t.Fatalf("to codex: %+v %v", b, err)
	}
	if sid := s.ClaudeSessionID(a.ID); sid != "" {
		t.Fatalf("codex should start fresh, resume id %q", sid)
	}
	if m, _, e := s.NativeConfig(a.ID); m != "gpt-5.6-sol" || e != "" {
		t.Fatalf("codex config %q %q", m, e)
	}
	s.SetClaudeSessionID(a.ID, "conv-codex")
	if _, err := s.SwitchTool(a.ID, BindRequest{Preset: "claude-code", Command: "claude", NativeModel: "claude-opus-5-5", NativeEffort: "high", AutoProfile: "claude-opus55-high"}, 5); err != nil {
		t.Fatal(err)
	}
	if sid := s.ClaudeSessionID(a.ID); sid != "conv-claude" {
		t.Fatalf("claude should resume its own conversation, got %q", sid)
	}
	if ts, ok := s.ToolSessionOf(a.ID, "claude"); !ok || ts.Turns != 3 {
		t.Fatalf("claude left at turn %+v", ts)
	}
	if ts, ok := s.ToolSessionOf(a.ID, "codex"); !ok || ts.Conv != "conv-codex" || ts.Turns != 5 {
		t.Fatalf("codex session %+v", ts)
	}
}
