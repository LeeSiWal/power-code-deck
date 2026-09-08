package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StartIntegration uses the same Run slot as task dispatch. Completion produces
// a verified, retained result; applying it to the user's branch is separate.
func (w *Worker) StartIntegration(id string) (string, error) {
	return w.startIntegration(id, nil)
}

func (w *Worker) StartConflictResolution(id string, request ResolutionRequest) (string, error) {
	return w.startIntegration(id, &request)
}

func (w *Worker) startIntegration(id string, request *ResolutionRequest) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return "", ErrConflict
	}
	run, err := w.store.Get(id)
	if err != nil {
		return "", err
	}
	if run.State != "awaiting_integration" && run.State != "integration_failed" {
		return "", ErrConflict
	}
	if w.reviewer == nil {
		return "", fmt.Errorf("%w: independent reviewer required", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// Reuse the pinned source's check policy, never the agents' edited manifest.
	// A moved or dirty source must be handled in a new Run.
	top, err := git(ctx, run.Path, "rev-parse", "--show-toplevel")
	if err == nil {
		top, err = filepath.EvalSymlinks(strings.TrimSpace(top))
	}
	if err != nil || top != run.Path {
		cancel()
		return "", fmt.Errorf("%w: workspace must be a Git repository root", ErrInvalid)
	}
	head, err := git(ctx, run.Path, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != run.BaseCommit {
		cancel()
		return "", fmt.Errorf("%w: source revision changed", ErrConflict)
	}
	status, err := git(ctx, run.Path, "status", "--porcelain")
	if err != nil || status != "" {
		cancel()
		return "", fmt.Errorf("%w: source workspace must be clean", ErrInvalid)
	}
	checks, err := loadVerificationPlan(run.Path)
	if err != nil {
		cancel()
		return "", err
	}
	var repairs *resolutionPlan
	if request != nil {
		repairs, err = w.loadResolution(id, *request)
		if err != nil {
			cancel()
			return "", err
		}
	}
	attempt, err := w.store.BeginIntegration(id, checks)
	if err != nil {
		cancel()
		return "", err
	}
	reviewer := w.reviewer
	w.run, w.cancel, w.done = id, cancel, make(chan struct{})
	go func() {
		defer func() { cancel(); w.mu.Lock(); w.cancel = nil; close(w.done); w.mu.Unlock() }()
		if err := w.integrateWithResolutions(ctx, run, attempt, checks, reviewer, repairs); err != nil {
			if saveErr := w.store.FailIntegration(attempt, err.Error()); saveErr != nil && !errors.Is(saveErr, ErrConflict) {
				log.Printf("integration %s: %v", attempt, saveErr)
			}
		}
	}()
	return attempt, nil
}

func (w *Worker) integrateWithResolutions(ctx context.Context, run Run, id string, checks []CheckSpec, reviewer Factory, repairs *resolutionPlan) error {
	plan, err := w.store.GetPlan(run.ID)
	if err != nil {
		return err
	}
	// Including every task as a dependency of a synthetic final task walks the
	// entire graph once, even disconnected branches and shared ancestors.
	final := PlannedTask{}
	for _, task := range plan.Tasks {
		final.DependsOn = append(final.DependsOn, task.ID)
	}
	commits, err := w.dependencyResults(run.ID, final)
	if err != nil {
		return err
	}
	dir := filepath.Join(w.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	if repairs != nil {
		raw, err := json.Marshal(repairs)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, "resolution-plan.json")
		if err := writeExclusive(path, raw); err != nil {
			return err
		}
		if err := w.store.AddIntegrationArtifact(id, "resolution_plan", path, run.BaseCommit); err != nil {
			return err
		}
	}
	hooks := filepath.Join(dir, "empty-hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		return err
	}
	cwd := filepath.Join(dir, "workspace")
	if err := w.store.AddIntegrationArtifact(id, "workspace", cwd, run.BaseCommit); err != nil {
		return err
	}
	if _, err := git(ctx, run.Path, "-c", "core.hooksPath="+hooks, "worktree", "add", "--detach", cwd, run.BaseCommit); err != nil {
		return err
	}
	appliedRepairs := 0
	for _, commit := range commits {
		if _, err := git(ctx, cwd, "-c", "core.hooksPath="+hooks, "cherry-pick", "--no-commit", commit); err != nil {
			if repairs != nil {
				resolved := false
				for _, repair := range repairs.Resolutions {
					if repair.IncomingCommit == commit {
						if err := applyConflictResolution(ctx, cwd, repair); err != nil {
							return err
						}
						resolved = true
						appliedRepairs++
						break
					}
				}
				if resolved {
					continue
				}
			}
			if captureErr := captureConflict(ctx, cwd, dir, id, run.BaseCommit, commit, err.Error(), w.store.AddIntegrationArtifact); captureErr != nil {
				return fmt.Errorf("%w; conflict evidence: %v", err, captureErr)
			}
			return fmt.Errorf("task result integration conflict: %w", err)
		}
	}
	if repairs != nil && appliedRepairs != len(repairs.Resolutions) {
		return fmt.Errorf("%w: not all submitted conflict resolutions were applied", ErrConflict)
	}
	result, err := snapshot(ctx, cwd, run.BaseCommit)
	if err != nil {
		return err
	}
	patch, err := git(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", "--binary", run.BaseCommit, result, "--")
	if err != nil {
		return err
	}
	status, err := git(ctx, cwd, "status", "--porcelain")
	if err != nil {
		return err
	}
	for name, content := range map[string]string{"changes.patch": patch, "status.txt": status} {
		path := filepath.Join(dir, name)
		if err := writeExclusive(path, []byte(content)); err != nil {
			return err
		}
		if err := w.store.AddIntegrationArtifact(id, name, path, run.BaseCommit); err != nil {
			return err
		}
	}
	_, err = git(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", "--check", run.BaseCommit, result, "--")
	detail := "git diff --check passed"
	if err != nil {
		detail = err.Error()
	}
	if saveErr := w.store.RecordIntegrationCheck(id, "diff_check", err == nil, detail); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	for _, check := range checks {
		before, err := reviewFingerprint(ctx, cwd)
		if err != nil {
			return err
		}
		passed, detail, logPath, checkErr := runCheck(ctx, cwd, dir, check)
		if checkErr != nil {
			passed = false
			detail = checkErr.Error()
		}
		if logPath != "" {
			if err := w.store.AddIntegrationArtifact(id, "check_log:"+check.Name, logPath, ""); err != nil {
				return err
			}
		}
		after, err := reviewFingerprint(ctx, cwd)
		if err != nil {
			passed = false
			detail = err.Error()
		} else if before != after {
			passed = false
			detail = "check changed source files; final verification rejected"
		}
		if err := w.store.RecordIntegrationCheck(id, check.Name, passed, detail); err != nil {
			return err
		}
		if !passed {
			return fmt.Errorf("%s: %s", check.Name, detail)
		}
	}
	// Review the combined changes against the original base and original request.
	passed, detail, err := reviewExecution(ctx, run, id, cwd, run.BaseCommit, dir, reviewer, w.store.AddIntegrationArtifact)
	if err != nil {
		passed = false
		detail = err.Error()
	}
	if err := w.store.RecordIntegrationCheck(id, "review", passed, detail); err != nil {
		return err
	}
	if !passed {
		return fmt.Errorf("final review failed: %s", detail)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := git(ctx, cwd, "update-ref", "refs/powercodedeck/integrations/"+id, result, ""); err != nil {
		return err
	}
	if err := w.store.AddIntegrationArtifact(id, "result_commit", "", result); err != nil {
		return err
	}
	return w.store.CompleteIntegration(id)
}
