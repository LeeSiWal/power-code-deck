package orchestration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"powercodedeck/internal/providers"
)

// scripted runs a callback in the workspace and reports a model name, like a
// provider that edits files and tells us which model answered.
type scripted struct {
	id, cwd  string
	provider providers.ID
	model    string
	act      func(cwd, prompt string)
	prompt   string
	once     sync.Once
	sent     bool
}

func (e *scripted) Identity() providers.Identity {
	return providers.Identity{ExecutionID: e.id, Provider: e.provider}
}
func (e *scripted) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (e *scripted) Start() error                         { return nil }
func (e *scripted) Send(p string) error {
	e.prompt, e.sent = p, true
	if e.act != nil {
		e.act(e.cwd, p)
	}
	return nil
}
func (e *scripted) Next(context.Context) (providers.Event, error) {
	return providers.Event{Identity: e.Identity(), Kind: providers.TurnFinished, Model: e.model, Outcome: &providers.Outcome{Status: providers.CompletionSuccess, Text: "done"}}, nil
}
func (e *scripted) Interrupt() error               { return nil }
func (e *scripted) Stop()                          {}
func (e *scripted) ConversationID() string         { return "" }
func (e *scripted) SetPermissionMode(string) error { return nil }

func waitResult(t *testing.T, ch chan AttemptResult) AttemptResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("no attempt result")
	}
	return AttemptResult{}
}

// Tests 12, 13 (worker half): a Codex attempt edits, adds an untracked file and
// a sensitive .env; a Claude attempt inherits the edits (not the .env) with a
// handoff prompt; the observer sees models, checks and verified quiescence.
func TestRoutedAttemptsCarryWorkAcrossProviders(t *testing.T) {
	SensitivePath = func(p string) bool { return strings.HasPrefix(filepath.Base(p), ".env") }
	defer func() { SensitivePath = func(string) bool { return false } }()
	repo := testRepo(t)
	os.MkdirAll(filepath.Join(repo, ".powercodedeck"), 0700)
	// A project check that fails until the second attempt fixes it.
	manifest := `{"version":1,"checks":[{"name":"has_fix","argv":["grep","-q","fixed","tracked.txt"],"timeoutSeconds":30}]}`
	os.WriteFile(filepath.Join(repo, ".powercodedeck", "checks.json"), []byte(manifest), 0600)
	git(context.Background(), repo, "add", ".")
	git(context.Background(), repo, "-c", "user.name=T", "-c", "user.email=t@e.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "checks")

	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, err := store.Create("routed", repo, "fix tracked.txt; keep notes.md", "codex")
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorker(store, t.TempDir(), map[string]Factory{})
	if err != nil {
		t.Fatal(err)
	}
	w.quiesceWait = 2 * time.Second
	var mu sync.Mutex
	var second *scripted
	var secondSawEdit bool
	w.SetLaunchFactory(func(id, cwd string, l Launch) (providers.Execution, error) {
		mu.Lock()
		defer mu.Unlock()
		if l.Provider == "codex" {
			return &scripted{id: id, cwd: cwd, provider: providers.Codex, model: "gpt-5.6-luna", act: func(cwd, _ string) {
				os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("partial\n"), 0600)
				os.WriteFile(filepath.Join(cwd, "notes.md"), []byte("decision: keep API\n"), 0600)
				os.WriteFile(filepath.Join(cwd, ".env"), []byte("TOKEN=secret\n"), 0600)
			}}, nil
		}
		second = &scripted{id: id, cwd: cwd, provider: providers.Claude, model: "claude-opus-5", act: func(cwd, _ string) {
			b, _ := os.ReadFile(filepath.Join(cwd, "tracked.txt"))
			n, _ := os.ReadFile(filepath.Join(cwd, "notes.md"))
			_, envErr := os.Stat(filepath.Join(cwd, ".env"))
			secondSawEdit = string(b) == "partial\n" && string(n) == "decision: keep API\n" && os.IsNotExist(envErr)
			os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("fixed\n"), 0600)
		}}
		return second, nil
	})
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExecution{id: id, cwd: cwd, response: `{"verdict":"pass","summary":"ok"}`, stopped: make(chan struct{})}, nil
	})
	results := make(chan AttemptResult, 4)
	w.SetAttemptObserver(func(r AttemptResult) { results <- r })

	first, err := w.StartWith(run.ID, Launch{ProfileID: "codex-fast", Provider: "codex", Model: "gpt-5.6-luna", Effort: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	r1 := waitResult(t, results)
	if r1.ExecutionID != first || r1.ObservedModel != "gpt-5.6-luna" || r1.ProviderStatus != "success" || !r1.QuiesceVerified || r1.RunState != "failed" {
		t.Fatalf("first result %+v", r1)
	}
	failed := false
	for _, c := range r1.Checks {
		if c.Name == "has_fix" && !c.Passed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("expected failing project check, got %+v", r1.Checks)
	}
	raw, err := w.ReadArtifact(run.ID, first, "checkpoint.json")
	if err != nil {
		t.Fatal(err)
	}
	var cp Checkpoint
	json.Unmarshal(raw, &cp)
	paths := map[string]string{}
	for _, f := range cp.Files {
		paths[f.Path] = f.Status
	}
	if paths["tracked.txt"] != "modified" || paths["notes.md"] != "added" || paths[".env"] != "" || len(cp.Excluded) != 1 {
		t.Fatalf("checkpoint %+v", cp)
	}

	// Inheriting from an attempt of another Run is refused.
	if _, err := w.StartWith(run.ID, Launch{Provider: "claude", InheritFrom: "exec_nope"}); err == nil {
		t.Fatal("foreign inheritance accepted")
	}
	handoff := "## PowerCodeDeck handoff\n### User goal (verbatim)\nfix tracked.txt; keep notes.md\n"
	secondID, err := w.StartWith(run.ID, Launch{ProfileID: "claude-opus", Provider: "claude", Model: "opus", InheritFrom: first, Handoff: handoff})
	if err != nil {
		t.Fatal(err)
	}
	r2 := waitResult(t, results)
	if r2.ExecutionID != secondID || r2.RunState != "succeeded" || !r2.Routed {
		t.Fatalf("second result %+v", r2)
	}
	if !secondSawEdit {
		t.Fatal("second provider did not start from the first attempt's changes (or saw the sensitive file)")
	}
	if second.prompt != handoff {
		t.Fatal("handoff prompt not delivered")
	}
	if b, _ := w.ReadArtifact(run.ID, secondID, "handoff"); string(b) != handoff {
		t.Fatal("handoff artifact missing")
	}
	// The source checkout is never touched.
	if b, _ := os.ReadFile(filepath.Join(repo, "tracked.txt")); string(b) != "original\n" {
		t.Fatal("source modified")
	}
	// Identity must match the launch provider, not the Run's original provider.
	w.SetLaunchFactory(func(id, cwd string, l Launch) (providers.Execution, error) {
		return &scripted{id: id, cwd: cwd, provider: providers.Codex}, nil
	})
	run2, _ := store.Create("routed-2", testRepo(t), "x", "codex")
	if _, err := w.StartWith(run2.ID, Launch{Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	if r := waitResult(t, results); !strings.Contains(r.Diagnostics, "identity mismatch") {
		t.Fatalf("identity mismatch not detected: %+v", r)
	}
}

// Test 15: a tool process left running in the workspace makes quiescence
// unverified, so no second writer is started automatically.
func TestQuiesceDetectsLeftoverProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process scan is Linux-only; other platforms report unverified")
	}
	store := openTest(t, filepath.Join(t.TempDir(), "runs.db"))
	run, _ := store.Create("q", testRepo(t), "x", "codex")
	w, _ := NewWorker(store, t.TempDir(), nil)
	w.quiesceWait = 500 * time.Millisecond
	var child *exec.Cmd
	w.SetLaunchFactory(func(id, cwd string, l Launch) (providers.Execution, error) {
		return &scripted{id: id, cwd: cwd, provider: providers.Codex, act: func(cwd, _ string) {
			child = exec.Command("sleep", "30")
			child.Dir = cwd
			child.Start()
		}}, nil
	})
	results := make(chan AttemptResult, 1)
	w.SetAttemptObserver(func(r AttemptResult) { results <- r })
	if _, err := w.StartWith(run.ID, Launch{Provider: "codex"}); err != nil {
		t.Fatal(err)
	}
	r := waitResult(t, results)
	if child != nil && child.Process != nil {
		child.Process.Kill()
		child.Wait()
	}
	if r.QuiesceVerified || !strings.Contains(r.QuiesceDetail, "still running") {
		t.Fatalf("leftover process not detected: %+v", r)
	}
}
