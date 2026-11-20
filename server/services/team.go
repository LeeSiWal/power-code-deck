package services

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/providers"
)

// teamFullTextTTL is how long a member's untruncated answer stays readable.
const teamFullTextTTL = time.Hour

// TeamOffReason is what a conductor hears when it calls the team while the toggle is off.
const TeamOffReason = "드림팀 모드가 꺼져 있습니다. 사용자에게 켜 달라고 하지 말고 직접 처리하세요."

// TeamDeps is what TeamService needs; NewExec and Conductor are seams so tests
// never start a real CLI.
type TeamDeps struct {
	DB     *sql.DB
	Agents *AgentService
	Usage  *AutoUsage
	// NewExec starts a read-only member execution (prod: RunProviders.NewRoleResume with effort "").
	NewExec func(provider providers.ID, id, cwd, model, mode, resume string) (providers.Execution, error)
	// Conductor reports the session's current tool, model and working dir.
	Conductor func(agentID string) (adapter, model, cwd string, ok bool)
}

// TeamCall is one ask_member request from a conductor session.
type TeamCall struct {
	AgentID, Member, Task string
	Files                 []string
}

// TeamResult is what the conductor gets back from ask_member.
type TeamResult struct {
	Status    string `json:"status"` // done | refused | limit | failed | timeout | not_installed | not_logged_in | quota
	Member    string `json:"member"`
	Label     string `json:"label"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	CallID    string `json:"callId"`
	ElapsedMs int64  `json:"elapsedMs"`
	Tokens    *struct {
		In  int `json:"in"`
		Out int `json:"out"`
	} `json:"tokens"` // nil = not reported
	Reason string `json:"reason,omitempty"`
}

type teamFull struct {
	agentID string
	text    string
	at      time.Time
}

// TeamService runs one member call at a time per request: it gates on the
// session's toggle and caps, runs the member read-only, and records the cost.
type TeamService struct {
	d TeamDeps

	mu       sync.Mutex
	turns    map[string]int // calls in the conductor's current turn
	tokens   map[string]int // Claude member tokens this server run
	extraCap map[string]int // RaiseCap additions
	full     map[string]teamFull

	sessMu sync.Mutex // serializes SetTeamSession's read-modify-write
}

func NewTeamService(d TeamDeps) *TeamService {
	return &TeamService{d: d, turns: map[string]int{}, tokens: map[string]int{}, extraCap: map[string]int{}, full: map[string]teamFull{}}
}

// ResetTurn starts a new conductor turn: the per-turn counter goes back to zero.
func (t *TeamService) ResetTurn(agentID string) {
	t.mu.Lock()
	delete(t.turns, agentID)
	t.mu.Unlock()
}

// RaiseCap lifts this session's token cap by one more SessionTokenCap, until
// the server restarts.
func (t *TeamService) RaiseCap(agentID string) {
	add := LoadTeamSettings(t.d.DB).SessionTokenCap
	t.mu.Lock()
	t.extraCap[agentID] += add
	t.mu.Unlock()
}

// FullText returns a call's untruncated answer for an hour after it finished.
func (t *TeamService) FullText(agentID, callID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.full[callID]
	if !ok || f.agentID != agentID || time.Since(f.at) > teamFullTextTTL {
		return "", false
	}
	return f.text, true
}

// Ask runs one member call and always returns a result; failures are statuses.
func (t *TeamService) Ask(ctx context.Context, c TeamCall) TeamResult {
	start := time.Now()
	res := TeamResult{Member: c.Member, CallID: "tm_" + rand.Text()[:12]}
	finish := func(status, reason string) TeamResult {
		res.Status, res.Reason = status, reason
		res.ElapsedMs = time.Since(start).Milliseconds()
		return res
	}

	if !t.d.Agents.TeamMode(c.AgentID) {
		return finish("refused", TeamOffReason)
	}
	s := LoadTeamSettings(t.d.DB)
	task := strings.TrimSpace(c.Task)
	if task == "" {
		return finish("refused", "요청서(task)가 비어 있습니다.")
	}
	var m TeamMember
	found := false
	for _, x := range s.Members {
		if x.ID == c.Member {
			m, found = x, true
		}
	}
	if !found {
		adapter, model, _, _ := t.d.Conductor(c.AgentID)
		var ids []string
		for _, x := range s.Members {
			if x.Enabled && !isTeamSelf(x, adapter, model) {
				ids = append(ids, x.ID)
			}
		}
		avail := "없음"
		if len(ids) > 0 {
			avail = strings.Join(ids, ", ")
		}
		return finish("refused", fmt.Sprintf("알 수 없는 직원입니다: %q. 부를 수 있는 직원 id: %s", c.Member, avail))
	}
	res.Label = m.Label
	if !m.Enabled {
		return finish("refused", m.Label+"은(는) 설정에서 꺼져 있습니다.")
	}
	adapter, model, cwd, ok := t.d.Conductor(c.AgentID)
	if !ok {
		return finish("refused", "지휘 세션을 찾을 수 없습니다.")
	}
	if isTeamSelf(m, adapter, model) {
		return finish("refused", m.Label+"은(는) 지금 세션 자신입니다. 직접 처리하세요.")
	}
	for _, f := range c.Files {
		if !teamFileInside(f) {
			return finish("refused", fmt.Sprintf("작업 폴더 밖의 파일은 넘길 수 없습니다: %q", f))
		}
	}
	provider := map[string]providers.ID{"claude": providers.Claude, "codex": providers.Codex}[m.Adapter]
	if provider == "" {
		return finish("refused", "지원하지 않는 직원 도구입니다: "+m.Adapter)
	}

	// Caps: check and count under one lock so parallel calls cannot overshoot.
	t.mu.Lock()
	if used := t.turns[c.AgentID]; used >= s.MaxCallsPerTurn {
		t.mu.Unlock()
		return finish("limit", fmt.Sprintf("이번 턴의 직원 호출 한도에 도달했습니다 (%d/%d). 남은 일은 직접 처리하세요.", used, s.MaxCallsPerTurn))
	}
	if m.Adapter == "claude" {
		limit := s.SessionTokenCap + t.extraCap[c.AgentID]
		if used := t.tokens[c.AgentID]; used >= limit {
			t.mu.Unlock()
			return finish("limit", fmt.Sprintf("이 세션의 직원 토큰 상한에 도달했습니다 (%d/%d). 직접 처리하세요.", used, limit))
		}
	}
	t.turns[c.AgentID]++
	t.mu.Unlock()

	brief := task
	if len(c.Files) > 0 {
		brief += "\n\n참고할 파일(직접 읽으세요):\n- " + strings.Join(c.Files, "\n- ")
	}
	brief += fmt.Sprintf("\n\n답은 핵심만 요약해서 %d자 이내로 주세요. 파일을 수정하지 마세요.", s.ResultMaxChars)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSec)*time.Second)
	defer cancel()
	resume := t.d.Agents.TeamSessions(c.AgentID)[m.ID]
	answer, conv, usage, err := t.runMember(runCtx, res.CallID, provider, cwd, m.Model, resume, brief)
	if err != nil {
		// At most one automatic retry: a stale resume starts fresh; a fresh run
		// tries once more unless it is a timeout, login or quota error.
		kind := classifyTeamError(runCtx, err)
		// A canceled call (the conductor was interrupted) is not retried either.
		if kind != "timeout" && runCtx.Err() == nil && (resume != "" || (kind != "not_logged_in" && kind != "quota")) {
			var more *providers.Usage
			answer, conv, more, err = t.runMember(runCtx, res.CallID+"_2", provider, cwd, m.Model, "", brief)
			usage = addTeamUsage(usage, more)
		}
	}

	if t.d.Usage != nil {
		t.d.Usage.RecordTeam(c.AgentID, m, len(brief)/3, usage)
	}
	if usage != nil {
		res.Tokens = &struct {
			In  int `json:"in"`
			Out int `json:"out"`
		}{usage.InputTokens + usage.CacheCreationInputTokens, usage.OutputTokens}
		if m.Adapter == "claude" {
			t.mu.Lock()
			t.tokens[c.AgentID] += usage.InputTokens + usage.OutputTokens + usage.CacheCreationInputTokens
			t.mu.Unlock()
		}
	}
	if err != nil {
		kind := classifyTeamError(runCtx, err)
		return finish(kind, teamErrorReason(kind, err))
	}

	if conv != "" {
		t.sessMu.Lock()
		_ = t.d.Agents.SetTeamSession(c.AgentID, m.ID, conv)
		t.sessMu.Unlock()
	}
	t.mu.Lock()
	for id, f := range t.full {
		if time.Since(f.at) > teamFullTextTTL {
			delete(t.full, id)
		}
	}
	t.full[res.CallID] = teamFull{agentID: c.AgentID, text: answer, at: time.Now()}
	t.mu.Unlock()
	res.Text = answer
	if r := []rune(answer); len(r) > s.ResultMaxChars {
		res.Text, res.Truncated = string(r[:s.ResultMaxChars]), true
	}
	return finish("done", "")
}

// runMember runs one attempt and returns the answer, the member's conversation
// id and the usage it reported (also on failure, when known).
func (t *TeamService) runMember(ctx context.Context, id string, provider providers.ID, cwd, model, resume, brief string) (string, string, *providers.Usage, error) {
	exec, err := t.d.NewExec(provider, id, cwd, model, "plan", resume)
	if err != nil {
		return "", "", nil, err
	}
	defer exec.Stop() // kills the process group, also on timeout
	if err := exec.Start(); err != nil {
		return "", "", nil, err
	}
	if err := exec.Send(brief); err != nil {
		return "", "", nil, err
	}
	answer, o, err := collectAnswer(ctx, exec)
	if err != nil {
		return "", "", nil, err
	}
	if answerFailed(o, answer) {
		return "", "", o.Usage, fmt.Errorf("%.300s", outcomeDetail(o))
	}
	return answer, exec.ConversationID(), o.Usage, nil
}

func addTeamUsage(a, b *providers.Usage) *providers.Usage {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	sum := *a
	sum.InputTokens += b.InputTokens
	sum.OutputTokens += b.OutputTokens
	sum.CacheCreationInputTokens += b.CacheCreationInputTokens
	sum.CacheReadInputTokens += b.CacheReadInputTokens
	sum.ThinkingTokens, sum.TotalTokens = nil, nil
	return &sum
}

// classifyTeamError maps a failed attempt to a result status.
func classifyTeamError(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return "timeout"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "login") || strings.Contains(text, "authenticat") || strings.Contains(text, "not logged"):
		return "not_logged_in"
	case strings.Contains(text, "rate limit") || strings.Contains(text, "usage limit") || strings.Contains(text, "quota"):
		return "quota"
	case errors.Is(err, osexec.ErrNotFound) || strings.Contains(text, "executable file not found"):
		return "not_installed"
	}
	return "failed"
}

func teamErrorReason(kind string, err error) string {
	switch kind {
	case "timeout":
		return "직원이 제한 시간 안에 답하지 않았습니다."
	case "not_logged_in":
		return "직원 CLI에 로그인되어 있지 않습니다: " + err.Error()
	case "quota":
		return "직원 CLI의 사용 한도에 걸렸습니다: " + err.Error()
	case "not_installed":
		return "직원 CLI가 설치되어 있지 않습니다."
	}
	return "직원 호출 실패: " + err.Error()
}

// isTeamSelf: a member is the conductor itself when it runs the same tool and
// either the conductor's model or the tool's default model.
func isTeamSelf(m TeamMember, adapter, model string) bool {
	return m.Adapter == adapter && (m.Model == "" || m.Model == model)
}

// teamFileInside accepts a relative path that stays inside the working dir.
func teamFileInside(f string) bool {
	if strings.TrimSpace(f) == "" || filepath.IsAbs(f) {
		return false
	}
	c := filepath.Clean(f)
	return c != ".." && !strings.HasPrefix(c, ".."+string(filepath.Separator))
}

// TeamBrief tells the conductor who it can call and when; it is prefixed to the
// session's next message when dream team mode is turned on.
func TeamBrief(s TeamSettings, conductorAdapter, conductorModel string) string {
	var lines []string
	for _, m := range s.Members {
		if !m.Enabled || isTeamSelf(m, conductorAdapter, conductorModel) {
			continue
		}
		name := m.Label
		if m.Model != "" {
			name += " (" + modelDisplay(m.Model) + ")"
		}
		lines = append(lines, "- "+m.ID+": "+name)
	}
	var b strings.Builder
	b.WriteString("<드림팀 모드>\n")
	if len(lines) == 0 {
		b.WriteString("지금 부를 수 있는 직원이 없습니다. 모든 일을 직접 처리하세요.\n")
	} else {
		b.WriteString("ask_member 도구로 다른 CLI 직원에게 일을 맡길 수 있습니다. 부를 수 있는 직원:\n")
		b.WriteString(strings.Join(lines, "\n") + "\n")
		b.WriteString("- 큰 파일·여러 파일을 읽어 요약하거나, 독립적인 검토·교차 확인이 필요할 때만 맡기세요. 작은 일은 직접 하는 편이 빠릅니다.\n")
		b.WriteString("- 직원은 읽기 전용입니다. 파일 수정·명령 실행은 맡기지 말고 결과를 받아 직접 하세요.\n")
		fmt.Fprintf(&b, "- 요청서는 짧고 구체적으로 쓰고, 필요한 파일은 files에 상대 경로로 넘기세요. 한 턴에 최대 %d번 부를 수 있습니다.\n", s.MaxCallsPerTurn)
	}
	b.WriteString("- 이미지 생성, YouTube 같은 앱 전용 기능은 직원도 할 수 없습니다. 사용자가 그 앱에 붙여 넣을 수 있는 요청문을 대신 써 주세요.\n")
	b.WriteString("- agy(Antigravity)는 직접 부를 수 없습니다. 맡길 일이 있으면 handoff_to_agy로만 넘기세요.\n")
	b.WriteString("</드림팀 모드>\n\n")
	return b.String()
}
