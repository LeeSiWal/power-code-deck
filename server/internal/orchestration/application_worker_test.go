package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"powercodedeck/internal/orchestration/taskgraph"
)

// The integration worker has separate end-to-end tests. This fixture installs
// verified evidence for a real Git commit so application edge cases stay fast.
func applicationFixture(t *testing.T) (*Store, Run, *Worker, ApplyPreview) {
	t.Helper()
	ctx := context.Background()
	repo := testRepo(t)
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r, err := s.Create("apply", repo, "new file", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(r.ID, TaskPlan{Concurrency: 1, Tasks: []taskgraph.Task{{ID: "a", Provider: "codex", Prompt: "a"}}}); err != nil {
		t.Fatal(err)
	}
	base, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(base)
	if err := s.BindBase(r.ID, base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("verified\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := snapshot(ctx, repo, base)
	if err != nil {
		t.Fatal(err)
	}
	// Reset only this throwaway fixture's staged synthetic change.
	if _, err := git(ctx, repo, "reset", "--hard", base); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ClaimTasks(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0]
	if err := s.FinishPlannedAttempt(task.AttemptID, true, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPlannedArtifact(task.AttemptID, "result_commit", "", result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tests", "review"} {
		if err := s.RecordPlannedCheck(task.AttemptID, name, true, "pass"); err != nil {
			t.Fatal(err)
		}
	}
	integration, err := s.BeginIntegration(r.ID, []CheckSpec{{Name: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddIntegrationArtifact(integration, "result_commit", "", result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"diff_check", "test", "review"} {
		if err := s.RecordIntegrationCheck(integration, name, true, "pass"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CompleteIntegration(integration); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, repo, "update-ref", "refs/powercodedeck/integrations/"+integration, result); err != nil {
		t.Fatal(err)
	}
	w, err := NewWorker(s, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := w.PreviewApplication(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = s.Get(r.ID)
	return s, r, w, preview
}

func TestApplyVerifiedResultAndIdempotentRetry(t *testing.T) {
	s, r, w, p := applicationFixture(t)
	if !strings.Contains(p.Summary, "new.txt") {
		t.Fatal(p)
	}
	// Neither preview nor applying a result should invoke repository hooks.
	hook := filepath.Join(r.Path, ".git", "hooks", "post-merge")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho unexpected > hook-ran.txt\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Path, ".git", "hooks", "reference-transaction"), []byte("#!/bin/sh\necho unexpected > hook-ran.txt\n"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := w.ApplyResult(r.ID, p.ApplyTarget)
	if err != nil || got.State != "applied" {
		t.Fatal(got, err)
	}
	content, err := os.ReadFile(filepath.Join(r.Path, "new.txt"))
	if err != nil || string(content) != "verified\n" {
		t.Fatal(string(content), err)
	}
	if _, err := os.Stat(filepath.Join(r.Path, "hook-ran.txt")); !os.IsNotExist(err) {
		t.Fatal("hook executed", err)
	}
	head, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
	if strings.TrimSpace(head) != p.ResultCommit {
		t.Fatal(head)
	}
	backup, err := git(context.Background(), r.Path, "rev-parse", "refs/powercodedeck/applications/"+got.ID+"/base")
	if err != nil || strings.TrimSpace(backup) != p.BaseCommit {
		t.Fatal("base not retained", backup, err)
	}
	retry, err := w.ApplyResult(r.ID, p.ApplyTarget)
	if err != nil || retry.ID != got.ID {
		t.Fatal("duplicate application", retry, err)
	}
	plan, _ := s.GetPlan(r.ID)
	if len(plan.Applications) != 1 {
		t.Fatal(plan.Applications)
	}
	if _, err := w.ApplyResult("another-run", p.ApplyTarget); err == nil {
		t.Fatal("foreign Run accepted")
	}
}

func TestApplyRejectsStaleOrUnsafeSource(t *testing.T) {
	for _, mode := range []string{"dirty", "staged", "untracked", "branch", "head", "detached", "merge", "ref", "payload", "skip-worktree", "assume-unchanged"} {
		t.Run(mode, func(t *testing.T) {
			s, r, w, p := applicationFixture(t)
			ctx := context.Background()
			runGit := func(args ...string) {
				t.Helper()
				if _, err := git(ctx, r.Path, args...); err != nil {
					t.Fatal(err)
				}
			}
			write := func(name string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(r.Path, name), []byte("keep me\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "skip-worktree", "assume-unchanged":
				runGit("update-index", "--"+mode, "tracked.txt")
				write("tracked.txt")
			case "dirty":
				write("tracked.txt")
			case "staged":
				write("tracked.txt")
				runGit("add", "tracked.txt")
			case "untracked":
				write("local.txt")
			case "branch":
				runGit("checkout", "-b", "other")
			case "head":
				runGit("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "advance")
			case "detached":
				runGit("checkout", "--detach")
			case "merge":
				write(".git/MERGE_HEAD")
			case "ref":
				runGit("update-ref", "refs/powercodedeck/integrations/"+p.IntegrationID, p.BaseCommit)
			case "payload":
				p.ResultCommit = p.BaseCommit
			}
			before, _ := git(ctx, r.Path, "rev-parse", "HEAD")
			if _, err := w.ApplyResult(r.ID, p.ApplyTarget); !errors.Is(err, ErrConflict) {
				t.Fatal("unsafe source accepted", err)
			}
			after, _ := git(ctx, r.Path, "rev-parse", "HEAD")
			if after != before {
				t.Fatal("rejected application moved branch")
			}
			if _, err := os.Stat(filepath.Join(r.Path, "new.txt")); !os.IsNotExist(err) {
				t.Fatal("new file applied", err)
			}
			plan, _ := s.GetPlan(r.ID)
			if len(plan.Applications) != 0 {
				t.Fatal("preflight reserved application")
			}
		})
	}
}

func TestApplyPreservesIgnoredCollision(t *testing.T) {
	s, r, w, p := applicationFixture(t)
	if err := os.WriteFile(filepath.Join(r.Path, ".git", "info", "exclude"), []byte("new.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Path, "new.txt"), []byte("local ignored data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := w.ApplyResult(r.ID, p.ApplyTarget)
	if err != nil || got.State != "failed" {
		t.Fatal(got, err)
	}
	data, _ := os.ReadFile(filepath.Join(r.Path, "new.txt"))
	if string(data) != "local ignored data\n" {
		t.Fatal("ignored file overwritten")
	}
	head, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
	if strings.TrimSpace(head) != p.BaseCommit {
		t.Fatal("failed apply moved HEAD")
	}
	plan, _ := s.GetPlan(r.ID)
	if len(plan.Applications) != 1 || plan.Applications[0].State != "failed" {
		t.Fatal(plan)
	}
}

func TestApplicationRecoveryInspectsWithoutRepeatingMerge(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-merge", true: "after-merge"}[completed], func(t *testing.T) {
			s, r, w, p := applicationFixture(t)
			first, err := s.beginApplication(r.ID, p.ApplyTarget)
			if err != nil {
				t.Fatal(err)
			}
			if completed {
				if _, err := git(context.Background(), r.Path, "merge", "--ff-only", p.ResultCommit); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Recover(); err != nil {
				t.Fatal(err)
			}
			if _, err := w.ApplyResult(r.ID, p.ApplyTarget); !errors.Is(err, ErrConflict) {
				t.Fatal("unknown outcome auto-retried", err)
			}
			before, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
			got, err := w.ReconcileApplication(r.ID)
			if err != nil || got.ID != first.ID {
				t.Fatal(got, err)
			}
			expected := "failed"
			if completed {
				expected = "applied"
			}
			if got.State != expected {
				t.Fatal(got)
			}
			after, _ := git(context.Background(), r.Path, "rev-parse", "HEAD")
			if before != after {
				t.Fatal("reconciliation changed Git")
			}
			if !completed {
				next, err := w.ApplyResult(r.ID, p.ApplyTarget)
				if err != nil || next.ID == first.ID || next.State != "applied" {
					t.Fatal(next, err)
				}
			}
		})
	}
}
