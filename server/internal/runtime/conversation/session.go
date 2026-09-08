// Package conversation coordinates sequential single-turn provider executions.
// It owns no UI, database, transport, or approval policy. The caller consumes each
// turn through Next before starting another, so completion cannot overtake output.
package conversation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"

	"powercodedeck/internal/providers"
)

var (
	ErrBusy   = errors.New("conversation already has an active turn")
	ErrClosed = errors.New("conversation is closed")
)

// Factory receives a fresh process identity and an explicit conversation ID.
// It must return a new, unstarted, single-turn execution. CLI configuration and
// authentication belong to the provider factory, never to a browser payload.
type Factory func(executionID, resumeID string) (providers.Execution, error)

type Session struct {
	mu       sync.Mutex
	factory  Factory
	resumeID string
	active   *Turn
	closed   bool
}

func New(factory Factory, resumeID string) (*Session, error) {
	if factory == nil {
		return nil, fmt.Errorf("conversation factory is required")
	}
	return &Session{factory: factory, resumeID: resumeID}, nil
}

// StartTurn serializes process creation with Stop and concurrent submissions.
// Failed startup does not erase the last known conversation or occupy the slot.
func (s *Session) StartTurn(prompt string) (*Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.active != nil {
		return nil, ErrBusy
	}
	id := rand.Text()
	e, err := s.factory(id, s.resumeID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("conversation factory returned no execution")
	}
	if e.Identity().ExecutionID != id || e.Capabilities().MultiTurn {
		e.Stop()
		return nil, fmt.Errorf("conversation requires a fresh single-turn execution")
	}
	if err := e.Start(); err != nil {
		e.Stop()
		return nil, err
	}
	if err := e.Send(prompt); err != nil {
		e.Stop()
		return nil, err
	}
	t := &Turn{session: s, execution: e}
	s.active = t
	return t, nil
}

func (s *Session) ConversationID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resumeID
}

// Stop closes the conversation permanently and releases a blocked event reader.
// Interrupt, in contrast, keeps the conversation available after its result.
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.active != nil {
		s.active.execution.Stop()
	}
}

type Turn struct {
	session   *Session
	execution providers.Execution
	done      bool // owned by the single Next reader
}

func (t *Turn) Identity() providers.Identity { return t.execution.Identity() }
func (t *Turn) Interrupt() error {
	t.session.mu.Lock()
	defer t.session.mu.Unlock()
	if t.session.closed {
		return ErrClosed
	}
	if t.session.active != t {
		return fmt.Errorf("turn is no longer active")
	}
	return t.execution.Interrupt()
}

// Next has one reader. Canceling its context only cancels this wait; it neither
// frees the active slot nor loses the provider's terminal outcome. Events retain
// their original execution identity, sequence, usage scope, and diagnostics.
func (t *Turn) Next(ctx context.Context) (providers.Event, error) {
	if t.done {
		return providers.Event{}, io.EOF
	}
	event, err := t.execution.Next(ctx)
	if err != nil && ctx.Err() != nil {
		return providers.Event{}, err
	}
	if err != nil || event.Kind == providers.TurnFinished {
		t.done = true
		t.session.mu.Lock()
		if t.session.active == t {
			if id := t.execution.ConversationID(); id != "" {
				t.session.resumeID = id
			}
			t.execution.Stop()
			t.session.active = nil
		}
		t.session.mu.Unlock()
		// EOF without a terminal event is observable failure, never success.
		if errors.Is(err, io.EOF) {
			return providers.Event{}, fmt.Errorf("provider stream ended without a turn outcome: %w", io.ErrUnexpectedEOF)
		}
	}
	return event, err
}
