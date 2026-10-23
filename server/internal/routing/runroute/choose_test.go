package runroute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"powercodedeck/internal/routing"
)

func TestChooseNeverLaunchesOrCallsDecider(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	fd := &fakeDecider{answer: verdict(dispatchCodex)}
	h.coord.decider = fd
	ch, err := h.coord.Choose(context.Background(), "fix app.txt")
	if err != nil || ch.Profile == nil || ch.Profile.ID != ch.Decision.Selected {
		t.Fatalf("choice=%+v err=%v", ch, err)
	}
	if fd.calls.Load() != 0 {
		t.Fatalf("decider called %d times", fd.calls.Load())
	}
	if _, err := h.coord.Choose(context.Background(), "   "); err == nil {
		t.Fatal("empty request accepted")
	}
}

func TestChooseReportsNoProfile(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), map[string]string{})
	ch, err := h.coord.Choose(context.Background(), "fix app.txt")
	if !errors.Is(err, ErrNoProfile) || ch.Decision.Reason == "" {
		t.Fatalf("choice=%+v err=%v", ch, err)
	}
}

func TestTurnSwitchPolicy(t *testing.T) {
	low := routing.Profile{ID: "low", Tiers: []routing.Tier{routing.Easy, routing.Medium}}
	low2 := routing.Profile{ID: "low2", Tiers: []routing.Tier{routing.Medium}}
	high := routing.Profile{ID: "high", Tiers: []routing.Tier{routing.High}}
	big := SmallContextTokens * 4
	cases := []struct {
		cur    *routing.Profile
		next   routing.Profile
		idle   time.Duration
		ctx    int
		apply  bool
		reason string
	}{
		{nil, low, 0, big, true, "current_unknown"},
		{&low, low, time.Hour, big, false, "same"},
		{&low, high, 0, big, true, "harder"},
		{&low, low2, time.Hour, big, false, "same_tier"},
		{&high, low, time.Minute, big, false, "downgrade_deferred"},
		{&high, low, TurnIdleDowngrade, big, true, "idle_downgrade"},
		// Up to ModelSwitchTokens a session moves down at once: one hard request
		// must not keep it on a pricier model for the easy ones after it.
		{&high, low, time.Minute, SmallContextTokens - 1, true, "small_context_downgrade"},
		{&high, low, time.Minute, ModelSwitchTokens - 1, true, "small_context_downgrade"},
		{&high, low, time.Minute, ModelSwitchTokens, false, "downgrade_deferred"},
	}
	for _, c := range cases {
		apply, reason := TurnSwitch(c.cur, c.next, c.idle, c.ctx, TurnLoad{})
		if apply != c.apply || reason != c.reason {
			t.Errorf("%v→%s idle %v: got %v %s", c.cur, c.next.ID, c.idle, apply, reason)
		}
	}
}

// With prices and a measured turn, moving down is a cost comparison over
// DowngradeHorizonTurns turns instead of a size rule: a long conversation moves
// down when its agentic turns re-read far more than the move writes once, and
// stays when a turn barely re-reads it. Prices here are made-up round numbers
// in the shape of a cache-write surcharge and a cheap cache read.
func TestTurnSwitchWeighsStayingAgainstMoving(t *testing.T) {
	hi := routing.Profile{ID: "hi", Tiers: []routing.Tier{routing.High}, Price: &routing.Price{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}}
	lo := routing.Profile{ID: "lo", Tiers: []routing.Tier{routing.Medium}, Price: &routing.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}}
	ctx := ModelSwitchTokens * 5 // past the size rule, which would defer
	cases := []struct {
		name   string
		load   TurnLoad
		apply  bool
		reason string
	}{
		// 20 requests per turn re-reading the conversation: staying costs more.
		{"agentic turns", TurnLoad{CacheRead: 20 * ctx, Output: 4000}, true, "cost_downgrade"},
		// One request per turn: the move's one cache write is never repaid.
		{"single-request turns", TurnLoad{CacheRead: ctx, Output: 300}, false, "downgrade_deferred"},
		// Nothing reported (Codex): the size rule decides.
		{"unreported", TurnLoad{}, false, "downgrade_deferred"},
	}
	for _, c := range cases {
		apply, reason := TurnSwitch(&hi, lo, time.Minute, ctx, c.load)
		if apply != c.apply || reason != c.reason {
			t.Errorf("%s: got %v %s, want %v %s", c.name, apply, reason, c.apply, c.reason)
		}
	}
	// Without a price on either side the size rule decides, even with usage.
	noPrice := lo
	noPrice.Price = nil
	if apply, reason := TurnSwitch(&hi, noPrice, time.Minute, ModelSwitchTokens-1, TurnLoad{CacheRead: ctx}); !apply || reason != "small_context_downgrade" {
		t.Errorf("no price: got %v %s", apply, reason)
	}
	// Moving up and idle moves never depend on prices.
	if apply, reason := TurnSwitch(&lo, hi, 0, ctx, TurnLoad{CacheRead: ctx}); !apply || reason != "harder" {
		t.Errorf("up: got %v %s", apply, reason)
	}
}

// Later turns stay on the session's tool even when another tool would win.
func TestChooseTurnPinsAdapter(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	for i := 0; i < 3; i++ {
		tc, err := h.coord.ChooseTurn(context.Background(), "fix app.txt", "codex", "codex-fast", false, "")
		if err != nil || tc.Profile == nil || tc.Profile.Adapter != "codex" || tc.Current == nil || tc.Current.ID != "codex-fast" {
			t.Fatalf("turn choice: %+v %v", tc, err)
		}
	}
}

func TestWithoutLocalDropsLocalProfiles(t *testing.T) {
	cfg := routing.Config{Profiles: []routing.Profile{
		{ID: "paid", Adapter: "codex", Model: "gpt-5.6-luna"},
		{ID: "mac", Adapter: "codex", EndpointRef: "mac", Model: "qwen"},
		{ID: "text", Adapter: routing.AdapterLocal, EndpointRef: "lan"},
	}}
	got := withoutLocal(cfg).Profiles
	if len(got) != 1 || got[0].ID != "paid" || len(cfg.Profiles) != 3 {
		t.Fatalf("kept %+v (original %d)", got, len(cfg.Profiles))
	}
}

// judgeTier skips a down endpoint (no call, no wait) and caches answers.
func TestJudgeTierCachesAndSkipsDownEndpoint(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"choices":[{"message":{"content":"EASY"}}]}`)
	}))
	defer srv.Close()
	c := &Coordinator{prober: &routing.Prober{HTTP: routing.NewLocalClient()}, now: time.Now}
	cfg := routing.Config{LocalEndpoints: []routing.LocalEndpoint{{ID: "mac", URL: srv.URL, Kind: "openai", Model: "m"}},
		TierJudge: &routing.TierJudgeConfig{EndpointRef: "mac"}}
	up := map[string]routing.AdapterStatus{"local:mac": {Installation: routing.Observation{Value: routing.Installed}}}
	if got := c.judgeTier(context.Background(), cfg, map[string]routing.AdapterStatus{}, "README 오타 고쳐줘"); got != routing.TierUnset || calls != 0 {
		t.Fatalf("down endpoint: tier %v, %d calls", got, calls)
	}
	for i := 0; i < 3; i++ {
		if got := c.judgeTier(context.Background(), cfg, up, "README 오타 고쳐줘"); got != routing.VeryEasy {
			t.Fatalf("judge tier %v", got)
		}
	}
	if calls != 1 {
		t.Fatalf("cache: %d calls for one request", calls)
	}
}

func TestIsFollowUp(t *testing.T) {
	for goal, want := range map[string]bool{
		"한번더 붙여줘": true, "다시 해줘": true, "그거 되돌려줘": true, "계속 진행해줘": true, "좋아 이어서 해줘": true,
		"port도 9090으로 바꿔줘": false, "README 오타 고쳐줘": false, "테스트 파일에 모든 123을 때줘": false,
		"회원가입 폼에도 똑같이 만들어줘": true, "인증 시스템 전체를 새 구조로 옮기고 기존 데이터도 옮겨줘": false, "": false,
	} {
		if got := routing.IsFollowUp(goal); got != want {
			t.Errorf("%q: %v, want %v", goal, got, want)
		}
	}
}

// A follow-up is rated like the previous request, not as a fragment.
func TestJudgeTurnTierInheritsForFollowUps(t *testing.T) {
	asked := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		c := body.Messages[0].Content
		asked = append(asked, c[strings.LastIndex(c, "Request: ")+9:])
		answer := "HARD" // a context-free fragment looks risky to the judge
		if strings.Contains(c, "붙여줘") && strings.Contains(c, "test.txt") {
			answer = "EASY"
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+answer+`"}}]}`)
	}))
	defer srv.Close()
	c := &Coordinator{prober: &routing.Prober{HTTP: routing.NewLocalClient()}, now: time.Now}
	cfg := routing.Config{LocalEndpoints: []routing.LocalEndpoint{{ID: "mac", URL: srv.URL, Kind: "openai", Model: "m"}}, TierJudge: &routing.TierJudgeConfig{EndpointRef: "mac"}}
	up := map[string]routing.AdapterStatus{"local:mac": {Installation: routing.Observation{Value: routing.Installed}}}
	if got := c.judgeTurnTier(context.Background(), cfg, up, "한번더 붙여줘", "test.txt에 '123'을 붙여줘"); got != routing.VeryEasy {
		t.Fatalf("follow-up rated %v; judge was asked %q", got, asked)
	}
	if got := c.judgeTurnTier(context.Background(), cfg, up, "한번더 붙여줘", ""); got != routing.High {
		t.Fatalf("without a previous request the fragment is judged itself: %v", got)
	}
}

func TestCrossToolAllowed(t *testing.T) {
	big := SmallContextTokens * 3
	for _, c := range []struct {
		idle    time.Duration
		ctx     int
		onLocal bool
		want    bool
	}{
		{time.Minute, big, true, true},                     // leaving the local model
		{TurnIdleDowngrade, big, false, true},              // cache already cold
		{time.Minute, SmallContextTokens - 1, false, true}, // little to hand over
		{time.Minute, big, false, false},                   // warm, long, paid: stay
	} {
		if got, reason := CrossToolAllowed(c.idle, c.ctx, c.onLocal); got != c.want {
			t.Errorf("%+v: %v (%s)", c, got, reason)
		}
	}
}
