package handlers

import (
	"testing"

	"powercodedeck/services"
)

// A derived session must stay on the CLI its source agent runs. NewSession and
// ResumeSession used to hardcode "claude" while copying the source preset, so a
// /clear inside a Codex chat minted preset=codex-cli + command=claude. That row ran
// Claude Code on the Codex agent's PTY track and — because it matched BOTH driver
// branches of inheritedNativeConfig — handed a Codex model to the next Claude
// session created anywhere.
func TestSessionLaunchCommandKeepsTheAgentsCLI(t *testing.T) {
	cases := []struct {
		name  string
		agent services.Agent
		want  string
	}{
		{"codex preset", services.Agent{Preset: "codex-cli", Command: "codex"}, "codex"},
		{"codex preset with corrupted command", services.Agent{Preset: "codex-cli", Command: "claude"}, "codex"},
		{"codex by command alone", services.Agent{Preset: "", Command: "codex"}, "codex"},
		{"claude-code preset", services.Agent{Preset: "claude-code", Command: "claude"}, "claude"},
		{"claude preset", services.Agent{Preset: "claude", Command: "claude"}, "claude"},
		{"custom keeps its own command", services.Agent{Preset: "custom", Command: "/bin/bash"}, "/bin/bash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionLaunchCommand(&tc.agent); got != tc.want {
				t.Fatalf("sessionLaunchCommand(%+v) = %q, want %q", tc.agent, got, tc.want)
			}
		})
	}
}

// The preset must win over a command that disagrees with it: that is exactly the
// corrupted shape this fix exists to stop from propagating into new rows.
func TestSessionLaunchCommandNeverPropagatesTheHybridRow(t *testing.T) {
	broken := services.Agent{Preset: "codex-cli", Command: "claude"}
	if got := sessionLaunchCommand(&broken); got == "claude" {
		t.Fatal("a codex-cli agent must never derive a claude session")
	}
}
