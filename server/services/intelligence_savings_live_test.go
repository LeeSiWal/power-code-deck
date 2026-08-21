package services

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"powercodedeck/db"

	_ "modernc.org/sqlite"
)

// The measurement this whole feature rests on, and the one nobody has run.
//
// The UI reported "96% reduction" from rawTokens−optimizedTokens. That is a
// LOCAL COMPRESSION ratio, not a saving: rawTokens is the candidate context
// PowerCodeDeck assembles, and CLOUD_ONLY never sends it (it forwards the user's
// task byte-for-byte and lets the CLI read files itself). The only honest
// comparison is what the CLOUD actually spent, per mode, on the same task.
//
// Each run gets a FRESH native session so runs are independent — turns in one
// conversation accumulate history and cache, which would bias whichever mode ran
// later. Plan mode keeps every run read-only.
//
// Opt-in: this spends real Claude quota, once per run per mode.
//
//	PCD_LIVE_SAVINGS=1 PCD_LIVE_SAVINGS_REPO=$PWD \
//	PCD_LIVE_SAVINGS_PROVIDER_URL=http://192.168.1.22:11434 \
//	PCD_LIVE_SAVINGS_MODEL=qwen3-coder:30b \
//	go test ./services -run TestLiveHybridSavingsMeasurement -v -count=1 -timeout 60m
func TestLiveHybridSavingsMeasurement(t *testing.T) {
	if os.Getenv("PCD_LIVE_SAVINGS") != "1" {
		t.Skip("set PCD_LIVE_SAVINGS=1 to measure hybrid savings against real cloud usage")
	}
	repo := envOr("PCD_LIVE_SAVINGS_REPO", ".")
	baseURL := envOr("PCD_LIVE_SAVINGS_PROVIDER_URL", "http://192.168.1.22:11434")
	model := envOr("PCD_LIVE_SAVINGS_MODEL", "qwen3-coder:30b")
	task := envOr("PCD_LIVE_SAVINGS_TASK",
		"Explain how this repository decides whether a tool call needs human approval. Do not change any files.")
	runs, _ := strconv.Atoi(envOr("PCD_LIVE_SAVINGS_RUNS", "2"))
	if runs < 1 {
		runs = 1
	}

	database := liveSavingsDB(t)
	agents := NewAgentService(database, nil)
	native := NewNativeService("http://127.0.0.1:0")
	// Nothing is watching for approvals in a test process, and the permission
	// bridge base URL above is deliberately unreachable. Deny immediately so an
	// unexpected tool request fails the run fast instead of hanging until the
	// timeout and silently spending a turn on nothing.
	native.SetHandlers(nil, func(req PermissionRequest) {
		native.Decide(req.ID, PermissionDecision{
			Behavior: "deny", Message: "savings measurement is read-only",
		})
	})
	registry := NewProviderRegistry(database)
	if _, err := registry.Upsert(LocalProvider{
		Name: "live", Type: "ollama", BaseURL: baseURL, Model: model, Enabled: true,
	}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	svc := NewIntelligenceService(database, registry, agents, native)

	type row struct {
		mode      string
		trace     IntelligenceTrace
		wallClock time.Duration
	}
	var results []row

	// Alternate so a drifting provider or account state hits both modes evenly.
	modes := []string{}
	for i := 0; i < runs; i++ {
		modes = append(modes, ModeCloudOnly, ModeLocalPreprocessCloud)
	}

	for i, mode := range modes {
		agentID := fmt.Sprintf("live%02d", i)
		if _, err := database.Exec(
			`INSERT INTO agents(id,preset,name,tmux_session,working_dir,command,args,status,color_hue,color_name,created_at,updated_at)
			 VALUES(?,'claude-code','live','',?,'claude','[]','running',220,'blue',datetime('now'),datetime('now'))`,
			agentID, repo); err != nil {
			t.Fatalf("insert agent: %v", err)
		}
		// plan mode: the run must never modify the repository it is measuring.
		if err := native.Start(agentID, "claude", repo, "", "", "plan", ""); err != nil {
			t.Fatalf("start native session: %v", err)
		}

		started := time.Now()
		res, err := svc.Run(context.Background(), IntelligenceRunRequest{
			AgentID: agentID, Task: task, Mode: mode, Provider: "live",
		})
		if err != nil {
			t.Logf("run %d (%s) returned error: %v", i, mode, err)
		}
		final := waitForCloudCompletion(t, svc, res.Trace.ID, 15*time.Minute)
		results = append(results, row{mode: mode, trace: final, wallClock: time.Since(started)})
		native.Stop(agentID)

		t.Logf("run %d %-22s status=%-30s cloudKnown=%v cost=%.4f in=%d out=%d cacheRead=%d "+
			"raw=%d opt=%d localMs=%d wall=%s err=%s",
			i, mode, final.Status, final.CloudUsageKnown, final.CloudCostUSD,
			final.CloudInputTokens, final.CloudOutputTokens, final.CloudCacheReadTokens,
			final.RawTokens, final.OptimizedTokens, final.LatencyMS,
			time.Since(started).Truncate(time.Second), final.ErrorCode)
	}

	t.Log("=== PCD_SAVINGS_SUMMARY ===")
	for _, mode := range []string{ModeCloudOnly, ModeLocalPreprocessCloud} {
		var costs []float64
		var inputs []float64
		var walls []float64
		measured := 0
		for _, r := range results {
			if r.mode != mode {
				continue
			}
			walls = append(walls, r.wallClock.Seconds())
			if !r.trace.CloudUsageKnown {
				continue
			}
			measured++
			costs = append(costs, r.trace.CloudCostUSD)
			inputs = append(inputs, float64(r.trace.CloudInputTokens))
		}
		t.Logf("%-22s runs=%d measured=%d medianCostUSD=%s medianCloudInput=%s medianWallSec=%s",
			mode, len(walls), measured, fmtMedian(costs), fmtMedian(inputs), fmtMedian(walls))
	}
	t.Log("Interpretation: hybrid wins only if medianCostUSD is materially lower AND the " +
		"extra wall-clock is acceptable. measured=0 means the driver reported no usage " +
		"(Codex) — no conclusion may be drawn from those runs.")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func liveSavingsDB(t *testing.T) *sql.DB {
	t.Helper()
	path := t.TempDir() + "/live.db"
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// waitForCloudCompletion polls until the trace reaches a terminal state. The cloud
// turn closes asynchronously through observeNativeEvent, so the Run return value
// is only the dispatch snapshot.
func waitForCloudCompletion(t *testing.T, s *IntelligenceService, id string, limit time.Duration) IntelligenceTrace {
	t.Helper()
	deadline := time.Now().Add(limit)
	var last IntelligenceTrace
	for time.Now().Before(deadline) {
		tr, err := s.Trace(id)
		if err == nil {
			last = tr
			switch tr.Status {
			case "CLOUD_COMPLETED", "CLOUD_COMPLETED_WITH_FALLBACK", "SUCCESS", "FAILED":
				return tr
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("trace %s did not reach a terminal state within %s (last=%s)", id, limit, last.Status)
	return last
}

func fmtMedian(v []float64) string {
	if len(v) == 0 {
		return "n/a"
	}
	sort.Float64s(v)
	m := v[len(v)/2]
	if len(v)%2 == 0 {
		m = (v[len(v)/2-1] + v[len(v)/2]) / 2
	}
	return strconv.FormatFloat(m, 'f', 4, 64)
}
