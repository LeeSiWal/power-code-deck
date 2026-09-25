package services

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"powercodedeck/internal/routing"
)

// Opt-in, consumes the signed-in account's usage:
//
//	PCD_DECIDER_LIVE=claude PCD_DECIDER_MODEL=sonnet go test ./services -run TestDeciderLive -v
//	PCD_DECIDER_LIVE=codex  PCD_DECIDER_MODEL=gpt-5.6-luna go test ./services -run TestDeciderLive -v
//
// One CLI is enough; the other and any local model are not required.
func TestDeciderLive(t *testing.T) {
	adapter := os.Getenv("PCD_DECIDER_LIVE")
	if adapter == "" {
		t.Skip("set PCD_DECIDER_LIVE=claude|codex")
	}
	p := routing.Profile{ID: "live-decider", Adapter: adapter, Model: os.Getenv("PCD_DECIDER_MODEL"), Effort: os.Getenv("PCD_DECIDER_EFFORT")}
	cands := []routing.Candidate{
		{Profile: routing.Profile{ID: "fast", Adapter: adapter, Tiers: []routing.Tier{routing.Easy, routing.Medium}}},
		{Profile: routing.Profile{ID: "deep", Adapter: adapter, Tiers: []routing.Tier{routing.High, routing.Ultra}}},
	}
	task := routing.DescribeTask("Fix the typo 'recieve' in README.md", routing.KindCode, nil)
	req := routing.BuildDecisionRequest("dq_live", "run_live", 1, task, routing.Easy, "", cands, map[string]int{"attemptsLeft": 3}, nil)
	caller := &routing.CLIDecider{Runner: RoutingRunner{}, TempRoot: t.TempDir()}
	rec := routing.Consult(context.Background(), routing.DeciderConfig{MaxFormatRetries: -1, MaxContextRounds: -1}, []routing.Profile{p}, req, caller, nil)
	for _, c := range rec.Calls {
		in, out := "unreported", "unreported"
		if c.Usage != nil && c.Usage.InputTokens != nil {
			in = strconv.Itoa(*c.Usage.InputTokens)
		}
		if c.Usage != nil && c.Usage.OutputTokens != nil {
			out = strconv.Itoa(*c.Usage.OutputTokens)
		}
		t.Logf("call profile=%s latency=%dms valid=%v class=%s models=%s input=%s output=%s err=%s", c.ProfileID, c.LatencyMS, c.Valid, c.ErrorClass, c.ObservedModel, in, out, c.Error)
	}
	if rec.Verdict == nil {
		t.Fatalf("no valid verdict: %s", rec.Note)
	}
	t.Logf("verdict %+v", *rec.Verdict)
	if _, err := routing.ParseVerdict([]byte(`{"action":"x"}`), req, 0); !errors.Is(err, routing.ErrInvalidVerdict) {
		t.Fatal("contract check")
	}
}
