package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"powercodedeck/services"
)

// agyHandoffText tells the conductor the agy card is up and to wait for the user.
const agyHandoffText = "사용자에게 agy 카드를 보여 줬습니다. 사용자가 결과를 붙여 넣을 때까지 이 일은 기다리세요."

// NativeTeam is the deck side of the dream team MCP tools: `pcd mcp-approve`
// (spawned by a Claude conductor) POSTs {sessionId, tool, arguments} here. Like
// NativeApprove it is loopback-only and checked by the per-session token; the
// member call is tied to the request, so a conductor that is interrupted (its
// bridge hangs up) stops the member too.
func NativeTeam(team *services.TeamService, agents *services.AgentService, tokens services.ApproveTokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string          `json:"sessionId"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.SessionID == "" {
			http.Error(w, "missing sessionId", http.StatusBadRequest)
			return
		}
		if !tokens.Valid(req.SessionID, strings.TrimSpace(r.Header.Get("X-PCD-Approve-Token"))) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch req.Tool {
		case "ask_member":
			var a struct {
				Member string   `json:"member"`
				Task   string   `json:"task"`
				Files  []string `json:"files"`
			}
			if len(req.Arguments) > 0 {
				if err := json.Unmarshal(req.Arguments, &a); err != nil {
					http.Error(w, "bad arguments", http.StatusBadRequest)
					return
				}
			}
			writeJSON(w, http.StatusOK, team.Ask(r.Context(), services.TeamCall{
				AgentID: req.SessionID, Member: a.Member, Task: a.Task, Files: a.Files,
			}))
		case "handoff_to_agy":
			if !agents.TeamMode(req.SessionID) {
				writeJSON(w, http.StatusOK, map[string]string{"status": "refused", "reason": services.TeamOffReason})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "handed_off", "text": agyHandoffText})
		default:
			http.Error(w, "unknown tool", http.StatusBadRequest)
		}
	}
}

// TeamResultGet returns a member's untruncated answer for an hour after the call.
func TeamResultGet(team *services.TeamService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := mux.Vars(r)
		text, ok := team.FullText(v["id"], v["callId"])
		if !ok {
			jsonError(w, "result not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, map[string]string{"text": text})
	}
}

// TeamRaise lifts the session's member token cap by one more step.
func TeamRaise(team *services.TeamService, agents *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if _, err := agents.Get(id); err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		team.RaiseCap(id)
		jsonResponse(w, map[string]bool{"ok": true})
	}
}
