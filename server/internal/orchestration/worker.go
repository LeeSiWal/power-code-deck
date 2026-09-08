package orchestration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/providers"
)

type Factory func(id, cwd string) (providers.Execution, error)

// Worker has one active slot. Worktrees isolate Git changes, not OS permissions.
// Source application is explicit; modified files are never cleaned automatically.
type Worker struct {
	store     *Store
	root      string
	factories map[string]Factory
	reviewer  Factory
	planner   Factory
	mu        sync.Mutex
	run       string
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
}

// SetReviewer installs a fresh-context reviewer factory. The application should
// configure a provider-specific read-only/plan mode factory before dispatch.
func (w *Worker) SetReviewer(factory Factory) {
	w.mu.Lock()
	w.reviewer = factory
	w.mu.Unlock()
}

func NewWorker(store *Store, root string, factories map[string]Factory) (*Worker, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	copy := map[string]Factory{}
	for name, factory := range factories {
		copy[name] = factory
	}
	return &Worker{store: store, root: root, factories: copy}, nil
}

func (w *Worker) Start(id string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.cancel != nil {
		return "", ErrConflict
	}
	run, err := w.store.Get(id)
	if err != nil {
		return "", err
	}
	factory := w.factories[run.Provider]
	if factory == nil {
		return "", fmt.Errorf("%w: provider worker not connected", ErrInvalid)
	}
	plan, err := loadVerificationPlan(run.Path)
	if err != nil {
		return "", err
	}
	required := []string{"diff_check", "review"}
	for _, check := range plan {
		required = append(required, check.Name)
	}
	if err := w.store.RequireChecks(id, required...); err != nil {
		return "", err
	}
	reviewer := w.reviewer
	attempt, err := w.store.StartAttempt(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	w.run, w.cancel, w.done = id, cancel, make(chan struct{})
	go func() {
		defer func() { cancel(); w.mu.Lock(); w.cancel = nil; close(w.done); w.mu.Unlock() }()
		w.execute(ctx, run, attempt, factory, reviewer, plan)
	}()
	return attempt, nil
}

func (w *Worker) Cancel(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.store.Cancel(id); err != nil {
		return err
	}
	if w.run == id && w.cancel != nil {
		w.cancel()
	}
	return nil
}

// ReadArtifact serves bounded text evidence without exposing arbitrary host
// paths. The workspace artifact is deliberately metadata-only.
func (w *Worker) ReadArtifact(run, execution, kind string) ([]byte, error) {
	if strings.TrimSpace(kind) == "" || kind == "workspace" {
		return nil, ErrInvalid
	}
	artifact, err := w.store.Artifact(run, execution, kind)
	if err != nil {
		return nil, err
	}
	path, err := filepath.EvalSymlinks(artifact.Path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(w.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, ErrInvalid
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 8*1024*1024 {
		return nil, ErrInvalid
	}
	return os.ReadFile(path)
}

// Close stops dispatch, cancels the owned process, and waits with a caller budget.
func (w *Worker) Close(ctx context.Context) error {
	w.mu.Lock()
	w.closed = true
	done := w.done
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8*1024*1024 {
		return 0, fmt.Errorf("Git output exceeds 8 MiB")
	}
	return b.Buffer.Write(p)
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git operation failed: %w: %s", err, output.String())
	}
	return output.String(), nil
}

func writeExclusive(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if written != len(content) {
		return io.ErrShortWrite
	}
	return closeErr
}

func (w *Worker) execute(ctx context.Context, run Run, id string, factory, reviewer Factory, plan []CheckSpec) {
	fail := func(err error) {
		if saveErr := w.store.FinishAttempt(id, false, err.Error()); saveErr != nil && !errors.Is(saveErr, ErrConflict) {
			log.Printf("run %s failed to persist failure: %v", run.ID, saveErr)
		}
	}
	top, err := git(ctx, run.Path, "rev-parse", "--show-toplevel")
	if err != nil {
		fail(err)
		return
	}
	top, err = filepath.EvalSymlinks(strings.TrimSpace(top))
	if err != nil || top != run.Path {
		fail(fmt.Errorf("workspace must be the Git repository root"))
		return
	}
	status, err := git(ctx, run.Path, "status", "--porcelain")
	if err != nil {
		fail(err)
		return
	}
	if status != "" {
		fail(fmt.Errorf("source workspace has uncommitted changes; commit or stash before starting a Run"))
		return
	}
	base, err := git(ctx, run.Path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		fail(err)
		return
	}
	base = strings.TrimSpace(base)
	if err := w.store.BindBase(run.ID, base); err != nil {
		fail(fmt.Errorf("source revision changed since the first attempt: %w", err))
		return
	}
	dir := filepath.Join(w.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		fail(err)
		return
	}
	hooks := filepath.Join(dir, "empty-hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		fail(err)
		return
	}
	worktree := filepath.Join(dir, "workspace")
	// Persist the location before allocation so partial preparation remains discoverable.
	if err := w.store.AddArtifact(id, "workspace", worktree, base); err != nil {
		fail(err)
		return
	}
	if _, err := git(ctx, run.Path, "-c", "core.hooksPath="+hooks, "worktree", "add", "--detach", worktree, base); err != nil {
		fail(err)
		return
	}
	e, err := factory(id, worktree)
	if err != nil {
		fail(err)
		return
	}
	if e == nil {
		fail(fmt.Errorf("provider factory returned no execution"))
		return
	}
	defer e.Stop()
	if e.Identity().ExecutionID != id || string(e.Identity().Provider) != run.Provider {
		fail(fmt.Errorf("provider execution identity mismatch"))
		return
	}
	if err := e.Start(); err != nil {
		fail(err)
		return
	}
	if err := e.Send(run.Prompt); err != nil {
		fail(err)
		return
	}
	var outcome *providers.Outcome
	for {
		event, err := e.Next(ctx)
		if err != nil {
			e.Stop()
			fail(err)
			return
		}
		if event.Kind == providers.TurnFinished {
			if event.Identity != e.Identity() {
				fail(fmt.Errorf("provider outcome identity mismatch"))
				return
			}
			outcome = event.Outcome
			break
		}
	}
	e.Stop()
	status, err = git(ctx, worktree, "status", "--porcelain")
	if err != nil {
		fail(err)
		return
	}
	patch, err := git(ctx, worktree, "diff", "--no-ext-diff", "--no-textconv", "--binary", base, "--")
	if err != nil {
		fail(err)
		return
	}
	for name, content := range map[string]string{"changes.patch": patch, "status.txt": status} {
		path := filepath.Join(dir, name)
		if err := writeExclusive(path, []byte(content)); err != nil {
			fail(err)
			return
		}
		if err := w.store.AddArtifact(id, name, path, ""); err != nil {
			fail(err)
			return
		}
	}
	if outcome == nil {
		fail(fmt.Errorf("provider returned no outcome"))
		return
	}
	detail := outcome.Text
	if outcome.Diagnostics != "" {
		detail += "\n" + outcome.Diagnostics
	}
	if err := w.store.FinishAttempt(id, outcome.Status == providers.CompletionSuccess && !outcome.IsError, detail); err != nil {
		if !errors.Is(err, ErrConflict) {
			log.Printf("run %s failed to persist outcome: %v", run.ID, err)
		}
		return
	}
	// No automatic Complete: a human/code review check remains mandatory.
	if outcome.Status == providers.CompletionSuccess && !outcome.IsError {
		_, err := git(ctx, worktree, "diff", "--no-ext-diff", "--no-textconv", "--check", base, "--")
		checkDetail := "git diff --check passed"
		if err != nil {
			checkDetail = err.Error()
		}
		if saveErr := w.store.RecordCheck(id, "diff_check", err == nil, checkDetail); saveErr != nil && !errors.Is(saveErr, ErrConflict) {
			log.Printf("run %s failed to persist check: %v", run.ID, saveErr)
		}
		if err != nil {
			return
		}
		for _, check := range plan {
			beforeCheck, snapshotErr := reviewFingerprint(ctx, worktree)
			if snapshotErr != nil {
				if saveErr := w.store.RecordCheck(id, check.Name, false, "failed to snapshot worktree before check: "+snapshotErr.Error()); saveErr != nil && !errors.Is(saveErr, ErrConflict) {
					log.Printf("run %s failed to persist project check: %v", run.ID, saveErr)
				}
				return
			}
			passed, detail, logPath, runErr := runCheck(ctx, worktree, dir, check)
			if runErr != nil {
				passed, detail = false, runErr.Error()
			}
			afterCheck, snapshotErr := reviewFingerprint(ctx, worktree)
			if snapshotErr != nil {
				passed, detail = false, "failed to snapshot worktree after check: "+snapshotErr.Error()
			} else if beforeCheck != afterCheck {
				passed, detail = false, "check changed tracked or untracked source files; verification rejected"
			}
			if logPath != "" {
				if saveErr := w.store.AddArtifact(id, "check_log:"+check.Name, logPath, ""); saveErr != nil {
					if !errors.Is(saveErr, ErrConflict) {
						log.Printf("run %s failed to persist check artifact: %v", run.ID, saveErr)
						_ = w.store.RecordCheck(id, check.Name, false, "failed to persist check artifact: "+saveErr.Error())
					}
					return
				}
			}
			if saveErr := w.store.RecordCheck(id, check.Name, passed, detail); saveErr != nil {
				if !errors.Is(saveErr, ErrConflict) {
					log.Printf("run %s failed to persist project check: %v", run.ID, saveErr)
				}
				return
			}
			if !passed {
				return
			}
		}
		if reviewer == nil {
			return
		}
		passed, detail, reviewErr := w.review(ctx, run, id, worktree, base, dir, reviewer)
		if reviewErr != nil {
			passed, detail = false, reviewErr.Error()
		}
		if saveErr := w.store.RecordCheck(id, "review", passed, detail); saveErr != nil {
			if !errors.Is(saveErr, ErrConflict) {
				log.Printf("run %s failed to persist review: %v", run.ID, saveErr)
			}
			return
		}
		if passed {
			if completeErr := w.store.Complete(run.ID); completeErr != nil && !errors.Is(completeErr, ErrConflict) {
				log.Printf("run %s failed to complete: %v", run.ID, completeErr)
			}
		}
	}
}
