package services

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/providers"
)

// delegateTimeout bounds one advice call. The advisor runs in plan mode, so a
// tool that needs approval has nobody to approve it; the timeout ends that wait.
const delegateTimeout = 5 * time.Minute

// DelegateTarget is the profile asked for advice.
type DelegateTarget struct {
	ProfileID string `json:"id"`
	Adapter   string `json:"adapter"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
}

// DelegateJob is one read-only advice call for a hard request, made instead of
// moving a long conversation to a stronger model.
type DelegateJob struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agentId"`
	Status      string         `json:"status"` // running | done | failed | canceled
	Target      DelegateTarget `json:"target"`
	BriefTokens int            `json:"briefTokens"`
	Error       string         `json:"error,omitempty"`
	started     time.Time
	cancel      context.CancelFunc
}

// Delegator runs advice calls and hands the answer to the session's own CLI:
// it becomes the prefix of the next message, and is shown in the chat.
type Delegator struct {
	RP     *RunProviders
	Native *NativeService
	Usage  *AutoUsage
	mu     sync.Mutex
	jobs   map[string]*DelegateJob
}

func NewDelegator(rp *RunProviders, native *NativeService, usage *AutoUsage) *Delegator {
	return &Delegator{RP: rp, Native: native, Usage: usage, jobs: map[string]*DelegateJob{}}
}

// Start begins an advice call in the background and returns its job.
func (d *Delegator) Start(agentID, cwd string, target DelegateTarget, brief string) (*DelegateJob, error) {
	provider := map[string]providers.ID{"claude": providers.Claude, "codex": providers.Codex}[target.Adapter]
	if provider == "" {
		return nil, fmt.Errorf("no advice adapter for %q", target.Adapter)
	}
	ctx, cancel := context.WithTimeout(context.Background(), delegateTimeout)
	job := &DelegateJob{ID: "dlg_" + rand.Text()[:12], AgentID: agentID, Status: "running", Target: target, BriefTokens: len(brief) / 3, started: time.Now(), cancel: cancel}
	d.mu.Lock()
	for id, j := range d.jobs { // forget finished jobs after an hour
		if j.Status != "running" && time.Since(j.started) > time.Hour {
			delete(d.jobs, id)
		}
	}
	d.jobs[job.ID] = job
	d.mu.Unlock()
	go d.run(ctx, job, provider, cwd, brief)
	return job, nil
}

func (d *Delegator) run(ctx context.Context, job *DelegateJob, provider providers.ID, cwd, brief string) {
	defer job.cancel()
	answer, usage, err := d.ask(ctx, job, provider, cwd, brief)
	d.mu.Lock()
	defer d.mu.Unlock()
	if job.Status == "canceled" {
		return
	}
	if d.Usage != nil {
		d.Usage.RecordDelegate(job.AgentID, job.Target, job.BriefTokens, usage)
	}
	if err != nil {
		job.Status, job.Error = "failed", err.Error()
		return
	}
	job.Status = "done"
	name := adapterDisplay(job.Target.Adapter) + " · " + modelDisplay(job.Target.Model)
	if d.Native != nil {
		d.Native.SetNextNote(job.AgentID, "[조언 · "+name+"]\n\n"+answer)
		d.Native.SetPrefix(job.AgentID, "<강한 모델의 조언>\n더 강한 모델("+name+")이 아래 요청에 대해 읽기 전용으로 검토한 조언입니다. 참고해서 아래 [현재 요청]을 처리하세요. 조언이 틀렸다고 판단되면 따르지 않아도 됩니다.\n\n"+answer+"\n</강한 모델의 조언>\n\n[현재 요청]\n")
	}
}

func (d *Delegator) ask(ctx context.Context, job *DelegateJob, provider providers.ID, cwd, brief string) (string, *providers.Usage, error) {
	exec, err := d.RP.NewRole(provider, job.ID, cwd, job.Target.Model, job.Target.Effort, "plan")
	if err != nil {
		return "", nil, err
	}
	defer exec.Stop()
	if err := exec.Start(); err != nil {
		return "", nil, err
	}
	if err := exec.Send(brief); err != nil {
		return "", nil, err
	}
	// The answer is the result text (Claude) or, when the turn end carries none
	// (Codex reports no result text or status), the assistant's own messages.
	var said []string
	for {
		ev, err := exec.Next(ctx)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return "", nil, fmt.Errorf("조언이 %s 안에 끝나지 않았습니다", delegateTimeout)
			}
			return "", nil, err
		}
		if ev.Kind == providers.Message && !ev.Delta && ev.Role == "assistant" && ev.ParentToolCallID == "" {
			for _, b := range ev.Blocks {
				if b.Kind == providers.Text && strings.TrimSpace(b.Text) != "" {
					said = append(said, b.Text)
				}
			}
		}
		if ev.Kind != providers.TurnFinished || ev.Outcome == nil {
			continue
		}
		o := ev.Outcome
		answer := strings.TrimSpace(o.Text)
		if answer == "" {
			answer = strings.TrimSpace(strings.Join(said, "\n\n"))
		}
		failed := o.IsError || o.Status == providers.CompletionFailed || o.Status == providers.CompletionInterrupted
		if failed || answer == "" {
			return "", o.Usage, fmt.Errorf("조언 호출 실패: %.300s", strings.TrimSpace(o.Text+" "+o.Reason+" "+o.Diagnostics))
		}
		return answer, o.Usage, nil
	}
}

// Get returns a job of this agent.
func (d *Delegator) Get(agentID, id string) (DelegateJob, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[id]
	if !ok || j.AgentID != agentID {
		return DelegateJob{}, false
	}
	return *j, true
}

// Cancel stops a running job; its answer is then neither shown nor handed on.
func (d *Delegator) Cancel(agentID, id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if j, ok := d.jobs[id]; ok && j.AgentID == agentID && j.Status == "running" {
		j.Status = "canceled"
		j.cancel()
	}
}

// modelDisplay mirrors the client's modelName: "gpt-5.6-sol" → "GPT-5.6 Sol",
// "claude-opus-5-5" → "Opus 5.5".
func modelDisplay(m string) string {
	if m == "" {
		return "기본 모델"
	}
	if rest, ok := strings.CutPrefix(m, "oss:"); ok {
		if _, model, ok := strings.Cut(rest, ":"); ok {
			return "로컬 · " + localModelName(model)
		}
	}
	title := func(p string) string {
		if p == "" || (p[0] >= '0' && p[0] <= '9') {
			return p
		}
		return strings.ToUpper(p[:1]) + p[1:]
	}
	if strings.HasPrefix(strings.ToLower(m), "gpt-") {
		parts := strings.Split(m[4:], "-")
		out := "GPT-" + parts[0]
		for _, p := range parts[1:] {
			out += " " + title(p)
		}
		return out
	}
	var out []string
	for _, p := range strings.Split(strings.TrimPrefix(strings.TrimPrefix(m, "claude-"), "gemini-"), "-") {
		isNum := p != "" && strings.Trim(p, "0123456789") == ""
		if isNum && len(p) == 8 {
			continue // date suffix
		}
		if isNum && len(out) > 0 && strings.Trim(out[len(out)-1], "0123456789.") == "" {
			out[len(out)-1] += "." + p
			continue
		}
		out = append(out, title(p))
	}
	if len(out) == 0 {
		return m
	}
	return strings.Join(out, " ")
}

func adapterDisplay(a string) string {
	switch a {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	}
	return a
}

// localModelName shortens "mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit" to
// "Qwen3 30B A3B": the organization, release date, quantization and the
// "Instruct" tag say nothing the user needs.
func localModelName(m string) string {
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	var keep []string
	for _, p := range strings.Split(m, "-") {
		l := strings.ToLower(p)
		if l == "instruct" || l == "mlx" || l == "gguf" || strings.HasSuffix(l, "bit") || (len(p) == 4 && strings.Trim(p, "0123456789") == "") {
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == 0 {
		return m
	}
	return strings.Join(keep, " ")
}
