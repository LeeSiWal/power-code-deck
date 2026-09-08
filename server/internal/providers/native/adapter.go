// Package native adapts the existing Claude stream-json and Codex app-server
// drivers to providers.Execution. The shared legacy wire is an implementation
// detail; Envelope keeps it available only to the compatibility application.
package native

import (
	"context"
	"fmt"
	"io"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/legacywire"
)

// Driver is the existing native CLI surface. Drivers and their approval policy
// stay in services during this migration slice; any implementation can be wrapped.
type Driver interface {
	Start() error
	Events() <-chan *legacywire.StreamEvent
	Send(string) error
	Interrupt() error
	ConversationID() string
	Stop()
	SetPermissionMode(string) error
}

// Envelope carries the original wire object unchanged for legacy history/WS.
// New orchestration code uses Execution.Next and never depends on Wire.
type Envelope struct {
	Event providers.Event
	Wire  *legacywire.StreamEvent
}

type Adapter struct {
	identity providers.Identity
	driver   Driver
	sequence uint64
}

func New(identity providers.Identity, driver Driver) (*Adapter, error) {
	if identity.ExecutionID == "" {
		return nil, fmt.Errorf("provider execution ID is required")
	}
	if identity.Provider != providers.Claude && identity.Provider != providers.Codex {
		return nil, fmt.Errorf("unsupported native provider %q", identity.Provider)
	}
	if driver == nil {
		return nil, fmt.Errorf("native driver is required")
	}
	return &Adapter{identity: identity, driver: driver}, nil
}

func (a *Adapter) Identity() providers.Identity { return a.identity }
func (a *Adapter) Capabilities() providers.Capabilities {
	return providers.Capabilities{MultiTurn: true, ApprovalHandling: providers.ApplicationBroker}
}
func (a *Adapter) Start() error                        { return a.driver.Start() }
func (a *Adapter) Send(text string) error              { return a.driver.Send(text) }
func (a *Adapter) Interrupt() error                    { return a.driver.Interrupt() }
func (a *Adapter) ConversationID() string              { return a.driver.ConversationID() }
func (a *Adapter) Stop()                               { a.driver.Stop() }
func (a *Adapter) SetPermissionMode(mode string) error { return a.driver.SetPermissionMode(mode) }

func (a *Adapter) Next(ctx context.Context) (providers.Event, error) {
	envelope, err := a.NextEnvelope(ctx)
	return envelope.Event, err
}

// NextEnvelope and Next share one stream and must not have competing readers.
// No forwarding goroutine or second queue is introduced; existing backpressure
// and driver shutdown behavior are preserved.
func (a *Adapter) NextEnvelope(ctx context.Context) (Envelope, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Envelope{}, err
		}
		select {
		case <-ctx.Done():
			return Envelope{}, ctx.Err()
		case wire, ok := <-a.driver.Events():
			if !ok {
				return Envelope{}, io.EOF
			}
			if wire == nil {
				continue
			}
			a.sequence++
			event := normalize(wire)
			event.Identity, event.Sequence = a.identity, a.sequence
			if event.ConversationID == "" {
				event.ConversationID = a.driver.ConversationID()
			}
			return Envelope{Event: event, Wire: wire}, nil
		}
	}
}

var _ providers.Execution = (*Adapter)(nil)
