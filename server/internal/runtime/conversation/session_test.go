package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
)

// Run the test binary as a CLI to cover actual process arguments and lifecycle.
func TestConversationCLI(t *testing.T) {
	if os.Getenv("PCD_CONVERSATION_HELPER") != "1" {
		return
	}
	value := func(flag string) string {
		for i, arg := range os.Args {
			if arg == flag && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return ""
	}
	fmt.Println(`{"event":"init","conversation_id":"owned-conversation","init":{"model":"fake"}}`)
	if value("--prompt") == "block" {
		for {
			time.Sleep(time.Second)
		}
	}
	observation, _ := json.Marshal(map[string]string{"prompt": value("--prompt"), "resume": value("--conversation")})
	result, _ := json.Marshal(map[string]any{"event": "result", "result": map[string]any{"conversation_id": "owned-conversation", "status": "SUCCESS", "response": string(observation), "usage": map[string]int{"input_tokens": 12}}})
	fmt.Println(string(result))
	if value("--prompt") == "crash" {
		os.Exit(1)
	}
	os.Exit(0)
}

func testSession(t *testing.T) *Session {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := antigravity.Config{Command: bin, PrefixArgs: []string{"-test.run=^TestConversationCLI$", "--"}, Cwd: t.TempDir(), Env: append(os.Environ(), "PCD_CONVERSATION_HELPER=1")}
	s, err := New(func(id, resume string) (providers.Execution, error) {
		cfg := cfg
		cfg.ResumeID = resume
		return antigravity.New(id, cfg)
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func readOutcome(t *testing.T, turn *Turn) providers.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		event, err := turn.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Identity != turn.Identity() {
			t.Fatal("execution identity lost")
		}
		if event.Kind == providers.TurnFinished {
			return event
		}
	}
}

func TestSequentialAntigravityTurnsResumeWithNewIdentity(t *testing.T) {
	s := testSession(t)
	var previous providers.Identity
	for i, prompt := range []string{"first", "second 'quoted' ; $(literal)"} {
		turn, err := s.StartTurn(prompt)
		if err != nil {
			t.Fatal(err)
		}
		if turn.Identity() == previous {
			t.Fatal("reused process identity")
		}
		previous = turn.Identity()
		event := readOutcome(t, turn)
		if event.Outcome.Status != providers.CompletionSuccess || event.Outcome.Usage.Scope != providers.ConversationUsage {
			t.Fatalf("outcome altered: %+v", event)
		}
		var observation map[string]string
		if err := json.Unmarshal([]byte(event.Outcome.Text), &observation); err != nil {
			t.Fatal(err)
		}
		resume := ""
		if i > 0 {
			resume = "owned-conversation"
		}
		if !reflect.DeepEqual(observation, map[string]string{"prompt": prompt, "resume": resume}) {
			t.Fatalf("arguments: %#v", observation)
		}
		if s.ConversationID() != "owned-conversation" {
			t.Fatal("resume not recorded before completion")
		}
		if _, err := turn.Next(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("completed reader: %v", err)
		}
		if err := turn.Interrupt(); err == nil {
			t.Fatal("old turn interrupt accepted")
		}
	}
}

func TestBusyCancellationInterruptAndReuse(t *testing.T) {
	s := testSession(t)
	turn, err := s.StartTurn("block")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if event, err := turn.Next(ctx); err != nil || event.Kind != providers.Ready {
		t.Fatalf("init: %+v %v", event, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := turn.Next(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.StartTurn("overlap"); !errors.Is(err, ErrBusy) {
				t.Errorf("overlap: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := turn.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if event := readOutcome(t, turn); event.Outcome.Status != providers.CompletionInterrupted {
		t.Fatalf("interrupt: %+v", event)
	}
	next, err := s.StartTurn("continue")
	if err != nil {
		t.Fatal(err)
	}
	if event := readOutcome(t, next); event.Outcome.Status != providers.CompletionSuccess {
		t.Fatal(event)
	}
}

func TestProcessFailureAllowsNextTurn(t *testing.T) {
	s := testSession(t)
	turn, err := s.StartTurn("crash")
	if err != nil {
		t.Fatal(err)
	}
	if event := readOutcome(t, turn); event.Outcome.Status != providers.CompletionFailed {
		t.Fatal(event)
	}
	next, err := s.StartTurn("recover")
	if err != nil {
		t.Fatal(err)
	}
	readOutcome(t, next)
}

func TestStopUnblocksReaderAndPreventsNewTurns(t *testing.T) {
	s := testSession(t)
	turn, err := s.StartTurn("block")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := turn.Next(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := turn.Next(ctx); done <- err }()
	s.Stop()
	s.Stop()
	select {
	case err := <-done:
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("reader leaked")
		}
	case <-ctx.Done():
		t.Fatal("reader leaked")
	}
	if _, err := s.StartTurn("late"); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestStartupFailurePreservesResumeAndCanRetry(t *testing.T) {
	count := 0
	s, err := New(func(id, resume string) (providers.Execution, error) {
		count++
		if resume != "saved" {
			t.Fatal("resume lost")
		}
		return antigravity.New(id, antigravity.Config{Command: "/nonexistent/pcd-test-agy", Cwd: t.TempDir(), ResumeID: resume})
	}, "saved")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	for range 2 {
		if _, err := s.StartTurn("retry"); err == nil || errors.Is(err, ErrBusy) {
			t.Fatalf("startup: %v", err)
		}
	}
	if count != 2 || s.ConversationID() != "saved" {
		t.Fatal("failed launch changed conversation")
	}
}

type emptyExecution struct {
	providers.Execution
	id      providers.Identity
	stopped bool
	sendErr error
}

func (e *emptyExecution) Identity() providers.Identity         { return e.id }
func (e *emptyExecution) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (e *emptyExecution) Start() error                         { return nil }
func (e *emptyExecution) Send(string) error                    { return e.sendErr }
func (e *emptyExecution) Next(context.Context) (providers.Event, error) {
	return providers.Event{}, io.EOF
}
func (e *emptyExecution) ConversationID() string { return "" }
func (e *emptyExecution) Stop()                  { e.stopped = true }

func TestMissingOutcomeReleasesSlotWithoutClaimingSuccess(t *testing.T) {
	var execution *emptyExecution
	s, err := New(func(id, resume string) (providers.Execution, error) {
		execution = &emptyExecution{id: providers.Identity{ExecutionID: id, Provider: providers.Antigravity}}
		return execution, nil
	}, "saved")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	turn, err := s.StartTurn("first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Next(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("missing outcome: %v", err)
	}
	if !execution.stopped || s.ConversationID() != "saved" {
		t.Fatal("cleanup or resume lost")
	}
	if _, err := s.StartTurn("retry"); err != nil {
		t.Fatal(err)
	}
}

func TestSendFailureStopsExecutionAndReleasesSlot(t *testing.T) {
	var execution *emptyExecution
	s, err := New(func(id, resume string) (providers.Execution, error) {
		execution = &emptyExecution{id: providers.Identity{ExecutionID: id}, sendErr: errors.New("spawn failed")}
		return execution, nil
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	for range 2 {
		if _, err := s.StartTurn("retry"); err == nil || errors.Is(err, ErrBusy) {
			t.Fatalf("send failure: %v", err)
		}
		if !execution.stopped {
			t.Fatal("failed execution leaked")
		}
	}
}
