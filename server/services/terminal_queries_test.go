package services

import (
	"bytes"
	"testing"
)

// A single chunk carrying both startup DECRQM queries gets both answered, in a
// form the app accepts (2026 recognized/reset, 2027 not recognized). The Kitty
// keyboard query (ESC[?u) in the same burst is deliberately left unanswered.
func TestPtyQueryResponderStartupBurst(t *testing.T) {
	var r ptyQueryResponder
	// The exact prefix a 2026/2027-probing TUI emits on launch.
	burst := []byte("\x1b[?2026$p\x1b[?2027$p\x1b[>4m\x1b[=0;1u\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[?u\x1b[H\x1b[2J")
	got := r.respond(burst)
	want := []byte("\x1b[?2026;2$y\x1b[?2027;0$y")
	if !bytes.Equal(got, want) {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	if bytes.Contains(got, []byte("u")) {
		t.Fatalf("must not answer the Kitty keyboard query, got %q", got)
	}
}

// No queries → no reply (the common case for Claude Code / Codex output).
func TestPtyQueryResponderNoQuery(t *testing.T) {
	var r ptyQueryResponder
	if got := r.respond([]byte("hello \x1b[32mworld\x1b[0m\r\n")); got != nil {
		t.Fatalf("reply = %q, want nil", got)
	}
}

// Any private DECRQM query is now answered generically: a mode we don't specially
// know reports "not recognized" (0), which still unblocks an app waiting on a
// reply — so a new TUI that probes some other mode no longer hangs.
func TestPtyQueryResponderGenericMode(t *testing.T) {
	var r ptyQueryResponder
	got := r.respond([]byte("\x1b[?1049$p"))
	if want := []byte("\x1b[?1049;0$y"); !bytes.Equal(got, want) {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

// A mode SET (ESC[?25h) and other non-query CSI sequences must NOT be answered —
// only actual `$p` queries are.
func TestPtyQueryResponderIgnoresNonQueries(t *testing.T) {
	var r ptyQueryResponder
	// cursor hide, alt-screen enable, SGR, bracketed-paste enable — all sets, no query.
	if got := r.respond([]byte("\x1b[?25l\x1b[?1049h\x1b[0m\x1b[?2004h")); got != nil {
		t.Fatalf("reply = %q, want nil (no query present)", got)
	}
}

// A query split across two reads is answered exactly once, when the second half
// arrives — and never double-answered from the carried prefix.
func TestPtyQueryResponderSplitAcrossReads(t *testing.T) {
	var r ptyQueryResponder
	if got := r.respond([]byte("noise\x1b[?202")); got != nil {
		t.Fatalf("first half should not answer yet, got %q", got)
	}
	got := r.respond([]byte("6$p more"))
	if !bytes.Equal(got, []byte("\x1b[?2026;2$y")) {
		t.Fatalf("second half reply = %q, want the 2026 answer", got)
	}
	// A following unrelated chunk must not re-answer from any stale carry.
	if got := r.respond([]byte("plain text")); got != nil {
		t.Fatalf("stale carry re-answered: %q", got)
	}
}
