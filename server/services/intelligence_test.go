package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func intelligenceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE local_ai_providers (
		name TEXT PRIMARY KEY,type TEXT,base_url TEXT,model TEXT,timeout_ms INTEGER,
		enabled BOOLEAN,updated_at TEXT DEFAULT (datetime('now')))`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func intelligenceRunTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := intelligenceTestDB(t)
	_, err := db.Exec(`CREATE TABLE agents (
		id TEXT PRIMARY KEY,preset TEXT,name TEXT,tmux_session TEXT,working_dir TEXT,
		command TEXT,args TEXT,status TEXT,color_hue INTEGER,color_name TEXT,
		created_at TEXT,updated_at TEXT);
	CREATE TABLE intelligence_traces (
		id TEXT PRIMARY KEY,agent_id TEXT,mode TEXT,status TEXT,provider TEXT,model TEXT,
		raw_tokens INTEGER,optimized_tokens INTEGER,local_tokens INTEGER,latency_ms INTEGER,
		error_code TEXT,fallback BOOLEAN,events_json TEXT,created_at TEXT,updated_at TEXT,
		cloud_cost_usd REAL DEFAULT 0,cloud_input_tokens INTEGER DEFAULT 0,
		cloud_output_tokens INTEGER DEFAULT 0,cloud_cache_read_tokens INTEGER DEFAULT 0,
		cloud_cache_creation_tokens INTEGER DEFAULT 0,cloud_tool_calls INTEGER DEFAULT 0,
		cloud_usage_known BOOLEAN DEFAULT FALSE)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

type intelligenceFakeDriver struct {
	// mu guards sent: runs are goroutines now, so a test that asserts on what
	// reached the cloud reads this from a different goroutine than the one writing it.
	mu      sync.Mutex
	sent    []string
	events  chan *StreamEvent
	sendErr error
}

func (d *intelligenceFakeDriver) sentTexts() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sent...)
}

func (d *intelligenceFakeDriver) Start() error                { return nil }
func (d *intelligenceFakeDriver) Events() <-chan *StreamEvent { return d.events }
func (d *intelligenceFakeDriver) Send(s string) error {
	d.mu.Lock()
	d.sent = append(d.sent, s)
	d.mu.Unlock()
	return d.sendErr
}
func (d *intelligenceFakeDriver) Interrupt() error               { return nil }
func (d *intelligenceFakeDriver) ConversationID() string         { return "fake" }
func (d *intelligenceFakeDriver) Stop()                          {}
func (d *intelligenceFakeDriver) SetPermissionMode(string) error { return nil }

func intelligenceRunFixture(t *testing.T) (*IntelligenceService, *intelligenceFakeDriver, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "-C", dir, "add", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	db := intelligenceRunTestDB(t)
	_, err := db.Exec(`INSERT INTO agents VALUES('a1','codex-cli','n','pcd-a1',?,'codex','[]','running',220,'blue','','')`, dir)
	if err != nil {
		t.Fatal(err)
	}
	agents := NewAgentService(db, nil)
	driver := &intelligenceFakeDriver{events: make(chan *StreamEvent)}
	native := NewNativeService("http://127.0.0.1:0")
	native.sessions["a1"] = &nativeSession{id: "a1", kind: "codex", cwd: dir, driver: driver}
	registry := NewProviderRegistry(db)
	return NewIntelligenceService(db, registry, agents, native), driver, dir
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderDefaultsToLongGenerationTimeout(t *testing.T) {
	p, err := validateProvider(LocalProvider{
		Name: "local", Type: "ollama", BaseURL: "http://local.test", Model: "coder", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.TimeoutMS != 180000 {
		t.Fatalf("default timeout = %d, want 180000", p.TimeoutMS)
	}
}

func TestOllamaGenerateBoundsContextAndKeepsModelWarm(t *testing.T) {
	r := NewProviderRegistry(intelligenceTestDB(t))
	var payload struct {
		Stream    bool   `json:"stream"`
		KeepAlive string `json:"keep_alive"`
		Options   struct {
			NumCtx     int `json:"num_ctx"`
			NumPredict int `json:"num_predict"`
		} `json:"options"`
	}
	r.httpClient = func(timeout time.Duration) *http.Client {
		if timeout != 2*time.Second {
			t.Fatalf("HTTP timeout = %s, want 2s", timeout)
		}
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"response":"OK","prompt_eval_count":4,"eval_count":1}`)), Header: make(http.Header)}, nil
		})}
	}
	response, _, err := r.ollamaGenerate(context.Background(), LocalProvider{
		BaseURL: "http://local.test", Model: "coder", TimeoutMS: 2000,
	}, "probe", 777)
	if err != nil || response != "OK" {
		t.Fatalf("generation = %q, %v", response, err)
	}
	if payload.Stream || payload.KeepAlive != "30m" {
		t.Fatalf("unexpected transport options: %#v", payload)
	}
	if payload.Options.NumCtx != 65536 || payload.Options.NumPredict != 777 {
		t.Fatalf("unexpected generation options: %#v", payload.Options)
	}
}

func TestProviderHealthProvesAllOllamaStages(t *testing.T) {
	r := NewProviderRegistry(intelligenceTestDB(t))
	if _, err := r.Upsert(LocalProvider{Name: "remote", Type: "ollama", BaseURL: "http://100.64.0.8:11434", Model: "qwen-coder", TimeoutMS: 1000, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r.dial = func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go b.Close()
		return a, nil
	}
	r.httpClient = func(time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"models":[{"name":"qwen-coder:latest"}]}`
			if strings.HasSuffix(req.URL.Path, "/api/generate") {
				body = `{"response":"OK","prompt_eval_count":4,"eval_count":1}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
	}
	h := r.Health(context.Background(), "remote")
	if !h.Reachable || !h.APIHealthy || !h.ModelAvailable || !h.GenerationTest {
		t.Fatalf("health stages were not independently proven: %#v", h)
	}
}

func TestProviderHealthDistinguishesMissingModel(t *testing.T) {
	r := NewProviderRegistry(intelligenceTestDB(t))
	_, _ = r.Upsert(LocalProvider{Name: "remote", Type: "ollama", BaseURL: "http://host:11434", Model: "missing", TimeoutMS: 1000, Enabled: true})
	r.dial = func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go b.Close()
		return a, nil
	}
	r.httpClient = func(time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"models":[{"name":"other:latest"}]}`)), Header: make(http.Header)}, nil
		})}
	}
	h := r.Health(context.Background(), "remote")
	if !h.Reachable || !h.APIHealthy || h.ModelAvailable || h.GenerationTest || h.ErrorCode != ErrModelUnavailable {
		t.Fatalf("missing model was flattened into the wrong state: %#v", h)
	}
}

func TestBuildCandidateContextUsesRealGitRepositoryData(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "PCD Test")
	if err := os.WriteFile(filepath.Join(dir, "token_service.go"), []byte("package token\n\nfunc RefreshToken() string { return \"fresh\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token_service_test.go"), []byte("package token\n\nfunc TestRefreshToken() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "add token service")

	c, err := BuildCandidateContext(context.Background(), dir, "fix refresh token")
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "git" || c.EstimatedTokens <= 0 || !strings.Contains(c.Text, "RefreshToken") || len(c.Files) == 0 {
		t.Fatalf("candidate context did not come from repository content: %#v", c)
	}
}

func TestBuildCandidateContextFallsBackToSafeFilesystemScan(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"meetjul"}`)
	write("src/app/main.ts", "export function MeetjulMain() { return 'safe source' }\n")
	write("node_modules/dependency/index.js", "DO_NOT_INCLUDE_DEPENDENCY")
	write(".env", "SECRET_TOKEN=do-not-include")
	write("credentials.json", `{"token":"do-not-include"}`)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("DO_NOT_FOLLOW_SYMLINK"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}

	c, err := BuildCandidateContext(context.Background(), dir, "explain MeetjulMain")
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "filesystem" || !strings.Contains(c.Text, "MeetjulMain") || !strings.Contains(c.Text, "package.json") {
		t.Fatalf("filesystem context missing project source: %#v\n%s", c, c.Text)
	}
	for _, forbidden := range []string{"DO_NOT_INCLUDE_DEPENDENCY", "SECRET_TOKEN", "do-not-include", "DO_NOT_FOLLOW_SYMLINK"} {
		if strings.Contains(c.Text, forbidden) {
			t.Fatalf("unsafe filesystem content was included: %s", forbidden)
		}
	}
}

func TestBuildCandidateContextHonorsLocalInferenceBudget(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	for i := 0; i < 12; i++ {
		name := filepath.Join(dir, fmt.Sprintf("source-%02d.go", i))
		if err := os.WriteFile(name, []byte("package source\n"+strings.Repeat("// bounded repository evidence\n", 2000)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")

	c, err := BuildCandidateContext(context.Background(), dir, "inspect repository evidence")
	if err != nil {
		t.Fatal(err)
	}
	if c.Bytes > maxContextBytes {
		t.Fatalf("candidate context exceeded local inference budget: %d > %d", c.Bytes, maxContextBytes)
	}
	if c.Bytes < maxContextBytes/2 || len(c.Files) < 2 {
		t.Fatalf("candidate context was bounded too aggressively: bytes=%d files=%d", c.Bytes, len(c.Files))
	}
}

func TestEstimatedTokenMeasurementAndPackValidation(t *testing.T) {
	if got := EstimateTokens("12345"); got != 2 {
		t.Fatalf("EstimateTokens = %d, want 2", got)
	}
	if EstimateTokens(strings.Repeat("x", maxContextBytes))+contextPackTokens >= ollamaContextTokens {
		t.Fatal("candidate context leaves no room for instructions and generated output")
	}
	pack := "TASK\nx\nFILES\na\nSYMBOLS\nb\nCALL FLOW\nc\nLIKELY CHANGE POINTS\nd\nTESTS\ne\nUNCERTAINTIES\nf"
	if !validContextPack(pack) {
		t.Fatal("valid structured pack rejected")
	}
	if validContextPack("TASK only") {
		t.Fatal("unstructured pack accepted")
	}
}

func TestCloudOnlyPreservesPromptExactly(t *testing.T) {
	svc, driver, _ := intelligenceRunFixture(t)
	result, err := svc.Run(context.Background(), IntelligenceRunRequest{AgentID: "a1", Task: "original task", Mode: ModeCloudOnly})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Dispatched || len(driver.sent) != 1 || driver.sent[0] != "original task" {
		t.Fatalf("CLOUD_ONLY changed the baseline prompt: %#v sent=%q", result, driver.sent)
	}
}

func TestHybridRejectsBeforeLocalWorkWhenNativeSessionIsNotReady(t *testing.T) {
	svc, driver, _ := intelligenceRunFixture(t)
	delete(svc.native.sessions, "a1")
	result, err := svc.Run(context.Background(), IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "missing",
	})
	if err == nil || result.Trace.ErrorCode != ErrNativeSession || result.Trace.Fallback {
		t.Fatalf("native readiness failure was not explicit: result=%#v err=%v", result.Trace, err)
	}
	if len(driver.sent) != 0 {
		t.Fatalf("task was sent without a native session: %q", driver.sent)
	}
	if len(result.Trace.Events) < 2 || result.Trace.Events[1].Stage != "cloud_execution" || result.Trace.Events[1].Details["errorCode"] != ErrNativeSession {
		t.Fatalf("native readiness trace is incomplete: %#v", result.Trace.Events)
	}
}

func TestNativeSendWithDisplayTextRoutesOnceAndPreservesUserTask(t *testing.T) {
	_, driver, _ := intelligenceRunFixture(t)
	native := NewNativeService("http://127.0.0.1:0")
	native.sessions["a1"] = &nativeSession{id: "a1", kind: "claude", driver: driver}

	if err := native.SendWithDisplayText("a1", "optimized transport prompt", "original user task"); err != nil {
		t.Fatal(err)
	}
	if len(driver.sent) != 1 || driver.sent[0] != "optimized transport prompt" {
		t.Fatalf("driver received %q, want one optimized prompt", driver.sent)
	}
	history := native.History("a1")
	if len(history) != 1 || history[0].Message == nil || len(history[0].Message.Content) != 1 || history[0].Message.Content[0].Text != "original user task" {
		t.Fatalf("chat history did not preserve the original task: %#v", history)
	}
}

func TestHybridRoutesOptimizedPromptOnceAndDisplaysOriginalTask(t *testing.T) {
	svc, driver, dir := intelligenceRunFixture(t)
	largeSource := "package main\n" + strings.Repeat("// repository context for hybrid preprocessing\n", 1000) + "func main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(largeSource), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.providers.Upsert(LocalProvider{
		Name: "local", Type: "ollama", BaseURL: "http://local.test", Model: "test-model", TimeoutMS: 1000, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	pack := "TASK x FILES main.go SYMBOLS main CALL FLOW main LIKELY CHANGE POINTS none TESTS none UNCERTAINTIES none"
	svc.providers.httpClient = func(time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			body := `{"response":"` + pack + `","prompt_eval_count":100,"eval_count":25}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
	}

	const task = "explain the repository entry point"
	result, err := svc.Run(context.Background(), IntelligenceRunRequest{
		AgentID: "a1", Task: task, Mode: ModeLocalPreprocessCloud, Provider: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Dispatched || result.Trace.Fallback || result.Trace.OptimizedTokens >= result.Trace.RawTokens {
		t.Fatalf("hybrid preprocessing was not validated: %#v", result.Trace)
	}
	if len(driver.sent) != 1 || !strings.Contains(driver.sent[0], pack) || !strings.Contains(driver.sent[0], task) {
		t.Fatalf("driver did not receive exactly one optimized advisory prompt: %q", driver.sent)
	}
	history := svc.native.History("a1")
	if len(history) != 1 || history[0].Message == nil || history[0].Message.Content[0].Text != task {
		t.Fatalf("hybrid chat history exposed the transport prompt: %#v", history)
	}
}

func TestHybridLocalFailureRecordsFallbackAndContinuesCloud(t *testing.T) {
	svc, driver, _ := intelligenceRunFixture(t)
	result, err := svc.Run(context.Background(), IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "missing-provider",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Dispatched || !result.Trace.Fallback || result.Trace.ErrorCode != ErrProviderUnreachable {
		t.Fatalf("fallback was hidden or cloud did not continue: %#v", result.Trace)
	}
	if len(driver.sent) != 1 || driver.sent[0] != "explain main" {
		t.Fatalf("fallback did not preserve the original cloud task: %q", driver.sent)
	}
}

func TestHybridLocalDeadlineFallsBackBeforeTransportDeadline(t *testing.T) {
	svc, driver, _ := intelligenceRunFixture(t)
	svc.hybridTimeout = 15 * time.Millisecond
	if _, err := svc.providers.Upsert(LocalProvider{
		Name: "slow", Type: "ollama", BaseURL: "http://local.test", Model: "test-model",
		TimeoutMS: defaultProviderTimeoutMS, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc.providers.httpClient = func(time.Duration) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}
	}

	started := time.Now()
	result, err := svc.Run(context.Background(), IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "slow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("hybrid fallback exceeded its local deadline: %s", elapsed)
	}
	if !result.Dispatched || !result.Trace.Fallback || result.Trace.ErrorCode != ErrLocalTimeout {
		t.Fatalf("hybrid deadline did not dispatch cloud fallback: %#v", result.Trace)
	}
	if len(driver.sent) != 1 || driver.sent[0] != "explain main" {
		t.Fatalf("deadline fallback did not preserve task: %q", driver.sent)
	}
	var timeoutMS any
	for _, event := range result.Trace.Events {
		if event.Stage == "local_request" {
			timeoutMS = event.Details["timeoutMs"]
		}
	}
	if timeoutMS != int64(15) {
		t.Fatalf("effective Hybrid timeout missing from trace: %#v", result.Trace.Events)
	}
}

func TestClassifyRequestCancellationSeparatelyFromProviderReachability(t *testing.T) {
	if got := classifyLocalError(context.Canceled); got != ErrRequestCanceled {
		t.Fatalf("context cancellation classified as %q, want %q", got, ErrRequestCanceled)
	}
}

func TestHybridPreservesLocalAndCloudFallbackErrors(t *testing.T) {
	svc, driver, _ := intelligenceRunFixture(t)
	driver.sendErr = errors.New("native driver unavailable")
	result, err := svc.Run(context.Background(), IntelligenceRunRequest{
		AgentID: "a1", Task: "explain main", Mode: ModeLocalPreprocessCloud, Provider: "missing-provider",
	})
	if err == nil || !result.Trace.Fallback || result.Trace.ErrorCode != ErrCloudExecution {
		t.Fatalf("cloud fallback failure was not recorded: result=%#v err=%v", result.Trace, err)
	}
	var localCode, cloudCode any
	for _, event := range result.Trace.Events {
		if event.Stage == "local_processing" {
			localCode = event.Details["errorCode"]
		}
		if event.Stage == "cloud_execution" && event.Status == "FAILED" {
			cloudCode = event.Details["errorCode"]
		}
	}
	if localCode != ErrProviderUnreachable || cloudCode != ErrCloudExecution {
		t.Fatalf("local/cloud errors were not preserved: local=%v cloud=%v events=%#v", localCode, cloudCode, result.Trace.Events)
	}
}

// Opt-in evidence run against a real checkout. It intentionally stops before
// local inference; a remote provider must supply the optimized half honestly.
func TestLiveRepositoryCandidateMeasurement(t *testing.T) {
	dir := os.Getenv("PCD_POC_REPO")
	if dir == "" {
		t.Skip("set PCD_POC_REPO to measure a real repository")
	}
	c, err := BuildCandidateContext(context.Background(), dir, "audit Codex execution and build the Local Intelligence context reduction POC")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("repository=%s candidateFiles=%d rawBytes=%d rawEstimatedTokens=%d", dir, len(c.Files), c.Bytes, c.EstimatedTokens)
	if c.EstimatedTokens == 0 || len(c.Files) == 0 {
		t.Fatal("real repository produced no candidate context")
	}
}
