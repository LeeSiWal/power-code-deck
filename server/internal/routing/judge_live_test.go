package routing

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// TestJudgeLive re-measures the tier judge against a real local model:
//
//	RUN_TIER_JUDGE_LIVE=1 TIER_JUDGE_URL=http://192.168.1.22:8080 TIER_JUDGE_MODEL=<id> go test ./internal/routing -run JudgeLive -v
//
// It fails if any non-easy request is rated easy (that would send real work to
// a small local model), and reports accuracy per set.
func TestJudgeLive(t *testing.T) {
	if os.Getenv("RUN_TIER_JUDGE_LIVE") != "1" {
		t.Skip("set RUN_TIER_JUDGE_LIVE=1 with TIER_JUDGE_URL and TIER_JUDGE_MODEL")
	}
	ep := LocalEndpoint{ID: "judge", URL: os.Getenv("TIER_JUDGE_URL"), Kind: "openai", Model: os.Getenv("TIER_JUDGE_MODEL"), AllowPrivate: true, AllowInsecureHTTP: true}
	var data struct {
		Dev, Heldout, Followup []struct{ Label, Goal, Prev string }
	}
	b, err := os.ReadFile("testdata/tier_eval.json")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &data)
	class := map[Tier]string{VeryEasy: "easy", Medium: "medium", High: "hard"}
	client := NewLocalClient()
	for name, set := range map[string][]struct{ Label, Goal, Prev string }{"dev": data.Dev, "heldout": data.Heldout, "followup": data.Followup} {
		ok := 0
		for _, x := range set {
			goal := x.Goal
			if x.Prev != "" && IsFollowUp(goal) {
				goal = x.Prev // the coordinator rates a follow-up like the previous request
			}
			tier, err := client.JudgeTier(context.Background(), ep, "", goal)
			if err != nil {
				t.Fatalf("%s: %v", x.Goal, err)
			}
			got := class[tier]
			if got == x.Label {
				ok++
			} else if got == "easy" {
				t.Errorf("%s: %q rated easy (label %s)", name, x.Goal, x.Label)
			} else {
				t.Logf("%s miss: %s → %s | %s", name, x.Label, got, x.Goal)
			}
		}
		t.Logf("%s: %d/%d", name, ok, len(set))
	}
}
