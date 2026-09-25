package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
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
func RouteAgent(agentSvc *services.AgentService, hub *ws.Hub, choose ChooseFunc) http.HandlerFunc {
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
			bind.NativeModel, bind.NativeEffort = ch.Profile.Model, ch.Profile.Effort
			resp.Profile = &routingProfile{ID: ch.Profile.ID, Adapter: ch.Profile.Adapter, Model: ch.Profile.Model, Effort: ch.Profile.Effort}
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
