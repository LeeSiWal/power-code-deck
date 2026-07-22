package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"powercodedeck/services"
)

// ControlSummaries returns the full Control Room snapshot for the initial page load.
// After this, the client applies agent:summaries deltas over the WebSocket.
func ControlSummaries(cr *services.ControlRoomService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, cr.Summaries())
	}
}

// PendingApproval is one unanswered approval, across all sessions.
type PendingApproval struct {
	RequestID string          `json:"requestId"`
	AgentID   string          `json:"agentId"`
	ToolName  string          `json:"toolName"`
	Input     json.RawMessage `json:"input"`
	AskedAt   string          `json:"askedAt"`
}

// ListApprovals is the initial snapshot of the global approval queue — what the
// Control Room shows before any native:approval delta arrives. Pairs with the REST
// summaries call so a freshly opened /control has complete state without waiting on
// the next event.
func ListApprovals(native *services.NativeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqs := native.Pending("") // "" = every session
		out := make([]PendingApproval, 0, len(reqs))
		for _, req := range reqs {
			out = append(out, PendingApproval{
				RequestID: req.ID,
				AgentID:   req.SessionID,
				ToolName:  req.ToolName,
				Input:     req.Input,
				AskedAt:   req.AskedAt.Format(time.RFC3339),
			})
		}
		jsonResponse(w, out)
	}
}
