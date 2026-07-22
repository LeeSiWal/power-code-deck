package services

import (
	"reflect"
	"sync"
	"time"
)

// ControlRoomService assembles the multi-session overview (v0.3.0 Control Room).
//
// It owns no new state of its own beyond a per-agent activity cache and revision
// bookkeeping — every summary is a projection of things that already exist:
// AgentService (status/name/dir), the PermissionBroker (pending approvals),
// ActivityManager snapshots (last tool/target), and NotificationService (unread).
//
// Delivery is change-based + periodically corrected, not a fixed per-session
// broadcast: state changes mark an agent dirty, a short timer coalesces the dirty
// set into ONE batch, and a slow timer re-checks everything to self-heal any missed
// trigger. Summaries whose meaningful fields didn't change are dropped before
// emit, so an idle deck goes quiet instead of streaming identical payloads.
type ControlRoomService struct {
	agents *AgentService
	broker *PermissionBroker
	notifs *NotificationService
	th     AttentionThresholds

	mu          sync.Mutex
	activity    map[string]AgentActivitySnapshot // latest snapshot per agent
	revision    map[string]int
	lastEmitted map[string]AgentSummary // revision-zeroed, for change detection
	dirty       map[string]struct{}
	emit        func([]AgentSummary)
	nowFn       func() int64

	// onStalled fires ONCE when an agent enters the stalled state; stalledNotified
	// tracks who is currently in a notified-stalled episode so a session that stays
	// quiet doesn't re-alert every batch. Cleared when the agent leaves stalled.
	onStalled       func(agentID string)
	stalledNotified map[string]bool
}

func NewControlRoomService(agents *AgentService, broker *PermissionBroker, notifs *NotificationService, th AttentionThresholds) *ControlRoomService {
	return &ControlRoomService{
		agents:      agents,
		broker:      broker,
		notifs:      notifs,
		th:          th,
		activity:        make(map[string]AgentActivitySnapshot),
		revision:        make(map[string]int),
		lastEmitted:     make(map[string]AgentSummary),
		dirty:           make(map[string]struct{}),
		stalledNotified: make(map[string]bool),
	}
}

// SetEmitter wires how a summary batch leaves the service (to the WS hub).
func (s *ControlRoomService) SetEmitter(fn func([]AgentSummary)) {
	s.mu.Lock()
	s.emit = fn
	s.mu.Unlock()
}

// SetOnStalled wires the callback fired once when a session enters the stalled state
// (a running agent quiet past the idle threshold). Used to raise a push notification.
func (s *ControlRoomService) SetOnStalled(fn func(agentID string)) {
	s.mu.Lock()
	s.onStalled = fn
	s.mu.Unlock()
}

func (s *ControlRoomService) now() int64 {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now().UnixMilli()
}

// OnActivity caches an agent's latest activity snapshot and marks it dirty — the
// tile's last tool/target and the stalled test both read from here.
func (s *ControlRoomService) OnActivity(agentID string, snap AgentActivitySnapshot) {
	s.mu.Lock()
	s.activity[agentID] = snap
	s.dirty[agentID] = struct{}{}
	s.mu.Unlock()
}

// MarkDirty flags one agent for recomputation on the next batch tick. Empty id is
// ignored. Called on approvals, notifications, and lifecycle changes.
func (s *ControlRoomService) MarkDirty(agentID string) {
	if agentID == "" {
		return
	}
	s.mu.Lock()
	s.dirty[agentID] = struct{}{}
	s.mu.Unlock()
}

// Run starts the two timers: batchInterval coalesces the dirty set into one emit;
// correctionInterval re-checks every agent to recover from any missed trigger.
// Blocks; run in a goroutine.
func (s *ControlRoomService) Run(batchInterval, correctionInterval time.Duration) {
	if batchInterval <= 0 {
		batchInterval = 750 * time.Millisecond
	}
	if correctionInterval <= 0 {
		correctionInterval = 60 * time.Second
	}
	batch := time.NewTicker(batchInterval)
	correct := time.NewTicker(correctionInterval)
	defer batch.Stop()
	defer correct.Stop()
	for {
		select {
		case <-batch.C:
			s.recompute(true)
		case <-correct.C:
			s.recompute(false)
		}
	}
}

// recompute builds summaries (dirty-only, or all for a correction pass), keeps only
// the ones that actually changed, bumps their revision, and emits one batch.
func (s *ControlRoomService) recompute(onlyDirty bool) {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = make(map[string]struct{})
	emit := s.emit
	s.mu.Unlock()
	if emit == nil {
		return
	}
	if onlyDirty && len(dirty) == 0 {
		return
	}

	agents, err := s.agents.List()
	if err != nil {
		return
	}
	now := s.now()
	live := make(map[string]struct{}, len(agents))
	var out []AgentSummary
	for _, a := range agents {
		live[a.ID] = struct{}{}
		if onlyDirty {
			if _, ok := dirty[a.ID]; !ok {
				continue
			}
		}
		sum := s.compute(a, now)
		s.checkStalledTransition(a.ID, sum.Attention.Primary == "stalled")
		if s.changedAndStore(&sum) {
			out = append(out, sum)
		}
	}
	// A correction pass also forgets bookkeeping for agents that no longer exist,
	// so revision/lastEmitted maps don't grow without bound.
	if !onlyDirty {
		s.mu.Lock()
		for id := range s.lastEmitted {
			if _, ok := live[id]; !ok {
				delete(s.lastEmitted, id)
				delete(s.revision, id)
				delete(s.activity, id)
				delete(s.stalledNotified, id)
			}
		}
		s.mu.Unlock()
	}
	if len(out) > 0 {
		emit(out)
	}
}

// Summaries is the full snapshot for the REST initial load. It does NOT bump
// revisions (a read, not a change) — it reports each agent's current revision so a
// later delta strictly increases.
func (s *ControlRoomService) Summaries() []AgentSummary {
	agents, err := s.agents.List()
	if err != nil {
		return []AgentSummary{}
	}
	now := s.now()
	out := make([]AgentSummary, 0, len(agents))
	for _, a := range agents {
		sum := s.compute(a, now)
		s.mu.Lock()
		sum.Revision = s.revision[a.ID]
		s.mu.Unlock()
		out = append(out, sum)
	}
	return out
}

// compute projects one agent into a summary. Revision is left 0 here; the caller
// assigns it (bump on change, or read-through for the snapshot).
func (s *ControlRoomService) compute(a Agent, now int64) AgentSummary {
	s.mu.Lock()
	snap := s.activity[a.ID]
	s.mu.Unlock()

	sum := AgentSummary{
		AgentID:      a.ID,
		Status:       a.Status,
		Preset:       a.Preset,
		Name:         a.Name,
		ColorHue:     a.ColorHue,
		ProjectKey:   ProjectKey(a.WorkingDir),
		ProjectLabel: ProjectLabel(a.WorkingDir),
	}

	in := AttentionInput{Status: a.Status, Now: now}

	if main := mainActivityNode(snap); main != nil {
		in.HasActivity = true
		in.StartedAt = main.StartedAt
		in.LastActivityAt = main.LastActivityAt
		sum.LastActivityAt = main.LastActivityAt
		sum.ToolCount = main.ToolCount
		sum.LastTool = main.CurrentTool
		sum.LastTarget = main.CurrentTarget
	}
	// When no tool is in flight, fall back to the most recent finished tool so the
	// tile shows what it last did instead of a blank.
	if sum.LastTool == "" && len(snap.Recent) > 0 {
		r := snap.Recent[0] // recent is newest-first
		sum.LastTool = r.Tool
		sum.LastTarget = r.Target
	}

	if pending, oldest := s.pendingApprovals(a.ID); pending > 0 {
		in.PendingApprovals = pending
		in.OldestApprovalAt = oldest
	}

	if s.notifs != nil {
		if uc, err := s.notifs.UnreadCounts(a.ID); err == nil {
			sum.Unread = uc
			in.UnreadErrors = uc.Errors
		}
	}

	sum.Attention = ComputeAttention(in, s.th)
	return sum
}

// pendingApprovals returns how many approvals a session is blocked on and the ms
// epoch of the oldest (the "since" the tile counts up from).
func (s *ControlRoomService) pendingApprovals(agentID string) (int, int64) {
	if s.broker == nil {
		return 0, 0
	}
	reqs := s.broker.Pending(agentID)
	if len(reqs) == 0 {
		return 0, 0
	}
	oldest := reqs[0].AskedAt
	for _, r := range reqs[1:] {
		if r.AskedAt.Before(oldest) {
			oldest = r.AskedAt
		}
	}
	return len(reqs), oldest.UnixMilli()
}

// changedAndStore compares a freshly computed summary (revision 0) against the last
// one emitted for that agent. Unchanged → false (dropped). Changed → bump the
// revision, stamp it onto sum, remember the new baseline, return true.
func (s *ControlRoomService) changedAndStore(sum *AgentSummary) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.lastEmitted[sum.AgentID]
	baseline := *sum // Revision still 0 at this point
	if ok && reflect.DeepEqual(prev, baseline) {
		return false
	}
	s.revision[sum.AgentID]++
	sum.Revision = s.revision[sum.AgentID]
	s.lastEmitted[sum.AgentID] = baseline
	return true
}

// checkStalledTransition fires onStalled exactly once as an agent crosses into the
// stalled state, and re-arms when it leaves — so a session that stays quiet doesn't
// re-notify on every batch/correction pass.
func (s *ControlRoomService) checkStalledTransition(agentID string, isStalled bool) {
	s.mu.Lock()
	was := s.stalledNotified[agentID]
	fire := false
	if isStalled && !was {
		s.stalledNotified[agentID] = true
		fire = true
	} else if !isStalled && was {
		delete(s.stalledNotified, agentID)
	}
	cb := s.onStalled
	s.mu.Unlock()
	if fire && cb != nil {
		cb(agentID)
	}
}

// mainActivityNode returns the "main" node of a snapshot, or nil if absent.
func mainActivityNode(snap AgentActivitySnapshot) *ActivityNode {
	for i := range snap.Nodes {
		if snap.Nodes[i].ID == mainNodeID {
			return &snap.Nodes[i]
		}
	}
	return nil
}
