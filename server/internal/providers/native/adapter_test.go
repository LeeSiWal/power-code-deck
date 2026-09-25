package native_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/legacywire"
	"powercodedeck/internal/providers/native"
)

type fakeDriver struct {
	events                                   chan *legacywire.StreamEvent
	startErr, sendErr, interruptErr, modeErr error
	started, stopped                         int
	text, mode                               string
}

func (d *fakeDriver) Start() error                           { d.started++; return d.startErr }
func (d *fakeDriver) Events() <-chan *legacywire.StreamEvent { return d.events }
func (d *fakeDriver) Send(text string) error                 { d.text = text; return d.sendErr }
func (d *fakeDriver) Interrupt() error                       { return d.interruptErr }
func (d *fakeDriver) ConversationID() string                 { return "conversation-1" }
func (d *fakeDriver) Stop()                                  { d.stopped++ }
func (d *fakeDriver) SetPermissionMode(mode string) error    { d.mode = mode; return d.modeErr }

func TestLifecycleAndCanceledRead(t *testing.T) {
	denied := errors.New("driver refused")
	d := &fakeDriver{events: make(chan *legacywire.StreamEvent, 1), startErr: denied, sendErr: denied, interruptErr: denied, modeErr: denied}
	a, err := native.New(providers.Identity{ExecutionID: "execution-1", Provider: providers.Codex}, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{a.Start(), a.Send("request with spaces"), a.Interrupt(), a.SetPermissionMode("plan")} {
		if !errors.Is(err, denied) {
			t.Fatalf("lost driver error: %v", err)
		}
	}
	if d.started != 1 || d.text != "request with spaces" || d.mode != "plan" || d.stopped != 0 {
		t.Fatalf("unexpected lifecycle forwarding: %+v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation: %v", err)
	}
	if d.stopped != 0 {
		t.Fatal("canceling a read stopped the process")
	}
	close(d.events)
	if _, err := a.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("closed stream: %v", err)
	}
	a.Stop()
	if d.stopped != 1 {
		t.Fatal("Stop was not forwarded")
	}
}

func TestEventsKeepCorrelationUnknownsAndOriginalWire(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"resume-42","model":"selected","future_field":{"keep":true}}`,
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"a"}}]},"parent_tool_use_id":"parent-1"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","is_error":true,"content":[{"type":"text","text":"denied"}]}]}}`,
		`{"type":"future_event","future_field":42}`,
		`{"type":"result","subtype":"success","permission_denials":[{"tool_name":"Read","tool_use_id":"tool-1","tool_input":{"path":"a"}}],"usage":{"input_tokens":4,"output_tokens":5},"total_cost_usd":0}`,
		`{"type":"result","is_error":true,"terminal_reason":"failed"}`,
	}
	for _, provider := range []providers.ID{providers.Claude, providers.Codex} {
		t.Run(string(provider), func(t *testing.T) {
			d := &fakeDriver{events: make(chan *legacywire.StreamEvent, len(lines))}
			for _, line := range lines {
				e, err := legacywire.ParseStreamEvent([]byte(line))
				if err != nil {
					t.Fatal(err)
				}
				d.events <- e
			}
			close(d.events)
			identity := providers.Identity{ExecutionID: "execution-distinct-from-agent", Provider: provider}
			a, err := native.New(identity, d)
			if err != nil {
				t.Fatal(err)
			}
			events := []providers.Event{}
			for i, line := range lines {
				envelope, err := a.NextEnvelope(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if string(envelope.Wire.Raw) != line {
					t.Fatal("legacy raw event changed")
				}
				e := envelope.Event
				if e.Identity != identity || e.Sequence != uint64(i+1) {
					t.Fatalf("bad correlation: %+v", e)
				}
				if i > 0 && e.ConversationID != "conversation-1" {
					t.Fatal("missing conversation fallback")
				}
				events = append(events, e)
			}
			if events[0].Kind != providers.Ready || events[0].ConversationID != "resume-42" {
				t.Fatal("init mapping")
			}
			msg := events[1]
			if msg.ParentToolCallID != "parent-1" || msg.MessageID != "m1" || len(msg.Blocks) != 2 || msg.Blocks[1].ToolCallID != "tool-1" {
				t.Fatalf("message mapping: %+v", msg)
			}
			result := events[2].Blocks[0]
			if result.Kind != providers.ToolResult || !result.IsError || result.ToolCallID != "tool-1" {
				t.Fatal("tool correlation")
			}
			if events[3].Kind != providers.Other || events[3].Outcome != nil {
				t.Fatal("unknown event implied completion")
			}
			outcome := events[4].Outcome
			if outcome == nil || outcome.CostUSD == nil || *outcome.CostUSD != 0 || outcome.Usage.InputTokens != 4 || len(outcome.Denials) != 1 {
				t.Fatalf("usage/denials: %+v", outcome)
			}
			if events[5].Outcome.Usage != nil || events[5].Outcome.CostUSD != nil || !events[5].Outcome.IsError {
				t.Fatal("unknown usage must remain unknown")
			}
		})
	}
}
