package services

import "testing"

func TestTerminalModes_BasicSetReset(t *testing.T) {
	m := newTerminalModes()
	m.scan([]byte("\x1b[?1049h\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h"))
	got := string(m.prefix())
	// modeReplayOrder: 1049 first, then mouse modes in order.
	want := "\x1b[?1049h\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h"
	if got != want {
		t.Fatalf("prefix = %q, want %q", got, want)
	}

	// Disabling mouse tracking drops it from the prefix; alt-screen stays.
	m.scan([]byte("\x1b[?1000l\x1b[?1002l\x1b[?1003l"))
	got = string(m.prefix())
	if want := "\x1b[?1049h\x1b[?1006h"; got != want {
		t.Fatalf("after reset prefix = %q, want %q", got, want)
	}
}

func TestTerminalModes_MultiModeInOneSequence(t *testing.T) {
	m := newTerminalModes()
	m.scan([]byte("\x1b[?1000;1002;1003;1006h"))
	got := string(m.prefix())
	if want := "\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h"; got != want {
		t.Fatalf("prefix = %q, want %q", got, want)
	}
}

func TestTerminalModes_SplitAcrossReads(t *testing.T) {
	m := newTerminalModes()
	// The enable sequence is split mid-sequence across two PTY reads.
	m.scan([]byte("some output\x1b[?10"))
	m.scan([]byte("49h more output"))
	if got := string(m.prefix()); got != "\x1b[?1049h" {
		t.Fatalf("split prefix = %q, want %q", got, "\x1b[?1049h")
	}
}

func TestTerminalModes_SplitAtTerminator(t *testing.T) {
	m := newTerminalModes()
	m.scan([]byte("\x1b[?1000")) // number complete, terminator not yet seen
	m.scan([]byte("h"))
	if got := string(m.prefix()); got != "\x1b[?1000h" {
		t.Fatalf("split-terminator prefix = %q, want %q", got, "\x1b[?1000h")
	}
}

func TestTerminalModes_IgnoresUntracked(t *testing.T) {
	m := newTerminalModes()
	// 12 (blinking cursor) and 2026 are not in the tracked set; 2004 is.
	m.scan([]byte("\x1b[?12h\x1b[?2004h\x1b[?2026h"))
	if got := string(m.prefix()); got != "\x1b[?2004h" {
		t.Fatalf("prefix = %q, want %q", got, "\x1b[?2004h")
	}
}

func TestTerminalModes_EmptyWhenNothingSet(t *testing.T) {
	m := newTerminalModes()
	m.scan([]byte("plain text, no escapes at all"))
	if got := m.prefix(); got != nil {
		t.Fatalf("prefix = %q, want nil", got)
	}
}
