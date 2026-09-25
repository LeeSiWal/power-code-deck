package runroute

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	cases := []struct {
		cur    *routing.Profile
		next   routing.Profile
		idle   time.Duration
		apply  bool
		reason string
	}{
		{nil, low, 0, true, "current_unknown"},
		{&low, low, time.Hour, false, "same"},
		{&low, high, 0, true, "harder"},
		{&low, low2, time.Hour, false, "same_tier"},
		{&high, low, time.Minute, false, "downgrade_deferred"},
		{&high, low, TurnIdleDowngrade, true, "idle_downgrade"},
	}
	for _, c := range cases {
		apply, reason := TurnSwitch(c.cur, c.next, c.idle)
		if apply != c.apply || reason != c.reason {
			t.Errorf("%v→%s idle %v: got %v %s", c.cur, c.next.ID, c.idle, apply, reason)
		}
	}
}

// Later turns stay on the session's tool even when another tool would win.
func TestChooseTurnPinsAdapter(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	for i := 0; i < 3; i++ {
		tc, err := h.coord.ChooseTurn(context.Background(), "fix app.txt", "codex", "codex-fast", false)
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
