package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTranscript는 mtime을 지정해 트랜스크립트 파일을 만든다. 지목 규칙이 mtime
// 순서에 의존하므로 순서를 테스트가 직접 정해야 한다.
func writeTranscript(t *testing.T, dir, name string, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

// 에이전트의 claude_session_id가 있으면 그 트랜스크립트를 본다 — 같은 프로젝트에서
// 더 최근에 쓰인 다른 세션의 파일이 있어도 마찬가지다. 이것이 교차오염을 막는 규칙이다.
func TestTargetTranscriptPrefersTheAgentsOwnSession(t *testing.T) {
	dir := t.TempDir()
	mine := writeTranscript(t, dir, "mine", time.Now().Add(-time.Hour))
	writeTranscript(t, dir, "someone-else", time.Now())

	w := &transcriptWatcher{
		agentID:      "a1",
		dir:          dir,
		sessionIDFor: func(string) string { return "mine" },
	}
	if got := w.targetTranscript(); got != mine {
		t.Fatalf("targetTranscript() = %q, want the agent's own file %q", got, mine)
	}
}

// 두 에이전트가 한 프로젝트에 있어도 각자 자기 파일을 본다.
func TestTargetTranscriptDoesNotCrossBetweenAgents(t *testing.T) {
	dir := t.TempDir()
	a := writeTranscript(t, dir, "sid-a", time.Now().Add(-time.Hour))
	b := writeTranscript(t, dir, "sid-b", time.Now())

	wa := &transcriptWatcher{agentID: "a", dir: dir, sessionIDFor: func(string) string { return "sid-a" }}
	wb := &transcriptWatcher{agentID: "b", dir: dir, sessionIDFor: func(string) string { return "sid-b" }}
	if got := wa.targetTranscript(); got != a {
		t.Fatalf("agent a targeted %q, want %q", got, a)
	}
	if got := wb.targetTranscript(); got != b {
		t.Fatalf("agent b targeted %q, want %q", got, b)
	}
}

// id를 아직 모르는 경우(네이티브는 system/init 전, 터미널 트랙은 영영)에는 예전처럼
// 최신 파일로 물러난다. 아무것도 안 보여주는 것보다 낫다.
func TestTargetTranscriptFallsBackToNewest(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "old", time.Now().Add(-time.Hour))
	newest := writeTranscript(t, dir, "new", time.Now())

	for name, lookup := range map[string]func(string) string{
		"게터 없음":    nil,
		"빈 id":     func(string) string { return "" },
		"파일 없는 id": func(string) string { return "does-not-exist" },
	} {
		w := &transcriptWatcher{agentID: "a1", dir: dir, sessionIDFor: lookup}
		if got := w.targetTranscript(); got != newest {
			t.Fatalf("%s: targetTranscript() = %q, want newest %q", name, got, newest)
		}
	}
}
