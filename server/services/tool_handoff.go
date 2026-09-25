package services

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CountUserTurns is how many user messages the chat history holds.
func CountUserTurns(events []*StreamEvent) int {
	n := 0
	for _, ev := range events {
		if ev.Type == "user" && ev.Message != nil && hasTextBlock(ev.Message.Content) {
			n++
		}
	}
	return n
}

// BuildToolHandoff renders the part of a chat another tool has not seen, for
// that tool to read before the next request. It is built by plain code from the
// recorded events, never summarized by a model: user messages are verbatim;
// replies and tool calls are clipped. Only turns after sinceTurn are included
// (0 = everything). Over maxChars, the first request and the newest entries are
// kept. It returns "" when there is nothing to hand over, and the turn count.
func BuildToolHandoff(events []*StreamEvent, sinceTurn int, returning bool, maxChars int) (string, int) {
	var entries []string
	turn, first := 0, ""
	for _, ev := range events {
		if ev.Message == nil || ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
			continue
		}
		isUser := ev.Type == "user" && hasTextBlock(ev.Message.Content)
		if isUser {
			turn++
		}
		if turn <= sinceTurn {
			continue
		}
		for _, b := range ev.Message.Content {
			switch {
			case ev.Type == "user" && b.Type == "text":
				e := "[사용자]\n" + b.Text
				if first == "" {
					first = e
				}
				entries = append(entries, e)
			case ev.Type == "assistant" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
				entries = append(entries, "[이전 어시스턴트]\n"+clipRunes(b.Text, 1500))
			case ev.Type == "assistant" && b.Type == "tool_use":
				entries = append(entries, "[이전 어시스턴트의 도구 사용] "+b.Name+toolInputSummary(b.Input))
			}
		}
	}
	if len(entries) == 0 {
		return "", 0
	}
	head := "아래는 같은 프로젝트 폴더에서 다른 AI 코딩 도구와 나눈 이전 대화입니다. 그 도구가 한 파일 변경은 이미 작업 폴더에 반영되어 있습니다. 맥락으로만 참고하고, 맨 끝의 [현재 요청]에 답하세요."
	if returning {
		head = "당신이 이 대화를 잠시 떠난 사이, 같은 프로젝트 폴더에서 다른 AI 코딩 도구와 아래 대화가 이어졌습니다. 그 도구가 한 파일 변경은 이미 작업 폴더에 반영되어 있습니다. 맥락으로만 참고하고, 맨 끝의 [현재 요청]에 답하세요."
	}
	tail := "\n\n[현재 요청]\n"
	body := strings.Join(entries, "\n\n")
	if len(head)+len(body)+len(tail) > maxChars {
		// Keep the first request (it states the goal) and as many newest entries as fit.
		budget := maxChars - len(head) - len(tail) - len(first) - 64
		kept := []string{}
		for i := len(entries) - 1; i > 0 && budget > 0; i-- {
			if len(entries[i])+2 > budget {
				break
			}
			budget -= len(entries[i]) + 2
			kept = append([]string{entries[i]}, kept...)
		}
		skipped := len(entries) - 1 - len(kept)
		body = first + fmt.Sprintf("\n\n(중간 %d개 항목 생략)\n\n", skipped) + strings.Join(kept, "\n\n")
	}
	return "<이전 대화 인계>\n" + head + "\n\n" + body + "\n</이전 대화 인계>" + tail, turn - sinceTurn
}

func toolInputSummary(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "command", "pattern", "url", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			return ": " + clipRunes(v, 200)
		}
	}
	return ""
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
