package services

import "sync"

// RingBuffer is a fixed-capacity byte buffer that keeps the most recent bytes
// written to it. It backs per-session terminal scrollback: on Attach the engine
// replays a snapshot so a (re)connecting viewer sees the current screen, while
// memory per session stays bounded.
type RingBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

// NewRingBuffer creates a buffer that retains at most `capacity` bytes.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 512 * 1024
	}
	return &RingBuffer{cap: capacity}
}

// Write appends p, discarding the oldest bytes so the total never exceeds cap.
func (r *RingBuffer) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(p) >= r.cap {
		// p alone overflows the buffer — keep only its tail.
		r.buf = append(r.buf[:0], p[len(p)-r.cap:]...)
		return
	}
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.cap {
		// Left-shift to drop the oldest bytes (forward copy is safe).
		excess := len(r.buf) - r.cap
		r.buf = append(r.buf[:0], r.buf[excess:]...)
	}
}

// Snapshot returns a copy of the current contents (oldest → newest).
func (r *RingBuffer) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// Len reports the number of retained bytes.
func (r *RingBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}
