package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"powercodedeck/internal/routing"
	"powercodedeck/internal/routing/runroute"
	"powercodedeck/services"
	"powercodedeck/ws"
)

// ChooseFunc picks a profile for a request (runroute.Coordinator.Choose). It is
// nil when the v2 routing runtime is off.
type ChooseFunc func(ctx context.Context, goal string) (runroute.Choice, error)

// Adapters an auto session can be bound to, as the preset/command a session of
// that tool is created with.
var autoBindTargets = map[string]services.BindRequest{
	"claude":      {Preset: "claude-code", Command: "claude"},
	"codex":       {Preset: "codex-cli", Command: "codex"},
	"antigravity": {Preset: "antigravity", Command: "agy"},
}

type routeAgentResponse struct {
	Agent    *services.Agent `json:"agent"`
	Profile  *routingProfile `json:"profile,omitempty"`
	RuleTier string          `json:"ruleTier,omitempty"`
	Source   string          `json:"source,omitempty"`
	Fallback string          `json:"fallback,omitempty"` // routing_off | no_profile | routing_error | unsupported_adapter
}

type routingProfile struct {
	ID      string `json:"id"`
	Adapter string `json:"adapter"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// RouteAgent binds an auto session to the tool/model routed from its first
// request. When routing cannot pick, the session becomes Claude Code and the
// response says why, so the first message is never lost.
func RouteAgent(agentSvc *services.AgentService, hub *ws.Hub, choose ChooseFunc, usage *services.AutoUsage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Goal    string `json:"goal"`
			Adapter string `json:"adapter"` // bind this tool directly instead of routing
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		id := mux.Vars(r)["id"]
		resp := routeAgentResponse{}
		bind := autoBindTargets["claude"]
		if body.Adapter != "" {
			target, ok := autoBindTargets[body.Adapter]
			if !ok {
				jsonError(w, "unknown adapter", http.StatusBadRequest)
				return
			}
			choose, bind = nil, target
		}
		switch ch, err := callChoose(r.Context(), choose, body.Goal); {
		case choose == nil && body.Adapter != "":
		case choose == nil:
			resp.Fallback = "routing_off"
		case errors.Is(err, runroute.ErrNoProfile):
			resp.Fallback = "no_profile"
		case err != nil:
			resp.Fallback = "routing_error"
		default:
			target, ok := autoBindTargets[ch.Profile.Adapter]
			if !ok {
				resp.Fallback = "unsupported_adapter"
				break
			}
			bind = target
			bind.NativeModel, bind.NativeEffort, bind.AutoProfile = ch.Profile.LaunchModel(), ch.Profile.Effort, ch.Profile.ID
			resp.Profile = toRoutingProfile(ch.Profile)
			resp.RuleTier, resp.Source = ch.Decision.RuleTier.String(), ch.Decision.Source
		}
		agent, err := agentSvc.Bind(id, bind)
		if errors.Is(err, services.ErrAlreadyBound) {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		if agent == nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		hub.BroadcastAll(ws.EventAgentStatus, ws.AgentStatusPayload{AgentID: id, Status: agent.Status})
		hub.NoteAgentChange(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.Agent = agent
		if usage != nil && agent.AutoProfile != "" {
			usage.Note(id, services.TurnMeta{Switch: "start", IdleSeconds: -1})
		}
		jsonResponse(w, resp)
	}
}

func callChoose(ctx context.Context, choose ChooseFunc, goal string) (runroute.Choice, error) {
	if choose == nil {
		return runroute.Choice{}, nil
	}
	ch, err := choose(ctx, goal)
	if err == nil && ch.Profile == nil {
		err = runroute.ErrNoProfile
	}
	return ch, err
}

// TurnFunc re-routes a later message of an auto session within its tool
// (runroute.Coordinator.ChooseTurn). Nil when the v2 routing runtime is off.
type TurnFunc func(ctx context.Context, goal, adapter, current string, noLocal bool, previous string) (runroute.TurnChoice, error)

type routeTurnResponse struct {
	Applied  bool            `json:"applied"`
	Reason   string          `json:"reason"` // why it did or did not switch
	From     *routingProfile `json:"from,omitempty"`
	To       *routingProfile `json:"to,omitempty"`
	RuleTier string          `json:"ruleTier,omitempty"`
	// Set when the session moved (or would move) to another tool.
	Agent         *services.Agent `json:"agent,omitempty"`
	HandoffTurns  int             `json:"handoffTurns,omitempty"`
	HandoffTokens int             `json:"handoffTokens,omitempty"`
	// Set when a hard request was sent to a stronger model for advice instead.
	Delegate *services.DelegateJob `json:"delegate,omitempty"`
}

// Tool switches only happen once a session has been idle this long (the prompt
// cache is cold, so re-reading is paid either way). A handoff estimated above
// toolSwitchConfirmTokens asks the user first; maxHandoffChars bounds it.
const (
	toolSwitchConfirmTokens = 10000
	maxHandoffChars         = 120000
	// A conversation at least this long is not moved up to a stronger model for
	// one hard request (the stronger model would re-read all of it); the request
	// goes to that model as a short brief for read-only advice instead.
	delegateMinTokens = 20000
)

// estimateTokens is a rough count for the confirm prompt: ~1 token per 3 bytes
// (close for Korean, a slight overestimate for English).
func estimateTokens(s string) int { return len(s) / 3 }

func toRoutingProfile(p *routing.Profile) *routingProfile {
	if p == nil {
		return nil
	}
	return &routingProfile{ID: p.ID, Adapter: p.Adapter, Model: p.LaunchModel(), Effort: p.Effort}
}

// RouteTurn runs before a later message of an auto session is sent: it may move
// the session to another model of the same tool (one CLI restart that resumes
// the conversation). It never switches mid-answer, and moves down only after
// the session has been idle long enough that the prompt cache is cold anyway.
// Any "not applied" answer is normal — the client just sends the message.
func RouteTurn(agentSvc *services.AgentService, native *services.NativeService, choose TurnFunc, usage *services.AutoUsage, delegator *services.Delegator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Goal        string `json:"goal"`
			ConfirmTool bool   `json:"confirmTool"` // the user approved a large tool handoff
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		id := mux.Vars(r)["id"]
		agent, err := agentSvc.Get(id)
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		skip := func(reason string) { jsonResponse(w, routeTurnResponse{Reason: reason}) }
		adapter := map[string]string{"claude-code": "claude", "codex-cli": "codex"}[agent.Preset]
		switch {
		case agent.AutoProfile == "":
			skip("not_auto")
			return
		case choose == nil || native == nil:
			skip("routing_off")
			return
		case adapter == "":
			skip("no_model_control")
			return
		}
		running, active, lastEnd := native.TurnState(id)
		if !running {
			skip("not_running")
			return
		}
		if active {
			skip("turn_active")
			return
		}
		noLocal := agentSvc.AutoNoLocal(id)
		history := native.History(id)
		previous := lastUserText(history)
		var idle time.Duration
		idleSeconds := -1
		if !lastEnd.IsZero() {
			idle = time.Since(lastEnd)
			idleSeconds = int(idle.Seconds())
		}
		note := func(kind string, handoff int) {
			if usage != nil {
				usage.Note(id, services.TurnMeta{Switch: kind, HandoffTokens: handoff, IdleSeconds: idleSeconds})
			}
		}
		// Other tools compete when moving there loses nothing: idle past the
		// cache lifetime, a short conversation, or a session on the free local
		// model (which runs through Codex and would otherwise keep every later
		// request on Codex models).
		curModel, _, _ := agentSvc.NativeConfig(id)
		if cross, _ := runroute.CrossToolAllowed(idle, contextTokens(history), strings.HasPrefix(curModel, "oss:")); cross {
			if wide, err := choose(r.Context(), body.Goal, "", agent.AutoProfile, noLocal, previous); err == nil && wide.Profile.Adapter != adapter {
				if target, ok := autoBindTargets[wide.Profile.Adapter]; ok && target.Preset != "antigravity" {
					if ok, handoff := switchTool(w, agentSvc, native, id, agent, wide, target, body.ConfirmTool); ok {
						note("tool", handoff)
					}
					return
				}
			}
		}
		tc, err := choose(r.Context(), body.Goal, adapter, agent.AutoProfile, noLocal, previous)
		if err != nil {
			note("", 0)
			skip("no_profile")
			return
		}
		apply, reason := runroute.TurnSwitch(tc.Current, *tc.Profile, idle, contextTokens(history))
		resp := routeTurnResponse{Reason: reason, From: toRoutingProfile(tc.Current), To: toRoutingProfile(tc.Profile), RuleTier: tc.Decision.RuleTier.String()}
		// Hard request in a long conversation: ask the stronger model for advice on
		// a short brief, and let the current model (which has the context) do it.
		if apply && reason == "harder" && delegator != nil {
			if contextTokens(history) >= delegateMinTokens {
				advisor := tc.Profile
				if wide, err := choose(r.Context(), body.Goal, "", agent.AutoProfile, true, previous); err == nil && autoBindTargets[wide.Profile.Adapter].Preset != "antigravity" {
					advisor = wide.Profile
				}
				target := services.DelegateTarget{ProfileID: advisor.ID, Adapter: advisor.Adapter, Model: advisor.LaunchModel(), Effort: advisor.Effort}
				if job, err := delegator.Start(id, agent.WorkingDir, target, services.BuildDelegateBrief(history, body.Goal)); err == nil {
					note("", 0)
					snapshot, _ := delegator.Get(id, job.ID) // a copy; the job keeps running
					resp.Reason, resp.To, resp.Delegate = "delegating", toRoutingProfile(advisor), &snapshot
					jsonResponse(w, resp)
					return
				}
			}
		}
		if apply {
			if err := native.SetModelEffort(id, tc.Profile.LaunchModel(), tc.Profile.Effort); err != nil {
				resp.Reason = "switch_failed"
				jsonResponse(w, resp)
				return
			}
			agentSvc.SetAutoProfile(id, tc.Profile.ID)
			resp.Applied = true
			note("model", 0)
		} else {
			note("", 0)
		}
		jsonResponse(w, resp)
	}
}

// ClearAutoProfile stops per-turn routing for a session (the user picked a
// model or effort by hand).
func ClearAutoProfile(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if _, err := agentSvc.Get(id); err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		agentSvc.SetAutoProfile(id, "")
		w.WriteHeader(http.StatusNoContent)
	}
}

// switchTool hands an auto session over to another tool: the new tool gets the
// turns it has not seen (all of them, or — if it had a conversation here before —
// only those since it left) in front of the next message, and resumes its own
// earlier conversation when there is one.
func switchTool(w http.ResponseWriter, agentSvc *services.AgentService, native *services.NativeService, id string, agent *services.Agent, tc runroute.TurnChoice, target services.BindRequest, confirmed bool) (switched bool, handoffTokens int) {
	history := native.History(id)
	prev, returning := agentSvc.ToolSessionOf(id, tc.Profile.Adapter)
	since := 0
	if returning {
		since = prev.Turns
	}
	handoff, turns := services.BuildToolHandoff(history, since, returning, maxHandoffChars)
	resp := routeTurnResponse{From: toRoutingProfile(tc.Current), To: toRoutingProfile(tc.Profile), RuleTier: tc.Decision.RuleTier.String(),
		HandoffTurns: turns, HandoffTokens: estimateTokens(handoff)}
	if resp.HandoffTokens > toolSwitchConfirmTokens && !confirmed {
		resp.Reason = "confirm_tool_switch"
		jsonResponse(w, resp)
		return false, 0
	}
	target.NativeModel, target.NativeEffort, target.AutoProfile = tc.Profile.LaunchModel(), tc.Profile.Effort, tc.Profile.ID
	moved, err := agentSvc.SwitchTool(id, target, services.CountUserTurns(history))
	if err != nil {
		resp.Reason = "switch_failed"
		jsonResponse(w, resp)
		return false, 0
	}
	kind := map[string]string{"claude-code": "claude", "codex-cli": "codex"}[target.Preset]
	if err := native.SwitchKind(id, kind, target.NativeModel, target.NativeEffort, handoff); err != nil {
		// The row already says the new tool; the next open starts it (without the handoff).
		resp.Reason = "switch_failed"
		resp.Agent = moved
		jsonResponse(w, resp)
		return false, 0
	}
	resp.Applied, resp.Reason, resp.Agent = true, "tool_switch", moved
	jsonResponse(w, resp)
	return true, resp.HandoffTokens
}

// AutoUsageOf reports one session's recorded auto turns.
func AutoUsageOf(usage *services.AutoUsage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sum, err := usage.Summary(mux.Vars(r)["id"], time.Time{})
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sum.IdleThresholdS = int(runroute.TurnIdleDowngrade.Seconds())
		jsonResponse(w, sum)
	}
}

// AutoUsageRecent reports every auto session's turns of the last ?days=N (7).
func AutoUsageRecent(usage *services.AutoUsage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := 7
		if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && n > 0 && n <= 365 {
			days = n
		}
		sum, err := usage.Summary("", time.Now().AddDate(0, 0, -days))
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sum.IdleThresholdS = int(runroute.TurnIdleDowngrade.Seconds())
		jsonResponse(w, sum)
	}
}

// DelegateStatus reports an advice call; DELETE cancels it (its answer is then
// neither shown nor handed to the session).
func DelegateStatus(delegator *services.Delegator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if delegator == nil {
			jsonError(w, "delegation unavailable", http.StatusNotFound)
			return
		}
		if r.Method == http.MethodDelete {
			delegator.Cancel(vars["id"], vars["job"])
			w.WriteHeader(http.StatusNoContent)
			return
		}
		job, ok := delegator.Get(vars["id"], vars["job"])
		if !ok {
			jsonError(w, "delegation not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, job)
	}
}

// contextTokens is how much a model re-reads to continue this conversation: the
// context the last turn reported (input + cache writes + cache reads). When the
// CLI reports no usage (Codex), the handoff text size stands in for it.
func contextTokens(history []*services.StreamEvent) int {
	for i := len(history) - 1; i >= 0; i-- {
		if ev := history[i]; ev.Type == "result" && ev.Usage != nil {
			return ev.Usage.InputTokens + ev.Usage.CacheCreationInputTokens + ev.Usage.CacheReadInputTokens
		}
	}
	full, _ := services.BuildToolHandoff(history, 0, false, maxHandoffChars)
	return estimateTokens(full)
}

// Escalate re-routes the session's last request to a paid (non-local) model
// after the user rejected a local answer ("유료 모델로 다시"): same tool → one
// model switch; another tool → a tool switch with the usual handoff (the user
// asked, so no confirm step). Local models stay out of this session after.
func Escalate(agentSvc *services.AgentService, native *services.NativeService, choose TurnFunc, usage *services.AutoUsage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Goal string `json:"goal"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}
		id := mux.Vars(r)["id"]
		agent, err := agentSvc.Get(id)
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		if choose == nil || native == nil || agent.AutoProfile == "" {
			jsonError(w, "this session is not auto-routed", http.StatusConflict)
			return
		}
		running, active, _ := native.TurnState(id)
		if !running || active {
			jsonError(w, "wait for the current answer to finish", http.StatusConflict)
			return
		}
		tc, err := choose(r.Context(), body.Goal, "", agent.AutoProfile, true, "")
		if err != nil {
			jsonError(w, "no paid model is available for this request", http.StatusConflict)
			return
		}
		agentSvc.SetAutoNoLocal(id)
		adapter := map[string]string{"claude-code": "claude", "codex-cli": "codex"}[agent.Preset]
		if tc.Profile.Adapter != adapter {
			target, ok := autoBindTargets[tc.Profile.Adapter]
			if !ok || target.Preset == "antigravity" {
				jsonError(w, "no switchable paid model", http.StatusConflict)
				return
			}
			if ok, handoff := switchTool(w, agentSvc, native, id, agent, tc, target, true); ok && usage != nil {
				usage.Note(id, services.TurnMeta{Switch: "tool", HandoffTokens: handoff, IdleSeconds: -1})
			}
			return
		}
		resp := routeTurnResponse{Reason: "model", From: toRoutingProfile(tc.Current), To: toRoutingProfile(tc.Profile), RuleTier: tc.Decision.RuleTier.String()}
		if err := native.SetModelEffort(id, tc.Profile.LaunchModel(), tc.Profile.Effort); err != nil {
			jsonError(w, "model switch failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		agentSvc.SetAutoProfile(id, tc.Profile.ID)
		if usage != nil {
			usage.Note(id, services.TurnMeta{Switch: "model", IdleSeconds: -1})
		}
		resp.Applied = true
		jsonResponse(w, resp)
	}
}

// lastUserText is the most recent request in the chat (the one before the
// message being routed now).
func lastUserText(history []*services.StreamEvent) string {
	for i := len(history) - 1; i >= 0; i-- {
		ev := history[i]
		if ev.Type != "user" || ev.Message == nil || ev.ParentToolUseID != nil && *ev.ParentToolUseID != "" {
			continue
		}
		for _, b := range ev.Message.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return b.Text
			}
		}
	}
	return ""
}
