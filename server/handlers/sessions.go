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
		hub.BroadcastAll(ws.EventAgentCreated, newAgent)
		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, newAgent)
	}
}
