package routing

import "fmt"

// FreshStartConfig turns on fresh starts: when an auto session comes back
// after a pause (the prompt cache is cold, so the whole conversation would be
// re-read at full price) and its conversation is long, a cheap model writes a
// one-page handoff memo and the session continues in a new conversation that
// starts from the memo, the last turns verbatim and the repository state.
type FreshStartConfig struct {
	Enabled bool `json:"enabled"`
	// The memo model: the cheapest model that writes good memos. Measured with
	// services.TestMemoEvalLive (6 cuts of this project's longest conversations,
	// memos graded blind by Opus 5.5 against the whole conversation and what the
	// full-context agent did next): Sonnet 5 at low effort averaged 4.2/5 with 2
	// factual errors in all six memos; Haiku 4.5 averaged 2.0 with 23 errors.
	// Both took ~30s.
	Adapter string `json:"adapter"` // claude | codex
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
	// MinContextTokens: shorter conversations just resume (default 50000); the
	// memo costs a model call and the user's wait, which a short re-read does
	// not repay.
	MinContextTokens int `json:"minContextTokens,omitempty"`
}

// DefaultFreshMinTokens is the MinContextTokens default.
const DefaultFreshMinTokens = 50000

func (f FreshStartConfig) MinTokens() int {
	if f.MinContextTokens > 0 {
		return f.MinContextTokens
	}
	return DefaultFreshMinTokens
}

func (f FreshStartConfig) Validate() error {
	if f.Adapter != "claude" && f.Adapter != "codex" {
		return fmt.Errorf("routing config: freshStart.adapter must be claude or codex")
	}
	if f.MinContextTokens < 0 {
		return fmt.Errorf("routing config: freshStart.minContextTokens must not be negative")
	}
	return nil
}
