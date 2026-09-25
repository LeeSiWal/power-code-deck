package services

import (
	"context"
	"encoding/json"

	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"powercodedeck/internal/routing"
)

// TestMemoEvalLive compares memo models on this project's own past Claude Code
// conversations: at cut points in the longest ones, each model writes a memo,
// and a grader that reads the whole conversation plus what the full-context
// agent actually did next scores the memos blind (A/B order shuffled).
//
//	RUN_MEMO_EVAL=1 MEMO_EVAL_CWD=/path/to/project MEMO_EVAL_OUT=report.json go test ./services -run MemoEvalLive -v -timeout 60m
//
// It spends real subscription usage (memo models + the grader).
func TestMemoEvalLive(t *testing.T) {
	if os.Getenv("RUN_MEMO_EVAL") != "1" {
		t.Skip("set RUN_MEMO_EVAL=1 (spends subscription usage)")
	}
	cwd := os.Getenv("MEMO_EVAL_CWD")
	writers := map[string]MemoWriter{
		"haiku":  {Adapter: "claude", Model: "claude-haiku-4-5"},
		"sonnet": {Adapter: "claude", Model: "claude-sonnet-5", Effort: "low"},
	}
	grader := "claude-opus-5-5"
	samples := memoEvalSamples(t, cwd, 3, []float64{0.5, 0.85})
	type verdict struct {
		Score   int      `json:"score"`
		Missing []string `json:"missing"`
		Wrong   []string `json:"wrong"`
	}
	type result struct {
		Session, Goal string
		InputChars    int
		Memo          map[string]string
		Seconds       map[string]float64
		Usage         map[string]any
		Verdict       map[string]verdict
	}
	var results []result
	rng := rand.New(rand.NewSource(1))
	for i, s := range samples {
		req := BuildMemoRequest(s.history, s.goal)
		r := result{Session: s.session, Goal: s.goal, InputChars: len(req), Memo: map[string]string{}, Seconds: map[string]float64{}, Usage: map[string]any{}, Verdict: map[string]verdict{}}
		for name, w := range writers {
			start := time.Now()
			memo, usage, err := WriteMemo(context.Background(), w, cwd, req)
			r.Seconds[name] = time.Since(start).Seconds()
			if err != nil {
				t.Fatalf("sample %d %s: %v", i, name, err)
			}
			r.Memo[name], r.Usage[name] = memo, usage
		}
		names := []string{"haiku", "sonnet"}
		rng.Shuffle(len(names), func(a, b int) { names[a], names[b] = names[b], names[a] })
		body, _ := handoffBody(s.history, 0, memoInputMaxChars)
		prompt := "<대화 기록>\n" + body + "\n</대화 기록>\n\n[다음 요청]\n" + s.goal +
			"\n\n<전체 대화를 가진 에이전트가 실제로 한 일>\n" + s.next + "\n</전체 대화를 가진 에이전트가 실제로 한 일>\n\n" +
			"<메모 A>\n" + r.Memo[names[0]] + "\n</메모 A>\n\n<메모 B>\n" + r.Memo[names[1]] + "\n</메모 B>\n\n" + graderPrompt
		out, err := runMemoCLI(context.Background(), "claude", []string{"-p", "--output-format", "json", "--tools", "", "--no-session-persistence", "--model", grader,
			"--system-prompt", "You grade handoff memos for coding agents. Reply with JSON only."}, cwd, prompt)
		if err != nil {
			t.Fatalf("grade %d: %v", i, err)
		}
		var env struct{ Result string }
		json.Unmarshal(out, &env)
		raw := env.Result
		if a, b := strings.Index(raw, "{"), strings.LastIndex(raw, "}"); a >= 0 && b > a {
			raw = raw[a : b+1]
		}
		var g map[string]verdict
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			t.Fatalf("grade %d: unreadable %q", i, env.Result)
		}
		r.Verdict[names[0]], r.Verdict[names[1]] = g["A"], g["B"]
		t.Logf("sample %d (%d chars in): haiku %d (%.0fs, %d chars) · sonnet %d (%.0fs, %d chars) | %s", i, len(req),
			r.Verdict["haiku"].Score, r.Seconds["haiku"], utf8.RuneCountInString(r.Memo["haiku"]),
			r.Verdict["sonnet"].Score, r.Seconds["sonnet"], utf8.RuneCountInString(r.Memo["sonnet"]), clipRunes(s.goal, 60))
		results = append(results, r)
	}
	if p := os.Getenv("MEMO_EVAL_OUT"); p != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		os.WriteFile(p, b, 0600)
	}
}

const graderPrompt = `위 대화를 새 대화로 옮기면서, 다음 AI는 대화 기록 대신 메모 하나와 최근 2턴 원문, 저장소 파일만 보고 [다음 요청]을 처리합니다. 메모 A와 B를 각각 채점하세요.

점수 (1~5):
5 = 이 메모만으로 다음 요청을 전체 대화를 가진 에이전트처럼 올바르게 이어갈 수 있다
4 = 사소한 것만 빠져서, 파일을 조금 더 읽으면 된다
3 = 이어갈 수는 있지만 중요한 결정이나 맥락 일부를 다시 찾거나 사용자에게 물어야 한다
2 = 중요한 결정·제약이 빠져 잘못된 방향으로 갈 가능성이 크다
1 = 틀린 내용이 있거나 거의 쓸모없다

missing: 다음 요청을 처리하는 데 필요한데 빠진 것 (없으면 빈 배열)
wrong: 대화와 다른 틀린 내용 (없으면 빈 배열)

JSON만 답하세요: {"A":{"score":n,"missing":[...],"wrong":[...]},"B":{"score":n,"missing":[...],"wrong":[...]}}`

type memoSample struct {
	session, goal, next string
	history             []*StreamEvent
}

// memoEvalSamples cuts the `sessions` longest conversations of cwd at the given
// fractions of their user turns, at the first request there that stands on its
// own (a follow-up like "다시 해줘" is never where a fresh start happens).
func memoEvalSamples(t *testing.T, cwd string, sessions int, at []float64) []memoSample {
	files, _ := filepath.Glob(filepath.Join(claudeProjectDir(cwd), "*.jsonl"))
	sort.Slice(files, func(i, j int) bool { return fileSize(files[i]) > fileSize(files[j]) })
	var out []memoSample
	for _, f := range files {
		if len(out) >= sessions*len(at) {
			break
		}
		sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		evs, err := ReadSessionEvents(cwd, sid)
		if err != nil {
			continue
		}
		var userIdx []int
		for i, ev := range evs {
			if ev.Type == "user" && ev.Message != nil && hasTextBlock(ev.Message.Content) {
				userIdx = append(userIdx, i)
			}
		}
		if len(userIdx) < 10 {
			continue
		}
		for _, frac := range at {
			for k := int(float64(len(userIdx)) * frac); k < len(userIdx)-1; k++ {
				goal := userText(evs[userIdx[k]])
				if routing.IsFollowUp(goal) || utf8.RuneCountInString(goal) < 15 || strings.HasPrefix(goal, "<") || strings.Contains(goal, "This session is being continued") {
					continue
				}
				hist := evs[:userIdx[k]]
				if body, _ := handoffBody(hist, 0, memoInputMaxChars); len(body) < 30000 {
					break
				}
				next, _ := handoffBody(evs[userIdx[k]:userIdx[k+1]], 0, 20000)
				out = append(out, memoSample{session: sid, goal: goal, next: next, history: hist})
				break
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no samples")
	}
	return out
}

func userText(ev *StreamEvent) string {
	var parts []string
	for _, b := range ev.Message.Content {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}
