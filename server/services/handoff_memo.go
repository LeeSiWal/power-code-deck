package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"powercodedeck/internal/providers"
)

// A fresh start continues a long conversation in a new CLI conversation that
// begins with a one-page handoff memo instead of re-reading the old one.
// Measured on 30 days of real Claude Code use: 83% of the tokens billed were
// the conversation being re-read (cache reads), the median request after a
// pause of 5+ minutes re-read ~525k tokens, and the plain-text handoff of the
// same conversation (tool output left out) was ~18k (3%). A cheap model reads
// that text once and writes the memo; the working model then re-reads the memo
// instead of the whole conversation on every call.

// memoInputMaxChars bounds the conversation text the memo model reads
// (~150k tokens at 3 bytes/token; the smallest memo model has a 200k window).
const memoInputMaxChars = 450000

// memoTimeout bounds one memo call; the user is waiting for their message.
const memoTimeout = 90 * time.Second

// MemoWriter is the model that writes handoff memos.
type MemoWriter struct {
	Adapter string `json:"adapter"` // claude | codex
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
}

// memoSystem replaces the coding agent's system prompt for the memo call: the
// memo model only reads the text it is given.
const memoSystem = `You write handoff memos. A coding conversation between a user and an AI coding agent is being moved to a fresh conversation, and the next agent will read only your memo, the last turns verbatim and the repository itself. You have no tools; use only the text you are given. Write in the language the user writes in.`

// memoPrompt is the instruction in front of the conversation text.
const memoPrompt = `아래 <대화 기록>은 사용자와 AI 코딩 도구의 긴 대화입니다(도구 실행 결과는 빠져 있습니다). 이 작업을 새 대화에서 이어갈 다음 AI를 위해 인계 메모를 쓰세요. 다음 AI는 이 메모, 최근 대화 원문, 그리고 저장소 파일만 봅니다.

아래 양식을 그대로 쓰고, 해당 없는 항목은 "없음"으로 쓰세요.

## 목표
이 대화 전체가 하려는 일 (1~3줄)

## 사용자가 정한 것
사용자가 결정하거나 요구한 것, 선호, 금지 사항 (각 항목 한 줄. 사용자 표현을 살려서)

## 한 일
완료된 작업과 바뀐 파일 경로, 커밋·PR·배포 여부

## 현재 상태
진행 중인 것, 실패했거나 해결 안 된 문제, 기다리는 사용자 답

## 다음 요청에 필요한 맥락
맨 끝 [다음 요청]을 처리하는 데 필요한 사실: 관련 파일·함수·명령·수치·에러 메시지를 정확히

규칙:
- 대화에 있는 사실만 쓰고, 추측하지 마세요.
- 파일 경로, 함수 이름, 명령어, 숫자는 대화에 나온 그대로 쓰세요.
- 코드 내용은 옮기지 마세요. 다음 AI가 파일을 직접 읽습니다.
- 전체 4000자 이내.

`

// BuildMemoRequest is the text the memo model reads: the instruction, the
// conversation as plain text, and the next request (so the memo keeps what
// that request needs).
func BuildMemoRequest(history []*StreamEvent, goal string) string {
	body, _ := handoffBody(history, 0, memoInputMaxChars)
	return memoPrompt + "<대화 기록>\n" + body + "\n</대화 기록>\n\n[다음 요청]\n" + goal
}

// recentTurns renders the last n user turns verbatim (replies clipped long),
// so the newest context never depends on the memo.
func recentTurns(history []*StreamEvent, n int) string {
	total := CountUserTurns(history)
	since := total - n
	if since < 0 {
		since = 0
	}
	var entries []string
	turn := 0
	for _, ev := range history {
		if ev.Message == nil || ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
			continue
		}
		if ev.Type == "user" && hasTextBlock(ev.Message.Content) {
			turn++
		}
		if turn <= since {
			continue
		}
		for _, b := range ev.Message.Content {
			switch {
			case ev.Type == "user" && b.Type == "text":
				entries = append(entries, "[사용자]\n"+clipRunes(b.Text, 6000))
			case ev.Type == "assistant" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
				entries = append(entries, "[어시스턴트]\n"+clipRunes(b.Text, 6000))
			case ev.Type == "assistant" && b.Type == "tool_use":
				entries = append(entries, "[도구 사용] "+b.Name+toolInputSummary(b.Input))
			}
		}
	}
	return strings.Join(entries, "\n\n")
}

// BuildFreshPrefix is what the new conversation reads in front of the user's
// next message: the memo, the last turns verbatim and the repository state.
func BuildFreshPrefix(memo string, history []*StreamEvent, repoState string) string {
	var b strings.Builder
	b.WriteString("<이전 대화 인계>\n")
	b.WriteString("이 대화는 같은 프로젝트에서 이어지던 긴 대화를 토큰 절약을 위해 새로 시작한 것입니다. 아래 인계 메모(다른 모델이 이전 대화를 읽고 작성)와 최근 대화 원문을 참고하세요. 이전 대화에서 한 파일 변경은 이미 작업 폴더에 반영되어 있습니다. 메모와 실제 파일이 다르면 파일이 맞습니다. 필요한 파일은 직접 읽으세요. 이전 대화에서 정한 것은 그대로 지키세요.\n\n")
	b.WriteString("[인계 메모]\n" + strings.TrimSpace(memo) + "\n\n")
	if recent := recentTurns(history, 2); recent != "" {
		b.WriteString("[최근 대화 원문]\n" + recent + "\n\n")
	}
	if repoState != "" {
		b.WriteString("[작업 폴더 상태]\n" + repoState + "\n\n")
	}
	b.WriteString("</이전 대화 인계>\n\n[현재 요청]\n")
	return b.String()
}

// WithMemo puts the last fresh start's memo in front of a tool handoff (the
// handoff then covers only the turns since that fresh start; "" = none).
func WithMemo(memo, handoff string) string {
	block := "<이전 대화 인계 메모>\n더 앞선 대화는 아래 메모로 요약되어 있습니다(다른 모델이 작성). 메모와 실제 파일이 다르면 파일이 맞습니다.\n\n" + strings.TrimSpace(memo) + "\n</이전 대화 인계 메모>\n\n"
	if handoff == "" {
		return block + "[현재 요청]\n"
	}
	return block + handoff
}

// RepoState is the working tree as git reports it (branch, uncommitted files,
// recent commits) — the facts a memo could get wrong. "" outside a repository.
func RepoState(cwd string) string {
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(out), "\n")
	}
	branch := run("rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("브랜치: " + branch + "\n")
	if st := run("status", "--short"); st != "" {
		lines := strings.Split(st, "\n")
		if len(lines) > 40 {
			lines = append(lines[:40], fmt.Sprintf("… 외 %d개", len(lines)-40))
		}
		b.WriteString("커밋 안 된 변경:\n" + strings.Join(lines, "\n") + "\n")
	} else {
		b.WriteString("커밋 안 된 변경: 없음\n")
	}
	if log := run("log", "--oneline", "-5"); log != "" {
		b.WriteString("최근 커밋:\n" + log)
	}
	return strings.TrimRight(b.String(), "\n")
}

// WriteMemo asks the memo model for a handoff memo. The call has no tools and
// keeps no session: it reads the given text and answers once.
func WriteMemo(ctx context.Context, w MemoWriter, cwd, request string) (string, *providers.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, memoTimeout)
	defer cancel()
	var memo string
	var usage *providers.Usage
	var err error
	switch w.Adapter {
	case "claude":
		memo, usage, err = claudeMemo(ctx, w, cwd, request)
	case "codex":
		memo, err = codexMemo(ctx, w, cwd, request)
	default:
		return "", nil, fmt.Errorf("no memo adapter %q", w.Adapter)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return "", usage, fmt.Errorf("인계 메모가 %s 안에 끝나지 않았습니다", memoTimeout)
	}
	if err == nil && strings.TrimSpace(memo) == "" {
		err = fmt.Errorf("인계 메모가 비어 있습니다")
	}
	return strings.TrimSpace(memo), usage, err
}

func claudeMemo(ctx context.Context, w MemoWriter, cwd, request string) (string, *providers.Usage, error) {
	bin := "claude"
	if resolved := findAgentCommand("claude"); resolved != "" {
		bin = resolved
	}
	args := []string{"-p", "--output-format", "json", "--tools", "", "--no-session-persistence", "--system-prompt", memoSystem}
	if w.Model != "" {
		args = append(args, "--model", w.Model)
	}
	if w.Effort != "" {
		args = append(args, "--effort", w.Effort)
	}
	out, err := runMemoCLI(ctx, bin, args, cwd, request)
	if err != nil {
		return "", nil, err
	}
	var r struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Usage   *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", nil, fmt.Errorf("인계 메모 응답을 읽지 못했습니다: %.200s", out)
	}
	var usage *providers.Usage
	if r.Usage != nil {
		usage = &providers.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens,
			CacheCreationInputTokens: r.Usage.CacheCreationInputTokens, CacheReadInputTokens: r.Usage.CacheReadInputTokens}
	}
	if r.IsError {
		return "", usage, fmt.Errorf("인계 메모 호출 실패: %.300s", r.Result)
	}
	return r.Result, usage, nil
}

func codexMemo(ctx context.Context, w MemoWriter, cwd, request string) (string, error) {
	bin := "codex"
	if resolved := findAgentCommand("codex"); resolved != "" {
		bin = resolved
	}
	f, err := os.CreateTemp("", "pcd-memo-*.txt")
	if err != nil {
		return "", err
	}
	f.Close()
	defer os.Remove(f.Name())
	args := []string{"exec", "--skip-git-repo-check", "--sandbox", "read-only", "--output-last-message", f.Name()}
	if w.Model != "" {
		args = append(args, "--model", w.Model)
	}
	if w.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+w.Effort)
	}
	if _, err := runMemoCLI(ctx, bin, append(args, "-"), cwd, memoSystem+"\n\n"+request); err != nil {
		return "", err
	}
	b, err := os.ReadFile(f.Name())
	return string(b), err
}

func runMemoCLI(ctx context.Context, bin string, args []string, cwd, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cmd.Dir = os.TempDir()
	}
	locale := utf8Locale()
	cmd.Env = withAgentPath(append(os.Environ(), "LANG="+locale, "LC_ALL="+locale))
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %v: %.300s", filepath.Base(bin), err, strings.TrimSpace(stderr.String()+" "+string(out)))
	}
	return out, nil
}
