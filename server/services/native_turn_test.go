package services

import "testing"

// Turn tracking behind per-turn routing: a user text starts a turn, a result
// ends it; a tool_result "user" event (no text block) does not start one.
func TestNativeTurnState(t *testing.T) {
	s := &NativeService{sessions: map[string]*nativeSession{}}
	sess := &nativeSession{id: "a", kind: "claude"}
	s.sessions["a"] = sess
	if running, active, end := s.TurnState("a"); !running || active || !end.IsZero() {
		t.Fatalf("fresh: %v %v %v", running, active, end)
	}
	s.emit(sess, nativeTextEvent("user", "hi"))
	if _, active, _ := s.TurnState("a"); !active {
		t.Fatal("user text did not start a turn")
	}
	s.emit(sess, &StreamEvent{Type: "user", Message: &StreamMessage{Role: "user", Content: []ContentBlock{{Type: "tool_result"}}}})
	s.emit(sess, &StreamEvent{Type: "result"})
	if _, active, end := s.TurnState("a"); active || end.IsZero() {
		t.Fatalf("after result: active=%v end=%v", active, end)
	}
	if running, _, _ := s.TurnState("missing"); running {
		t.Fatal("unknown session reported running")
	}
}
