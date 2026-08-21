package services

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// slowLocalProvider registers a provider whose generation blocks for delay before
// returning a valid context pack — or fails the moment the RUN context dies. That
// second half is the point: it lets a test tell "the run was killed" apart from
// "the run finished", which is exactly the distinction the request lifetime used
// to erase.
func slowLocalProvider(t *testing.T, svc *IntelligenceService, delay time.Duration) {
	t.Helper()
	if _, err := svc.providers.Upsert(LocalProvider{
		Name: "slow", Type: "ollama", BaseURL: "http://local.test", Model: "test-model",
		TimeoutMS: defaultProviderTimeoutMS, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	const pack = "TASK x FILES main.go SYMBOLS main CALL FLOW main LIKELY CHANGE POINTS none TESTS none UNCERTAINTIES none"
	svc.providers.httpClient = func(time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			select {
			case <-time.After(delay):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			body := `{"response":"` + pack + `","prompt_eval_count":100,"eval_count":25}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
	}
}

// writeCompressibleSource makes the repository big enough that a context pack is a
// real reduction, so hybrid runs reach dispatch instead of failing validation.
func writeCompressibleSource(t *testing.T, dir string) {
	t.Helper()
	source := "package main\n" + strings.Repeat("// repository context for hybrid preprocessing\n", 1000) + "func main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func waitForTrace(t *testing.T, svc *IntelligenceService, id string, accept func(IntelligenceTrace) bool) IntelligenceTrace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last IntelligenceTrace
	for time.Now().Before(deadline) {
		trace, err := svc.Trace(id)
		if err == nil {
			last = trace
			if accept(trace) {
				return trace
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("trace %s never reached the expected state: %#v", id, last)
	return last
}

func traceSettled(trace IntelligenceTrace) bool {
	switch trace.Status {
	case "SUCCESS", "FAILED", "CLOUD_DISPATCHED", "FALLBACK_CLOUD_DISPATCHED",
		"CLOUD_COMPLETED", "CLOUD_COMPLETED_WITH_FALLBACK":
		return true
	}
	return false
}

// The request context must have no say over the run. This is the direct cause of
// the five production traces that died at ~125s with "Post ...: context canceled":
// the browser (or a reverse proxy) hung up and took the local inference with it.
func TestStartSurvivesRequestCancellation(t *testing.T) {
	svc, driver, dir := intelligenceRunFixture(t)
	writeCompressibleSource(t, dir)
	slowLocalProvider(t, svc, 150*time.Millisecond)

	// Stands in for the HTTP request: created by the caller, cancelled while the
	// local model is still working.
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()

	started := time.Now()
	trace, err := svc.Start(IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "slow",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("Start blocked on the run for %s — it must return before local inference finishes", elapsed)
	}
	if trace.Status != "RUNNING" {
		t.Fatalf("status=%q, want RUNNING", trace.Status)
	}
	_ = requestCtx
	cancelRequest() // the tab closed

	final := waitForTrace(t, svc, trace.ID, traceSettled)
	if final.ErrorCode == ErrRequestCanceled {
		t.Fatalf("request cancellation killed the run: %#v", final)
	}
	if final.Fallback || final.Status != "CLOUD_DISPATCHED" {
		t.Fatalf("run did not complete on its own: %#v", final)
	}
	if sent := driver.sentTexts(); len(sent) != 1 || !strings.Contains(sent[0], "explain main") {
		t.Fatalf("optimized prompt did not reach the cloud: %q", sent)
	}
}

// Bad input must still be answered by the request that made it — a 400, not a
// trace the caller has to go read to learn it typed nothing.
func TestStartRejectsInvalidRequestSynchronously(t *testing.T) {
	svc, driver, _ := intelligenceRunFixture(t)
	cases := []struct {
		name string
		req  IntelligenceRunRequest
	}{
		{"empty task", IntelligenceRunRequest{AgentID: "a1", Task: "  ", Mode: ModeLocalPreprocessCloud, Provider: "slow"}},
		{"missing agent", IntelligenceRunRequest{AgentID: "", Task: "explain main", Mode: ModeLocalPreprocessCloud}},
		{"unknown agent", IntelligenceRunRequest{AgentID: "nope", Task: "explain main", Mode: ModeLocalPreprocessCloud}},
		{"unknown mode", IntelligenceRunRequest{AgentID: "a1", Task: "explain main", Mode: "TELEPATHY"}},
		{"local-only operation off the allow-list", IntelligenceRunRequest{
			AgentID: "a1", Task: "rewrite main", Mode: ModeLocalOnly, Provider: "slow", Operation: "refactor",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace, err := svc.Start(tc.req)
			if err == nil {
				t.Fatalf("invalid request was accepted: %#v", trace)
			}
			if trace.Status == "RUNNING" {
				t.Fatalf("rejected request was reported as running: %#v", trace)
			}
		})
	}
	if sent := driver.sentTexts(); len(sent) != 0 {
		t.Fatalf("invalid request reached the cloud: %q", sent)
	}
}

// A native session that is not ready is refused before any local work, and it is
// refused synchronously — the caller asked for a hybrid run and there is nothing
// to run it into.
func TestStartRejectsUnpreparedNativeSessionSynchronously(t *testing.T) {
	svc, _, _ := intelligenceRunFixture(t)
	delete(svc.native.sessions, "a1")
	trace, err := svc.Start(IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "slow",
	})
	if err == nil {
		t.Fatal("hybrid run started without a native session")
	}
	if trace.ErrorCode != ErrNativeSession {
		t.Fatalf("errorCode=%q, want %s", trace.ErrorCode, ErrNativeSession)
	}
}

// Cancelling is now a user's decision, not a browser accident — so it must NOT
// spend cloud tokens on a fallback the user just told us to stop.
func TestCancelEndsRunWithoutCloudFallback(t *testing.T) {
	svc, driver, dir := intelligenceRunFixture(t)
	writeCompressibleSource(t, dir)
	slowLocalProvider(t, svc, 30*time.Second)

	trace, err := svc.Start(IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "slow",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTrace(t, svc, trace.ID, func(tr IntelligenceTrace) bool {
		for _, ev := range tr.Events {
			if ev.Stage == "local_request" && ev.Status == "STARTED" {
				return true
			}
		}
		return false
	})
	if !svc.Cancel(trace.ID) {
		t.Fatal("Cancel did not find the running trace")
	}

	final := waitForTrace(t, svc, trace.ID, traceSettled)
	if final.Status != "FAILED" || final.ErrorCode != ErrRequestCanceled {
		t.Fatalf("cancel did not end the run as a cancellation: %#v", final)
	}
	if final.Fallback {
		t.Fatalf("cancelled run fell back to the cloud anyway: %#v", final)
	}
	if sent := driver.sentTexts(); len(sent) != 0 {
		t.Fatalf("cancelled run still spent a cloud turn: %q", sent)
	}
	if svc.Cancel(trace.ID) {
		t.Fatal("a finished trace is still cancellable — the registry leaks")
	}
}

// Provider timeouts must survive the split. Separating the run from the request
// removes one deadline; it must not remove the deadline that protects us from a
// local model that never answers.
func TestStartStillHonorsLocalTimeout(t *testing.T) {
	svc, driver, dir := intelligenceRunFixture(t)
	writeCompressibleSource(t, dir)
	slowLocalProvider(t, svc, 30*time.Second)
	svc.hybridTimeout = 20 * time.Millisecond

	trace, err := svc.Start(IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "slow",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitForTrace(t, svc, trace.ID, traceSettled)
	if final.ErrorCode != ErrLocalTimeout || !final.Fallback {
		t.Fatalf("local timeout no longer falls back to the cloud: %#v", final)
	}
	if sent := driver.sentTexts(); len(sent) != 1 || sent[0] != "explain main" {
		t.Fatalf("timeout fallback did not preserve the original task: %q", sent)
	}
}

// Every state transition is broadcast, and the terminal emission carries the
// payload that is deliberately never written to the database (the context pack).
// Without that, LOCAL_ONLY would have no way to show its own result.
func TestStartEmitsProgressAndTerminalResult(t *testing.T) {
	svc, _, dir := intelligenceRunFixture(t)
	writeCompressibleSource(t, dir)
	slowLocalProvider(t, svc, 10*time.Millisecond)

	emissions := make(chan IntelligenceRunResult, 16)
	svc.SetEmitter(func(r IntelligenceRunResult) { emissions <- r })

	trace, err := svc.Start(IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalOnly, Provider: "slow", Operation: "explain",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTrace(t, svc, trace.ID, traceSettled)

	var sawRunning, sawPack bool
	timeout := time.After(2 * time.Second)
	for !sawRunning || !sawPack {
		select {
		case r := <-emissions:
			if r.Trace.ID != trace.ID {
				t.Fatalf("emitted a foreign trace: %#v", r.Trace)
			}
			if r.Trace.Status == "RUNNING" {
				sawRunning = true
			}
			// SUCCESS is announced twice: once by the persistence hook (trace only)
			// and once by the job when it finishes (trace + the extras that are
			// never stored). Only the second one can carry the pack.
			if r.Trace.Status == "SUCCESS" && r.ContextPack != "" {
				if !validContextPack(r.ContextPack) {
					t.Fatalf("terminal emission carried an invalid context pack: %q", r.ContextPack)
				}
				sawPack = true
			}
		case <-timeout:
			t.Fatalf("missing emissions: running=%v terminalPack=%v", sawRunning, sawPack)
		}
	}
}
