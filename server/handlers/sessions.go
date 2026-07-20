package handlers

import (
	"net/http"

	"github.com/gorilla/mux"

	"powercodedeck/services"
	"powercodedeck/ws"
)

// ListSessions returns the past Claude Code sessions (transcripts) for an agent's
// project working dir — browsable even after the session has ended.
func ListSessions(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, err := agentSvc.Get(mux.Vars(r)["id"])
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		sessions, err := services.ListSessions(agent.WorkingDir)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, sessions)
	}
}

// GetSession returns a past session's conversation (rendered user/assistant turns).
func GetSession(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, err := agentSvc.Get(mux.Vars(r)["id"])
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		msgs, err := services.ReadSession(agent.WorkingDir, mux.Vars(r)["sid"])
		if err != nil {
			jsonError(w, "session not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, msgs)
	}
}

// DeleteSession removes a past session's transcript file.
func DeleteSession(agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, err := agentSvc.Get(mux.Vars(r)["id"])
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		if err := services.DeleteSession(agent.WorkingDir, mux.Vars(r)["sid"]); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// NewSession launches a new agent running a fresh Claude Code session
// (plain `claude`, no --resume) in the same project/working dir as agent {id}.
func NewSession(agentSvc *services.AgentService, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, err := agentSvc.Get(mux.Vars(r)["id"])
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		newAgent, err := agentSvc.Create(services.CreateAgentRequest{
			Preset:     agent.Preset,
			Name:       agent.Name,
			WorkingDir: agent.WorkingDir,
			Command:    "claude",
			Args:       nil,
		})
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Carry the chosen model + permission mode onto the new agent, so continuing
		// in the same project keeps your choices instead of snapping back to defaults.
		if model, mode := agentSvc.NativeConfig(agent.ID); model != "" || mode != "" {
			agentSvc.SetNativeConfig(newAgent.ID, model, mode)
		}
		hub.BroadcastAll(ws.EventAgentCreated, newAgent)
		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, newAgent)
	}
}

// ResumeSession launches a new agent that resumes a past session
// (claude --resume <sid>) in the same working dir.
func ResumeSession(agentSvc *services.AgentService, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, err := agentSvc.Get(mux.Vars(r)["id"])
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		sid := mux.Vars(r)["sid"]
		newAgent, err := agentSvc.Create(services.CreateAgentRequest{
			Preset:     agent.Preset,
			Name:       agent.Name,
			WorkingDir: agent.WorkingDir,
			Command:    "claude",
			Args:       []string{"--resume", sid},
		})
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Record the target conversation on the new agent so the NATIVE chat resumes
		// it too: native ignores the terminal `--resume` args above and resumes via
		// resumeIDFor = the agent's claude_session_id. Without this the native track
		// opens the resumed agent blank (fresh session, no prior conversation), and
		// NativeService can't seed history from the transcript either.
		agentSvc.SetClaudeSessionID(newAgent.ID, sid)
		// Carry the chosen model + permission mode across the resume, so 이어하기 keeps
		// your choices instead of resetting to defaults on the freshly created agent.
		if model, mode := agentSvc.NativeConfig(agent.ID); model != "" || mode != "" {
			agentSvc.SetNativeConfig(newAgent.ID, model, mode)
		}
		hub.BroadcastAll(ws.EventAgentCreated, newAgent)
		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, newAgent)
	}
}
