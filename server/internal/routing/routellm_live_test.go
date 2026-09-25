package routing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestRouterEvalLive compares the rule baseline with a running RouteLLM
// sidecar on a small, pre-labelled task set. It needs a sidecar:
//
//	PCD_ROUTELLM_URL=http://127.0.0.1:8765 go test ./internal/routing -run TestRouterEvalLive -v
//
// Criteria were fixed before the first run (docs/architecture/multimodel-routing.md):
// RouteLLM is eligible for code-task Auto only if its score separates hard
// (HIGH/ULTRA) from easy (VERY_EASY/EASY) tasks with AUC >= 0.75. The test
// reports; it does not fail on quality, only on protocol errors.
func TestRouterEvalLive(t *testing.T) {
	url := os.Getenv("PCD_ROUTELLM_URL")
	if url == "" {
		t.Skip("set PCD_ROUTELLM_URL to a running sidecar")
	}
	c, err := NewRouteLLMClient(RouteLLMConfig{Enabled: true, URL: url, Router: "bert", TimeoutMS: 5000})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("testdata/router_eval.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type row struct {
		ID, Goal string
		Tier     Tier
		Rule     Tier
		Score    float64
		MS       int64
	}
	var rows []row
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r struct {
			ID   string `json:"id"`
			Tier Tier   `json:"tier"`
			Goal string `json:"goal"`
		}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		task := DescribeTask(r.Goal, KindCode, nil)
		rule, _ := RuleTier(task)
		v, err := c.Score(context.Background(), RouterText(task, 4096), RouterPair{Weak: "w", Strong: "s", Threshold: 0.5}, "eval")
		if err != nil {
			t.Fatalf("%s: %v", r.ID, err)
		}
		rows = append(rows, row{r.ID, r.Goal, r.Tier, rule, v.Score, v.LatencyMS})
	}
	var exact, under, over int
	var lat []int64
	var easy, hard []float64
	t.Logf("%-4s %-9s %-9s %-7s %s", "id", "label", "rule", "score", "ms")
	for _, r := range rows {
		switch {
		case r.Rule == r.Tier:
			exact++
		case r.Rule < r.Tier:
			under++
		default:
			over++
		}
		lat = append(lat, r.MS)
		if r.Tier <= Easy {
			easy = append(easy, r.Score)
		} else if r.Tier >= High {
			hard = append(hard, r.Score)
		}
		t.Logf("%-4s %-9s %-9s %.4f  %d", r.ID, r.Tier, r.Rule, r.Score, r.MS)
	}
	// AUC = P(score_hard > score_easy), ties count half.
	var wins float64
	for _, h := range hard {
		for _, e := range easy {
			switch {
			case h > e:
				wins++
			case h == e:
				wins += 0.5
			}
		}
	}
	auc := wins / float64(len(hard)*len(easy))
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50, p95 := lat[len(lat)/2], lat[(len(lat)*95+99)/100-1]
	verdict := "NOT eligible for code-task Auto (stays Shadow)"
	if auc >= 0.75 {
		verdict = "meets the pre-registered AUC criterion"
	}
	t.Log(strings.Repeat("-", 40))
	t.Logf("rule baseline: exact %d/%d, under-routed %d, over-routed %d", exact, len(rows), under, over)
	t.Logf("RouteLLM hard-vs-easy AUC %.3f (n_hard=%d n_easy=%d): %s", auc, len(hard), len(easy), verdict)
	t.Logf("router latency p50 %dms p95 %dms (client-measured, warm)", p50, p95)
	fmt.Fprintf(os.Stderr, "ROUTER_EVAL auc=%.3f rule_exact=%d/%d rule_under=%d p50=%d p95=%d\n", auc, exact, len(rows), under, p50, p95)
}
