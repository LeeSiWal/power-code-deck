package services

import (
	"testing"
	"time"
)

// A viewer under the high-water mark never blocks the pump.
func TestFlowControlNotSaturated(t *testing.T) {
	f := newFlowControl()
	f.setViewers(1)
	f.added(f.high) // exactly at, not over
	if f.saturated() {
		t.Fatalf("at high-water should not be saturated")
	}
	done := make(chan struct{})
	go func() { f.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait() blocked while not saturated")
	}
}

// With no viewer attached, backpressure never engages no matter how much is
// "in flight" — output must keep flowing into the ring for later replay.
func TestFlowControlNoViewerNeverBlocks(t *testing.T) {
	f := newFlowControl()
	f.added(f.high * 4)
	if f.saturated() {
		t.Fatalf("no viewer should never be saturated")
	}
	done := make(chan struct{})
	go func() { f.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait() blocked with no viewer")
	}
}

// Over the high-water mark the pump blocks, and an ack that drains under the
// low-water mark releases it (hysteresis: an ack merely to just-under-high does
// NOT release).
func TestFlowControlBlocksUntilDrainedToLow(t *testing.T) {
	f := newFlowControl()
	f.maxPause = time.Hour // isolate the ack path from the self-heal timeout
	f.setViewers(1)
	f.added(f.high + 1)
	if !f.saturated() {
		t.Fatal("over high-water should be saturated")
	}

	released := make(chan struct{})
	go func() { f.wait(); close(released) }()
	time.Sleep(10 * time.Millisecond) // let wait() reach the blocked state before acking

	// Ack down to just above low — must stay blocked (hysteresis).
	f.ack(f.high - f.low) // unacked = low + 1
	select {
	case <-released:
		t.Fatal("released before draining under low-water")
	case <-time.After(50 * time.Millisecond):
	}

	// Ack under low — must release.
	f.ack(2) // unacked = low - 1
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("did not release after draining under low-water")
	}
}

// A detach (viewer count → 0) releases a blocked pump immediately — the core
// guard against a handoff wedging the session.
func TestFlowControlDetachReleases(t *testing.T) {
	f := newFlowControl()
	f.maxPause = time.Hour
	f.setViewers(1)
	f.added(f.high + 1)

	released := make(chan struct{})
	go func() { f.wait(); close(released) }()
	time.Sleep(10 * time.Millisecond) // let it reach the blocked state

	f.setViewers(0)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("detach did not release the blocked pump")
	}
}

// A viewer that stops acking (backgrounded tab) can't wedge the pump forever —
// the max-pause deadline self-heals it.
func TestFlowControlLostAckSelfHeals(t *testing.T) {
	f := newFlowControl()
	f.maxPause = 40 * time.Millisecond
	f.setViewers(1)
	f.added(f.high + 1)

	start := time.Now()
	f.wait() // no ack ever arrives
	elapsed := time.Since(start)
	if elapsed < f.maxPause {
		t.Fatalf("released too early (%v < %v) — timeout not honored", elapsed, f.maxPause)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("self-heal timeout did not fire (blocked %v)", elapsed)
	}
}

// A reattaching viewer acks replay bytes the server never metered; the counter
// must floor at zero rather than go negative (which would disable backpressure).
func TestFlowControlAckFloorsAtZero(t *testing.T) {
	f := newFlowControl()
	f.setViewers(1)
	f.added(1000)
	f.ack(100000) // acks far more than was sent (replay)
	f.added(f.high + 1)
	if !f.saturated() {
		t.Fatal("over-ack must floor at 0, not leave a negative credit that disables backpressure")
	}
}
