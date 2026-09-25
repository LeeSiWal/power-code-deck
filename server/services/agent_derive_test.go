package services

import "testing"

func TestNewSessionRequest(t *testing.T) {
	cases := []struct {
		name     string
		src      Agent
		wantAuto bool
		wantName string
		wantCmd  string
	}{
		{"auto-routed claude", Agent{Preset: "claude-code", Command: "claude", Name: "자동 - testCode", WorkingDir: "/home/u/code/testCode", AutoProfile: "claude-sonnet5-low"}, true, "자동 - testCode", ""},
		{"unbound auto", Agent{Preset: AutoPreset, Name: "자동 - testCode", WorkingDir: "/home/u/code/testCode/"}, true, "자동 - testCode", ""},
		// Auto routing turned off (a model picked by hand): same tool, honest name.
		{"auto turned off", Agent{Preset: "claude-code", Command: "claude", Name: "자동 - testCode", WorkingDir: "/home/u/code/testCode"}, false, "Claude Code - testCode", "claude"},
		{"codex copy", Agent{Preset: "codex-cli", Command: "codex", Name: "자동 - api", WorkingDir: "/srv/api"}, false, "Codex - api", "codex"},
		{"own name kept", Agent{Preset: "claude-code", Command: "claude", Name: "로그인 버그", WorkingDir: "/w"}, false, "로그인 버그", "claude"},
	}
	for _, c := range cases {
		req, auto := NewSessionRequest(&c.src, c.src.Command)
		if auto != c.wantAuto || req.Name != c.wantName || req.Command != c.wantCmd || (auto && req.Preset != AutoPreset) {
			t.Errorf("%s: auto=%v name=%q cmd=%q preset=%q", c.name, auto, req.Name, req.Command, req.Preset)
		}
	}
}
