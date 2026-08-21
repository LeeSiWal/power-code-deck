package services

import "testing"

// The CLI's result event carries the turn's usage and cost. StreamEvent typed the
// cost but dropped usage, so a trace could never record what the CLOUD actually
// spent — which is the only honest way to measure hybrid savings (raw−optimized
// compares against a baseline CLOUD_ONLY never sends).
func TestParseStreamEventCapturesResultUsage(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","total_cost_usd":0.0421,
		"usage":{"input_tokens":1200,"output_tokens":830,
		"cache_creation_input_tokens":15000,"cache_read_input_tokens":42000}}`)

	ev, err := ParseStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Usage == nil {
		t.Fatal("result event carried usage but StreamEvent dropped it")
	}
	if ev.Usage.InputTokens != 1200 {
		t.Errorf("InputTokens = %d, want 1200", ev.Usage.InputTokens)
	}
	if ev.Usage.OutputTokens != 830 {
		t.Errorf("OutputTokens = %d, want 830", ev.Usage.OutputTokens)
	}
	if ev.Usage.CacheCreationInputTokens != 15000 {
		t.Errorf("CacheCreationInputTokens = %d, want 15000", ev.Usage.CacheCreationInputTokens)
	}
	if ev.Usage.CacheReadInputTokens != 42000 {
		t.Errorf("CacheReadInputTokens = %d, want 42000", ev.Usage.CacheReadInputTokens)
	}
	if ev.TotalCostUSD != 0.0421 {
		t.Errorf("TotalCostUSD = %v, want 0.0421", ev.TotalCostUSD)
	}
}

// Usage is a POINTER so "the driver reported none" is distinguishable from "it
// reported zero". Codex is exactly the first case today (see nativeResultEvent),
// and a trace that silently shows 0 tokens for a real turn would be a lie.
func TestParseStreamEventLeavesUsageNilWhenAbsent(t *testing.T) {
	ev, err := ParseStreamEvent([]byte(`{"type":"result"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Usage != nil {
		t.Fatalf("Usage = %+v, want nil for a result event with no usage field", ev.Usage)
	}
}

// --- cloud consumption on the trace ------------------------------------------

func savingsTestService(t *testing.T) *IntelligenceService {
	t.Helper()
	db := intelligenceRunTestDB(t)
	return NewIntelligenceService(db, NewProviderRegistry(db), nil, nil)
}

// The closing result event is the only place the cloud's real spend is visible.
// Without it a trace can only report raw−optimized, which compares against a
// baseline CLOUD_ONLY never sends.
func TestObserveNativeEventRecordsCloudUsage(t *testing.T) {
	s := savingsTestService(t)
	tr := newTrace(IntelligenceRunRequest{AgentID: "a1", Mode: ModeLocalPreprocessCloud})
	tr.Status = "CLOUD_DISPATCHED"
	s.saveTrace(tr)
	s.mu.Lock()
	s.pending["a1"] = tr.ID
	s.mu.Unlock()

	s.observeNativeEvent("a1", &StreamEvent{
		Type:         StreamTypeResult,
		TotalCostUSD: 0.0421,
		Usage: &StreamUsage{
			InputTokens: 1200, OutputTokens: 830,
			CacheCreationInputTokens: 15000, CacheReadInputTokens: 42000,
		},
	})

	got, err := s.Trace(tr.ID)
	if err != nil {
		t.Fatalf("load trace: %v", err)
	}
	if !got.CloudUsageKnown {
		t.Fatal("CloudUsageKnown = false after a result event that reported usage")
	}
	if got.CloudCostUSD != 0.0421 {
		t.Errorf("CloudCostUSD = %v, want 0.0421", got.CloudCostUSD)
	}
	if got.CloudInputTokens != 1200 || got.CloudOutputTokens != 830 {
		t.Errorf("cloud tokens = %d/%d, want 1200/830", got.CloudInputTokens, got.CloudOutputTokens)
	}
	if got.CloudCacheReadTokens != 42000 {
		t.Errorf("CloudCacheReadTokens = %d, want 42000", got.CloudCacheReadTokens)
	}
	if got.Status != "CLOUD_COMPLETED" {
		t.Errorf("Status = %q, want CLOUD_COMPLETED", got.Status)
	}
}

// Codex reports no usage at all (verified — see codex_driver.go). Its traces must
// stay honestly blank rather than show a fabricated zero, or the savings
// comparison silently averages in turns that were never measured.
func TestObserveNativeEventLeavesCloudUsageUnknownWhenDriverReportsNone(t *testing.T) {
	s := savingsTestService(t)
	tr := newTrace(IntelligenceRunRequest{AgentID: "a2", Mode: ModeLocalPreprocessCloud})
	tr.Status = "CLOUD_DISPATCHED"
	s.saveTrace(tr)
	s.mu.Lock()
	s.pending["a2"] = tr.ID
	s.mu.Unlock()

	s.observeNativeEvent("a2", nativeResultEvent()) // exactly what the Codex path emits

	got, err := s.Trace(tr.ID)
	if err != nil {
		t.Fatalf("load trace: %v", err)
	}
	if got.CloudUsageKnown {
		t.Fatal("CloudUsageKnown = true although the driver reported no usage")
	}
	if got.CloudCostUSD != 0 || got.CloudInputTokens != 0 {
		t.Errorf("unreported usage leaked non-zero values: %+v", got)
	}
	if got.Status != "CLOUD_COMPLETED" {
		t.Errorf("Status = %q, want CLOUD_COMPLETED", got.Status)
	}
}
