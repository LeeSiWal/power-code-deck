package services

import (
	"bytes"
	"strconv"
	"sync"
)

// terminalModes tracks the DEC private modes an app has turned on, so a
// (re)connecting viewer can be told the current terminal state even when the
// bounded replay ring no longer holds the original enable sequences.
//
// Why this exists: Claude Code (and other full-screen TUIs) enable alt-screen,
// mouse tracking, SGR mouse encoding, application cursor keys and bracketed
// paste ONCE, at the very front of their output. On a long session — exactly
// what "이어하기(resume)" loads — total output exceeds the ring's capacity and
// those front bytes are the first evicted. A viewer that attaches then rebuilds
// state only from the ring never learns:
//   - the app owns the mouse  → the client never forwards wheel events → scroll
//     is dead (the reported resume-scroll bug), and
//   - the app is in alt-screen → the replay content is rendered into the normal
//     buffer instead of the alternate screen → misrender.
// So we parse the output stream, remember which modes are on, and prepend them
// to the replay on Attach. All tracked modes default to reset, so replaying only
// the currently-set ones as `CSI ? n h` fully and correctly restores state.
type terminalModes struct {
	mu    sync.Mutex
	on    map[int]bool
	carry []byte // trailing bytes of the last scan, to catch a sequence split across reads
}

func newTerminalModes() *terminalModes {
	return &terminalModes{on: make(map[int]bool)}
}

// trackedMode reports whether n is a DEC private mode we restore on attach.
// Every one defaults to reset, so "set" fully describes its non-default state.
func trackedMode(n int) bool {
	switch n {
	case 47, 1047, 1049, // alternate screen buffer
		1,                                            // DECCKM — application cursor keys (arrow-key encoding)
		1000, 1001, 1002, 1003, 1004, 1005, 1006, 1015, // mouse tracking + encodings
		2004: // bracketed paste
		return true
	}
	return false
}

// modeReplayOrder fixes the order modes are re-emitted on attach. Alt-screen
// comes first so the replayed screen content lands in the alternate buffer.
var modeReplayOrder = []int{1049, 1047, 47, 1, 1000, 1001, 1002, 1003, 1004, 1005, 1006, 1015, 2004}

// scan updates the tracked mode state from a chunk of raw PTY output. It is
// idempotent per final state: re-seeing a set/reset just reasserts it, so the
// small overlap carried between chunks is harmless.
func (t *terminalModes) scan(data []byte) {
	if len(data) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	win := data
	if len(t.carry) > 0 {
		win = append(append(make([]byte, 0, len(t.carry)+len(data)), t.carry...), data...)
	}

	// Find each  ESC [ ? <num>(;<num>)* (h|l)  sequence.
	for i := 0; i+2 < len(win); i++ {
		if win[i] != 0x1b || win[i+1] != '[' || win[i+2] != '?' {
			continue
		}
		j := i + 3
		cur, hasDigit := 0, false
		var nums []int
		for j < len(win) {
			c := win[j]
			if c >= '0' && c <= '9' {
				cur = cur*10 + int(c-'0')
				hasDigit = true
				j++
				continue
			}
			if c == ';' {
				if hasDigit {
					nums = append(nums, cur)
				}
				cur, hasDigit = 0, false
				j++
				continue
			}
			break
		}
		if j >= len(win) {
			break // sequence runs past this window — carry the rest and finish next time
		}
		set := win[j] == 'h'
		if (set || win[j] == 'l') && hasDigit {
			nums = append(nums, cur)
			for _, n := range nums {
				if trackedMode(n) {
					t.on[n] = set
				}
			}
		}
		i = j // resume after the terminator (loop's i++ moves past it)
	}

	// Carry the tail so a sequence split across reads is caught next time.
	const carryMax = 24 // longest tracked sequence (ESC[?1000;1002;1003;1006h) fits well under this
	if n := len(win); n > carryMax {
		t.carry = append(t.carry[:0], win[n-carryMax:]...)
	} else {
		t.carry = append(t.carry[:0], win...)
	}
}

// prefix returns the escape sequences that restore the currently-set modes,
// to prepend to a replay. Empty when no tracked mode is on.
func (t *terminalModes) prefix() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.on) == 0 {
		return nil
	}
	var b bytes.Buffer
	for _, n := range modeReplayOrder {
		if t.on[n] {
			b.WriteString("\x1b[?")
			b.WriteString(strconv.Itoa(n))
			b.WriteByte('h')
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return b.Bytes()
}
