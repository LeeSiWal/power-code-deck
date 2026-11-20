package services

import (
	"strings"
	"testing"
)

func TestSendResetsTeamTurn(t *testing.T) {
	s := &NativeService{sessions: map[string]*nativeSession{}}
	s.sessions["a"] = &nativeSession{id: "a", kind: "claude", driver: &fakeTurnDriver{}}
	var got []string
	s.SetTurnHook(func(id string) { got = append(got, id) })
	if err := s.Send("a", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("hook calls = %v", got)
	}
}

func TestTeamConductorAndReady(t *testing.T) {
	s := &NativeService{sessions: map[string]*nativeSession{}}
	s.sessions["a"] = &nativeSession{id: "a", kind: "claude", model: "claude-opus-5-5", cwd: "/w", teamTools: true}
	s.sessions["g"] = &nativeSession{id: "g", kind: "antigravity", cwd: "/w"}
	if k, m, c, ok := s.TeamConductor("a"); !ok || k != "claude" || m != "claude-opus-5-5" || c != "/w" {
		t.Fatalf("conductor = %q %q %q %v", k, m, c, ok)
	}
	if _, _, _, ok := s.TeamConductor("g"); ok {
		t.Fatal("antigravity cannot conduct")
	}
	if _, _, _, ok := s.TeamConductor("missing"); ok {
		t.Fatal("missing session reported ok")
	}
	if !s.TeamReady("a") || s.TeamReady("g") || s.TeamReady("missing") {
		t.Fatal("ready flags wrong")
	}
}

// A Codex conductor opened by NativeService gets the pcd MCP server with a fresh
// session token (the one /internal/native/team checks) and is marked ready.
func TestNativeServiceCodexConductorTeamMCP(t *testing.T) {
	argvFile, envFile := fakeCodex(t)
	s := NewNativeService("http://127.0.0.1:33033")
	s.selfBin = "/opt/pcd"
	s.SetTeamTools()
	if err := s.Start("agent-c", "codex", t.TempDir(), "", "", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop("agent-c")
	argv := strings.Join(readLines(t, argvFile), " ")
	if !strings.Contains(argv, `mcp_servers.pcd.command="/opt/pcd"`) || !strings.Contains(argv, "mcp_servers.pcd.tool_timeout_sec=660") {
		t.Fatalf("argv lacks pcd server: %q", readLines(t, argvFile))
	}
	token := ""
	for _, kv := range readLines(t, envFile) {
		if v, ok := strings.CutPrefix(kv, "PCD_APPROVE_TOKEN="); ok {
			token = v
		}
	}
	env := strings.Join(readLines(t, envFile), "\n")
	if !strings.Contains(env, "PCD_TEAM_URL=http://127.0.0.1:33033/internal/native/team") || !strings.Contains(env, "PCD_MCP_TOOLS=team") {
		t.Fatal("codex env lacks the team url / tools")
	}
	if token == "" || !s.Tokens().Valid("agent-c", token) {
		t.Fatalf("codex env token %q is not the session's native token", token)
	}
	if !s.TeamReady("agent-c") {
		t.Fatal("codex conductor not marked ready")
	}
}

// Without team tools configured (or with no pcd binary) a Codex session gets no
// MCP server, no token, and is not ready.
func TestNativeServiceCodexWithoutTeamTools(t *testing.T) {
	argvFile, _ := fakeCodex(t)
	s := NewNativeService("http://127.0.0.1:33033")
	s.selfBin = "/opt/pcd"
	if err := s.Start("agent-d", "codex", t.TempDir(), "", "", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop("agent-d")
	if got := strings.Join(readLines(t, argvFile), " "); got != "app-server --stdio" {
		t.Fatalf("argv = %q", got)
	}
	if s.TeamReady("agent-d") {
		t.Fatal("plain codex marked ready")
	}

	argvFile, _ = fakeCodex(t)
	s2 := NewNativeService("http://127.0.0.1:33033")
	s2.selfBin = ""
	s2.SetTeamTools()
	if err := s2.Start("agent-e", "codex", t.TempDir(), "", "", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s2.Stop("agent-e")
	if got := strings.Join(readLines(t, argvFile), " "); got != "app-server --stdio" || s2.TeamReady("agent-e") {
		t.Fatalf("no selfBin: argv = %q ready=%v", got, s2.TeamReady("agent-e"))
	}
}

// recordingDriver records each text the CLI receives.
type recordingDriver struct {
	nativeExecution
	sent []string
}

func (d *recordingDriver) Send(text string) error { d.sent = append(d.sent, text); return nil }
func (d *recordingDriver) Stop()                  {}

// A pending team brief survives a Delegator/SwitchKind SetPrefix and goes in
// front of the handoff prefix, consumed once.
func TestTeamBriefSurvivesSetPrefix(t *testing.T) {
	d := &recordingDriver{}
	s := &NativeService{sessions: map[string]*nativeSession{}}
	s.sessions["a"] = &nativeSession{id: "a", kind: "claude", driver: d}
	s.SetTeamBrief("a", "<드림팀 모드>\n</드림팀 모드>\n\n")
	s.SetPrefix("a", "<강한 모델의 조언>\nadvice\n</강한 모델의 조언>\n\n[현재 요청]\n")
	if err := s.Send("a", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := s.Send("a", "again"); err != nil {
		t.Fatal(err)
	}
	want := "<드림팀 모드>\n</드림팀 모드>\n\n<강한 모델의 조언>\nadvice\n</강한 모델의 조언>\n\n[현재 요청]\nhi"
	if len(d.sent) != 2 || d.sent[0] != want || d.sent[1] != "again" {
		t.Fatalf("sent = %q", d.sent)
	}
}

// Queuing the brief again (on→off→on before the next message) never doubles it;
// an empty brief clears a pending one.
func TestTeamBriefDedupe(t *testing.T) {
	d := &recordingDriver{}
	s := &NativeService{sessions: map[string]*nativeSession{}}
	s.sessions["a"] = &nativeSession{id: "a", kind: "claude", driver: d}
	s.SetTeamBrief("a", "B\n")
	s.SetTeamBrief("a", "B\n")
	s.Send("a", "x")
	s.SetTeamBrief("a", "B\n")
	s.SetTeamBrief("a", "")
	s.Send("a", "y")
	if len(d.sent) != 2 || d.sent[0] != "B\nx" || d.sent[1] != "y" {
		t.Fatalf("sent = %q", d.sent)
	}
}

// A tool switch starts a new conductor conversation: the brief is queued once,
// written for the new tool, alongside the handoff prefix.
func TestSwitchKindQueuesTeamBrief(t *testing.T) {
	fakeCodex(t)
	s := NewNativeService("http://127.0.0.1:33033")
	s.selfBin = "/opt/pcd"
	s.SetTeamTools()
	s.SetTeamBriefSource(func(id, kind, model string) (string, bool) { return "BRIEF:" + kind + "\n", true })
	s.sessions["a"] = &nativeSession{id: "a", kind: "claude", driver: &recordingDriver{}, cwd: t.TempDir(), teamTools: true}
	s.SetTeamBrief("a", "BRIEF:claude\n") // pending from the toggle
	if err := s.SwitchKind("a", "codex", "", "", "HANDOFF\n"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	defer s.Stop("a")
	s.mu.RLock()
	brief, prefix := s.teamBrief["a"], s.prefix["a"]
	s.mu.RUnlock()
	if brief != "BRIEF:codex\n" || prefix != "HANDOFF\n" {
		t.Fatalf("brief %q prefix %q", brief, prefix)
	}
}

// A fresh conductor conversation with team mode on gets the brief; off (or a
// resumed conversation) gets none.
func TestFreshStartQueuesTeamBrief(t *testing.T) {
	for _, c := range []struct {
		name   string
		on     bool
		resume string
		want   string
	}{
		{"fresh on", true, "", "BRIEF:codex\n"},
		{"fresh off", false, "", ""},
		{"resumed on", true, "thr-1", ""},
	} {
		fakeCodex(t)
		s := NewNativeService("http://127.0.0.1:33033")
		s.selfBin = "/opt/pcd"
		s.SetTeamTools()
		s.SetTeamBriefSource(func(id, kind, model string) (string, bool) { return "BRIEF:" + kind + "\n", c.on })
		if err := s.Start("b", "codex", t.TempDir(), "", c.resume, "", ""); err != nil {
			t.Fatalf("%s: start: %v", c.name, err)
		}
		s.mu.RLock()
		got := s.teamBrief["b"]
		s.mu.RUnlock()
		s.Stop("b")
		if got != c.want {
			t.Errorf("%s: brief = %q, want %q", c.name, got, c.want)
		}
	}
}

// A message queued while a turn is still running does not start a new turn, so
// it must not reset the member-call counter.
func TestSendMidTurnKeepsTeamTurn(t *testing.T) {
	s := &NativeService{sessions: map[string]*nativeSession{}}
	s.sessions["a"] = &nativeSession{id: "a", kind: "claude", driver: &recordingDriver{}}
	var got int
	s.SetTurnHook(func(string) { got++ })
	if err := s.Send("a", "first"); err != nil { // starts a turn
		t.Fatal(err)
	}
	if err := s.Send("a", "queued"); err != nil { // same turn, still running
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("hook calls = %d, want 1", got)
	}
	s.emit(s.sessions["a"], nativeResultEvent()) // the turn ends
	if err := s.Send("a", "next"); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("hook calls = %d, want 2", got)
	}
}
