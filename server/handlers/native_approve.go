package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"powercodedeck/services"
)

// NativeApprove is the deck side of the permission bridge: `pcd mcp-approve`
// (spawned by Claude, see cli/mcp_approve.go) POSTs here when Claude wants to run
// a tool, and this handler holds the HTTP response open until a human answers on
// some device. Claude is blocked on its own tool call for that whole time, which
// is exactly the behavior we want — the agent waits for you, not the reverse.
//
// This endpoint is NOT part of the public API:
//   - it is only reachable from loopback (pcd binds 127.0.0.1 by default), and
//   - every request must carry the per-session token we handed the bridge in env.
//
// It deliberately does not use the normal auth middleware: the caller is our own
// child process, not a browser with a JWT.
func NativeApprove(broker *services.PermissionBroker, tokens services.ApproveTokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string          `json:"sessionId"`
			ToolName  string          `json:"toolName"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"toolUseId"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.SessionID == "" || req.ToolName == "" {
			http.Error(w, "missing sessionId or toolName", http.StatusBadRequest)
			return
		}

		// The token is per-session, so a bridge for session A cannot raise prompts
		// on session B (nor can anything else that reaches loopback).
		token := strings.TrimSpace(r.Header.Get("X-PCD-Approve-Token"))
		if !tokens.Valid(req.SessionID, token) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		id := req.ToolUseID
		if id == "" {
			id = req.SessionID + ":" + req.ToolName // best effort; the CLI always sends one
		}

		decision, err := broker.Ask(services.PermissionRequest{
			ID:        id,
			SessionID: req.SessionID,
			ToolName:  req.ToolName,
			Input:     req.Input,
		}, r.Context().Done())
		if err != nil {
			// The session died or the client hung up while we waited. Deny — with a
			// message, because Claude reads it and adapts rather than retrying blind.
			writeJSON(w, http.StatusOK, services.PermissionDecision{
				Behavior: "deny",
				Message:  "PowerCodeDeck: the request was cancelled before anyone answered.",
			})
			return
		}

		// Allow must carry updatedInput — echo the original when the user didn't
		// edit it, so "approve as proposed" and "approve with changes" are one path.
		if decision.Behavior == "allow" && len(decision.UpdatedInput) == 0 {
			decision.UpdatedInput = req.Input
		}
		writeJSON(w, http.StatusOK, decision)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
