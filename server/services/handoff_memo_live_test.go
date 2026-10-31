package services

import (
	"context"
	"encoding/json"
	"fmt"
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
// agent actually did next scores the memos blind (A/B/… order shuffled).
//
//	RUN_MEMO_EVAL=1 MEMO_EVAL_CWD=/path/to/project MEMO_EVAL_OUT=report.json go test ./services -run MemoEvalLive -v -timeout 60m
//
// MEMO_EVAL_WRITERS picks the claude memo models as name=model[:effort],…
// (default: the shipped Sonnet 5 low against Haiku 4.5).
//
// It spends real subscription usage (memo models + the grader).
func TestMemoEvalLive(t *testing.T) {
	if os.Getenv("RUN_MEMO_EVAL") != "1" {
		t.Skip("set RUN_MEMO_EVAL=1 (spends subscription usage)")
	}
	cwd := os.Getenv("MEMO_EVAL_CWD")
	spec := os.Getenv("MEMO_EVAL_WRITERS")
	if spec == "" {
		spec = "haiku=claude-haiku-4-5,sonnet=claude-sonnet-5:low"
	}
	writers := map[string]MemoWriter{}
	var order []string
	for _, item := range strings.Split(spec, ",") {
		name, model, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok || name == "" || model == "" {
			t.Fatalf("MEMO_EVAL_WRITERS item %q: want name=model[:effort]", item)
		}
		model, effort, _ := strings.Cut(model, ":")
		writers[name] = MemoWriter{Adapter: "claude", Model: model, Effort: effort}
		order = append(order, name)
	}
	if len(order) > 26 {
		t.Fatal("at most 26 writers")
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
		for _, name := range order {
			start := time.Now()
			memo, usage, err := WriteMemo(context.Background(), writers[name], cwd, req)
			r.Seconds[name] = time.Since(start).Seconds()
			if err != nil {
				t.Fatalf("sample %d %s: %v", i, name, err)
			}
			r.Memo[name], r.Usage[name] = memo, usage
		}
		names := append([]string(nil), order...)
		rng.Shuffle(len(names), func(a, b int) { names[a], names[b] = names[b], names[a] })
		body, _ := handoffBody(s.history, 0, memoInputMaxChars)
		var memos, shape []string
		for k, name := range names {
			l := string(rune('A' + k))
			memos = append(memos, "<메모 "+l+">\n"+r.Memo[name]+"\n</메모 "+l+">")
			shape = append(shape, `"`+l+`":{"score":n,"missing":[...],"wrong":[...]}`)
		}
		prompt := "<대화 기록>\n" + body + "\n</대화 기록>\n\n[다음 요청]\n" + s.goal +
			"\n\n<전체 대화를 가진 에이전트가 실제로 한 일>\n" + s.next + "\n</전체 대화를 가진 에이전트가 실제로 한 일>\n\n" +
			strings.Join(memos, "\n\n") + "\n\n" + graderPrompt + "{" + strings.Join(shape, ",") + "}"
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
		var line []string
		for k, name := range names {
			v, ok := g[string(rune('A'+k))]
			if !ok {
				t.Fatalf("grade %d: no score for %s in %q", i, name, env.Result)
			}
			r.Verdict[name] = v
		}
		for _, name := range order {
			line = append(line, fmt.Sprintf("%s %d (%.0fs, %d chars)", name, r.Verdict[name].Score, r.Seconds[name], utf8.RuneCountInString(r.Memo[name])))
		}
		t.Logf("sample %d (%d chars in): %s | %s", i, len(req), strings.Join(line, " · "), clipRunes(s.goal, 60))
		results = append(results, r)
	}
	for _, name := range order {
		var sum, missing, wrong int
		for _, r := range results {
			sum += r.Verdict[name].Score
			missing += len(r.Verdict[name].Missing)
			wrong += len(r.Verdict[name].Wrong)
		}
		t.Logf("%s (%s %s): mean %.2f/5, %d missing, %d wrong over %d samples", name, writers[name].Model, writers[name].Effort,
			float64(sum)/float64(len(results)), missing, wrong, len(results))
	}
	if p := os.Getenv("MEMO_EVAL_OUT"); p != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		os.WriteFile(p, b, 0600)
	}
}

const graderPrompt = `위 대화를 새 대화로 옮기면서, 다음 AI는 대화 기록 대신 메모 하나와 최근 2턴 원문, 저장소 파일만 보고 [다음 요청]을 처리합니다. 메모를 각각 따로 채점하세요.

점수 (1~5):
5 = 이 메모만으로 다음 요청을 전체 대화를 가진 에이전트처럼 올바르게 이어갈 수 있다
4 = 사소한 것만 빠져서, 파일을 조금 더 읽으면 된다
3 = 이어갈 수는 있지만 중요한 결정이나 맥락 일부를 다시 찾거나 사용자에게 물어야 한다
2 = 중요한 결정·제약이 빠져 잘못된 방향으로 갈 가능성이 크다
1 = 틀린 내용이 있거나 거의 쓸모없다

missing: 다음 요청을 처리하는 데 필요한데 빠진 것 (없으면 빈 배열)
wrong: 대화와 다른 틀린 내용 (없으면 빈 배열)

JSON만 답하세요: `

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
				if routing.IsFollowUp(goal) || utf8.RuneCountInString(goal) < 15 || strings.HasPrefix(goal, "<") || strings.Contains(goal, "This session is being continued") ||
					strings.HasPrefix(goal, "Base directory for this skill") { // a skill body, not something the user typed
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
