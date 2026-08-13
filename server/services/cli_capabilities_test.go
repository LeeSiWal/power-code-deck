package services

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The deck and the CLI ship separately. A flag the CLI has never heard of is not
// ignored — it kills the process on startup — so the deck must ask before passing one.
//
// This is the regression that broke every native session: --forward-subagent-text and
// --autocompact were added against a newer CLI on the developer's PATH, while the
// service resolved an older build that rejected both.
func TestBuildArgsOmitsFlagsTheCLIDoesNotSupport(t *testing.T) {
	// An older CLI: knows the protocol flags and --effort, but not the two newer ones.
	old := map[string]bool{
		"-p": true, "--input-format": true, "--output-format": true, "--verbose": true,
		"--include-partial-messages": true, "--mcp-config": true,
		"--permission-prompt-tool": true, "--model": true, "--resume": true,
		"--append-system-prompt": true, "--permission-mode": true,
		"--effort": true, "--add-dir": true,
	}

	d := NewClaudeDriver(ClaudeConfig{
		SessionID: "a1", SelfPath: "/opt/pcd", Effort: "high", Model: "claude-opus-5",
		Options: NativeOptions{
			AddDirs:       []string{"/one"},
			MaxBudgetUSD:  10,
			Autocompact:   "300000",
			FallbackModel: "claude-sonnet-5",
		},
	})
	d.supports = old
	args := strings.Join(d.buildArgs(), " ")

	for _, missing := range []string{"--forward-subagent-text", "--autocompact", "--max-budget-usd", "--fallback-model"} {
		if strings.Contains(args, missing) {
			t.Errorf("passed %s to a CLI that does not support it: %s", missing, args)
		}
	}
	// What the CLI does support must still be passed — capability gating must not
	// become a blanket excuse to drop settings.
	for _, present := range []string{"--effort high", "--add-dir /one", "--model claude-opus-5"} {
		if !strings.Contains(args, present) {
			t.Errorf("dropped %s even though the CLI supports it: %s", present, args)
		}
	}
}

// An unknown capability set means "couldn't tell", and the deck falls back to passing
// everything — the behaviour from before the probe existed. Guessing "unsupported"
// would silently disable working features.
func TestBuildArgsPassesEverythingWhenCapabilitiesAreUnknown(t *testing.T) {
	d := NewClaudeDriver(ClaudeConfig{SessionID: "a1", SelfPath: "/opt/pcd"})
	if d.supports != nil {
		t.Fatal("a fresh driver must start with no capability knowledge")
	}
	args := strings.Join(d.buildArgs(), " ")
	for _, flag := range []string{"--effort", "--forward-subagent-text", "--autocompact"} {
		if !strings.Contains(args, flag) {
			t.Errorf("unknown capabilities should pass %s: %s", flag, args)
		}
	}
}

// The probe reads the real --help of a real binary, so a CLI upgrade is picked up
// without a deck restart (the cache is keyed by path + mtime + size).
func TestSupportedFlagsParsesHelpOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub is POSIX")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecli")
	script := "#!/bin/sh\necho 'Options:'\necho '  --effort <level>   effort'\necho '  --model <m>        model'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	flags := supportedFlags(bin)
	if flags == nil {
		t.Fatal("probe returned nil for a working binary")
	}
	if !flags["--effort"] || !flags["--model"] {
		t.Errorf("did not parse advertised flags: %v", flags)
	}
	if flags["--forward-subagent-text"] {
		t.Error("reported a flag the help output never mentions")
	}

	// A binary that cannot be probed at all yields nil, not an empty set — the caller
	// distinguishes "supports nothing" from "couldn't tell", and only the latter is safe
	// to treat as "pass everything".
	if got := supportedFlags(filepath.Join(dir, "does-not-exist")); got != nil {
		t.Errorf("probe of a missing binary returned %v, want nil", got)
	}
}
