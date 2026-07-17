package services

import (
	"bytes"
	"strconv"
)

// ptyQueryResponder answers the terminal capability queries that some full-screen
// TUIs emit at startup and then BLOCK on until they get a reply. Our PTY layer is
// a pass-through pipe — it never emulates a terminal — so nothing answered these
// and such an app hung with a blank (cleared) alt-screen: the "터미널에 표시되지
// 않는" bug.
//
// Antigravity's `agy` is the motivating case. On launch it enters the alt-screen,
// clears it, and emits DECRQM requests for synchronized output (mode 2026) and
// grapheme clustering (mode 2027) — then renders NOTHING until both are answered.
// Claude Code / Codex render immediately and treat such replies as optional, so
// they were never affected; only apps that gate their first paint on the reply
// (as a real terminal always sends one) were stuck.
//
// Our client's xterm parser does NOT reply to DECRQM (if it did, `agy` would have
// unblocked without us), so rather than hard-code one mode per newly-broken app we
// answer ANY private DECRQM query `ESC[?<n>$p` generically — the structural end of
// the "each new TUI blocks on a different mode" class. Known modes report their
// specific value; every other mode reports "not recognized" (0), the honest answer
// for a pass-through terminal that still unblocks an app waiting for a reply. We
// deliberately do NOT answer the Kitty keyboard query (ESC[?u) — it isn't a `$p`
// query so this parser skips it, keeping keyboard input in the legacy encoding our
// client actually parses.
type ptyQueryResponder struct {
	carry []byte // trailing partial query from the last scan (a query split across reads)
}

// decrqmValue is the DECRQM status to report for a private mode: 0 = not
// recognized, 1 = set, 2 = reset, 3 = permanently set, 4 = permanently reset.
// We only special-case the modes with a known-good answer; everything else is
// reported "not recognized", which unblocks the app without claiming a capability
// our renderer doesn't have.
func decrqmValue(mode int) int {
	switch mode {
	case 2026: // synchronized output — recognized but reset (we don't batch frames)
		return 2
	default: // includes 2027 (grapheme clustering) and any other probed mode
		return 0
	}
}

// decrqmReply builds the `ESC[?<mode>;<value>$y` answer for a queried mode.
func decrqmReply(mode int) []byte {
	out := append([]byte("\x1b[?"), []byte(strconv.Itoa(mode))...)
	out = append(out, ';')
	out = append(out, []byte(strconv.Itoa(decrqmValue(mode)))...)
	return append(out, '$', 'y')
}

// parse status for a candidate DECRQM query starting at an ESC byte.
const (
	decrqmNoMatch    = iota // definitely not a private DECRQM query — skip this ESC
	decrqmIncomplete        // could still become one — carry the tail and wait
	decrqmComplete          // a full ESC[?<n>$p — mode/end are set
)

// parseDECRQM inspects b (which begins with ESC) for a private DECRQM query of
// the form ESC [ ? <digits> $ p. It returns the parsed mode and the index just
// past the query on a complete match; decrqmIncomplete when b is a prefix that
// might still complete into one on the next read; decrqmNoMatch otherwise.
func parseDECRQM(b []byte) (mode, end, status int) {
	// b[0] == ESC (caller guarantees).
	if len(b) < 2 {
		return 0, 0, decrqmIncomplete // just ESC so far
	}
	if b[1] != '[' {
		return 0, 0, decrqmNoMatch
	}
	if len(b) < 3 {
		return 0, 0, decrqmIncomplete
	}
	if b[2] != '?' {
		return 0, 0, decrqmNoMatch // CSI but not a private (?) sequence
	}
	// Read the mode digits.
	i := 3
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	if i == 3 {
		// No digit after '?': ESC[?p, ESC[?u (Kitty), ESC[?25h, … — not a query.
		if i >= len(b) {
			return 0, 0, decrqmIncomplete // only ESC[? so far
		}
		return 0, 0, decrqmNoMatch
	}
	if i >= len(b) {
		return 0, 0, decrqmIncomplete // digits still arriving, no terminator yet
	}
	if b[i] != '$' {
		return 0, 0, decrqmNoMatch // ESC[?<n>h / l / … — a mode SET, not a query
	}
	if i+1 >= len(b) {
		return 0, 0, decrqmIncomplete // have '$', await the 'p'
	}
	if b[i+1] != 'p' {
		return 0, 0, decrqmNoMatch
	}
	m, err := strconv.Atoi(string(b[3:i]))
	if err != nil {
		return 0, 0, decrqmNoMatch
	}
	return m, i + 2, decrqmComplete
}

// respond scans a chunk of raw PTY output and returns the bytes to write back to
// the PTY in reply (nil when the chunk holds no query). A trailing partial query
// is carried between calls so a query split across two reads is still matched,
// and — because only an incomplete query is ever carried — a query is answered
// exactly once. Note the carry is for matching only; the full output is still
// forwarded to the client unchanged.
func (r *ptyQueryResponder) respond(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	win := data
	if len(r.carry) > 0 {
		win = append(append(make([]byte, 0, len(r.carry)+len(data)), r.carry...), data...)
	}

	var out []byte
	i := 0
	for i < len(win) {
		j := bytes.IndexByte(win[i:], 0x1b)
		if j < 0 {
			break // no more escape sequences
		}
		p := i + j
		mode, end, status := parseDECRQM(win[p:])
		switch status {
		case decrqmComplete:
			out = append(out, decrqmReply(mode)...)
			i = p + end
		case decrqmIncomplete:
			// A partial query at the read boundary: carry it so the next chunk can
			// complete it. Nothing after it, so stop here.
			r.carry = append(r.carry[:0], win[p:]...)
			return out
		default: // decrqmNoMatch
			i = p + 1 // skip this ESC; scan for the next one
		}
	}
	r.carry = r.carry[:0]
	return out
}
