package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func assistantText(t string) *StreamEvent {
	return &StreamEvent{Type: "assistant", Message: &StreamMessage{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: t}}}}
}

func TestBuildToolHandoff(t *testing.T) {
	edit, _ := json.Marshal(map[string]string{"file_path": "README.md", "old_string": "x"})
	evs := []*StreamEvent{
		nativeTextEvent("user", "README 오타 고쳐줘"),
		{Type: "assistant", Message: &StreamMessage{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", Name: "Edit", Input: edit}}}},
		{Type: "user", Message: &StreamMessage{Role: "user", Content: []ContentBlock{{Type: "tool_result"}}}},
		assistantText("고쳤습니다."),
		{Type: "result"},
		nativeTextEvent("user", "테스트도 추가해줘"),
		assistantText("추가했습니다."),
	}
	if CountUserTurns(evs) != 2 {
		t.Fatalf("turns = %d", CountUserTurns(evs))
	}
	full, n := BuildToolHandoff(evs, 0, false, 100000)
	for _, want := range []string{"README 오타 고쳐줘", "Edit: README.md", "고쳤습니다.", "테스트도 추가해줘", "[현재 요청]"} {
		if !strings.Contains(full, want) {
			t.Errorf("full handoff missing %q", want)
		}
	}
	if n != 2 || strings.Contains(full, "old_string") {
		t.Fatalf("n=%d or raw tool input leaked", n)
	}
	delta, n := BuildToolHandoff(evs, 1, true, 100000)
	if n != 1 || strings.Contains(delta, "README 오타") || !strings.Contains(delta, "테스트도 추가해줘") || !strings.Contains(delta, "떠난 사이") {
		t.Fatalf("delta n=%d:\n%s", n, delta)
	}
	if none, n := BuildToolHandoff(evs, 2, true, 100000); none != "" || n != 0 {
		t.Fatal("nothing missed should hand over nothing")
	}
}

func TestBuildToolHandoffKeepsGoalWhenClipped(t *testing.T) {
	evs := []*StreamEvent{nativeTextEvent("user", "목표: 인증 재설계")}
	for i := 0; i < 200; i++ {
		evs = append(evs, assistantText(strings.Repeat("긴 답변 ", 100)), nativeTextEvent("user", "다음 단계"))
	}
	evs = append(evs, nativeTextEvent("user", "마지막 요청"))
	out, _ := BuildToolHandoff(evs, 0, false, 20000)
	if len(out) > 20000 || !strings.Contains(out, "목표: 인증 재설계") || !strings.Contains(out, "마지막 요청") || !strings.Contains(out, "생략") {
		t.Fatalf("clipped handoff len=%d wrong", len(out))
	}
}

func TestBuildDelegateBrief(t *testing.T) {
	edit, _ := json.Marshal(map[string]string{"file_path": "auth/session.go"})
	evs := []*StreamEvent{
		nativeTextEvent("user", "로그인 버그 고쳐줘"),
		{Type: "assistant", Message: &StreamMessage{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", Name: "Edit", Input: edit}}}},
		assistantText("세션 만료 처리를 고쳤습니다."),
		nativeTextEvent("user", strings.Repeat("긴 대화 ", 5000)),
	}
	brief := BuildDelegateBrief(evs, "인증 구조 전체를 재설계해줘")
	for _, want := range []string{"로그인 버그 고쳐줘", "auth/session.go", "세션 만료 처리를 고쳤습니다.", "인증 구조 전체를 재설계해줘", "수정·생성·삭제하지"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief missing %q", want)
		}
	}
	if strings.Contains(brief, "긴 대화 긴 대화") || len(brief) > 12000 {
		t.Fatalf("brief carried the conversation (%d bytes)", len(brief))
	}
}

func TestModelDisplay(t *testing.T) {
	for in, want := range map[string]string{"gpt-5.6-sol": "GPT-5.6 Sol", "claude-opus-5-5": "Opus 5.5", "claude-haiku-4-5-20251001": "Haiku 4.5", "claude-sonnet-5": "Sonnet 5", "": "기본 모델", "oss:mac:mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit": "로컬 · Qwen3 30B A3B"} {
		if got := modelDisplay(in); got != want {
			t.Errorf("modelDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}
