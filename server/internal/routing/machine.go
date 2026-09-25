package routing

import "fmt"

// Phase is the per-Run switching state. The happy path of a switch is
// running → quiescing → checkpointed → routing → handoff → running.
type Phase string

const (
	PhaseIdle         Phase = "idle"
	PhaseRunning      Phase = "running"
	PhaseQuiescing    Phase = "quiescing"
	PhaseCheckpointed Phase = "checkpointed"
	PhaseRouting      Phase = "routing"
	PhaseHandoff      Phase = "handoff"
	PhaseWaitApproval Phase = "waiting_approval"
	PhaseWaitPolicy   Phase = "waiting_policy"
	PhaseWaitBilling  Phase = "waiting_billing"
	PhaseWaitUser     Phase = "waiting_user"
	PhaseBlockedEnv   Phase = "blocked_environment"
	PhaseReconcile    Phase = "reconcile"
	PhaseFailed       Phase = "failed"
	PhaseCanceled     Phase = "canceled"
	PhaseSucceeded    Phase = "succeeded"
)

var transitions = map[Phase][]Phase{
	PhaseIdle:         {PhaseRouting, PhaseCanceled},
	PhaseRouting:      {PhaseHandoff, PhaseRunning, PhaseWaitPolicy, PhaseWaitBilling, PhaseWaitUser, PhaseBlockedEnv, PhaseFailed, PhaseCanceled},
	PhaseHandoff:      {PhaseRunning, PhaseFailed, PhaseCanceled},
	PhaseRunning:      {PhaseQuiescing, PhaseWaitApproval, PhaseCanceled},
	PhaseWaitApproval: {PhaseRunning, PhaseQuiescing, PhaseCanceled},
	// Quiescing ends only when the process is known to be gone (checkpointed)
	// or unknown (reconcile). There is no edge back to running.
	PhaseQuiescing:    {PhaseCheckpointed, PhaseReconcile, PhaseCanceled},
	PhaseCheckpointed: {PhaseRouting, PhaseSucceeded, PhaseFailed, PhaseWaitUser, PhaseBlockedEnv, PhaseReconcile, PhaseCanceled},
	PhaseWaitPolicy:   {PhaseRouting, PhaseCanceled},
	PhaseWaitBilling:  {PhaseRouting, PhaseCanceled},
	PhaseWaitUser:     {PhaseRouting, PhaseCanceled},
	PhaseBlockedEnv:   {PhaseRouting, PhaseCanceled},
	PhaseReconcile:    {PhaseRouting, PhaseCanceled},
	PhaseFailed:       {PhaseRouting, PhaseCanceled},
	PhaseSucceeded:    {},
	PhaseCanceled:     {},
}

func CanTransition(from, to Phase) bool {
	for _, p := range transitions[from] {
		if p == to {
			return true
		}
	}
	return false
}

// ErrStaleEpoch means the caller acted on an older view of the Run (a late
// event, a duplicate request, or a router answer that raced a cancel).
var ErrStaleEpoch = fmt.Errorf("routing state changed (stale epoch)")

// ErrBadTransition means the requested edge is not part of the machine.
type ErrBadTransition struct{ From, To Phase }

func (e ErrBadTransition) Error() string {
	return fmt.Sprintf("routing: %s → %s is not allowed", e.From, e.To)
}

// Writable reports whether a new writer may be started from this phase.
func (p Phase) Writable() bool { return p == PhaseRouting || p == PhaseHandoff }

// Terminal phases accept no further automatic work.
func (p Phase) Terminal() bool { return p == PhaseSucceeded || p == PhaseCanceled }
