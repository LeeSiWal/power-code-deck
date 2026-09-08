package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"powercodedeck/internal/providers"
)

type fakeExecution struct {
	id      string
	cwd     string
	block   bool
	entered chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (e *fakeExecution) Identity() providers.Identity {
	return providers.Identity{ExecutionID: e.id, Provider: providers.Antigravity}
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
	return os.WriteFile(filepath.Join(e.cwd, "tracked.txt"), []byte("worker change\n"), 0600)
}
func (e *fakeExecution) Next(ctx context.Context) (providers.Event, error) {
	if e.block {
		<-ctx.Done()
		return providers.Event{}, ctx.Err()
	}
	return providers.Event{Identity: e.Identity(), Kind: providers.TurnFinished, Outcome: &providers.Outcome{Status: providers.CompletionSuccess, Text: "done"}}, nil
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
