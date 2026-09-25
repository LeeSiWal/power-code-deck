package runroute

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"powercodedeck/internal/orchestration"
	"powercodedeck/internal/providers"
	"powercodedeck/internal/routing"
)

// ---- fake CLIs ----

type probeRunner struct{ out map[string]string }

func (p probeRunner) LookPath(n string) (string, error) {
	for k := range p.out {
		if strings.HasPrefix(k, n+" ") {
			return "/fake/" + n, nil
		}
	}
	return "", errors.New("not found")
}
func (p probeRunner) Run(_ context.Context, bin string, args []string, _ string) (string, error) {
	if o, ok := p.out[filepath.Base(bin)+" "+strings.Join(args, " ")]; ok {
		return o, nil
	}
	return "", errors.New("no fixture")
}

func allCLIs() map[string]string {
	return map[string]string{
		"claude --version":   "2.1.239 (Claude Code)",
		"claude auth status": `{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty"}`,
		"codex --version":    "codex-cli 0.154.0",
		"codex login status": "Logged in using ChatGPT",
		"codex app-server":   `{"id":2,"result":{"data":[{"id":"gpt-5.6-luna","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]},{"id":"gpt-5.6-sol","supportedReasoningEfforts":[{"reasoningEffort":"xhigh"}]}]}}`,
		"agy --version":      "1.1.28",
		"agy --help":         "--print --output-format",
		"agy models":         "gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)",
	}
}

// step is what one scripted attempt does.
type step struct {
	edit     func(cwd string)
	status   providers.CompletionStatus
	text     string
	block    bool
	denied   bool
	sawFiles map[string]string
}

type script struct {
	mu      sync.Mutex
	steps   map[string][]*step // by provider
	prompts []string
	launch  []orchestration.Launch
}

type fakeExec struct {
	id, cwd  string
	provider providers.ID
	model    string
	s        *step
	sc       *script
}

func (e *fakeExec) Identity() providers.Identity {
	return providers.Identity{ExecutionID: e.id, Provider: e.provider}
}
func (e *fakeExec) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (e *fakeExec) Start() error                         { return nil }
func (e *fakeExec) Send(p string) error {
	e.sc.mu.Lock()
	e.sc.prompts = append(e.sc.prompts, p)
	e.sc.mu.Unlock()
	if e.s.sawFiles != nil {
		for name := range e.s.sawFiles {
			b, _ := os.ReadFile(filepath.Join(e.cwd, name))
			e.s.sawFiles[name] = string(b)
		}
	}
	if e.s.edit != nil {
		e.s.edit(e.cwd)
	}
	return nil
}
func (e *fakeExec) Next(ctx context.Context) (providers.Event, error) {
	if e.s.block {
		<-ctx.Done()
		return providers.Event{}, ctx.Err()
	}
	st := e.s.status
	if st == "" {
		st = providers.CompletionSuccess
	}
	out := &providers.Outcome{Status: st, Text: e.s.text, IsError: st != providers.CompletionSuccess}
	if e.s.denied {
		out.Denials = []providers.DeniedTool{{ToolName: "Bash"}}
	}
	return providers.Event{Identity: e.Identity(), Kind: providers.TurnFinished, Model: e.model, Outcome: out}, nil
}
func (e *fakeExec) Interrupt() error               { return nil }
func (e *fakeExec) Stop()                          {}
func (e *fakeExec) ConversationID() string         { return "conv-" + e.id }
func (e *fakeExec) SetPermissionMode(string) error { return nil }

type harness struct {
	t      *testing.T
	runs   *orchestration.Store
	worker *orchestration.Worker
	coord  *Coordinator
	sc     *script
	repo   string
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func newHarness(t *testing.T, cfg routing.Config, cli map[string]string) *harness {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	os.WriteFile(filepath.Join(repo, "app.txt"), []byte("bug\n"), 0600)
	os.MkdirAll(filepath.Join(repo, ".powercodedeck"), 0700)
	os.WriteFile(filepath.Join(repo, ".powercodedeck", "checks.json"), []byte(`{"version":1,"checks":[{"name":"unit","argv":["grep","-q","fixed","app.txt"],"timeoutSeconds":30}]}`), 0600)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "-c", "commit.gpgsign=false", "commit", "-qm", "base")
	repo, _ = filepath.EvalSymlinks(repo)

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db")+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	runs, err := orchestration.New(db)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := routing.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	w, err := orchestration.NewWorker(runs, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sc := &script{steps: map[string][]*step{}}
	w.SetLaunchFactory(func(id, cwd string, l orchestration.Launch) (providers.Execution, error) {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		sc.launch = append(sc.launch, l)
		q := sc.steps[l.Provider]
		if len(q) == 0 {
			return nil, errors.New("unexpected launch of " + l.Provider)
		}
		s := q[0]
		sc.steps[l.Provider] = q[1:]
		return &fakeExec{id: id, cwd: cwd, provider: providers.ID(l.Provider), model: l.Model, s: s, sc: sc}, nil
	})
	w.SetReviewer(func(id, cwd string) (providers.Execution, error) {
		return &fakeExec{id: id, cwd: cwd, provider: providers.Antigravity, s: &step{text: `{"verdict":"pass","summary":"ok"}`}, sc: &script{}}, nil
	})
	pol, _ := routing.LoadPolicyEvidence()
	orchestration.SensitivePath = routing.IsSensitivePath
	prober := &routing.Prober{Runner: probeRunner{cli}, Getenv: func(string) string { return "" }}
	coord, err := New(Options{Config: cfg, Store: rs, Runs: runs, Worker: w, Prober: prober, Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	w.SetAttemptObserver(coord.OnAttempt)
	return &harness{t: t, runs: runs, worker: w, coord: coord, sc: sc, repo: repo}
}

func (h *harness) queue(provider string, s *step) {
	h.sc.steps[provider] = append(h.sc.steps[provider], s)
}

func (h *harness) waitPhase(run string, want ...routing.Phase) routing.Timeline {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tl, _ := h.coord.Timeline(run)
		for _, p := range want {
			if tl.State.Phase == p && !h.worker.Busy() {
				return tl
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	tl, _ := h.coord.Timeline(run)
	h.t.Fatalf("phase %s, want %v; transitions %+v", tl.State.Phase, want, tl.Transitions)
	return tl
}

func autoConfig() routing.Config {
	c := routing.DefaultConfig()
	c.Mode = routing.ModeAuto
	c.Switching = routing.SwitchPolicy{MaxSwitchesPerRun: 2, MaxAttemptsPerRun: 3, MaxRunMinutes: 30, AutoEscalate: true, Stickiness: 0.5}
	c.Profiles = []routing.Profile{
		{ID: "codex-fast", Adapter: "codex", Model: "gpt-5.6-luna", Effort: "medium", Billing: routing.BillingSubscription, Tiers: []routing.Tier{routing.Easy, routing.Medium}, Allow: true, AccountRef: "me"},
		{ID: "claude-opus", Adapter: "claude", Model: "opus", Effort: "high", Billing: routing.BillingSubscription, Tiers: []routing.Tier{routing.High, routing.Ultra}, Allow: true, AccountRef: "me"},
		{ID: "agy-flash", Adapter: "antigravity", Model: "gemini-3.8-flash-medium", Billing: routing.BillingSubscription, Tiers: []routing.Tier{routing.Medium}, Allow: true},
	}
	return c
}

func fix(cwd string)     { os.WriteFile(filepath.Join(cwd, "app.txt"), []byte("fixed\n"), 0600) }
func partial(cwd string) { os.WriteFile(filepath.Join(cwd, "helper.txt"), []byte("half done\n"), 0600) }

// Tests 12, 13, 23: auto picks the cheapest adequate profile, escalates on
// failed checks with the previous edits and a handoff, then succeeds.
func TestAutoEscalationCarriesWorkAndSucceeds(t *testing.T) {
	h := newHarness(t, autoConfig(), allCLIs())
	run, _ := h.runs.Create("k1", h.repo, "app.txt의 버그를 고쳐줘. 반드시 helper.txt는 유지해.", "codex")
	h.queue("codex", &step{edit: partial, text: "I fixed it (not really)"})
	saw := &step{edit: fix, sawFiles: map[string]string{"helper.txt": ""}}
	h.queue("claude", saw)
	res, err := h.coord.Start(context.Background(), run.ID, "")
	if err != nil || res.Launched != "codex-fast" {
		t.Fatalf("start %+v %v", res, err)
	}
	tl := h.waitPhase(run.ID, routing.PhaseSucceeded)
	if len(tl.Attempts) != 2 || tl.Attempts[0].Class != routing.QualityFailure || tl.Attempts[1].ProfileID != "claude-opus" || tl.Attempts[1].InheritedFrom != tl.Attempts[0].ExecutionID {
		t.Fatalf("attempts %+v", tl.Attempts)
	}
	if tl.State.Switches != 1 || tl.State.Attempts != 2 {
		t.Fatalf("state %+v", tl.State)
	}
	if saw.sawFiles["helper.txt"] != "half done\n" {
		t.Fatal("escalated provider did not inherit the previous edits")
	}
	handoff := h.sc.prompts[1]
	for _, want := range []string{"app.txt의 버그를 고쳐줘. 반드시 helper.txt는 유지해.", "unit: FAILED", "added helper.txt", "untrusted"} {
		if !strings.Contains(handoff, want) {
			t.Fatalf("handoff missing %q:\n%s", want, handoff)
		}
	}
	if h.sc.launch[0].Model != "gpt-5.6-luna" || h.sc.launch[0].Effort != "medium" || h.sc.launch[1].Model != "opus" {
		t.Fatalf("launches %+v", h.sc.launch)
	}
	if r := tl.Attempts[0].Report; r == nil || r.ObservedModel != "gpt-5.6-luna" || r.Timings.ExecMS < 0 {
		t.Fatalf("report %+v", r)
	}
	if len(tl.Decisions) != 2 || tl.Decisions[1].Stage != "escalation" || tl.Decisions[1].RuleTier != routing.High {
		t.Fatalf("decisions %+v", tl.Decisions)
	}
	if len(tl.Bindings) != 2 || tl.Bindings[0].Resumable {
		t.Fatalf("bindings %+v", tl.Bindings)
	}
	// Terminal: nothing more may start.
	if _, err := h.coord.Start(context.Background(), run.ID, ""); err == nil {
		t.Fatal("succeeded run restarted")
	}
}

// Test 23 (limits) & pinning: without AutoEscalate, or when pinned, a quality
// failure waits for the user.
func TestNoAutoContinuationWhenDisabledOrPinned(t *testing.T) {
	cfg := autoConfig()
	cfg.Switching.AutoEscalate = false
	h := newHarness(t, cfg, allCLIs())
	run, _ := h.runs.Create("k2", h.repo, "fix app.txt", "codex")
	h.queue("codex", &step{edit: partial})
	if _, err := h.coord.Start(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	tl := h.waitPhase(run.ID, routing.PhaseWaitUser)
	if len(tl.Attempts) != 1 {
		t.Fatal("continued without permission")
	}
	// Pinned to the codex provider: the escalation needs explicit confirmation.
	h2 := newHarness(t, autoConfig(), allCLIs())
	run2, _ := h2.runs.Create("k3", h2.repo, "fix app.txt", "codex")
	h2.coord.Settings(run2.ID, routing.ModeAuto, "", "codex")
	h2.queue("codex", &step{edit: partial})
	h2.coord.Start(context.Background(), run2.ID, "")
	tl = h2.waitPhase(run2.ID, routing.PhaseWaitUser)
	if len(tl.Attempts) != 1 || !strings.Contains(tl.Transitions[len(tl.Transitions)-1].Detail, "pinned") {
		t.Fatalf("pinned run switched: %+v", tl.Transitions)
	}
}

// Test 24: a user cancel is never resumed or escalated.
func TestCancelNeverRestarts(t *testing.T) {
	h := newHarness(t, autoConfig(), allCLIs())
	run, _ := h.runs.Create("k4", h.repo, "fix app.txt", "codex")
	h.queue("codex", &step{block: true})
	if _, err := h.coord.Start(context.Background(), run.ID, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := h.worker.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	tl := h.waitPhase(run.ID, routing.PhaseCanceled, routing.PhaseReconcile)
	if len(tl.Attempts) != 1 || tl.Attempts[0].Class != routing.UserCanceled {
		t.Fatalf("attempts %+v", tl.Attempts)
	}
	if _, err := h.coord.Start(context.Background(), run.ID, ""); err == nil {
		t.Fatal("canceled run restarted")
	}
}

// Test 22: a 429 moves to another allowed bucket; the exhausted bucket is
// excluded until its cooldown ends.
func TestAvailabilityFailover(t *testing.T) {
	cfg := autoConfig()
	cfg.Profiles = append(cfg.Profiles, routing.Profile{ID: "codex-deep", Adapter: "codex", Model: "gpt-5.6-sol", Effort: "xhigh", Billing: routing.BillingSubscription, Tiers: []routing.Tier{routing.High}, Allow: true, AccountRef: "me"})
	h := newHarness(t, cfg, allCLIs())
	run, _ := h.runs.Create("k5", h.repo, "fix app.txt", "codex")
	h.queue("codex", &step{status: providers.CompletionFailed, text: "429 Too Many Requests; Retry-After: 120"})
	h.queue("claude", &step{edit: fix})
	h.coord.Start(context.Background(), run.ID, "")
	tl := h.waitPhase(run.ID, routing.PhaseSucceeded)
	if tl.Attempts[0].Class != routing.AvailabilityFail || tl.Attempts[1].Adapter != "claude" {
		t.Fatalf("attempts %+v", tl.Attempts)
	}
	// The second decision excluded both codex profiles (shared bucket).
	d := tl.Decisions[1]
	for _, cnd := range d.Candidates {
		if cnd.Profile.Adapter == "codex" && (len(cnd.Excluded) == 0 || !hasExcl(cnd, routing.RRateLimited)) {
			t.Fatalf("codex profile %s not cooled: %+v", cnd.Profile.ID, cnd.Excluded)
		}
	}
}

func hasExcl(c routing.Candidate, r routing.Reason) bool {
	for _, e := range c.Excluded {
		if e.Reason == r {
			return true
		}
	}
	return false
}

// Permission denials stop; environment errors block; neither escalates.
func TestPermissionAndEnvironmentStop(t *testing.T) {
	h := newHarness(t, autoConfig(), allCLIs())
	run, _ := h.runs.Create("k6", h.repo, "fix app.txt", "codex")
	h.queue("codex", &step{denied: true, text: "done"})
	h.coord.Start(context.Background(), run.ID, "")
	tl := h.waitPhase(run.ID, routing.PhaseFailed)
	if tl.Attempts[0].Class != routing.PermissionFail || len(tl.Attempts) != 1 {
		t.Fatalf("%+v", tl.Attempts)
	}
	// A dirty source checkout is an environment problem, not a model problem.
	h2 := newHarness(t, autoConfig(), allCLIs())
	run2, _ := h2.runs.Create("k7", h2.repo, "fix app.txt", "codex")
	os.WriteFile(filepath.Join(h2.repo, "dirty.txt"), []byte("x"), 0600)
	h2.coord.Start(context.Background(), run2.ID, "")
	tl = h2.waitPhase(run2.ID, routing.PhaseBlockedEnv, routing.PhaseReconcile)
	if tl.Attempts[0].Class != routing.EnvironmentFail {
		t.Fatalf("%+v", tl.Attempts[0])
	}
}

// Shadow runs the Run's own provider and records what routing would pick.
func TestShadowModeRecordsOnly(t *testing.T) {
	cfg := autoConfig()
	cfg.Mode = routing.ModeShadow
	h := newHarness(t, cfg, allCLIs())
	run, _ := h.runs.Create("k8", h.repo, "Refactor the architecture of app.txt", "codex")
	h.queue("codex", &step{edit: fix})
	res, err := h.coord.Start(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Launched != "legacy:codex" || res.Decision.Selected != "claude-opus" {
		t.Fatalf("shadow %+v", res)
	}
	h.waitPhase(run.ID, routing.PhaseSucceeded)
	if h.sc.launch[0].Model != "" || h.sc.launch[0].Effort != "" || h.sc.launch[0].Provider != "codex" {
		t.Fatal("shadow changed the executed model")
	}
}

// Off mode, Google-only automatic, manual refusal of an unavailable profile.
func TestModesAndGoogleOnly(t *testing.T) {
	cfg := autoConfig()
	cfg.Mode = routing.ModeOff
	h := newHarness(t, cfg, allCLIs())
	run, _ := h.runs.Create("k9", h.repo, "fix", "codex")
	if _, err := h.coord.Start(context.Background(), run.ID, ""); !errors.Is(err, ErrRoutingOff) {
		t.Fatalf("off mode: %v", err)
	}
	googleOnly := map[string]string{"agy --version": "1.1.28", "agy --help": "--print --output-format", "agy models": "gemini-3.8-flash-medium\tx"}
	h = newHarness(t, autoConfig(), googleOnly)
	run, _ = h.runs.Create("k10", h.repo, "fix app.txt", "antigravity")
	_, err := h.coord.Start(context.Background(), run.ID, "")
	if !errors.Is(err, ErrNoProfile) {
		t.Fatalf("google-only auto: %v", err)
	}
	tl, _ := h.coord.Timeline(run.ID)
	if tl.State.Phase != routing.PhaseWaitPolicy && tl.State.Phase != routing.PhaseBlockedEnv {
		t.Fatalf("phase %s", tl.State.Phase)
	}
	snap := h.coord.Snapshot(context.Background())
	for _, p := range snap.Profiles {
		if p.Profile.ID == "agy-flash" && !containsReason(p.Automatic, routing.RPolicyReview) {
			t.Fatalf("agy automatic exclusions %+v", p.Automatic)
		}
		if p.Profile.ID == "agy-flash" && containsReason(p.Manual, routing.RPolicyReview) {
			t.Fatal("manual antigravity use must stay possible")
		}
	}
	// Manual choice of a missing CLI is refused, not substituted.
	if _, err := h.coord.Start(context.Background(), run.ID, "claude-opus"); err == nil {
		t.Fatal("unavailable manual profile accepted")
	}
}

func containsReason(xs []routing.Exclusion, r routing.Reason) bool {
	for _, x := range xs {
		if x.Reason == r {
			return true
		}
	}
	return false
}

// Test 15 & 27: switch now waits for the attempt to stop, keeps one writer,
// and the new attempt inherits the interrupted attempt's edits.
func TestSwitchNowKeepsOneWriter(t *testing.T) {
	cfg := autoConfig()
	cfg.Mode = routing.ModeManual
	h := newHarness(t, cfg, allCLIs())
	run, _ := h.runs.Create("k11", h.repo, "fix app.txt", "codex")
	h.queue("codex", &step{edit: partial, block: true})
	saw := &step{edit: fix, sawFiles: map[string]string{"helper.txt": ""}}
	h.queue("claude", saw)
	if _, err := h.coord.Start(context.Background(), run.ID, "codex-fast"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	// A second start while the first attempt runs is refused.
	if _, err := h.coord.Start(context.Background(), run.ID, "claude-opus"); err == nil {
		t.Fatal("second writer accepted")
	}
	if _, err := h.coord.SwitchNow(run.ID, "claude-opus"); err != nil {
		t.Fatal(err)
	}
	tl := h.waitPhase(run.ID, routing.PhaseSucceeded)
	if len(tl.Attempts) != 2 || tl.Attempts[1].ProfileID != "claude-opus" || saw.sawFiles["helper.txt"] != "half done\n" {
		t.Fatalf("attempts %+v saw %v", tl.Attempts, saw.sawFiles)
	}
	var sawQuiescing bool
	for _, tr := range tl.Transitions {
		if tr.To == routing.PhaseQuiescing {
			sawQuiescing = true
		}
	}
	if !sawQuiescing || tl.State.PendingProfile != "" {
		t.Fatalf("transitions %+v state %+v", tl.Transitions, tl.State)
	}
}

// Test 17: a result for an attempt that is no longer current changes nothing.
func TestStaleResultIgnored(t *testing.T) {
	h := newHarness(t, autoConfig(), allCLIs())
	run, _ := h.runs.Create("k12", h.repo, "fix app.txt", "codex")
	h.queue("codex", &step{edit: fix})
	h.coord.Start(context.Background(), run.ID, "")
	tl := h.waitPhase(run.ID, routing.PhaseSucceeded)
	before := len(tl.Transitions)
	h.coord.OnAttempt(orchestration.AttemptResult{RunID: run.ID, ExecutionID: "exec_old", Routed: true, ProviderStatus: "failed", Diagnostics: "429"})
	h.coord.OnAttempt(orchestration.AttemptResult{RunID: run.ID, ExecutionID: tl.Attempts[0].ExecutionID, Routed: true, ProviderStatus: "failed"})
	tl2, _ := h.coord.Timeline(run.ID)
	if len(tl2.Transitions) != before || tl2.State.Phase != routing.PhaseSucceeded {
		t.Fatal("stale or duplicate result changed state")
	}
}

// Opt-in: the coordinator consults a real RouteLLM sidecar and records its
// verdict with checkpoint/package provenance (advisory for unevaluated pairs).
func TestCoordinatorWithRealSidecar(t *testing.T) {
	url := os.Getenv("PCD_ROUTELLM_URL")
	if url == "" {
		t.Skip("set PCD_ROUTELLM_URL to a running sidecar")
	}
	cfg := autoConfig()
	cfg.RouteLLM = routing.RouteLLMConfig{Enabled: true, URL: url, Router: "bert", TimeoutMS: 5000, Pairs: []routing.RouterPair{{Weak: "codex-fast", Strong: "claude-opus", Threshold: 0.5}}}
	h := newHarness(t, cfg, allCLIs())
	run, _ := h.runs.Create("live", h.repo, "app.txt의 버그를 고쳐줘", "codex")
	h.queue("codex", &step{edit: fix})
	res, err := h.coord.Start(context.Background(), run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	d := res.Decision
	if d.Router == nil || d.Router.Checkpoint != "routellm/bert_gpt4_augmented" || !strings.HasPrefix(d.Router.Package, "routellm==") || res.Launched != "codex-fast" {
		t.Fatalf("decision %+v router %+v", d, d.Router)
	}
	t.Logf("score=%.4f choice=%s latency=%dms reason=%s", d.Router.Score, d.Router.Choice, d.Router.LatencyMS, d.Reason)
	h.waitPhase(run.ID, routing.PhaseSucceeded)
}
