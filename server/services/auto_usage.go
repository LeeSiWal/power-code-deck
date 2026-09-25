package services

import (
	"database/sql"
	"sync"
	"time"
)

// TurnMeta is what routing did right before a turn of a "자동" session.
// Switch: "" (stayed), "start" (first message), "model" or "tool".
type TurnMeta struct {
	Switch        string
	HandoffTokens int
	// IdleSeconds is how long the session had been idle; -1 = unknown.
	IdleSeconds int
}

// AutoUsage records every finished turn of an auto session with the usage the
// CLI reported, so routing's cost can be read from facts rather than guessed.
type AutoUsage struct {
	db      *sql.DB
	mu      sync.Mutex
	pending map[string]TurnMeta
}

func NewAutoUsage(db *sql.DB) *AutoUsage {
	return &AutoUsage{db: db, pending: map[string]TurnMeta{}}
}

// Note stashes routing's decision for the session's next turn.
func (u *AutoUsage) Note(agentID string, m TurnMeta) {
	u.mu.Lock()
	u.pending[agentID] = m
	u.mu.Unlock()
}

// Observe is a NativeService event observer: a result closes a turn.
func (u *AutoUsage) Observe(agentID string, ev *StreamEvent) {
	if ev == nil || ev.Type != "result" {
		return
	}
	var preset, command, auto, model, effort string
	if err := u.db.QueryRow(`SELECT preset, command, COALESCE(auto_profile, ''), COALESCE(native_model, ''), COALESCE(native_effort, '')
		FROM agents WHERE id = ?`, agentID).Scan(&preset, &command, &auto, &model, &effort); err != nil || auto == "" {
		return
	}
	u.mu.Lock()
	m, ok := u.pending[agentID]
	delete(u.pending, agentID)
	u.mu.Unlock()
	if !ok {
		m = TurnMeta{IdleSeconds: -1}
	}
	tool := nativeDriverFor(preset, command)
	if tool != "claude" {
		effort = ""
	}
	var idle any
	if m.IdleSeconds >= 0 {
		idle = m.IdleSeconds
	}
	var in, out, cc, cr any
	if ev.Usage != nil {
		in, out, cc, cr = ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.CacheCreationInputTokens, ev.Usage.CacheReadInputTokens
	}
	_, _ = u.db.Exec(`INSERT INTO auto_turns (agent_id, tool, model, effort, switch_kind, handoff_tokens, idle_seconds, input_tokens, output_tokens, cache_creation, cache_read)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, agentID, tool, model, effort, m.Switch, m.HandoffTokens, idle, in, out, cc, cr)
}

// ModelUsage is the usage of one tool/model/effort. Reported counts the turns
// whose CLI reported usage; token sums cover only those.
type ModelUsage struct {
	Tool          string `json:"tool"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	Turns         int    `json:"turns"`
	Reported      int    `json:"reported"`
	Input         int64  `json:"input"`
	Output        int64  `json:"output"`
	CacheCreation int64  `json:"cacheCreation"`
	CacheRead     int64  `json:"cacheRead"`
}

// IdleBucket measures the prompt cache after a pause: of the input read on
// turns that followed an idle gap in this range, how much came from cache.
type IdleBucket struct {
	Label      string `json:"label"`
	MinSeconds int    `json:"minSeconds"`
	Turns      int    `json:"turns"`
	Reported   int    `json:"reported"`
	CacheRead  int64  `json:"cacheRead"`
	InputTotal int64  `json:"inputTotal"` // input + cache creation + cache read
}

type UsageSummary struct {
	Turns          int          `json:"turns"`
	Models         []ModelUsage `json:"models"`
	ModelSwitches  int          `json:"modelSwitches"`
	ToolSwitches   int          `json:"toolSwitches"`
	HandoffTokens  int64        `json:"handoffTokens"` // estimated, from the handoff text size
	Idle           []IdleBucket `json:"idle"`
	IdleThresholdS int          `json:"idleThresholdSeconds"`
}

// idleBuckets split idle gaps around the 5-minute prompt cache lifetime.
var idleBuckets = []IdleBucket{{Label: "1분 미만", MinSeconds: 0}, {Label: "1–5분", MinSeconds: 60}, {Label: "5–10분", MinSeconds: 300}, {Label: "10분 이상", MinSeconds: 600}}

// Summary aggregates one session (agentID != "") or every auto session since.
func (u *AutoUsage) Summary(agentID string, since time.Time) (UsageSummary, error) {
	where, args := "created_at >= ?", []any{since.UTC().Format("2006-01-02 15:04:05")}
	if agentID != "" {
		where, args = "agent_id = ?", []any{agentID}
	}
	sum := UsageSummary{Models: []ModelUsage{}}
	rows, err := u.db.Query(`SELECT tool, model, effort, COUNT(*), COUNT(input_tokens), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_creation),0), COALESCE(SUM(cache_read),0) FROM auto_turns WHERE `+where+` GROUP BY tool, model, effort ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return sum, err
	}
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Tool, &m.Model, &m.Effort, &m.Turns, &m.Reported, &m.Input, &m.Output, &m.CacheCreation, &m.CacheRead); err != nil {
			rows.Close()
			return sum, err
		}
		sum.Turns += m.Turns
		sum.Models = append(sum.Models, m)
	}
	rows.Close()
	_ = u.db.QueryRow(`SELECT COALESCE(SUM(switch_kind = 'model'),0), COALESCE(SUM(switch_kind = 'tool'),0), COALESCE(SUM(handoff_tokens),0)
		FROM auto_turns WHERE `+where, args...).Scan(&sum.ModelSwitches, &sum.ToolSwitches, &sum.HandoffTokens)
	for i, b := range idleBuckets {
		max := 1 << 30
		if i+1 < len(idleBuckets) {
			max = idleBuckets[i+1].MinSeconds
		}
		_ = u.db.QueryRow(`SELECT COUNT(*), COUNT(input_tokens), COALESCE(SUM(cache_read),0),
			COALESCE(SUM(input_tokens + cache_creation + cache_read),0) FROM auto_turns
			WHERE `+where+` AND idle_seconds IS NOT NULL AND idle_seconds >= ? AND idle_seconds < ?`,
			append(append([]any{}, args...), b.MinSeconds, max)...).Scan(&b.Turns, &b.Reported, &b.CacheRead, &b.InputTotal)
		sum.Idle = append(sum.Idle, b)
	}
	return sum, nil
}
