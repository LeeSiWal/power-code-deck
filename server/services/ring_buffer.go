package services

import ptyruntime "powercodedeck/internal/runtime/pty"

// RingBuffer preserves the legacy scrollback API.
type RingBuffer = ptyruntime.RingBuffer

func NewRingBuffer(capacity int) *RingBuffer {
	return ptyruntime.NewRingBuffer(capacity)
}
