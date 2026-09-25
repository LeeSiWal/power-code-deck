package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func memoHistory() []*StreamEvent {
	return []*StreamEvent{
		nativeTextEvent("user", "첫 요청: 인증 재설계"),
		assistantText("계획을 세웠습니다."),
		nativeTextEvent("user", "두 번째 요청"),
		assistantText("두 번째 답"),
		nativeTextEvent("user", "세 번째 요청"),
		assistantText("세 번째 답"),
	}
}

func TestBuildMemoRequest(t *testing.T) {
	req := BuildMemoRequest(memoHistory(), "다음 할 일")
	for _, want := range []string{"## 목표", "<대화 기록>", "첫 요청: 인증 재설계", "세 번째 답", "[다음 요청]\n다음 할 일"} {
		if !strings.Contains(req, want) {
			t.Errorf("memo request missing %q", want)
		}
	}
}

func TestBuildFreshPrefix(t *testing.T) {
	p := BuildFreshPrefix("  메모 본문  ", memoHistory(), "브랜치: main")
	for _, want := range []string{"[인계 메모]\n메모 본문\n", "[최근 대화 원문]", "두 번째 요청", "세 번째 답", "[작업 폴더 상태]\n브랜치: main"} {
		if !strings.Contains(p, want) {
			t.Errorf("prefix missing %q", want)
		}
	}
	if strings.Contains(p, "첫 요청") {
		t.Error("only the last two turns are verbatim; the first belongs to the memo")
	}
	if !strings.HasSuffix(p, "[현재 요청]\n") {
		t.Error("prefix must end where the user's message goes")
	}
	if strings.Contains(BuildFreshPrefix("m", memoHistory(), ""), "[작업 폴더 상태]") {
		t.Error("no repo state outside a repository")
	}
}

func TestWithMemo(t *testing.T) {
	if got := WithMemo("메모", ""); !strings.Contains(got, "메모") || !strings.HasSuffix(got, "[현재 요청]\n") {
		t.Fatalf("memo only: %q", got)
	}
	h, _ := BuildToolHandoff(memoHistory(), 2, false, 100000)
	got := WithMemo("메모", h)
	if !strings.HasPrefix(got, "<이전 대화 인계 메모>") || !strings.Contains(got, "세 번째 요청") || strings.Contains(got, "첫 요청") {
		t.Fatalf("memo + delta: %q", got)
	}
}

func TestRepoState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	if RepoState(dir) != "" {
		t.Fatal("not a repository → empty")
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=a@b", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0600)
	st := RepoState(dir)
	for _, want := range []string{"브랜치: main", "?? new.txt", "first"} {
		if !strings.Contains(st, want) {
			t.Errorf("repo state missing %q:\n%s", want, st)
		}
	}
}
