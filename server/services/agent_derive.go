package services

import (
	"path/filepath"
	"strings"
)

// autoNamePrefix is the name an auto session gets ("자동 - <folder>").
const autoNamePrefix = "자동 - "

// NewSessionRequest is what "새 세션" (/clear) creates from a source session.
// A session that is auto-routed (or an auto session not bound yet) yields a new
// auto session, so the next first message is routed again; copying its tool
// used to mint a "자동 - …" session that silently ran one fixed tool. Any other
// session is copied as before, except that a leftover "자동 - …" name is
// replaced by the tool it actually runs.
func NewSessionRequest(src *Agent, command string) (req CreateAgentRequest, auto bool) {
	if src.AutoProfile != "" || src.Preset == AutoPreset {
		return CreateAgentRequest{Preset: AutoPreset, Name: autoNamePrefix + folderName(src.WorkingDir), WorkingDir: src.WorkingDir}, true
	}
	return CreateAgentRequest{Preset: src.Preset, Name: DerivedName(src), WorkingDir: src.WorkingDir, Command: command}, false
}

// DerivedName names a copy of src that runs src's tool: its own name, unless
// that name claims "자동" while the copy is not auto-routed.
func DerivedName(src *Agent) string {
	if !strings.HasPrefix(src.Name, autoNamePrefix) {
		return src.Name
	}
	return toolDisplayName(src) + " - " + folderName(src.WorkingDir)
}

func toolDisplayName(a *Agent) string {
	switch nativeDriverFor(a.Preset, a.Command) {
	case "codex":
		return "Codex"
	case "antigravity":
		return "Antigravity"
	}
	if a.Preset == "claude-code" || a.Preset == "claude" || a.Command == "claude" {
		return "Claude Code"
	}
	return a.Command
}

func folderName(dir string) string {
	if b := filepath.Base(strings.TrimRight(dir, "/")); b != "" && b != "." && b != "/" {
		return b
	}
	return dir
}
