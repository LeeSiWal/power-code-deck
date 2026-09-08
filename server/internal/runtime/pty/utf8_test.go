package pty

import "testing"

// Korean output split across PTY reads must not be corrupted: incomplete
// trailing UTF-8 bytes are held back, not sent as broken bytes.
func TestSplitIncompleteUTF8(t *testing.T) {
	full := []byte("안녕") // 6 bytes: 안 = EC 95 88, 녕 = EB 85 95

	if h, tl := splitIncompleteUTF8(full); string(h) != "안녕" || len(tl) != 0 {
		t.Fatalf("complete: head=%q tail=%v", h, tl)
	}
	// Cut after 4 bytes: "안" (3) complete + first byte of "녕" incomplete.
	if h, tl := splitIncompleteUTF8(full[:4]); string(h) != "안" || len(tl) != 1 {
		t.Fatalf("split: head=%q tail=%v", h, tl)
	}
	// Cut after 5 bytes: "안" + two bytes of "녕" (still incomplete).
	if h, tl := splitIncompleteUTF8(full[:5]); string(h) != "안" || len(tl) != 2 {
		t.Fatalf("split2: head=%q tail=%v", h, tl)
	}
	if h, tl := splitIncompleteUTF8([]byte("abc")); string(h) != "abc" || len(tl) != 0 {
		t.Fatalf("ascii: head=%q tail=%v", h, tl)
	}
}
