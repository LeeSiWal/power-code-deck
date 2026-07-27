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
	// CanRemember·RememberTarget: 관제실 최초 로드에서도 버튼을 올바르게 렌더하기
	// 위해 포함한다. WS 브로드캐스트와 같은 계산이 여기서도 일어나야 재접속이나
	// 최초 진입 때 버튼이 갑자기 사라지지 않는다.
	CanRemember    bool   `json:"canRemember"`
	RememberTarget string `json:"rememberTarget"`
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
			// WS 브로드캐스트·재접속과 동일한 계산: cwd가 없으면 CanRemember=false.
			cwd := native.SessionCwd(req.SessionID)
			canRemember, rememberTarget := services.CanRememberCall(req.ToolName, req.Input, cwd)
			out = append(out, PendingApproval{
				RequestID:      req.ID,
				AgentID:        req.SessionID,
				ToolName:       req.ToolName,
				Input:          req.Input,
				AskedAt:        req.AskedAt.Format(time.RFC3339),
				CanRemember:    canRemember,
				RememberTarget: rememberTarget,
			})
		}
		jsonResponse(w, out)
	}
}
