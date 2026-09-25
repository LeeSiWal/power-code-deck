package services

import "testing"

// fakeTurnDriver is a nativeExecution whose Send can emit events synchronously.
type fakeTurnDriver struct {
	nativeExecution
	onSend func()
}

func (d *fakeTurnDriver) Send(string) error {
	if d.onSend != nil {
		d.onSend()
	}
	return nil
}

// Turn tracking behind per-turn routing: sending a message starts a turn, a
// result ends it — even when the result arrives before Send returns.
func TestNativeTurnState(t *testing.T) {
	s := &NativeService{sessions: map[string]*nativeSession{}}
	sess := &nativeSession{id: "a", kind: "claude"}
	s.sessions["a"] = sess
	if running, active, end := s.TurnState("a"); !running || active || !end.IsZero() {
		t.Fatalf("fresh: %v %v %v", running, active, end)
	}
	sess.driver = &fakeTurnDriver{onSend: func() {
		// The CLI answers before Send returns: the turn must still end.
		s.emit(sess, &StreamEvent{Type: "result"})
	}}
	if err := s.Send("a", "hi"); err != nil {
		t.Fatal(err)
	}
	if _, active, end := s.TurnState("a"); active || end.IsZero() {
		t.Fatalf("fast reply left the turn open: active=%v end=%v", active, end)
	}
	sess.driver = &fakeTurnDriver{}
	if err := s.Send("a", "again"); err != nil {
		t.Fatal(err)
	}
	if _, active, _ := s.TurnState("a"); !active {
		t.Fatal("sent message did not start a turn")
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
