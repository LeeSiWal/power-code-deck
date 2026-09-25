package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ApplyPreview struct {
	ApplyTarget
	Summary string `json:"summary"`
}

// sourcePosition also rejects in-progress Git operations and dirty submodules.
// This application serializes its own workers, not external editors/Git tools.
func sourcePosition(ctx context.Context, root string) (branch, head string, err error) {
	top, err := git(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	top, err = filepath.EvalSymlinks(strings.TrimSpace(top))
	if err != nil || top != root {
		return "", "", fmt.Errorf("%w: repository root changed", ErrConflict)
	}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_LOG"} {
		path, err := git(ctx, root, "rev-parse", "--git-path", name)
		if err != nil {
			return "", "", err
		}
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if _, err := os.Stat(path); err == nil {
			return "", "", fmt.Errorf("%w: another Git operation is in progress", ErrConflict)
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
	}
	branch, err = git(ctx, root, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("%w: check out a local branch before applying", ErrConflict)
	}
	branch = strings.TrimSpace(branch)
	if !strings.HasPrefix(branch, "refs/heads/") {
		return "", "", ErrConflict
	}
	head, err = git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	status, err := git(ctx, root, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return "", "", err
	}
	if status != "" {
		return "", "", fmt.Errorf("%w: source contains uncommitted changes", ErrConflict)
	}
	entries, err := git(ctx, root, "ls-files", "-v", "-z")
	if err != nil {
		return "", "", err
	}
	for _, entry := range strings.Split(entries, "\x00") {
		if len(entry) > 0 && (entry[0] == 'S' || (entry[0] >= 'a' && entry[0] <= 'z')) {
			return "", "", fmt.Errorf("%w: index has hidden-change flags; remove assume-unchanged/skip-worktree before applying", ErrConflict)
		}
	}
	return branch, strings.TrimSpace(head), nil
}

func (w *Worker) previewApplication(ctx context.Context, runID string) (Run, ApplyPreview, error) {
	run, err := w.store.Get(runID)
	if err != nil {
		return run, ApplyPreview{}, err
	}
	target, err := w.store.verifiedTarget(runID)
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("%w: no verified integration result", ErrConflict)
	}
	if err != nil {
		return run, ApplyPreview{}, err
	}
	branch, head, err := sourcePosition(ctx, run.Path)
	if err != nil {
		return run, ApplyPreview{}, err
	}
	if head != target.BaseCommit {
		return run, ApplyPreview{}, fmt.Errorf("%w: source revision changed after verification", ErrConflict)
	}
	target.Branch = branch
	ref, err := git(ctx, run.Path, "rev-parse", "--verify", "refs/powercodedeck/integrations/"+target.IntegrationID)
	if err != nil || strings.TrimSpace(ref) != target.ResultCommit {
		return run, ApplyPreview{}, fmt.Errorf("%w: verified result ref is missing or changed", ErrConflict)
	}
	parents, err := git(ctx, run.Path, "rev-list", "--parents", "-n", "1", target.ResultCommit)
	if err != nil {
		return run, ApplyPreview{}, err
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != target.ResultCommit || fields[1] != target.BaseCommit {
		return run, ApplyPreview{}, fmt.Errorf("%w: verified result parent mismatch", ErrConflict)
	}
	summary, err := git(ctx, run.Path, "diff", "--no-ext-diff", "--no-textconv", "--stat", target.BaseCommit, target.ResultCommit, "--")
	return run, ApplyPreview{ApplyTarget: target, Summary: summary}, err
}

func (w *Worker) PreviewApplication(id string) (ApplyPreview, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return ApplyPreview{}, ErrConflict
	}
	if _, err := w.store.application(id); err == nil {
		return ApplyPreview{}, fmt.Errorf("%w: application already recorded; inspect its status", ErrConflict)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ApplyPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, preview, err := w.previewApplication(ctx, id)
	return preview, err
}

// ApplyResult is an explicit action, never part of integration completion.
// Git performs a fast-forward only; no stash, force reset, push, or merge commit.
func (w *Worker) ApplyResult(id string, target ApplyTarget) (Application, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return Application{}, ErrConflict
	}
	if previous, err := w.store.application(id); err == nil {
		if previous.State == "applied" && previous.ApplyTarget == target {
			return previous, nil
		}
		return Application{}, fmt.Errorf("%w: application pending or already recorded", ErrConflict)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Application{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, preview, err := w.previewApplication(ctx, id)
	if err != nil {
		return Application{}, err
	}
	if target != preview.ApplyTarget {
		return Application{}, fmt.Errorf("%w: reviewed application target changed", ErrConflict)
	}
	record, err := w.store.beginApplication(id, target)
	if err != nil {
		return Application{}, err
	}
	finish := func(state, detail string) (Application, error) {
		record.State = state
		record.Detail = detail
		return record, w.store.finishApplication(record.ID, state, detail)
	}
	dir := filepath.Join(w.root, record.ID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return finish("failed", err.Error())
	}
	hooks := filepath.Join(dir, "empty-hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		return finish("failed", err.Error())
	}
	if _, err := git(ctx, run.Path, "-c", "core.hooksPath="+hooks, "update-ref", "refs/powercodedeck/applications/"+record.ID+"/base", target.BaseCommit, ""); err != nil {
		return finish("failed", err.Error())
	}
	// Revalidate after recording intent and immediately before touching the source.
	branch, head, err := sourcePosition(ctx, run.Path)
	if err != nil || branch != target.Branch || head != target.BaseCommit {
		return finish("failed", "source changed before application; no merge was started")
	}
	_, mergeErr := git(ctx, run.Path, "-c", "core.hooksPath="+hooks, "-c", "submodule.recurse=false", "merge", "--ff-only", "--no-edit", "--no-stat", "--no-autostash", "--no-overwrite-ignore", "--no-verify-signatures", target.ResultCommit)
	// Use a fresh read budget after a timeout. Do not roll back possibly concurrent
	// user edits or guess whether a failed Git command moved the branch.
	inspect, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	branch, head, err = sourcePosition(inspect, run.Path)
	if err == nil && branch == target.Branch && head == target.ResultCommit {
		return finish("applied", "verified result applied to "+strings.TrimPrefix(branch, "refs/heads/"))
	}
	if err == nil && branch == target.Branch && head == target.BaseCommit && mergeErr != nil {
		return finish("failed", mergeErr.Error())
	}
	detail := "application outcome needs inspection; source was not rolled back"
	if mergeErr != nil {
		detail += "\n" + mergeErr.Error()
	}
	if err != nil {
		detail += "\n" + err.Error()
	}
	return finish("needs_attention", detail)
}

// ReconcileApplication reads Git and updates only the journal. Recovery cannot
// silently repeat a source mutation whose outcome is unknown.
func (w *Worker) ReconcileApplication(id string) (Application, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return Application{}, ErrConflict
	}
	record, err := w.store.application(id)
	if err != nil {
		return record, err
	}
	if record.State == "applied" {
		return record, nil
	}
	// Holding the worker mutex guarantees no application in this server is
	// still executing, including when its final database write failed.
	if record.State != "needs_attention" && record.State != "applying" {
		return record, ErrConflict
	}
	run, err := w.store.Get(id)
	if err != nil {
		return record, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	branch, head, err := sourcePosition(ctx, run.Path)
	if err != nil {
		return record, err
	}
	if branch != record.Branch {
		return record, ErrConflict
	}
	switch head {
	case record.ResultCommit:
		record.State = "applied"
		record.Detail = "confirmed verified result at source branch"
	case record.BaseCommit:
		record.State = "failed"
		record.Detail = "confirmed original base; application may be retried"
	default:
		return record, fmt.Errorf("%w: source differs from both expected commits; inspect manually", ErrConflict)
	}
	return record, w.store.finishApplication(record.ID, record.State, record.Detail)
}
