package services

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/antigravity"
	"powercodedeck/internal/providers/native"
	"powercodedeck/internal/runtime/conversation"
)

// nativeExecution keeps compatibility envelopes at the application boundary.
// Antigravity keeps its own per-turn identity instead of becoming a Claude wire driver.
type nativeExecution interface {
	Start() error
	Send(string) error
	Interrupt() error
	Stop()
	ConversationID() string
	Identity() providers.Identity
	SetPermissionMode(string) error
	NextEnvelope(context.Context) (native.Envelope, error)
}

type antigravityChat struct {
	cfg      antigravity.Config
	mu       sync.Mutex
	session  *conversation.Session
	turn     *conversation.Turn
	identity providers.Identity
	closed   bool
	events   chan native.Envelope
	stop     chan struct{}
}

func newAntigravityChat(cfg antigravity.Config) *antigravityChat {
	return &antigravityChat{cfg: cfg, events: make(chan native.Envelope, 64), stop: make(chan struct{})}
}

func (a *antigravityChat) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.session != nil {
		return fmt.Errorf("Antigravity chat already started or closed")
	}
	probe, err := antigravity.New(rand.Text(), a.cfg)
	if err != nil {
		return err
	}
	defer probe.Stop()
	if err := probe.Start(); err != nil {
		return err
	}
	a.session, err = conversation.New(func(id, resume string) (providers.Execution, error) {
		cfg := a.cfg
		cfg.ResumeID = resume
		return antigravity.New(id, cfg)
	}, a.cfg.ResumeID)
	return err
}

func (a *antigravityChat) Identity() providers.Identity {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.identity
}
func (a *antigravityChat) ConversationID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return a.cfg.ResumeID
	}
	return a.session.ConversationID()
}
func (a *antigravityChat) SetPermissionMode(string) error {
	return fmt.Errorf("Antigravity uses its configured CLI permissions")
}
func (a *antigravityChat) Send(text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.session == nil {
		return fmt.Errorf("Antigravity chat is not running")
	}
	if a.turn != nil {
		return conversation.ErrBusy
	}
	t, err := a.session.StartTurn(text)
	if err != nil {
		return err
	}
	a.turn, a.identity = t, t.Identity()
	go a.pump(t)
	return nil
}
func (a *antigravityChat) Interrupt() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.turn == nil {
		return fmt.Errorf("Antigravity has no active turn")
	}
	return a.turn.Interrupt()
}
func (a *antigravityChat) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	close(a.stop)
	if a.session != nil {
		a.session.Stop()
	}
}
func (a *antigravityChat) NextEnvelope(ctx context.Context) (native.Envelope, error) {
	select {
	case <-ctx.Done():
		return native.Envelope{}, ctx.Err()
	case <-a.stop:
		return native.Envelope{}, io.EOF
	case ev := <-a.events:
		if ev.Wire.Type == StreamTypeResult {
			a.mu.Lock()
			a.turn = nil
			a.mu.Unlock()
		}
		return ev, nil
	}
}

func (a *antigravityChat) pump(t *conversation.Turn) {
	hadText := false
	for {
		ev, err := t.Next(context.Background())
		if err != nil {
			return
		} // single-turn provider always emits its process outcome
		wires := antigravityWires(ev, &hadText)
		for i, wire := range wires {
			canonical := providers.Event{}
			if i == 0 {
				canonical = ev
			}
			select {
			case a.events <- native.Envelope{Event: canonical, Wire: wire}:
			case <-a.stop:
				return
			}
		}
		if ev.Kind == providers.TurnFinished {
			return
		}
	}
}

// Only the compatibility projection drops cumulative usage: legacy consumers
// treat usage as per-turn. The canonical event retains scope and all diagnostics.
func antigravityWires(ev providers.Event, hadText *bool) []*StreamEvent {
	makeWire := func(payload map[string]any) *StreamEvent {
		raw, _ := json.Marshal(payload)
		var wire StreamEvent
		_ = json.Unmarshal(raw, &wire)
		wire.Raw = raw
		return &wire
	}
	var result []*StreamEvent
	add := func(p map[string]any) { result = append(result, makeWire(p)) }
	switch ev.Kind {
	case providers.Ready:
		add(map[string]any{"type": "system", "subtype": "init", "session_id": ev.ConversationID, "model": ev.Model, "approval_handling": "cli_settings"})
	case providers.Message:
		for _, b := range ev.Blocks {
			switch b.Kind {
			case providers.Text:
				*hadText = true
				add(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": b.Text}}})
			case providers.ToolCall:
				add(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []ContentBlock{{Type: "tool_use", ID: b.ToolCallID, Name: b.ToolName, Input: b.Input}}}})
			case providers.ToolResult:
				output := b.Output
				var text string
				if json.Unmarshal(output, &text) != nil {
					output, _ = json.Marshal(string(output))
				}
				add(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []ContentBlock{{Type: "tool_result", ToolUseID: b.ToolCallID, Content: output, IsError: b.IsError}}}})
			}
		}
	case providers.TurnFinished:
		out := ev.Outcome
		if out != nil {
			if !*hadText && !out.IsError && out.Text != "" {
				result = append(result, nativeTextEvent("assistant", out.Text))
			}
			message := out.Diagnostics
			if out.IsError {
				message = out.Text
				if message == "" {
					message = out.Reason
				}
				if out.Diagnostics != "" && out.Diagnostics != message {
					message += "\n" + out.Diagnostics
				}
			}
			add(map[string]any{"type": "result", "is_error": out.IsError, "provider_notice": true, "result": message, "terminal_reason": out.Reason, "session_id": ev.ConversationID})
		}
	}
	if len(result) == 0 {
		add(map[string]any{"type": "provider_event"})
	}
	return result
}
