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

func TestAppendPrefixKeepsExisting(t *testing.T) {
	s := &NativeService{sessions: map[string]*nativeSession{}}
	s.AppendPrefix("a", "brief\n")
	if s.prefix["a"] != "brief\n" {
		t.Fatalf("first = %q", s.prefix["a"])
	}
	s.SetPrefix("b", "handoff")
	s.AppendPrefix("b", "brief")
	if s.prefix["b"] != "handoff\n\nbrief" {
		t.Fatalf("appended = %q", s.prefix["b"])
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
	s.SetTeamTools(func() int { return 180 })
	if err := s.Start("agent-c", "codex", t.TempDir(), "", "", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop("agent-c")
	argv := strings.Join(readLines(t, argvFile), " ")
	if !strings.Contains(argv, `mcp_servers.pcd.command="/opt/pcd"`) || !strings.Contains(argv, "mcp_servers.pcd.tool_timeout_sec=240") {
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
	s2.SetTeamTools(func() int { return 180 })
	if err := s2.Start("agent-e", "codex", t.TempDir(), "", "", "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s2.Stop("agent-e")
	if got := strings.Join(readLines(t, argvFile), " "); got != "app-server --stdio" || s2.TeamReady("agent-e") {
		t.Fatalf("no selfBin: argv = %q ready=%v", got, s2.TeamReady("agent-e"))
	}
}
