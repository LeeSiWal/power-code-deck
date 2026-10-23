package services

// ConversationTokens is how much the CLI re-reads to continue the conversation
// it is in now. Claude reports it: the last API request's input + cache writes
// + cache reads, from its assistant message (the result event sums every
// request of the turn, so it overstates the conversation by that many times;
// it is used only when no request reported). Codex reports none (see
// codex_driver.go), so its size is estimated from everything the model has seen
// in its conversation — tool output included, since a file read or a command's
// output is what makes a coding conversation long. The estimate errs high on
// purpose: overrating keeps a warm session in place or asks a stronger model
// for advice, underrating makes a model re-read a large conversation.
func ConversationTokens(events []*StreamEvent) int {
	run := sameToolTail(events)
	for i := len(run) - 1; i >= 0; i-- {
		if ev := run[i]; ev.Type == "assistant" && ev.Message != nil && ev.Message.Usage != nil && (ev.ParentToolUseID == nil || *ev.ParentToolUseID == "") {
			return contextOf(ev.Message.Usage)
		}
	}
	for i := len(run) - 1; i >= 0; i-- {
		if ev := run[i]; ev.Type == "result" && ev.Usage != nil {
			return contextOf(ev.Usage)
		}
	}
	return contentBytes(sameConversationTail(run)) / 3 // ~1 token per 3 bytes, as estimateTokens
}

func contextOf(u *StreamUsage) int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// recentLoadTurns is how many reported turns RecentTurnLoad averages.
const recentLoadTurns = 3

// RecentTurnLoad is what a turn of this conversation's tool has recently cost:
// the cache reads and output the last few reported turns summed over their
// requests, averaged. ok is false when no turn reported usage (Codex).
func RecentTurnLoad(events []*StreamEvent) (cacheRead, output int, ok bool) {
	run := sameToolTail(events)
	n := 0
	for i := len(run) - 1; i >= 0 && n < recentLoadTurns; i-- {
		if ev := run[i]; ev.Type == "result" && ev.Usage != nil {
			cacheRead += ev.Usage.CacheReadInputTokens
			output += ev.Usage.OutputTokens
			n++
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return cacheRead / n, output / n, true
}

func isInit(ev *StreamEvent) bool { return ev.Type == "system" && ev.Subtype == "init" }

// sameToolTail is the history since the session last moved to the tool it runs
// now. Usage another tool reported describes that tool's conversation, not
// this one. Without init events the whole history counts.
func sameToolTail(events []*StreamEvent) []*StreamEvent {
	start, codex, found := 0, false, false
	for i := len(events) - 1; i >= 0; i-- {
		if !isInit(events[i]) {
			continue
		}
		isCodex := events[i].Version == codexInitVersion
		if found && isCodex != codex {
			break
		}
		start, codex, found = i, isCodex, true
	}
	return events[start:]
}

// sameConversationTail narrows to the conversation the CLI resumes: a model
// switch resumes the same id, a fresh start opens a new one.
func sameConversationTail(events []*StreamEvent) []*StreamEvent {
	start, id, found := 0, "", false
	for i := len(events) - 1; i >= 0; i-- {
		if !isInit(events[i]) {
			continue
		}
		if found && events[i].SessionID != id {
			break
		}
		start, id, found = i, events[i].SessionID, true
	}
	return events[start:]
}

// contentBytes sums the main conversation's text, tool calls and tool results.
// A resumed Codex thread may replay its earlier items into a history that
// already holds them, so each tool call, result and message counts once.
func contentBytes(events []*StreamEvent) int {
	seen := map[string]bool{}
	n := 0
	once := func(key string, size int) {
		if !seen[key] {
			seen[key] = true
			n += size
		}
	}
	for _, ev := range events {
		if ev.Message == nil || ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
			continue
		}
		for _, b := range ev.Message.Content {
			switch {
			case b.Type == "text":
				once(ev.Type+"\x00"+b.Text, len(b.Text))
			case b.Type == "tool_use" && b.ID != "":
				once("use\x00"+b.ID, len(b.Name)+len(b.Input))
			case b.Type == "tool_result" && b.ToolUseID != "":
				once("result\x00"+b.ToolUseID, len(b.Content))
			}
		}
	}
	return n
}
