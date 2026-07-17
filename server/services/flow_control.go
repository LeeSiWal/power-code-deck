package services

import (
	"sync"
	"time"
)

// flowControl implements ACK-based backpressure between a session's PTY read
// pump and its (single, exclusive) attached viewer — the same scheme VS Code's
// integrated terminal uses. The read pump blocks once more than `high` bytes are
// in flight unacknowledged, and resumes only after the viewer acks enough to
// drain back under `low` (hysteresis avoids thrash). This throttles a flooding
// process (e.g. `cat huge.log`, a chatty build) to the speed the browser can
// actually parse and paint, instead of drowning the WebSocket and jamming the
// main thread — the usual source of render "잔상"/jank on big bursts.
//
// Safety, given the frequent iPad↔phone handoff (exclusive-viewer auto-reclaim):
//   - Backpressure engages ONLY while a viewer is attached.
//   - On every viewer change the in-flight counter is reset, so a handoff can
//     never leave the pump wedged on a stale count.
//   - A max-pause deadline self-heals a lost ack (e.g. a backgrounded mobile tab
//     that stops acking), so the pump can never block forever.
type flowControl struct {
	mu        sync.Mutex
	cond      *sync.Cond
	unacked   int
	hasViewer bool
	high      int
	low       int
	maxPause  time.Duration
	now       func() time.Time // overridable in tests
}

const (
	flowHighWater = 256 * 1024
	flowLowWater  = 64 * 1024
	flowMaxPause  = 3 * time.Second
)

func newFlowControl() *flowControl {
	f := &flowControl{
		high:     flowHighWater,
		low:      flowLowWater,
		maxPause: flowMaxPause,
		now:      time.Now,
	}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// added records bytes handed to the viewer (increasing the in-flight count).
func (f *flowControl) added(n int) {
	f.mu.Lock()
	f.unacked += n
	f.mu.Unlock()
}

// ack records bytes the viewer confirmed it processed, waking the pump if this
// drains the backlog. n may legitimately exceed the tracked count (a reattaching
// viewer acks replay bytes the server never metered) — the counter floors at 0.
func (f *flowControl) ack(n int) {
	f.mu.Lock()
	f.unacked -= n
	if f.unacked < 0 {
		f.unacked = 0
	}
	f.cond.Broadcast()
	f.mu.Unlock()
}

// setViewers reflects the current viewer count. Any change resets the in-flight
// counter (the previous count belonged to a viewer that may be gone) and wakes a
// blocked pump — so a detach or handoff never strands it.
func (f *flowControl) setViewers(n int) {
	f.mu.Lock()
	f.hasViewer = n > 0
	f.unacked = 0
	f.cond.Broadcast()
	f.mu.Unlock()
}

// saturated reports whether the pump would block right now (viewer attached and
// over the high-water mark). Exposed for tests and introspection.
func (f *flowControl) saturated() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasViewer && f.unacked > f.high
}

// wait blocks the read pump while the viewer is saturated, returning when the
// backlog drains under `low`, the viewer detaches, or the max-pause deadline
// hits (lost-ack self-heal). It is a cheap no-op on the common unsaturated path.
func (f *flowControl) wait() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasViewer || f.unacked <= f.high {
		return // fast path: not saturated — no timer, no blocking
	}
	deadline := f.now().Add(f.maxPause)
	// The timer only wakes Wait; the loop predicate re-checks the clock and
	// exits. No shared "timed out" flag, so nothing leaks between wait() calls.
	timer := time.AfterFunc(f.maxPause, func() {
		f.mu.Lock()
		f.cond.Broadcast()
		f.mu.Unlock()
	})
	defer timer.Stop()
	for f.hasViewer && f.unacked > f.low && f.now().Before(deadline) {
		f.cond.Wait()
	}
}
