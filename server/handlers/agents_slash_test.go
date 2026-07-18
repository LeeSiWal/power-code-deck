package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSlashFile(t *testing.T, root, kind, name, body string) {
	t.Helper()
	dir := filepath.Join(root, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// collect mirrors how the handler merges roots: later roots overwrite a same-named
// entry from an earlier one.
func collect(roots ...[2]string) []SlashCommand {
	var out []SlashCommand
	seen := map[string]int{}
	add := func(c SlashCommand) {
		if i, ok := seen[c.Name]; ok {
			out[i] = c
			return
		}
		seen[c.Name] = len(out)
		out = append(out, c)
	}
	for _, r := range roots {
		scanSlashRoot(r[0], r[1], add)
	}
	return out
}

// A project's .claude/commands must appear alongside the user's, and win when both
// define the same name — without this, per-project commands (the ones you actually
// invoke while working in a repo) were invisible to the picker.
func TestScanSlashRootMergesProjectOverUser(t *testing.T) {
	userRoot, projRoot := t.TempDir(), t.TempDir()
	writeSlashFile(t, userRoot, "commands", "shared.md", "user version\n")
	writeSlashFile(t, userRoot, "commands", "useronly.md", "only for this user\n")
	writeSlashFile(t, projRoot, "commands", "shared.md", "project version\n")
	writeSlashFile(t, projRoot, "agents", "reviewer.md", "")
	writeSlashFile(t, projRoot, "skills", "deploy.md", "")

	got := collect([2]string{userRoot, "user"}, [2]string{projRoot, "project"})

	byName := map[string]SlashCommand{}
	for _, c := range got {
		byName[c.Name] = c
	}

	shared, ok := byName["/shared"]
	if !ok {
		t.Fatalf("/shared missing from %+v", got)
	}
	if shared.Scope != "project" || shared.Description != "project version" {
		t.Errorf("project must win for a duplicate name, got scope=%q desc=%q", shared.Scope, shared.Description)
	}
	// The duplicate must be replaced, not listed twice.
	n := 0
	for _, c := range got {
		if c.Name == "/shared" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("/shared listed %d times, want 1", n)
	}

	if u, ok := byName["/useronly"]; !ok || u.Scope != "user" {
		t.Errorf("user-only command should survive the merge, got %+v", u)
	}
	if a, ok := byName["@reviewer"]; !ok || a.Type != "agent" {
		t.Errorf("project agent mention missing/mistyped: %+v", a)
	}
	if s, ok := byName["/deploy"]; !ok || s.Type != "skill" {
		t.Errorf("project skill missing/mistyped: %+v", s)
	}
}

// A missing .claude directory is the normal case and must not error or invent entries.
func TestScanSlashRootMissingDirIsEmpty(t *testing.T) {
	if got := collect([2]string{filepath.Join(t.TempDir(), "nope"), "user"}); len(got) != 0 {
		t.Errorf("expected no commands, got %+v", got)
	}
}

// A long Korean first line must not be sliced mid-character: Hangul is 3 bytes in
// UTF-8, so a byte-based cut produced U+FFFD in the picker's description.
func TestReadFirstLineTruncatesByRune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd.md")
	long := ""
	for i := 0; i < 120; i++ {
		long += "가"
	}
	if err := os.WriteFile(path, []byte(long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readFirstLine(path)
	if strings.ContainsRune(got, '�') {
		t.Fatalf("description contains a replacement character: %q", got)
	}
	if r := []rune(got); len(r) != 81 { // 80 runes + the ellipsis
		t.Errorf("want 80 runes plus ellipsis, got %d runes: %q", len(r), got)
	}
}
