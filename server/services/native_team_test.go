package services

import "testing"

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
