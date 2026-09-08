package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/providers"
)

// The test binary impersonates agy; no real model or authentication is used.
func TestAGYHelper(t *testing.T) {
	if os.Getenv("PCD_AGY_TEST_HELPER") != "1" {
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
	present := func(flag string) bool {
		for _, arg := range os.Args {
			if arg == flag {
				return true
			}
		}
		return false
	}
	prompt := value("--prompt")
	fmt.Println(`{"event":"init","conversation_id":"agy-conversation","init":{"model":"fake"}}`)
	if prompt == "block" {
		for {
			time.Sleep(time.Second)
		}
	}
	if prompt == "missing" {
		os.Exit(0)
	}
	if prompt == "permission-empty" || prompt == "empty-success" {
		if prompt == "permission-empty" {
			fmt.Fprintln(os.Stderr, `jetski: no output produced — a tool required the "read_file" permission that headless mode cannot prompt for, so it was auto-denied.`)
		}
		fmt.Println(`{"event":"result","result":{"conversation_id":"agy-conversation","status":"SUCCESS","response":""}}`)
		os.Exit(0)
	}
	cwd, _ := os.Getwd()
	observation, _ := json.Marshal(map[string]string{"prompt": prompt, "model": value("--model"), "mode": value("--mode"), "schema": value("--json-schema"), "resume": value("--conversation"), "cwd": cwd, "workspace": value("--add-dir"), "sandbox": fmt.Sprint(present("--sandbox")), "bypass": fmt.Sprint(present("--dangerously-skip-permissions"))})
	message, _ := json.Marshal(map[string]any{"event": "step_update", "step_update": map[string]any{"step_type": "agent_response", "step_index": 1, "state": "DONE", "conversation_id": "agy-conversation", "text_delta": string(observation)}})
	fmt.Println(string(message))
	fmt.Println(`{"event":"step_update","step_update":{"conversation_id":"agy-conversation","step_index":2,"step_type":"tool","state":"DONE","tool_name":"run_command","tool_info":{"parameters":{"CommandLine":"test"},"output":"denied"}}}`)
	fmt.Fprintln(os.Stderr, "approval required: command soft-denied")
	fmt.Println(`{"event":"result","result":{"conversation_id":"agy-conversation","status":"SUCCESS","response":"done","usage":{"input_tokens":10,"output_tokens":2,"cache_read_tokens":3,"thinking_tokens":1,"total_tokens":12}}}`)
	if prompt == "crash" {
		fmt.Fprintln(os.Stderr, "fake CLI crashed")
		os.Exit(1)
	}
	os.Exit(0)
}
func helperExecution(t *testing.T) *Execution {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "workspace with spaces")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	e, err := New("agy-execution", Config{Command: bin, PrefixArgs: []string{"-test.run=^TestAGYHelper$", "--"}, Cwd: cwd, Model: "chosen-model", Mode: "plan", JSONSchema: `{"type":"object"}`, Sandbox: true, ResumeID: "explicit-session", Env: append(os.Environ(), "PCD_AGY_TEST_HELPER=1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	if err := e.Start(); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestHeadlessPermissionDenialOverridesEmptySuccess(t *testing.T) {
	for _, prompt := range []string{"permission-empty", "empty-success"} {
		t.Run(prompt, func(t *testing.T) {
			e := helperExecution(t)
			if err := e.Send(prompt); err != nil {
				t.Fatal(err)
			}
			events := drain(t, e)
			last := events[len(events)-1]
			if last.Kind != providers.TurnFinished || last.Outcome == nil {
				t.Fatal(last)
			}
			if prompt == "permission-empty" {
				if !last.Outcome.IsError || last.Outcome.Status != providers.CompletionFailed || last.Outcome.Reason != "permission_denied" || !strings.Contains(last.Outcome.Diagnostics, "read_file") {
					t.Fatal(last.Outcome)
				}
			} else if last.Outcome.IsError || last.Outcome.Status != providers.CompletionSuccess {
				t.Fatal("empty response alone is not proof of denial", last.Outcome)
			}
		})
	}
}
func drain(t *testing.T, e *Execution) []providers.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []providers.Event
	for {
		event, err := e.Next(ctx)
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
}
func TestHeadlessArgsEventsResumeAndUsage(t *testing.T) {
	e := helperExecution(t)
	prompt := "spaces 'quotes' ; $(not-a-command)\nsecond line"
	if err := e.Send(prompt); err != nil {
		t.Fatal(err)
	}
	events := drain(t, e)
	if len(events) != 4 {
		t.Fatalf("events: %+v", events)
	}
	for i, event := range events {
		if event.Identity != e.Identity() || event.Sequence != uint64(i+1) {
			t.Fatalf("identity/sequence: %+v", event)
		}
	}
	if e.ConversationID() != "agy-conversation" || !events[1].Delta {
		t.Fatal("conversation or streaming lost")
	}
	var observed map[string]string
	if err := json.Unmarshal([]byte(events[1].Blocks[0].Text), &observed); err != nil {
		t.Fatal(err)
	}
	cwd, _ := filepath.EvalSymlinks(e.cfg.Cwd)
	want := map[string]string{"prompt": prompt, "model": "chosen-model", "mode": "plan", "schema": `{"type":"object"}`, "resume": "explicit-session", "cwd": cwd, "workspace": e.cfg.Cwd, "sandbox": "true", "bypass": "false"}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("argv/cwd: got %v want %v", observed, want)
	}
	blocks := events[2].Blocks
	if len(blocks) != 2 || blocks[0].Kind != providers.ToolCall || blocks[1].Kind != providers.ToolResult || blocks[0].ToolCallID != blocks[1].ToolCallID {
		t.Fatal("tool correlation lost")
	}
	result := events[3].Outcome
	if result == nil || result.IsError || result.CostUSD != nil || result.Usage == nil || result.Usage.InputTokens != 10 || result.Usage.Scope != providers.ConversationUsage || result.Diagnostics == "" {
		t.Fatalf("outcome: %+v", result)
	}
	if err := e.Send("second"); err == nil {
		t.Fatal("single-turn execution accepted another process")
	}
	if err := e.SetPermissionMode("always-proceed"); err == nil {
		t.Fatal("silently enabled unsupported approval mode")
	}
}
func TestProcessFailureCannotReportSuccess(t *testing.T) {
	for _, prompt := range []string{"missing", "crash"} {
		t.Run(prompt, func(t *testing.T) {
			e := helperExecution(t)
			if err := e.Send(prompt); err != nil {
				t.Fatal(err)
			}
			events := drain(t, e)
			last := events[len(events)-1]
			if last.Kind != providers.TurnFinished || last.Outcome == nil || !last.Outcome.IsError {
				t.Fatalf("failure reported as success: %+v", last)
			}
			n := 0
			for _, event := range events {
				if event.Kind == providers.TurnFinished {
					n++
				}
			}
			if n != 1 {
				t.Fatal("multiple terminal outcomes")
			}
		})
	}
}
func TestCancelReadAndInterruptProcess(t *testing.T) {
	e := helperExecution(t)
	if err := e.Send("block"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if event, err := e.Next(ctx); err != nil || event.Kind != providers.Ready {
		t.Fatalf("init: %+v %v", event, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := e.Next(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel read: %v", err)
	}
	if err := e.Interrupt(); err != nil {
		t.Fatal(err)
	}
	events := drain(t, e)
	last := events[len(events)-1]
	if last.Outcome == nil || last.Outcome.Reason != "interrupted" || !last.Outcome.IsError {
		t.Fatalf("interrupt: %+v", last)
	}
}
func TestStopBeforeSendAndValidation(t *testing.T) {
	if _, err := New("", Config{Cwd: t.TempDir()}); err == nil {
		t.Fatal("empty ID")
	}
	if _, err := New("id", Config{Cwd: t.TempDir(), ResumeID: "latest"}); err == nil {
		t.Fatal("implicit resume")
	}
	if _, err := New("id", Config{Cwd: t.TempDir(), Mode: "bypass"}); err == nil {
		t.Fatal("unsupported execution mode accepted")
	}
	if _, err := New("id", Config{Cwd: t.TempDir(), JSONSchema: "{"}); err == nil {
		t.Fatal("invalid JSON schema accepted")
	}
	e := helperExecution(t)
	e.Stop()
	e.Stop()
	if _, err := e.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("stop: %v", err)
	}
	if err := e.Send("late"); err == nil {
		t.Fatal("send after stop")
	}
}
func TestStatusesAndUnknownEvents(t *testing.T) {
	d := decoder{}
	for _, status := range []string{"SUCCESS", "ERROR", "CANCELED", "INTERRUPTED", "INVALID", "WAITING", "RUNNING", "future"} {
		event, err := d.parse([]byte(fmt.Sprintf(`{"event":"result","result":{"status":%q}}`, status)))
		if err != nil {
			t.Fatal(err)
		}
		if event.Outcome.IsError != (status != "SUCCESS") || event.Outcome.Usage != nil {
			t.Fatalf("status %s: %+v", status, event)
		}
	}
	event, err := d.parse([]byte(`{"event":"future","payload":42}`))
	if err != nil || event.Kind != providers.Other || event.Outcome != nil {
		t.Fatal("unknown event implied success")
	}
	if _, err := d.parse([]byte(`{"event":"result"}`)); err == nil {
		t.Fatal("missing result payload accepted")
	}
}
