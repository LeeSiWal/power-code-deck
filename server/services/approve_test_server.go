package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// newApproveServer is the deck side of the permission bridge, standalone.
//
// It mirrors handlers.NativeApprove — park the ask on the broker, hold the HTTP
// response open until a human (or a test) answers. It lives here, in the services
// package, because the live tests drive the driver directly and importing
// handlers from here would be a cycle. Kept out of _test.go so more than one test
// file can use it.
func newApproveServer(broker *PermissionBroker, tokens ApproveTokenStore) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string          `json:"sessionId"`
			ToolName  string          `json:"toolName"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"toolUseId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !tokens.Valid(req.SessionID, r.Header.Get("X-PCD-Approve-Token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		d, err := broker.Ask(PermissionRequest{
			ID: req.ToolUseID, SessionID: req.SessionID, ToolName: req.ToolName, Input: req.Input,
		}, r.Context().Done())
		if err != nil {
			d = PermissionDecision{Behavior: "deny", Message: "cancelled"}
		}
		if d.Behavior == "allow" && len(d.UpdatedInput) == 0 {
			d.UpdatedInput = req.Input
		}
		_ = json.NewEncoder(w).Encode(d)
	}))
}
