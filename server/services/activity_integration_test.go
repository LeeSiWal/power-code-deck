package services

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPollAgainstRealTranscript exercises the full tail pipeline (newest-file
// selection, offset read, JSON parse, snapshot build) against a real Claude Code
// transcript if one is present on this machine. It is skipped in CI/dev boxes that
// have no ~/.claude/projects transcripts.
func TestPollAgainstRealTranscript(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")
	src := findAnyTranscript(projectsRoot)
	if src == "" {
		t.Skip("no real transcript available")
	}

	// Copy into an isolated temp dir so the watcher's newest-file logic is deterministic.
	tmp := t.TempDir()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("cannot read %s: %v", src, err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "session.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	var got *AgentActivitySnapshot
	w := &transcriptWatcher{
		agentID: "real",
		dir:     tmp,
		nodes:   make(map[string]*activityNode),
		tools:   make(map[string]*toolRun),
		subByID: make(map[string]string),
		emit:    func(s AgentActivitySnapshot) { got = &s },
	}
	w.poll()
	w.emitSnapshot()

	t.Logf("parsed %d nodes, %d recent from %s", len(w.nodes), len(w.recent), filepath.Base(src))
	if len(w.nodes) == 0 {
		t.Fatal("expected at least the main node after parsing a real transcript")
	}
	if _, ok := w.nodes[mainNodeID]; !ok {
		t.Fatal("expected a main node")
	}
	if got != nil {
		for _, n := range got.Nodes {
			t.Logf("node kind=%s label=%q status=%s tools=%d tool=%q", n.Kind, n.Label, n.Status, n.ToolCount, n.CurrentTool)
		}
	}
}

func findAnyTranscript(root string) string {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var best string
	var bestSize int64
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			// Pick the largest transcript — most likely to contain tool calls.
			if info.Size() > bestSize {
				bestSize = info.Size()
				best = filepath.Join(root, d.Name(), f.Name())
			}
		}
	}
	return best
}
