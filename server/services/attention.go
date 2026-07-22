package services

import "sort"

// AttentionReason is one cause a session needs a human's eyes. A session can have
// several at once (waiting on approval AND stalled), so the model keeps them all
// rather than overwriting a single "kind" and losing the rest.
type AttentionReason struct {
	Kind  string `json:"kind"`            // "approval" | "error" | "stalled"
	Since int64  `json:"since"`           // ms epoch this condition began (best-effort)
	Count int    `json:"count,omitempty"` // e.g. number of pending approvals
}

// AgentAttention is the derived attention block: Primary drives the tile colour and
// sort order, Reasons carries the full picture for the detail view.
type AgentAttention struct {
	Primary string            `json:"primary"` // "" = normal, no attention
	Reasons []AttentionReason `json:"reasons"`
}

// UnreadCounts is the notification axis — deliberately SEPARATE from attention.
// "task_complete" is an unread event to acknowledge, not a state that needs action,
// so it lives here and never becomes an attention reason.
type UnreadCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Errors    int `json:"errors"`
}

// AgentSummary is the Control Room's per-session projection: enough to render a
// tile without watching the session's detailed stream. Assembled server-side and
// pushed as a batch; the client never computes it.
type AgentSummary struct {
	AgentID        string         `json:"agentId"`
	Revision       int            `json:"revision"` // monotonic per agent — client ignores lower
	Status         string         `json:"status"`   // running | stopped
	Preset         string         `json:"preset"`
	Name           string         `json:"name"`
	ColorHue       int            `json:"colorHue"`
	ProjectKey     string         `json:"projectKey"`
	ProjectLabel   string         `json:"projectLabel"`
	Attention      AgentAttention `json:"attention"`
	LastTool       string         `json:"lastTool,omitempty"`
	LastTarget     string         `json:"lastTarget,omitempty"`
	ToolCount      int            `json:"toolCount"`
	LastActivityAt int64          `json:"lastActivityAt"`
	Unread         UnreadCounts   `json:"unread"`
}

// AttentionThresholds are the config-backed tunables. Zero values fall back to
// sane defaults, so a nil/empty config never silently disables attention.
type AttentionThresholds struct {
	IdleMs         int64
	StartupGraceMs int64
}

func (t AttentionThresholds) withDefaults() AttentionThresholds {
	if t.IdleMs <= 0 {
		t.IdleMs = 10 * 60 * 1000
	}
	if t.StartupGraceMs <= 0 {
		t.StartupGraceMs = 60 * 1000
	}
	return t
}

// AttentionInput is everything ComputeAttention needs, passed explicitly so the
// function stays pure and unit-testable — no clock, no DB, no globals.
type AttentionInput struct {
	Status           string
	Now              int64
	StartedAt        int64 // ms; the session's activity start (0 = unknown)
	LastActivityAt   int64 // ms; last observed activity (0 = none)
	HasActivity      bool  // do we have activity data at all (Claude transcript exists)
	PendingApprovals int
	OldestApprovalAt int64
	UnreadErrors     int
	LastErrorAt      int64
}

// ComputeAttention derives the attention block. Multiple causes coexist; Primary is
// the highest-priority one by attnPriority (approval > error > stalled).
//
// "stalled" (the review's rename of "idle", to avoid clashing with the node-level
// "idle" status the activity model already uses) is intentionally conservative: an
// AI agent with no output may still be building/testing/thinking, so it fires only
// for a RUNNING agent that isn't waiting on approval, has real activity history,
// has been quiet past the idle threshold, and is past its startup grace window.
func ComputeAttention(in AttentionInput, th AttentionThresholds) AgentAttention {
	th = th.withDefaults()
	reasons := []AttentionReason{}

	if in.PendingApprovals > 0 {
		reasons = append(reasons, AttentionReason{
			Kind: "approval", Since: in.OldestApprovalAt, Count: in.PendingApprovals,
		})
	}
	if in.UnreadErrors > 0 {
		reasons = append(reasons, AttentionReason{
			Kind: "error", Since: in.LastErrorAt, Count: in.UnreadErrors,
		})
	}
	if in.Status == "running" &&
		in.PendingApprovals == 0 &&
		in.HasActivity &&
		in.LastActivityAt > 0 &&
		in.Now-in.LastActivityAt > th.IdleMs &&
		(in.StartedAt == 0 || in.Now-in.StartedAt > th.StartupGraceMs) {
		reasons = append(reasons, AttentionReason{Kind: "stalled", Since: in.LastActivityAt})
	}

	sort.SliceStable(reasons, func(i, j int) bool {
		return attnPriority(reasons[i].Kind) < attnPriority(reasons[j].Kind)
	})

	att := AgentAttention{Reasons: reasons}
	if len(reasons) > 0 {
		att.Primary = reasons[0].Kind
	}
	return att
}

func attnPriority(kind string) int {
	switch kind {
	case "approval":
		return 0
	case "error":
		return 1
	case "stalled":
		return 2
	}
	return 9
}
