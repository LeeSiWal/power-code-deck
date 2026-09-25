package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cleanupCandidate(t *testing.T, w *Worker, run, id string) CleanupCandidate {
	t.Helper()
	cursor := ""
	for {
		p, err := w.PreviewCleanup(run, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Candidates {
			if c.ID == id {
				return c
			}
		}
		if p.NextCursor == "" {
			t.Fatal("candidate missing", id)
		}
		cursor = p.NextCursor
	}
}

func TestCleanupPreservesEvidenceIntegrationAndApplication(t *testing.T) {
	s, r, w := integrationFixture(t, "pass", false)
	p, err := s.GetPlan(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range p.Tasks {
		c := cleanupCandidate(t, w, r.ID, task.AttemptID)
		if !c.Eligible {
			t.Fatal(c)
		}
		patch, err := w.ReadArtifact(r.ID, c.ID, "changes.patch")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(c)
		if strings.Contains(string(raw), w.root) {
			t.Fatal("path exposed")
		}
		if err := w.CleanupWorkspace(r.ID, c.ID, c.Fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
			t.Fatal("workspace not removed", err)
		}
		after, err := w.ReadArtifact(r.ID, c.ID, "changes.patch")
		if err != nil || string(after) != string(patch) {
			t.Fatal("evidence lost", err)
		}
		if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
			t.Fatal("removed workspace still eligible")
		}
		if err := w.CleanupWorkspace(r.ID, c.ID, c.Fingerprint); !errors.Is(err, ErrConflict) {
			t.Fatal("stale request accepted", err)
		}
	}
	id, err := w.StartIntegration(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	c := cleanupCandidate(t, w, r.ID, id)
	if !c.Eligible {
		t.Fatal(c)
	}
	if err := w.CleanupWorkspace(r.ID, id, c.Fingerprint); err != nil {
		t.Fatal(err)
	}
	preview, err := w.PreviewApplication(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Aggressive GC in a throwaway repository proves removed-worktree commits
	// still have refs before the explicit application consumes the result.
	if _, err := git(context.Background(), r.Path, "gc", "--prune=now"); err != nil {
		t.Fatal(err)
	}
	result, err := w.ApplyResult(r.ID, preview.ApplyTarget)
	if err != nil || result.State != "applied" {
		t.Fatal(result, err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(r.Path, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCleanupRejectsEditsExtrasFlagsLocksAndForeignRequests(t *testing.T) {
	_, r, w := integrationFixture(t, "pass", false)
	p, err := w.PreviewCleanup(r.ID, "")
	if err != nil || len(p.Candidates) != 2 {
		t.Fatal(p, err)
	}
	c := p.Candidates[0]
	if !c.Eligible {
		t.Fatal(c)
	}
	if err := w.CleanupWorkspace(r.ID, c.ID, strings.Repeat("0", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := w.CleanupWorkspace(r.ID, "../outside", c.Fingerprint); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	other := createTest(t, w.store, "cleanup-other")
	if err := w.CleanupWorkspace(other.ID, c.ID, c.Fingerprint); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign attempt accepted", err)
	}
	file := filepath.Join(c.Path, "tracked.txt")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("manual edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.CleanupWorkspace(r.ID, c.ID, c.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edited preview accepted", err)
	}
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	// Staged and unstaged changes that cancel in a combined diff still protect
	// the staged content from deletion.
	if err := os.WriteFile(file, []byte("staged only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), c.Path, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("opposing staged edit accepted")
	}
	if _, err := git(context.Background(), c.Path, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(c.Path, "local.txt")
	if err := os.WriteFile(extra, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("untracked file accepted")
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	// A repository-local exclude simulates ignored build output.
	exclude, err := git(context.Background(), r.Path, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		t.Fatal(err)
	}
	exclude = strings.TrimSpace(exclude)
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(r.Path, exclude)
	}
	if err := os.WriteFile(exclude, []byte("local.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extra, []byte("ignored content"), 0600); err != nil {
		t.Fatal(err)
	}
	if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("ignored file accepted")
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), c.Path, "update-index", "--assume-unchanged", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("hidden index flag accepted")
	}
	if _, err := git(context.Background(), c.Path, "update-index", "--no-assume-unchanged", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), r.Path, "worktree", "lock", c.Path); err != nil {
		t.Fatal(err)
	}
	if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("locked worktree accepted")
	}
	if _, err := git(context.Background(), r.Path, "worktree", "unlock", c.Path); err != nil {
		t.Fatal(err)
	}
	if !cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("restored workspace rejected")
	}
	// Replacing the owned directory with a link must not redirect deletion.
	moved := c.Path + "-moved"
	if err := os.Rename(c.Path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, c.Path); err != nil {
		t.Fatal(err)
	}
	if cleanupCandidate(t, w, r.ID, c.ID).Eligible {
		t.Fatal("symlink path accepted")
	}
	if err := os.Remove(c.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, c.Path); err != nil {
		t.Fatal(err)
	}
	// Even another Run's active worker occupies the global removal lock.
	w.cancel = func() {}
	if _, err := w.PreviewCleanup(r.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("busy worker accepted", err)
	}
	w.cancel = nil
}

func TestCleanupDraftAndConflictProtection(t *testing.T) {
	s, r, w := plannerFixture(t)
	id, err := w.StartPlanning(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	c := cleanupCandidate(t, w, r.ID, id)
	if !c.Eligible {
		t.Fatal(c)
	}
	if err := w.CleanupWorkspace(r.ID, id, c.Fingerprint); err != nil {
		t.Fatal(err)
	}
	draft, err := s.GetPlanDraft(r.ID)
	if err != nil || draft.Plan == nil {
		t.Fatal("draft lost", draft, err)
	}
	_, r2, w2 := integrationFixture(t, "pass", true)
	failed := failedIntegration(t, w2, r2.ID)
	if cleanupCandidate(t, w2, r2.ID, failed).Eligible {
		t.Fatal("conflicted worktree eligible")
	}
	if _, err := w2.ReadArtifact(r2.ID, failed, "conflicts"); err != nil {
		t.Fatal(err)
	}
}
