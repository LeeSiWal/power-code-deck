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

// sessionLaunchCommand resolves which CLI a derived session must run. It mirrors
// the hub's nativeLaunchIdentity (preset wins, codex checked first) so the two can
// never disagree about what an agent is.
//
// This exists because NewSession/ResumeSession used to hardcode "claude" while
// copying the source agent's preset. A /clear inside a Codex chat therefore minted
// a hybrid row — preset codex-cli, command claude — which ran Claude Code on the
// PTY track of a Codex agent AND matched both driver branches of
// AgentService.inheritedNativeConfig, so the next Claude session anywhere
// inherited a Codex model.
func sessionLaunchCommand(agent *services.Agent) string {
	switch {
	case agent.Preset == "codex-cli" || agent.Command == "codex":
		return "codex"
	case agent.Preset == "claude", agent.Preset == "claude-code", agent.Command == "claude":
		return "claude"
	}
	return agent.Command // custom preset: keep whatever it actually runs
}

// NewSession launches a new agent running a fresh session of the SAME CLI as
// agent {id} (no --resume) in the same project/working dir.
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
			Command:    sessionLaunchCommand(agent),
			Args:       nil,
		})
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Carry the chosen model + permission mode + effort onto the new agent, so
		// continuing in the same project keeps your choices instead of snapping back
		// to defaults.
		if model, mode, effort := agentSvc.NativeConfig(agent.ID); model != "" || mode != "" || effort != "" {
			agentSvc.SetNativeConfig(newAgent.ID, model, mode, effort)
		}
		if opts := agentSvc.NativeOptions(agent.ID); !opts.IsZero() {
			agentSvc.SetNativeOptions(newAgent.ID, opts)
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
		// The ids this endpoint takes come from ListSessions, which reads Claude Code
		// transcripts (~/.claude/projects). They are meaningless to Codex, whose threads
		// live in ~/.codex/sessions under a different id namespace. Resuming one into a
		// Codex agent used to write a Claude UUID into claude_session_id, which the Codex
		// driver then handed to thread/resume — and since NativeService only retries a
		// stale resume id for Claude, that agent could never be opened again. Refuse
		// instead of minting a permanently broken session.
		if cmd := sessionLaunchCommand(agent); cmd != "claude" {
			jsonError(w, "이어하기는 Claude Code 세션 기록에만 사용할 수 있습니다", http.StatusBadRequest)
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
		// Carry the chosen model + permission mode + effort across the resume, so 이어하기
		// keeps your choices instead of resetting to defaults on the freshly created agent.
		if model, mode, effort := agentSvc.NativeConfig(agent.ID); model != "" || mode != "" || effort != "" {
			agentSvc.SetNativeConfig(newAgent.ID, model, mode, effort)
		}
		if opts := agentSvc.NativeOptions(agent.ID); !opts.IsZero() {
			agentSvc.SetNativeOptions(newAgent.ID, opts)
		}
		hub.BroadcastAll(ws.EventAgentCreated, newAgent)
		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, newAgent)
	}
}
