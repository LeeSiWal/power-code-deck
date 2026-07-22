package services

import (
	"reflect"
	"testing"
)

func TestComputeAttentionPriority(t *testing.T) {
	th := AttentionThresholds{IdleMs: 10 * 60 * 1000, StartupGraceMs: 60 * 1000}
	now := int64(1_000_000_000_000)

	// Approval + stalled coexist → both kept, approval is primary.
	att := ComputeAttention(AttentionInput{
		Status: "running", Now: now,
		StartedAt: now - 30*60*1000, LastActivityAt: now - 20*60*1000, HasActivity: true,
		PendingApprovals: 2, OldestApprovalAt: now - 5*60*1000,
	}, th)
	if att.Primary != "approval" {
		t.Fatalf("primary = %q, want approval", att.Primary)
	}
	// stalled must NOT appear while an approval is pending (approval blocks the
	// stalled test), so only the approval reason is present.
	if len(att.Reasons) != 1 || att.Reasons[0].Kind != "approval" || att.Reasons[0].Count != 2 {
		t.Fatalf("reasons = %+v, want single approval count 2", att.Reasons)
	}

	// Error + stalled, no approval → error primary, both listed in priority order.
	att = ComputeAttention(AttentionInput{
		Status: "running", Now: now,
		StartedAt: now - 30*60*1000, LastActivityAt: now - 20*60*1000, HasActivity: true,
		UnreadErrors: 1, LastErrorAt: now - 3*60*1000,
	}, th)
	kinds := []string{}
	for _, r := range att.Reasons {
		kinds = append(kinds, r.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"error", "stalled"}) {
		t.Fatalf("kinds = %v, want [error stalled]", kinds)
	}
}

func TestComputeAttentionStalledGuards(t *testing.T) {
	th := AttentionThresholds{IdleMs: 10 * 60 * 1000, StartupGraceMs: 60 * 1000}
	now := int64(1_000_000_000_000)
	base := AttentionInput{
		Status: "running", Now: now,
		StartedAt: now - 30*60*1000, LastActivityAt: now - 20*60*1000, HasActivity: true,
	}
	if ComputeAttention(base, th).Primary != "stalled" {
		t.Fatal("expected stalled for a long-quiet running agent")
	}

	// Not running → never stalled.
	stopped := base
	stopped.Status = "stopped"
	if ComputeAttention(stopped, th).Primary != "" {
		t.Error("stopped agent should not be stalled")
	}

	// Within startup grace → not stalled even if quiet.
	fresh := base
	fresh.StartedAt = now - 10*1000 // 10s ago, inside 60s grace
	if ComputeAttention(fresh, th).Primary == "stalled" {
		t.Error("agent inside startup grace should not be stalled")
	}

	// No activity history → not stalled (can't tell quiet from building).
	noAct := base
	noAct.HasActivity = false
	if ComputeAttention(noAct, th).Primary == "stalled" {
		t.Error("agent without activity data should not be stalled")
	}

	// Recently active → not stalled.
	active := base
	active.LastActivityAt = now - 30*1000
	if ComputeAttention(active, th).Primary == "stalled" {
		t.Error("recently active agent should not be stalled")
	}
}

func TestComputeAttentionNormal(t *testing.T) {
	att := ComputeAttention(AttentionInput{Status: "running", Now: 1000, HasActivity: true, LastActivityAt: 900}, AttentionThresholds{})
	if att.Primary != "" {
		t.Errorf("primary = %q, want empty", att.Primary)
	}
	if att.Reasons == nil {
		t.Error("Reasons should be non-nil empty slice for stable JSON")
	}
}
