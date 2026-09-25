package services

import (
	"crypto/rand"
	"testing"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/native"
)

func testProviderAdapter(t *testing.T, driver NativeDriver) *native.Adapter {
	t.Helper()
	adapter, err := native.New(providers.Identity{ExecutionID: rand.Text(), Provider: providers.Claude}, driver)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

// Existing UI observers receive the original event; the new observer receives
// normalized identity. A replaced execution must not publish new run events.
func TestProviderPumpPreservesWireAndFiltersReplacedExecution(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")
	d := newFakeNativeDriver()
	d.events = make(chan *StreamEvent, 1)
	wire, err := ParseStreamEvent([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"unknown":123}`))
	if err != nil {
		t.Fatal(err)
	}
	d.events <- wire
	close(d.events)
	sess := &nativeSession{id: "agent-1", driver: testProviderAdapter(t, d)}
	s.sessions[sess.id] = sess
	if identity, ok := s.ExecutionIdentity(sess.id); !ok || identity != sess.driver.Identity() {
		t.Fatal("execution mapping missing")
	}
	var received []*StreamEvent
	s.SetHandlers(func(_ string, ev *StreamEvent) { received = append(received, ev) }, nil)
	var events []providers.Event
	s.AddExecutionObserver(func(event providers.Event) { events = append(events, event) })
	s.pump(sess)
	if len(received) != 1 || received[0] != wire {
		t.Fatal("legacy wire identity changed")
	}
	if len(events) != 1 || events[0].Identity.ExecutionID == sess.id || events[0].Kind != providers.Message {
		t.Fatalf("provider events: %+v", events)
	}
	replacement := &nativeSession{id: sess.id, driver: testProviderAdapter(t, newFakeNativeDriver())}
	s.sessions[sess.id] = replacement
	s.emitExecution(sess, events[0])
	if len(events) != 1 {
		t.Fatal("superseded execution published an event")
	}
	if replacement.driver.Identity().ExecutionID == sess.driver.Identity().ExecutionID {
		t.Fatal("restart reused execution ID")
	}
}
