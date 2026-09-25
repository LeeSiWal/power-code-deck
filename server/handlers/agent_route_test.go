package handlers

import (
	"strings"
	"testing"

	"powercodedeck/services"
)

func userEvent(text string) *services.StreamEvent {
	return &services.StreamEvent{Type: "user", Message: &services.StreamMessage{Role: "user", Content: []services.ContentBlock{{Type: "text", Text: text}}}}
}

// The re-read cost is the context the last turn reported; without reported
// usage the handoff text size stands in for it.
func TestContextTokens(t *testing.T) {
	reported := []*services.StreamEvent{
		userEvent("hi"),
		{Type: "result", Usage: &services.StreamUsage{InputTokens: 100, CacheCreationInputTokens: 900, CacheReadInputTokens: 29000}},
		userEvent("again"),
		{Type: "result"}, // a later turn without usage does not hide the last report
	}
	if got := contextTokens(reported); got != 30000 {
		t.Fatalf("reported context = %d", got)
	}
	long := strings.Repeat("가", 30000) // 90000 bytes ≈ 30000 tokens
	if got := contextTokens([]*services.StreamEvent{userEvent(long), {Type: "result"}}); got < 29000 {
		t.Fatalf("unreported context estimate = %d", got)
	}
}
