package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"powercodedeck/auth"
)

// The exchange used to return a full login pair (7-day access, 30-day refresh)
// for any redeemed QR. It must now return tokens confined to the QR's session.
func TestHandoffExchangeIssuesSessionScopedTokens(t *testing.T) {
	svc := auth.NewAuthService(true, "pin", "1234", "", "test-secret-0123456789abcdef")
	cookie, err := svc.GenerateHandoffCookie("agent-1")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/handoff/exchange", nil)
	req.AddCookie(&http.Cookie{Name: "handoff_session", Value: cookie})
	rec := httptest.NewRecorder()
	HandoffExchange(svc)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		SessionID    string `json:"sessionId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.VerifyAccess(out.AccessToken)
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	if scope.SessionID != "agent-1" || out.SessionID != "agent-1" {
		t.Fatalf("scope=%q sessionId=%q, want agent-1", scope.SessionID, out.SessionID)
	}
	refreshed, err := svc.RefreshAccessToken(out.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if s2, _ := svc.VerifyAccess(refreshed); s2.SessionID != "agent-1" {
		t.Fatalf("refresh widened scope to %q", s2.SessionID)
	}
}
