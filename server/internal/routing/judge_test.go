package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func judgeServer(t *testing.T, answer string, calls *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		var body struct {
			MaxTokens   int     `json:"max_tokens"`
			Temperature float64 `json:"temperature"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.MaxTokens != 5 || body.Temperature != 0 {
			t.Errorf("judge request: max_tokens=%d temperature=%v", body.MaxTokens, body.Temperature)
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": answer}}}})
	}))
}

func TestJudgeTierMapsAnswers(t *testing.T) {
	for answer, want := range map[string]Tier{"EASY": VeryEasy, " medium.": Medium, "HARD": High} {
		calls := 0
		srv := judgeServer(t, answer, &calls)
		got, err := NewLocalClient().JudgeTier(context.Background(), LocalEndpoint{ID: "mac", URL: srv.URL, Kind: "openai", Model: "m"}, "", "README 오타 고쳐줘")
		srv.Close()
		if err != nil || got != want {
			t.Errorf("%q → %v %v, want %v", answer, got, err, want)
		}
	}
	calls := 0
	srv := judgeServer(t, "I think it depends", &calls)
	defer srv.Close()
	if _, err := NewLocalClient().JudgeTier(context.Background(), LocalEndpoint{ID: "mac", URL: srv.URL, Kind: "openai"}, "m", "x"); err == nil {
		t.Fatal("an answer without a verdict was accepted")
	}
}

// The judge replaces the keyword tier for a first attempt, but not the rules'
// ULTRA (large scope) and not failure-based escalation.
func TestDecideUsesJudgeTier(t *testing.T) {
	pol := mustPolicy(t)
	cfg := cfgWith(baseProfiles()...)
	st := statuses(ready(AdapterCodex, BillingSubscription), ready(AdapterClaude, BillingSubscription))
	d := Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Health: NewHealth(), Mode: ModeAuto,
		Task: DescribeTask("package.json 버전을 0.6.0으로 바꿔줘", KindCode, nil), JudgeTier: VeryEasy})
	if d.RuleTier != VeryEasy || len(d.RuleWhy) == 0 || d.RuleWhy[0] != "local judge: VERY_EASY (rules: MEDIUM)" {
		t.Fatalf("judge not applied: %v %v", d.RuleTier, d.RuleWhy)
	}
	big := "인증 아키텍처를 재설계하고 보안 취약점까지 " + string(make([]byte, 0)) + stringsRepeat("전부 검토해줘 ", 700)
	d = Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Health: NewHealth(), Mode: ModeAuto,
		Task: DescribeTask(big, KindCode, nil), JudgeTier: VeryEasy})
	if d.RuleTier != Ultra {
		t.Fatalf("rules' ULTRA overridden: %v", d.RuleTier)
	}
	prior := []PriorAttempt{{Class: QualityFailure, Tier: Medium}}
	d = Decide(context.Background(), DecideInput{Config: cfg, Statuses: st, Policy: pol, Health: NewHealth(), Mode: ModeAuto,
		Task: DescribeTask("버전 바꿔줘", KindCode, prior), JudgeTier: VeryEasy})
	if d.RuleTier == VeryEasy {
		t.Fatal("judge overrode failure-based escalation")
	}
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
