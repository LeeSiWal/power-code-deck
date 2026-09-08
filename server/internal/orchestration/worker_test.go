package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"powercodedeck/internal/providers"
)

type fakeExecution struct {
	provider     providers.ID
	id           string
	cwd          string
	block        bool
	entered      chan struct{}
	stopped      chan struct{}
	once         sync.Once
	response     string
	reviewChange string
}

func (e *fakeExecution) Identity() providers.Identity {
	if e.provider != "" {
		return providers.Identity{ExecutionID: e.id, Provider: e.provider}
	}
	return providers.Identity{ExecutionID: e.id, Provider: providers.Antigravity}
}

func TestNativeRunWithIndependentAntigravityReview(t *testing.T) {
	for _, provider := range []providers.ID{providers.Claude, providers.Codex} {
		t.Run(string(provider), func(t *testing.T) {
			store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
			run, err := store.Create("native", testRepo(t), "edit tracked file", string(provider))
			if err != nil {
				t.Fatal(err)
			}
			worker, err := NewWorker(store, t.TempDir(), map[string]Factory{string(provider): func(id, cwd string) (providers.Execution, error) {
				return &fakeExecution{id: id, cwd: cwd, provider: provider, stopped: make(chan struct{})}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
				return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"independent review passed"}`, stopped: make(chan struct{})}, nil
			})
			if _, err := worker.Start(run.ID); err != nil {
				t.Fatal(err)
			}
			waitWorker(t, worker)
			got, err := store.Get(run.ID)
			if err != nil || got.State != "succeeded" {
				t.Fatal(got, err)
			}
		})
	}
}
func (e *fakeExecution) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (e *fakeExecution) Start() error                         { return nil }
func (e *fakeExecution) Send(string) error {
	if e.entered != nil {
		close(e.entered)
	}
	if e.block {
		return nil
	}
	if e.response != "" {
		if e.reviewChange != "" {
			return os.WriteFile(filepath.Join(e.cwd, "tracked.txt"), []byte(e.reviewChange), 0600)
		}
		return nil
	}
	return os.WriteFile(filepath.Join(e.cwd, "tracked.txt"), []byte("worker change\n"), 0600)
}
func (e *fakeExecution) Next(ctx context.Context) (providers.Event, error) {
	if e.block {
		<-ctx.Done()
		return providers.Event{}, ctx.Err()
	}
	text := e.response
	if text == "" {
		text = "done"
	}
	return providers.Event{Identity: e.Identity(), Kind: providers.TurnFinished, Outcome: &providers.Outcome{Status: providers.CompletionSuccess, Text: text}}, nil
}
func (e *fakeExecution) Interrupt() error               { e.Stop(); return nil }
func (e *fakeExecution) Stop()                          { e.once.Do(func() { close(e.stopped) }) }
func (e *fakeExecution) ConversationID() string         { return "" }
func (e *fakeExecution) SetPermissionMode(string) error { return nil }

func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := git(ctx, dir, "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, dir, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	return dir
}
func waitWorker(t *testing.T, w *Worker) {
	t.Helper()
	w.mu.Lock()
	done := w.done
	w.mu.Unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestWorkerIsolatesChangesAndRequiresReview(t *testing.T) {
	repo := testRepo(t)
	s := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	r, err := s.Create("worker", repo, "edit tracked.txt", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	var execution *fakeExecution
	w, err := NewWorker(s, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		execution = &fakeExecution{id: id, cwd: cwd, stopped: make(chan struct{})}
		return execution, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.Start(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	original, _ := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if string(original) != "original\n" {
		t.Fatal("source modified")
	}
	got, err := s.Get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "awaiting_checks" || len(got.Executions[0].Artifacts) != 3 || len(got.RequiredChecks) != 2 {
		t.Fatalf("result: %+v", got)
	}
	if err := s.Complete(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("missing review accepted", err)
	}
	for _, a := range got.Executions[0].Artifacts {
		if a.Kind == "changes.patch" {
			patch, err := os.ReadFile(a.Path)
			if err != nil || !strings.Contains(string(patch), "+worker change") {
				t.Fatal("patch missing", err)
			}
		}
	}
	if err := s.RecordCheck(id, "review", true, "reviewed separately"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(r.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-execution.stopped:
	default:
		t.Fatal("execution not closed")
	}
}

func TestWorkerCancellationStopsExecution(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	r, err := s.Create("cancel", testRepo(t), "wait", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	entered, stopped := make(chan struct{}), make(chan struct{})
	w, err := NewWorker(s, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, block: true, entered: entered, stopped: stopped}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.Start(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider not started")
	}
	if _, err := w.Start(r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent dispatch accepted")
	}
	if err := w.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	select {
	case <-stopped:
	default:
		t.Fatal("provider not stopped")
	}
	if err := s.FinishAttempt(id, true, "late"); !errors.Is(err, ErrConflict) {
		t.Fatal("late completion accepted")
	}
	got, _ := s.Get(r.ID)
	if got.State != "canceled" {
		t.Fatal(got.State)
	}
}

func TestWorkerRejectsDirtySourceWithoutCallingProvider(t *testing.T) {
	repo := testRepo(t)
	os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("user work"), 0600)
	s := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	r, err := s.Create("dirty", repo, "edit", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorker(s, t.TempDir(), map[string]Factory{"antigravity": func(string, string) (providers.Execution, error) {
		t.Error("dirty source dispatched")
		return nil, errors.New("unexpected")
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Start(r.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, w)
	got, _ := s.Get(r.ID)
	if got.State != "failed" || !strings.Contains(got.Executions[0].Detail, "uncommitted") {
		t.Fatal(got)
	}
}

func TestWorkerCheckHelper(t *testing.T) {
	helper := false
	for _, arg := range os.Args {
		if arg == "--pcd-worker-check-helper" {
			helper = true
		}
	}
	if !helper {
		return
	}
	fmt.Println("literal argument:", os.Args[len(os.Args)-1])
	if os.Args[len(os.Args)-1] == "verify-change" {
		data, err := os.ReadFile("tracked.txt")
		if err != nil || string(data) != "PCD_RUN_LIVE_OK\n" {
			fmt.Println("expected exact live test marker")
			os.Exit(5)
		}
	}
	if os.Args[len(os.Args)-1] == "mutate" {
		if err := os.WriteFile("tracked.txt", []byte("check mutation\n"), 0600); err != nil {
			os.Exit(4)
		}
	}
	if os.Args[len(os.Args)-1] == "fail" {
		os.Exit(3)
	}
	os.Exit(0)
}

func addCheckManifest(t *testing.T, repo, argument string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"version": 1, "checks": []any{map[string]any{
		"name": "project_test", "cwd": ".", "argv": []string{bin, "-test.run=^TestWorkerCheckHelper$", "--", "--pcd-worker-check-helper", argument}, "timeoutSeconds": 20,
	}}}
	data, _ := json.Marshal(manifest)
	if err := os.Mkdir(filepath.Join(repo, ".powercodedeck"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, checkManifest), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), repo, "add", checkManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := git(context.Background(), repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "checks"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRunsManifestChecksAndIndependentReview(t *testing.T) {
	repo := testRepo(t)
	addCheckManifest(t, repo, "$(must-not-run) ; literal")
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("verified", repo, "edit the tracked file", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, stopped: make(chan struct{})}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"change matches the task"}`, stopped: make(chan struct{})}, nil
	})
	if _, err := worker.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker)
	got, err := store.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || len(got.RequiredChecks) != 3 || len(got.Executions[0].Checks) != 3 {
		t.Fatalf("verified run: %+v", got)
	}
	foundCheckLog, foundReviewLog := false, false
	for _, artifact := range got.Executions[0].Artifacts {
		switch artifact.Kind {
		case "check_log:project_test":
			foundCheckLog = true
			content, err := os.ReadFile(artifact.Path)
			if err != nil || !strings.Contains(string(content), "$(must-not-run) ; literal") {
				t.Fatal("argv was not passed literally", err)
			}
		case "review_log":
			foundReviewLog = true
		}
	}
	if !foundCheckLog || !foundReviewLog {
		t.Fatal("verification artifacts missing")
	}
	content, err := worker.ReadArtifact(run.ID, got.Executions[0].ID, "check_log:project_test")
	if err != nil || !strings.Contains(string(content), "$(must-not-run) ; literal") {
		t.Fatal("registered check artifact was not readable", err)
	}
	if _, err := worker.ReadArtifact("another-run", got.Executions[0].ID, "check_log:project_test"); err == nil {
		t.Fatal("artifact escaped its run", err)
	}
	if _, err := worker.ReadArtifact(run.ID, got.Executions[0].ID, "workspace"); !errors.Is(err, ErrInvalid) {
		t.Fatal("workspace artifact became readable", err)
	}
}

func TestWorkerStopsAfterFailedProjectCheck(t *testing.T) {
	repo := testRepo(t)
	addCheckManifest(t, repo, "fail")
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("failed-check", repo, "edit", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := false
	worker, _ := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, stopped: make(chan struct{})}, nil
	}})
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		reviewed = true
		return nil, errors.New("must not run")
	})
	if _, err := worker.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker)
	got, _ := store.Get(run.ID)
	if got.State != "failed" || reviewed {
		t.Fatal("failed check did not stop review")
	}
}

func TestWorkerRejectsProjectCheckMutation(t *testing.T) {
	repo := testRepo(t)
	addCheckManifest(t, repo, "mutate")
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("mutating-check", repo, "edit", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := false
	worker, _ := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, stopped: make(chan struct{})}, nil
	}})
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		reviewed = true
		return nil, errors.New("must not run")
	})
	if _, err := worker.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker)
	got, _ := store.Get(run.ID)
	if got.State != "failed" || reviewed {
		t.Fatal("mutating check did not stop review")
	}
	checks := got.Executions[0].Checks
	if checks[len(checks)-1].Passed || !strings.Contains(checks[len(checks)-1].Detail, "changed") {
		t.Fatal("check mutation evidence missing")
	}
}

func TestWorkerRejectsReviewerMutation(t *testing.T) {
	repo := testRepo(t)
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("review-mutation", repo, "edit", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := NewWorker(store, t.TempDir(), map[string]Factory{"antigravity": func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, stopped: make(chan struct{})}, nil
	}})
	worker.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"looks good"}`, reviewChange: "reviewer changed it\n", stopped: make(chan struct{})}, nil
	})
	if _, err := worker.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker)
	got, _ := store.Get(run.ID)
	if got.State != "failed" {
		t.Fatal("reviewer mutation was accepted")
	}
	checks := got.Executions[0].Checks
	if checks[len(checks)-1].Name != "review" || checks[len(checks)-1].Passed || !strings.Contains(checks[len(checks)-1].Detail, "changed") {
		t.Fatal("review mutation evidence missing")
	}
}
