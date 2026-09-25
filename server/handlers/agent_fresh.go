package handlers

import (
	"context"
	"crypto/rand"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"powercodedeck/internal/routing"
	"powercodedeck/services"
)

// FreshFunc returns the fresh-start setting (runroute.Coordinator.FreshStart);
// nil, or a nil result, means fresh starts are off.
type FreshFunc func() *routing.FreshStartConfig

// freshJob is one fresh start in progress: the memo is being written, then the
// session moves to a new conversation. The client sends the user's message when
// it is done (or failed — the message then goes to the old conversation).
type freshJob struct {
	ID           string          `json:"id"`
	AgentID      string          `json:"agentId"`
	Status       string          `json:"status"` // running | done | failed | canceled
	Writer       string          `json:"writer"` // memo model
	BeforeTokens int             `json:"beforeTokens"`
	PrefixTokens int             `json:"prefixTokens,omitempty"`
	From         *routingProfile `json:"from,omitempty"`
	To           *routingProfile `json:"to,omitempty"`
	Agent        *services.Agent `json:"agent,omitempty"` // set when the tool changed
	Error        string          `json:"error,omitempty"`
	started      time.Time
	cancel       context.CancelFunc
}

type freshJobs struct {
	mu   sync.Mutex
	jobs map[string]*freshJob
}

var freshStore = &freshJobs{jobs: map[string]*freshJob{}}

func (s *freshJobs) add(j *freshJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, old := range s.jobs {
		if old.Status != "running" && time.Since(old.started) > time.Hour {
			delete(s.jobs, id)
		}
	}
	s.jobs[j.ID] = j
}

func (s *freshJobs) get(agentID, id string) (freshJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.AgentID != agentID {
		return freshJob{}, false
	}
	return *j, true
}

// finish sets the final state unless the user canceled meanwhile; it reports
// whether the job was still wanted.
func (s *freshJobs) finish(j *freshJob, update func(*freshJob)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.Status == "canceled" {
		return false
	}
	update(j)
	return true
}

// freshTarget is where a fresh start lands: the profile routing picked for the
// request (any tool — a new conversation costs the same everywhere), or the
// session's current profile when routing has no answer.
type freshTarget struct {
	profile *routing.Profile
	bind    services.BindRequest
}

// startFresh writes the memo in the background and then moves the session to a
// new conversation on target with the memo in front of the next message.
func startFresh(agentSvc *services.AgentService, native *services.NativeService, usage *services.AutoUsage, cfg routing.FreshStartConfig,
	agent *services.Agent, cur *routing.Profile, target freshTarget, history []*services.StreamEvent, goal string, before, idleSeconds int) *freshJob {
	writer := services.MemoWriter{Adapter: cfg.Adapter, Model: cfg.Model, Effort: cfg.Effort}
	ctx, cancel := context.WithCancel(context.Background())
	job := &freshJob{ID: "fr_" + rand.Text()[:12], AgentID: agent.ID, Status: "running", Writer: cfg.Model, BeforeTokens: before,
		From: toRoutingProfile(cur), To: toRoutingProfile(target.profile), started: time.Now(), cancel: cancel}
	freshStore.add(job)
	go func() {
		defer cancel()
		fail := func(err error) {
			log.Printf("fresh start %s: %v", agent.ID, err)
			freshStore.finish(job, func(j *freshJob) { j.Status, j.Error = "failed", err.Error() })
		}
		req := services.BuildMemoRequest(history, goal)
		memo, u, err := services.WriteMemo(ctx, writer, agent.WorkingDir, req)
		if usage != nil && (u != nil || err == nil) {
			usage.RecordMemo(agent.ID, writer, estimateTokens(req), u)
		}
		if err != nil {
			fail(err)
			return
		}
		prefix := services.BuildFreshPrefix(memo, history, services.RepoState(agent.WorkingDir))
		turns := services.CountUserTurns(history)
		// Canceled while the memo was written: leave the session as it was.
		freshStore.mu.Lock()
		canceled := job.Status == "canceled"
		freshStore.mu.Unlock()
		if canceled {
			return
		}
		p := target.profile
		kind := map[string]string{"claude-code": "claude", "codex-cli": "codex"}[target.bind.Preset]
		var moved *services.Agent
		if target.bind.Preset != agent.Preset {
			b := target.bind
			b.NativeModel, b.NativeEffort, b.AutoProfile = p.LaunchModel(), p.Effort, p.ID
			if moved, err = agentSvc.SwitchTool(agent.ID, b, turns); err != nil {
				fail(err)
				return
			}
		} else {
			_, mode, _ := agentSvc.NativeConfig(agent.ID)
			effort := p.Effort
			if kind != "claude" {
				effort = ""
			}
			agentSvc.SetNativeConfig(agent.ID, p.LaunchModel(), mode, effort)
			agentSvc.SetAutoProfile(agent.ID, p.ID)
		}
		agentSvc.MarkFreshStart(agent.ID, turns, memo)
		if err := native.SwitchKind(agent.ID, kind, p.LaunchModel(), p.Effort, prefix); err != nil {
			fail(err)
			return
		}
		if usage != nil {
			usage.Note(agent.ID, services.TurnMeta{Switch: "fresh", HandoffTokens: estimateTokens(prefix), IdleSeconds: idleSeconds})
		}
		freshStore.finish(job, func(j *freshJob) { j.Status, j.PrefixTokens, j.Agent = "done", estimateTokens(prefix), moved })
	}()
	snapshot, _ := freshStore.get(agent.ID, job.ID)
	return &snapshot
}

// FreshStatus reports a fresh start; DELETE cancels it (the message then goes
// to the old conversation).
func FreshStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if r.Method == http.MethodDelete {
			freshStore.mu.Lock()
			if j, ok := freshStore.jobs[vars["job"]]; ok && j.AgentID == vars["id"] && j.Status == "running" {
				j.Status = "canceled"
				j.cancel()
			}
			freshStore.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		job, ok := freshStore.get(vars["id"], vars["job"])
		if !ok {
			jsonError(w, "fresh start not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, job)
	}
}
