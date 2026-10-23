package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func initEvent(sessionID, version string) *StreamEvent {
	return &StreamEvent{Type: "system", Subtype: "init", SessionID: sessionID, Version: version}
}

func textEvent(typ, text string) *StreamEvent {
	return &StreamEvent{Type: typ, Message: &StreamMessage{Role: typ, Content: []ContentBlock{{Type: "text", Text: text}}}}
}

func toolUseEvent(id, command string) *StreamEvent {
	in, _ := json.Marshal(map[string]string{"command": command})
	return &StreamEvent{Type: "assistant", Message: &StreamMessage{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ID: id, Name: "Bash", Input: in}}}}
}

func toolResultEvent(id, output string) *StreamEvent {
	raw, _ := json.Marshal(output)
	return &StreamEvent{Type: "user", Message: &StreamMessage{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: id, Content: raw}}}}
}

func usageResult(n int) *StreamEvent {
	return &StreamEvent{Type: "result", Usage: &StreamUsage{InputTokens: n}}
}

// Codex reports no usage. Its tool output is most of what the model re-reads,
// so the estimate must count it; the old handoff-text estimate left it out and
// rated a conversation that had read 90 KB of files as a few hundred tokens.
func TestCodexConversationSizeCountsToolOutput(t *testing.T) {
	out := strings.Repeat("x", 90000) // ≈ 30000 tokens of command output
	h := []*StreamEvent{
		initEvent("thread-1", codexInitVersion),
		textEvent("user", "look at the server"),
		toolUseEvent("call-1", "cat server.go"),
		toolResultEvent("call-1", out),
		textEvent("assistant", "done"),
		nativeResultEvent(),
	}
	if got := ConversationTokens(h); got < 30000 {
		t.Fatalf("codex context = %d, want ≥ 30000", got)
	}
	if handoff, _ := BuildToolHandoff(h, 0, false, 120000); len(handoff)/3 > 1000 {
		t.Fatalf("control: the handoff text was expected to omit tool output (%d bytes)", len(handoff))
	}
}

// A model switch resumes the same Codex thread; if the thread replays its
// items into a history that already holds them, they count once.
func TestCodexResumeReplayCountsOnce(t *testing.T) {
	out := strings.Repeat("y", 30000)
	turn := []*StreamEvent{textEvent("user", "q"), toolUseEvent("c1", "ls"), toolResultEvent("c1", out), textEvent("assistant", "a"), nativeResultEvent()}
	once := append([]*StreamEvent{initEvent("t1", codexInitVersion)}, turn...)
	replayed := append(append(append([]*StreamEvent{}, once...), initEvent("t1", codexInitVersion)), turn...)
	if a, b := ConversationTokens(once), ConversationTokens(replayed); a != b {
		t.Fatalf("replayed history counted twice: %d vs %d", a, b)
	}
}

// After a fresh start (new thread id) only the new conversation counts, and
// after a move from Claude, Claude's reported usage no longer describes it.
func TestConversationSizeStopsAtConversationAndToolBoundaries(t *testing.T) {
	big := strings.Repeat("z", 60000)
	h := []*StreamEvent{
		initEvent("claude-1", "2.1.212"),
		textEvent("user", "start"),
		usageResult(80000),
		initEvent("thread-1", codexInitVersion),
		textEvent("user", big),
		nativeResultEvent(),
		initEvent("thread-2", codexInitVersion), // fresh start
		textEvent("user", "small"),
		nativeResultEvent(),
	}
	if got := ConversationTokens(h); got > 100 {
		t.Fatalf("fresh codex conversation = %d, want only its own turns", got)
	}
	if got := ConversationTokens(h[:6]); got < 20000 || got >= 80000 {
		t.Fatalf("codex after claude = %d, want its own estimate, not claude's usage", got)
	}
}

// Claude reports usage: that number is used as is, across its own model
// switches (resume), and subagent messages are not part of the main context.
func TestClaudeConversationSizeUsesReportedUsage(t *testing.T) {
	sub := "task-1"
	h := []*StreamEvent{
		initEvent("s1", "2.1.212"),
		textEvent("user", "hi"),
		usageResult(30000),
		initEvent("s2", "2.1.212"), // model switch
		textEvent("user", "again"),
		{Type: "result"},
	}
	if got := ConversationTokens(h); got != 30000 {
		t.Fatalf("claude context = %d", got)
	}
	subEv := textEvent("assistant", strings.Repeat("s", 9000))
	subEv.ParentToolUseID = &sub
	if got := ConversationTokens([]*StreamEvent{subEv}); got != 0 {
		t.Fatalf("subagent text counted: %d", got)
	}
}

// Escalating from a rejected local answer hands over the conversation before
// that request, never the local model's own work on it: not its answer, not
// the files it read. The working tree shows what it changed.
func TestEscalationHandoffLeavesOutRejectedLocalTurn(t *testing.T) {
	h := []*StreamEvent{
		initEvent("thread-1", codexInitVersion),
		textEvent("user", "첫 요청: 로그인 화면 만들기"),
		textEvent("assistant", "로그인 화면을 만들었습니다"),
		nativeResultEvent(),
		textEvent("user", "비밀번호 재설정도 추가해줘"),
		toolUseEvent("c1", "cat auth.go"),
		toolResultEvent("c1", strings.Repeat("LOCALREAD", 5000)),
		textEvent("assistant", "LOCAL-ANSWER 재설정을 추가했습니다"),
		nativeResultEvent(),
	}
	got, turns := BuildEscalationHandoff(h, 0, "", "브랜치: main\n커밋 안 된 변경:\n M auth.go", 120000)
	for _, want := range []string{"첫 요청: 로그인 화면 만들기", "로그인 화면을 만들었습니다", "M auth.go", "[현재 요청]"} {
		if !strings.Contains(got, want) {
			t.Errorf("handoff lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"LOCAL-ANSWER", "LOCALREAD", "cat auth.go", "비밀번호 재설정도 추가해줘"} {
		if strings.Contains(got, leak) {
			t.Errorf("handoff carries the rejected turn (%q)", leak)
		}
	}
	if turns != 1 {
		t.Errorf("turns = %d, want 1", turns)
	}
	if full := ConversationTokens(h); len(got)/3 >= full {
		t.Errorf("handoff (%d tokens) is not smaller than resuming (%d)", len(got)/3, full)
	}
}

// A first request answered by the local model leaves nothing earlier to hand
// over; a fresh-start memo stands for turns before the fresh point.
func TestEscalationHandoffFirstTurnAndMemo(t *testing.T) {
	h := []*StreamEvent{textEvent("user", "only request"), textEvent("assistant", "local reply"), nativeResultEvent()}
	got, turns := BuildEscalationHandoff(h, 0, "", "", 120000)
	if strings.Contains(got, "only request") || strings.Contains(got, "local reply") || turns != 0 || !strings.HasSuffix(got, "[현재 요청]\n") {
		t.Fatalf("first-turn handoff: %d %q", turns, got)
	}
	h = append([]*StreamEvent{textEvent("user", "old turn"), textEvent("assistant", "old reply")}, h...)
	got, _ = BuildEscalationHandoff(h, 1, "MEMO-TEXT", "", 120000)
	if !strings.Contains(got, "MEMO-TEXT") || strings.Contains(got, "old turn") {
		t.Fatalf("memo handoff: %q", got)
	}
}

// Claude's result usage sums every request of a turn; the conversation size is
// the last request's, from its assistant message. The turn load is the
// result's sums, averaged over the last few turns.
func TestClaudeContextIsLastRequestNotTurnSum(t *testing.T) {
	req := func(n int) *StreamEvent {
		ev := textEvent("assistant", "step")
		ev.Message.Usage = &StreamUsage{InputTokens: 10, CacheReadInputTokens: n}
		return ev
	}
	h := []*StreamEvent{
		initEvent("s1", "2.1.212"),
		textEvent("user", "go"),
		req(20000), req(21000), req(22000),
		{Type: "result", Usage: &StreamUsage{InputTokens: 30, CacheReadInputTokens: 63000, OutputTokens: 900}},
	}
	if got := ConversationTokens(h); got != 22010 {
		t.Fatalf("context = %d, want the last request's 22010", got)
	}
	h = append(h, textEvent("user", "again"), &StreamEvent{Type: "result", Usage: &StreamUsage{CacheReadInputTokens: 37000, OutputTokens: 100}})
	if cr, out, ok := RecentTurnLoad(h); !ok || cr != 50000 || out != 500 {
		t.Fatalf("load = %d %d %v", cr, out, ok)
	}
	if _, _, ok := RecentTurnLoad([]*StreamEvent{initEvent("t", codexInitVersion), nativeResultEvent()}); ok {
		t.Fatal("codex reported no usage; load must be unknown")
	}
}
