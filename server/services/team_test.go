package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"powercodedeck/internal/providers"
)

// teamFakeExec is a scripted member execution: one turn that answers text
// (or fails), optionally blocking until released.
type teamFakeExec struct {
	text     string
	conv     string
	usage    *providers.Usage
	fail     bool // the turn ends as an error with text as its message
	startErr error
	block    chan struct{} // nil = answer at once; otherwise wait for close
	hang     bool          // never finish (until ctx ends)
	stopped  atomic.Bool
	sent     bool
	done     bool
}

func (f *teamFakeExec) Identity() providers.Identity         { return providers.Identity{} }
func (f *teamFakeExec) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (f *teamFakeExec) Start() error                         { return f.startErr }
func (f *teamFakeExec) Send(string) error                    { f.sent = true; return nil }
func (f *teamFakeExec) Interrupt() error                     { return nil }
func (f *teamFakeExec) ConversationID() string               { return f.conv }
func (f *teamFakeExec) Stop()                                { f.stopped.Store(true) }
func (f *teamFakeExec) SetPermissionMode(string) error       { return nil }
func (f *teamFakeExec) Next(ctx context.Context) (providers.Event, error) {
	if f.done {
		return providers.Event{}, errors.New("EOF")
	}
	if f.hang {
		<-ctx.Done()
		return providers.Event{}, ctx.Err()
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return providers.Event{}, ctx.Err()
		}
	}
	f.done = true
	o := &providers.Outcome{Status: providers.CompletionSuccess, Text: f.text, Usage: f.usage}
	if f.fail {
		o.Status, o.IsError = providers.CompletionFailed, true
	}
	return providers.Event{Kind: providers.TurnFinished, Outcome: o}, nil
}

type teamCall struct {
	provider            providers.ID
	model, mode, resume string
}

type teamHarness struct {
	t      *testing.T
	db     *sql.DB
	agents *AgentService
	svc    *TeamService
	agent  string
	cwd    string

	mu        sync.Mutex
	calls     []teamCall
	script    func(n int, c teamCall) *teamFakeExec // n = 0-based call index
	condAdapt string
	condModel string
}

func newTeamHarness(t *testing.T) *teamHarness {
	t.Helper()
	database := teamTestDB(t)
	h := &teamHarness{t: t, db: database, agents: &AgentService{db: database}, cwd: t.TempDir(),
		condAdapt: "claude", condModel: "claude-opus-5-5"}
	a, err := h.agents.Create(CreateAgentRequest{Preset: AutoPreset, Name: "t", Command: "claude", WorkingDir: h.cwd})
	if err != nil {
		t.Fatal(err)
	}
	h.agent = a.ID
	if err := h.agents.SetTeamMode(a.ID, true); err != nil {
		t.Fatal(err)
	}
	h.script = func(int, teamCall) *teamFakeExec { return &teamFakeExec{text: "ok", conv: "c1"} }
	h.svc = NewTeamService(TeamDeps{
		DB:     database,
		Agents: h.agents,
		Usage:  NewAutoUsage(database),
		NewExec: func(p providers.ID, id, cwd, model, mode, resume string) (providers.Execution, error) {
			h.mu.Lock()
			c := teamCall{p, model, mode, resume}
			n := len(h.calls)
			h.calls = append(h.calls, c)
			script := h.script
			h.mu.Unlock()
			return script(n, c), nil
		},
		Conductor: func(string) (string, string, string, bool) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.condAdapt, h.condModel, h.cwd, true
		},
	})
	return h
}

func (h *teamHarness) settings(edit func(*TeamSettings)) {
	s := DefaultTeamSettings()
	edit(&s)
	if err := SaveTeamSettings(h.db, s); err != nil {
		h.t.Fatal(err)
	}
}

func (h *teamHarness) ask(member, task string, files ...string) TeamResult {
	return h.svc.Ask(context.Background(), TeamCall{AgentID: h.agent, Member: member, Task: task, Files: files})
}

func (h *teamHarness) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func TestAskRefusedWhenOff(t *testing.T) {
	h := newTeamHarness(t)
	if err := h.agents.SetTeamMode(h.agent, false); err != nil {
		t.Fatal(err)
	}
	r := h.ask("codex", "look")
	if r.Status != "refused" || r.Reason != "드림팀 모드가 꺼져 있습니다. 사용자에게 켜 달라고 하지 말고 직접 처리하세요." {
		t.Fatalf("got %+v", r)
	}
	if r.CallID == "" || h.callCount() != 0 {
		t.Fatalf("callID %q, calls %d", r.CallID, h.callCount())
	}
}

func TestAskRejectsBadInput(t *testing.T) {
	h := newTeamHarness(t)
	cases := []struct {
		member, task string
		files        []string
	}{
		{"codex", "  ", nil},
		{"nobody", "look", nil},
		{"claude-fable", "look", nil},
		{"codex", "look", []string{"../x"}},
		{"codex", "look", []string{"a/../../x"}},
		{"codex", "look", []string{"/etc/passwd"}},
	}
	for _, c := range cases {
		r := h.ask(c.member, c.task, c.files...)
		if r.Status != "refused" || r.Reason == "" {
			t.Errorf("%+v: got %+v", c, r)
		}
	}
	if h.callCount() != 0 {
		t.Fatalf("factory called %d times", h.callCount())
	}
	if r := h.ask("codex", "look", "src/a.go", "./b/../c.go"); r.Status != "done" {
		t.Fatalf("good files: %+v", r)
	}
}

func TestAskSelfExclusion(t *testing.T) {
	h := newTeamHarness(t)
	h.condAdapt, h.condModel = "claude", "claude-sonnet-5-5"
	if r := h.ask("claude-sonnet", "look"); r.Status != "refused" {
		t.Fatalf("same model: %+v", r)
	}
	if r := h.ask("claude-haiku", "look"); r.Status != "done" {
		t.Fatalf("other model: %+v", r)
	}
	h.svc.ResetTurn(h.agent)
	h.condAdapt, h.condModel = "codex", ""
	if r := h.ask("codex", "look"); r.Status != "refused" {
		t.Fatalf("codex self: %+v", r)
	}
}

func TestAskTruncatesAndKeepsFull(t *testing.T) {
	h := newTeamHarness(t)
	long := strings.Repeat("가", 10000)
	h.script = func(int, teamCall) *teamFakeExec { return &teamFakeExec{text: long, conv: "c1"} }
	r := h.ask("claude-haiku", "read everything", "a.go")
	if r.Status != "done" || !r.Truncated || len([]rune(r.Text)) != 6000 {
		t.Fatalf("status %s truncated %v len %d", r.Status, r.Truncated, len([]rune(r.Text)))
	}
	full, ok := h.svc.FullText(h.agent, r.CallID)
	if !ok || len([]rune(full)) != 10000 {
		t.Fatalf("full: ok %v len %d", ok, len([]rune(full)))
	}
	if _, ok := h.svc.FullText("other-agent", r.CallID); ok {
		t.Fatal("full text leaked to another agent")
	}
	if h.calls[0].mode != "plan" || h.calls[0].model != "claude-haiku-5-5" || h.calls[0].provider != providers.Claude {
		t.Fatalf("call %+v", h.calls[0])
	}
	if got := h.agents.TeamSessions(h.agent)["claude-haiku"]; got != "c1" {
		t.Fatalf("session = %q", got)
	}
}

func TestAskBriefFormat(t *testing.T) {
	h := newTeamHarness(t)
	var got string
	h.svc.d.NewExec = func(p providers.ID, id, cwd, model, mode, resume string) (providers.Execution, error) {
		return &briefCapture{teamFakeExec: &teamFakeExec{text: "ok"}, out: &got}, nil
	}
	h.ask("codex", "check it", "a.go", "b.go")
	want := "check it\n\n참고할 파일(직접 읽으세요):\n- a.go\n- b.go\n\n답은 핵심만 요약해서 6000자 이내로 주세요. 파일을 수정하지 마세요."
	if got != want {
		t.Fatalf("brief = %q", got)
	}
}

type briefCapture struct {
	*teamFakeExec
	out *string
}

func (b *briefCapture) Send(s string) error { *b.out = s; return nil }

func TestAskParallelCallsRespectTurnCap(t *testing.T) {
	h := newTeamHarness(t)
	var wg sync.WaitGroup
	results := make([]TeamResult, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = h.ask("codex", "look")
		}(i)
	}
	wg.Wait()
	count := map[string]int{}
	for _, r := range results {
		count[r.Status]++
		if r.Status == "limit" && !strings.Contains(r.Reason, "3/3") {
			t.Errorf("limit reason %q", r.Reason)
		}
	}
	if count["done"] != 3 || count["limit"] != 2 {
		t.Fatalf("counts %v", count)
	}
	h.svc.ResetTurn(h.agent)
	if r := h.ask("codex", "look"); r.Status != "done" {
		t.Fatalf("after reset: %+v", r)
	}
}

func TestAskSessionTokenCap(t *testing.T) {
	h := newTeamHarness(t)
	h.settings(func(s *TeamSettings) { s.SessionTokenCap = 100 })
	h.script = func(int, teamCall) *teamFakeExec {
		return &teamFakeExec{text: "ok", conv: "c1", usage: &providers.Usage{InputTokens: 120, OutputTokens: 30}}
	}
	r := h.ask("claude-haiku", "look")
	if r.Status != "done" || r.Tokens == nil || r.Tokens.In != 120 || r.Tokens.Out != 30 {
		t.Fatalf("first: %+v", r)
	}
	r = h.ask("claude-haiku", "look")
	if r.Status != "limit" || !strings.Contains(r.Reason, "150/100") {
		t.Fatalf("second: %+v", r)
	}
	if r := h.ask("codex", "look"); r.Status != "done" {
		t.Fatalf("codex ignores the token cap: %+v", r)
	}
	h.svc.RaiseCap(h.agent)
	if r := h.ask("claude-haiku", "look"); r.Status != "done" {
		t.Fatalf("after raise: %+v", r)
	}
}

func TestAskStaleResumeRetriesFresh(t *testing.T) {
	h := newTeamHarness(t)
	if err := h.agents.SetTeamSession(h.agent, "codex", "old"); err != nil {
		t.Fatal(err)
	}
	h.script = func(n int, c teamCall) *teamFakeExec {
		if c.resume != "" {
			return &teamFakeExec{text: "no rollout found for thread", fail: true}
		}
		return &teamFakeExec{text: "fresh answer", conv: "new"}
	}
	r := h.ask("codex", "look")
	if r.Status != "done" || r.Text != "fresh answer" {
		t.Fatalf("got %+v", r)
	}
	if len(h.calls) != 2 || h.calls[0].resume != "old" || h.calls[1].resume != "" {
		t.Fatalf("calls %+v", h.calls)
	}
	if got := h.agents.TeamSessions(h.agent)["codex"]; got != "new" {
		t.Fatalf("session = %q", got)
	}
}

func TestAskRetryPolicy(t *testing.T) {
	cases := []struct {
		text   string
		status string
		calls  int
	}{
		{"something broke", "failed", 2},
		{"Please run /login: not logged in", "not_logged_in", 1},
		{"Claude usage limit reached", "quota", 1},
	}
	for _, c := range cases {
		h := newTeamHarness(t)
		h.script = func(int, teamCall) *teamFakeExec { return &teamFakeExec{text: c.text, fail: true} }
		r := h.ask("codex", "look")
		if r.Status != c.status || h.callCount() != c.calls {
			t.Errorf("%q: status %s calls %d", c.text, r.Status, h.callCount())
		}
	}
	h := newTeamHarness(t)
	h.script = func(int, teamCall) *teamFakeExec {
		return &teamFakeExec{startErr: errors.New(`exec: "codex": executable file not found in $PATH`)}
	}
	if r := h.ask("codex", "look"); r.Status != "not_installed" {
		t.Fatalf("missing binary: %+v", r)
	}
}

func TestAskTimeout(t *testing.T) {
	h := newTeamHarness(t)
	h.settings(func(s *TeamSettings) { s.TimeoutSec = 1 })
	var fake *teamFakeExec
	h.script = func(int, teamCall) *teamFakeExec {
		fake = &teamFakeExec{hang: true}
		return fake
	}
	start := time.Now()
	r := h.ask("codex", "look")
	if r.Status != "timeout" {
		t.Fatalf("got %+v", r)
	}
	if h.callCount() != 1 || !fake.stopped.Load() {
		t.Fatalf("calls %d stopped %v", h.callCount(), fake.stopped.Load())
	}
	if el := time.Since(start); el > 3*time.Second || r.ElapsedMs < 900 {
		t.Fatalf("elapsed %v / %dms", el, r.ElapsedMs)
	}
}

func TestAskToggleOffMidCall(t *testing.T) {
	h := newTeamHarness(t)
	block := make(chan struct{})
	h.script = func(int, teamCall) *teamFakeExec { return &teamFakeExec{text: "ok", block: block} }
	first := make(chan TeamResult)
	go func() { first <- h.ask("codex", "look") }()
	for h.callCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.agents.SetTeamMode(h.agent, false); err != nil {
		t.Fatal(err)
	}
	close(block)
	if r := <-first; r.Status != "done" {
		t.Fatalf("first: %+v", r)
	}
	if r := h.ask("codex", "look"); r.Status != "refused" {
		t.Fatalf("second: %+v", r)
	}
}

func TestAskCodexTokensNil(t *testing.T) {
	h := newTeamHarness(t)
	r := h.ask("codex", "look")
	if r.Status != "done" || r.Tokens != nil {
		t.Fatalf("got %+v", r)
	}
	var kind, tool string
	var in sql.NullInt64
	if err := h.db.QueryRow("SELECT switch_kind, tool, input_tokens FROM auto_turns WHERE agent_id = ?", h.agent).Scan(&kind, &tool, &in); err != nil {
		t.Fatal(err)
	}
	if kind != "team" || tool != "codex" || in.Valid {
		t.Fatalf("row kind %q tool %q in %+v", kind, tool, in)
	}
}

func TestTeamBrief(t *testing.T) {
	s := DefaultTeamSettings()
	b := TeamBrief(s, "claude", "claude-sonnet-5-5")
	if strings.Contains(b, "claude-sonnet") || !strings.Contains(b, "claude-haiku") || !strings.Contains(b, "codex") ||
		strings.Contains(b, "claude-fable") || !strings.Contains(b, "handoff_to_agy") || !strings.Contains(b, "ask_member") {
		t.Fatalf("brief:\n%s", b)
	}
}

func TestAskCanceledDoesNotRetry(t *testing.T) {
	h := newTeamHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.script = func(int, teamCall) *teamFakeExec { return &teamFakeExec{hang: true} }
	go func() {
		for h.callCount() == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	r := h.svc.Ask(ctx, TeamCall{AgentID: h.agent, Member: "codex", Task: "look"})
	if r.Status != "failed" || h.callCount() != 1 {
		t.Fatalf("status %s calls %d", r.Status, h.callCount())
	}
}

// An unknown member id is refused with the ids the conductor can actually call
// (enabled, not itself), so it can correct the call.
func TestAskUnknownMemberListsIDs(t *testing.T) {
	h := newTeamHarness(t)
	h.condAdapt, h.condModel = "claude", "claude-sonnet-5-5"
	r := h.ask("gemini", "look")
	if r.Status != "refused" {
		t.Fatalf("got %+v", r)
	}
	for _, id := range []string{"codex", "claude-haiku"} {
		if !strings.Contains(r.Reason, id) {
			t.Errorf("reason %q lacks %s", r.Reason, id)
		}
	}
	for _, id := range []string{"claude-sonnet", "claude-fable"} {
		if strings.Contains(r.Reason, id) {
			t.Errorf("reason %q lists %s (self or disabled)", r.Reason, id)
		}
	}
}
