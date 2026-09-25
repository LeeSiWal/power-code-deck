package routing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// TierJudgeConfig lets a local model rate a request's difficulty instead of
// the keyword rules. Measured on 70 labeled requests (40 development, 30
// written afterwards as a held-out set) with Qwen3-30B-A3B on a Mac Studio:
// held-out accuracy 28/30 vs 12/30 for the rules, every easy request found
// (11/11), no non-easy request rated easy, no hard request missed; ~0.2s per
// call. Its only errors rated MEDIUM work HARD (safe side).
type TierJudgeConfig struct {
	EndpointRef string `json:"endpointRef"`
	Model       string `json:"model,omitempty"` // "" = the endpoint's model
	TimeoutMS   int    `json:"timeoutMs,omitempty"`
}

// judgePrompt is the exact prompt the measurement above used; changing it
// invalidates those numbers.
const judgePrompt = `You rate how hard a coding request is for an AI coding agent working in the user's repository. Reply with exactly one word: EASY, MEDIUM or HARD.

EASY: a small, mechanical, well-specified change the request fully spells out — fix a typo, change a value/text/version/port, rename one symbol and its call sites, add or remove a line, a comment or an import, create a tiny config file, fix a bug whose fix is stated. No design decisions, no investigation.
MEDIUM: a normal feature or fix in one area that needs some design or investigation — add a small feature, write tests, debug a clear failure, add validation, write a docs section.
HARD: broad or risky work — architecture or redesign, migrations, splitting services, concurrency/race conditions, memory leaks, performance tuning, security audits, flaky failures, changes across the whole app.

When unsure between EASY and MEDIUM, answer MEDIUM.

Request: `

var errNoVerdict = errors.New("judge gave no EASY/MEDIUM/HARD answer")

// JudgeTier asks a local model for the request's tier: EASY → VERY_EASY (the
// tier local profiles take), MEDIUM → MEDIUM, HARD → HIGH.
func (c *LocalClient) JudgeTier(ctx context.Context, e LocalEndpoint, model, goal string) (Tier, error) {
	if model == "" {
		model = e.Model
	}
	msgs := []map[string]string{{"role": "user", "content": judgePrompt + goal}}
	var word string
	if e.Kind == "ollama" {
		var r struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := c.do(ctx, e, http.MethodPost, "/api/chat", map[string]any{"model": model, "messages": msgs, "stream": false,
			"options": map[string]any{"temperature": 0, "num_predict": 5}}, &r); err != nil {
			return TierUnset, err
		}
		word = r.Message.Content
	} else {
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := c.do(ctx, e, http.MethodPost, "/v1/chat/completions", map[string]any{"model": model, "messages": msgs, "max_tokens": 5, "temperature": 0}, &r); err != nil {
			return TierUnset, err
		}
		if len(r.Choices) > 0 {
			word = r.Choices[0].Message.Content
		}
	}
	return parseJudgeWord(word)
}

func parseJudgeWord(s string) (Tier, error) {
	s = strings.ToUpper(s)
	switch {
	case strings.Contains(s, "EASY"):
		return VeryEasy, nil
	case strings.Contains(s, "MEDIUM"):
		return Medium, nil
	case strings.Contains(s, "HARD"):
		return High, nil
	}
	return TierUnset, errNoVerdict
}

func (j TierJudgeConfig) timeout() time.Duration {
	if j.TimeoutMS > 0 && j.TimeoutMS <= 30000 {
		return time.Duration(j.TimeoutMS) * time.Millisecond
	}
	return 3 * time.Second
}

// JudgeTimeout bounds one judge call (timeoutMs, default 3s).
func (j TierJudgeConfig) JudgeTimeout() time.Duration { return j.timeout() }
