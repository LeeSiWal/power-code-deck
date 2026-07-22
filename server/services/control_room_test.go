package services

import "testing"

// The projection + change-detection logic is exercised without a DB: compute() only
// touches the (nil-guarded) broker/notifs and the in-memory activity cache, and
// changedAndStore() is pure over the revision/lastEmitted maps.
func TestControlRoomComputeAndChangeDetection(t *testing.T) {
	cr := NewControlRoomService(nil, nil, nil, AttentionThresholds{})
	now := int64(1_000_000_000_000)

	agent := Agent{ID: "a1", Name: "api", Preset: "claude", Status: "running", WorkingDir: "/home/u/proj", ColorHue: 220}

	// Seed a live activity snapshot: a Bash tool in flight on the main node.
	cr.OnActivity("a1", AgentActivitySnapshot{
		Nodes: []ActivityNode{{
			ID: mainNodeID, Kind: "main", ToolCount: 5,
			CurrentTool: "Bash", CurrentTarget: "build.sh",
			StartedAt: now - 60_000, LastActivityAt: now - 1_000,
		}},
	})

	sum := cr.compute(agent, now)
	if sum.LastTool != "Bash" || sum.LastTarget != "build.sh" || sum.ToolCount != 5 {
		t.Fatalf("summary did not reflect activity: %+v", sum)
	}
	if sum.ProjectLabel == "" || sum.ProjectKey == "" {
		t.Fatalf("project grouping fields missing: %+v", sum)
	}
	if sum.Attention.Primary != "" {
		t.Fatalf("recently-active agent should have no attention, got %q", sum.Attention.Primary)
	}

	// First store → changed (revision 1). Identical recompute → unchanged (dropped).
	s1 := cr.compute(agent, now)
	if !cr.changedAndStore(&s1) || s1.Revision != 1 {
		t.Fatalf("first summary should be a change at revision 1, got rev %d", s1.Revision)
	}
	s2 := cr.compute(agent, now)
	if cr.changedAndStore(&s2) {
		t.Fatalf("identical summary should be dropped, not re-emitted")
	}

	// A real change (new tool) → emitted again at revision 2.
	cr.OnActivity("a1", AgentActivitySnapshot{
		Nodes: []ActivityNode{{
			ID: mainNodeID, Kind: "main", ToolCount: 6,
			CurrentTool: "Edit", CurrentTarget: "main.go",
			StartedAt: now - 60_000, LastActivityAt: now,
		}},
	})
	s3 := cr.compute(agent, now)
	if !cr.changedAndStore(&s3) || s3.Revision != 2 {
		t.Fatalf("changed summary should emit at revision 2, got rev %d changed=%v", s3.Revision, s3.LastTool)
	}
}
