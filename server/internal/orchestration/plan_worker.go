package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/orchestration/taskgraph"
	"powercodedeck/internal/providers"
)

// StartPlan shares the single Run slot with Start. Concurrency is bounded by the
// frozen plan; reservations remain internal and never accept client evidence.
func (w *Worker) StartPlan(id string) error {
	return w.startPlan(id, "", nil)
}

func (w *Worker) StartTaskResolution(id, task string, request ResolutionRequest) error {
	if task == "" {
		return ErrInvalid
	}
	return w.startPlan(id, task, &request)
}

func (w *Worker) startPlan(id, repairTask string, request *ResolutionRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return ErrConflict
	}
	run, err := w.store.Get(id)
	if err != nil {
		return err
	}
	if run.State != "planned" && run.State != "plan_running" {
		return ErrConflict
	}
	plan, err := w.store.GetPlan(id)
	if err != nil {
		return err
	}
	var repairs *resolutionPlan
	if request != nil {
		found := false
		for _, task := range plan.Tasks {
			if task.ID == repairTask && task.State == taskgraph.Failed && task.AttemptID == request.SourceAttempt {
				found = true
			}
		}
		if !found {
			return ErrConflict
		}
		repairs, err = w.resolutionFromEvidence(id, *request)
		if err != nil {
			return err
		}
	}
	if request == nil && len(plan.Selection.Ready) == 0 {
		return ErrConflict
	}
	if w.reviewer == nil {
		return fmt.Errorf("%w: independent reviewer required", ErrInvalid)
	}
	for _, t := range plan.Tasks {
		if w.factories[t.Provider] == nil {
			return fmt.Errorf("%w: task provider not connected", ErrInvalid)
		}
	}
	checks, err := loadVerificationPlan(run.Path)
	if err != nil {
		return err
	}
	// A whitespace check alone is not sufficient evidence for planned tasks.
	if len(checks) == 0 {
		return fmt.Errorf("%w: plan execution requires project checks", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	top, err := git(ctx, run.Path, "rev-parse", "--show-toplevel")
	if err == nil {
		top, err = filepath.EvalSymlinks(strings.TrimSpace(top))
	}
	if err != nil || top != run.Path {
		cancel()
		return fmt.Errorf("%w: workspace must be a Git repository root", ErrInvalid)
	}
	status, err := git(ctx, run.Path, "status", "--porcelain")
	if err != nil || status != "" {
		cancel()
		return fmt.Errorf("%w: source workspace must be clean", ErrInvalid)
	}
	base, err := git(ctx, run.Path, "rev-parse", "HEAD")
	if err != nil {
		cancel()
		return err
	}
	run.BaseCommit = strings.TrimSpace(base)
	if err = w.store.BindBase(id, run.BaseCommit); err != nil {
		cancel()
		return err
	}
	var tasks []PlannedTask
	if request == nil {
		tasks, err = w.store.ClaimTasks(id)
	} else {
		var task PlannedTask
		task, err = w.store.claimTaskResolution(id, repairTask, request.SourceAttempt)
		tasks = []PlannedTask{task}
	}
	if err != nil {
		cancel()
		return err
	}
	repairAttempt := ""
	if request != nil {
		repairAttempt = tasks[0].AttemptID
	}
	reviewer := w.reviewer
	w.run, w.cancel, w.done = id, cancel, make(chan struct{})
	go func() {
		defer func() { cancel(); w.mu.Lock(); w.cancel = nil; close(w.done); w.mu.Unlock() }()
		defer func() {
			// No automatic retry: failures leave explicit pending/failed task states.
			if _, err := w.store.db.Exec(`UPDATE v2_runs SET state='planned' WHERE id=? AND state='plan_running'`, id); err != nil {
				log.Printf("plan %s: %v", id, err)
			}
		}()
		for len(tasks) > 0 {
			var group sync.WaitGroup
			for _, task := range tasks {
				group.Add(1)
				go func(t PlannedTask) {
					defer group.Done()
					if t.AttemptID == repairAttempt {
						w.executePlanned(ctx, run, t, checks, reviewer, repairs)
					} else {
						w.executePlanned(ctx, run, t, checks, reviewer)
					}
				}(task)
			}
			group.Wait()
			if ctx.Err() != nil {
				return
			}
			current, err := w.store.Get(id)
			if err != nil || current.State != "plan_running" {
				return
			}
			tasks, err = w.store.ClaimTasks(id)
			if err != nil {
				return
			}
		}
	}()
	return nil
}

// dependencyResults walks ancestors once in deterministic topological order.
// Each result is incremental against its own input, so diamonds do not apply
// shared ancestor changes twice. Git rejects conflicting sibling changes.
func (w *Worker) dependencyResults(run string, task PlannedTask) ([]string, error) {
	plan, err := w.store.GetPlan(run)
	if err != nil {
		return nil, err
	}
	byID := map[string]PlannedTask{}
	for _, t := range plan.Tasks {
		byID[t.ID] = t
	}
	seen := map[string]bool{}
	var commits []string
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		t, ok := byID[id]
		if !ok || t.State != taskgraph.Succeeded {
			return ErrConflict
		}
		for _, dep := range t.DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		a, err := w.store.Artifact(run, t.AttemptID, "result_commit")
		if err != nil {
			return err
		}
		if a.BaseCommit == "" {
			return ErrConflict
		}
		commits = append(commits, a.BaseCommit)
		return nil
	}
	for _, id := range task.DependsOn {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return commits, nil
}

// snapshot records an immutable tree with an explicit parent, including new
// non-ignored files. It neither moves a branch nor invokes commit hooks.
func snapshot(ctx context.Context, cwd, parent string) (string, error) {
	if _, err := git(ctx, cwd, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	tree, err := git(ctx, cwd, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := git(ctx, cwd, "-c", "user.name=PowerCodeDeck", "-c", "user.email=worker@powercodedeck.local", "-c", "commit.gpgSign=false", "commit-tree", strings.TrimSpace(tree), "-p", parent, "-m", "PowerCodeDeck task snapshot")
	return strings.TrimSpace(commit), err
}

func (w *Worker) executePlanned(ctx context.Context, run Run, task PlannedTask, checks []CheckSpec, reviewer Factory, resolutions ...*resolutionPlan) {
	id := task.AttemptID
	verifying := false
	fail := func(err error) {
		var saveErr error
		if verifying {
			saveErr = w.store.RecordPlannedCheck(id, "review", false, err.Error())
		} else {
			saveErr = w.store.FinishPlannedAttempt(id, false, err.Error())
		}
		if saveErr != nil && saveErr != ErrConflict {
			log.Printf("plan attempt %s: %v", id, saveErr)
		}
	}
	dir := filepath.Join(w.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		fail(err)
		return
	}
	var repairs *resolutionPlan
	if len(resolutions) > 0 {
		repairs = resolutions[0]
	}
	if repairs != nil {
		raw, err := json.Marshal(repairs)
		if err != nil {
			fail(err)
			return
		}
		path := filepath.Join(dir, "resolution-plan.json")
		if err := writeExclusive(path, raw); err != nil {
			fail(err)
			return
		}
		if err := w.store.AddPlannedArtifact(id, "resolution_plan", path, run.BaseCommit); err != nil {
			fail(err)
			return
		}
	}
	hooks := filepath.Join(dir, "empty-hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		fail(err)
		return
	}
	cwd := filepath.Join(dir, "workspace")
	if err := w.store.AddPlannedArtifact(id, "workspace", cwd, run.BaseCommit); err != nil {
		fail(err)
		return
	}
	if _, err := git(ctx, run.Path, "-c", "core.hooksPath="+hooks, "worktree", "add", "--detach", cwd, run.BaseCommit); err != nil {
		fail(err)
		return
	}
	commits, err := w.dependencyResults(run.ID, task)
	if err != nil {
		fail(err)
		return
	}
	appliedRepairs := 0
	for _, commit := range commits {
		if _, err := git(ctx, cwd, "-c", "core.hooksPath="+hooks, "cherry-pick", "--no-commit", commit); err != nil {
			resolved := false
			if repairs != nil {
				for _, repair := range repairs.Resolutions {
					if repair.IncomingCommit == commit {
						if err := applyConflictResolution(ctx, cwd, repair); err != nil {
							fail(err)
							return
						}
						appliedRepairs++
						resolved = true
						break
					}
				}
			}
			if resolved {
				continue
			}
			if captureErr := captureConflict(ctx, cwd, dir, id, run.BaseCommit, commit, err.Error(), w.store.AddPlannedArtifact); captureErr != nil {
				err = fmt.Errorf("%w; conflict evidence: %v", err, captureErr)
			}
			fail(fmt.Errorf("dependency integration failed: %w", err))
			return
		}
	}
	if repairs != nil && appliedRepairs != len(repairs.Resolutions) {
		fail(fmt.Errorf("%w: not all submitted conflict resolutions were applied", ErrConflict))
		return
	}
	base, err := snapshot(ctx, cwd, run.BaseCommit)
	if err != nil {
		fail(err)
		return
	}
	if _, err := git(ctx, cwd, "reset", "--soft", base); err != nil {
		fail(err)
		return
	}
	if err := w.store.AddPlannedArtifact(id, "input_commit", "", base); err != nil {
		fail(err)
		return
	}
	execution, err := w.factories[task.Provider](id, cwd)
	if err != nil {
		fail(err)
		return
	}
	if execution == nil {
		fail(fmt.Errorf("provider returned no execution"))
		return
	}
	defer execution.Stop()
	if execution.Identity() != (providers.Identity{ExecutionID: id, Provider: providers.ID(task.Provider)}) {
		fail(fmt.Errorf("execution identity mismatch"))
		return
	}
	if err := execution.Start(); err != nil {
		fail(err)
		return
	}
	if err := execution.Send(task.Prompt); err != nil {
		fail(err)
		return
	}
	for {
		event, err := execution.Next(ctx)
		if err != nil {
			fail(err)
			return
		}
		if event.Kind != providers.TurnFinished {
			continue
		}
		if event.Identity != execution.Identity() || event.Outcome == nil {
			fail(fmt.Errorf("invalid provider outcome"))
			return
		}
		execution.Stop()
		if event.Outcome.Status != providers.CompletionSuccess || event.Outcome.IsError {
			fail(fmt.Errorf("provider failed: %s\n%s", event.Outcome.Text, event.Outcome.Diagnostics))
			return
		}
		if err := w.store.FinishPlannedAttempt(id, true, event.Outcome.Text); err != nil {
			fail(err)
			return
		}
		verifying = true
		break
	}
	result, err := snapshot(ctx, cwd, base)
	if err != nil {
		fail(err)
		return
	}
	verificationBase := base
	if repairs != nil {
		verificationBase = run.BaseCommit
	}
	patch, err := git(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", "--binary", verificationBase, result, "--")
	if err != nil {
		fail(err)
		return
	}
	path := filepath.Join(dir, "changes.patch")
	if err := writeExclusive(path, []byte(patch)); err != nil {
		fail(err)
		return
	}
	if err := w.store.AddPlannedArtifact(id, "changes.patch", path, verificationBase); err != nil {
		fail(err)
		return
	}
	_, err = git(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", "--check", verificationBase, result, "--")
	detail := "project checks passed"
	for _, check := range checks {
		if err != nil {
			break
		}
		before, snapshotErr := reviewFingerprint(ctx, cwd)
		if snapshotErr != nil {
			err = snapshotErr
			break
		}
		passed, checkDetail, logPath, checkErr := runCheck(ctx, cwd, dir, check)
		if logPath != "" {
			if saveErr := w.store.AddPlannedArtifact(id, "check_log:"+check.Name, logPath, ""); saveErr != nil {
				err = saveErr
				break
			}
		}
		after, snapshotErr := reviewFingerprint(ctx, cwd)
		switch {
		case checkErr != nil:
			err = checkErr
		case snapshotErr != nil:
			err = snapshotErr
		case before != after:
			err = fmt.Errorf("check changed source files")
		case !passed:
			err = fmt.Errorf("%s: %s", check.Name, checkDetail)
		}
	}
	if err != nil {
		detail = err.Error()
	}
	if saveErr := w.store.RecordPlannedCheck(id, "tests", err == nil, detail); saveErr != nil {
		return
	}
	if err != nil {
		return
	}
	reviewRun := run
	reviewRun.Prompt = task.Prompt
	if repairs != nil {
		reviewRun.Prompt = run.Prompt + "\n\nReview dependency conflict resolutions and task implementation together. Task: " + task.Prompt
	}
	passed, detail, err := reviewExecution(ctx, reviewRun, id, cwd, verificationBase, dir, reviewer, w.store.AddPlannedArtifact)
	if err != nil {
		passed = false
		detail = err.Error()
	}
	if passed {
		// Keep the result and its input parent reachable even after Git GC.
		if _, err := git(ctx, cwd, "update-ref", "refs/powercodedeck/attempts/"+id, result, ""); err != nil {
			fail(err)
			return
		}
		if err := w.store.AddPlannedArtifact(id, "result_commit", "", result); err != nil {
			fail(err)
			return
		}
	}
	if err := w.store.RecordPlannedCheck(id, "review", passed, detail); err != nil && err != ErrConflict {
		log.Printf("plan attempt %s review: %v", id, err)
	}
}
