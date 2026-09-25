package runroute

import (
	"context"
	"errors"
	"testing"
)

func TestChooseNeverLaunchesOrCallsDecider(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), allCLIs())
	fd := &fakeDecider{answer: verdict(dispatchCodex)}
	h.coord.decider = fd
	ch, err := h.coord.Choose(context.Background(), "fix app.txt")
	if err != nil || ch.Profile == nil || ch.Profile.ID != ch.Decision.Selected {
		t.Fatalf("choice=%+v err=%v", ch, err)
	}
	if fd.calls.Load() != 0 {
		t.Fatalf("decider called %d times", fd.calls.Load())
	}
	if _, err := h.coord.Choose(context.Background(), "   "); err == nil {
		t.Fatal("empty request accepted")
	}
}

func TestChooseReportsNoProfile(t *testing.T) {
	h := newHarness(t, deciderRoutingConfig(true), map[string]string{})
	ch, err := h.coord.Choose(context.Background(), "fix app.txt")
	if !errors.Is(err, ErrNoProfile) || ch.Decision.Reason == "" {
		t.Fatalf("choice=%+v err=%v", ch, err)
	}
}
