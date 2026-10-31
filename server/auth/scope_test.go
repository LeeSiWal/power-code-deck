package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func expOf(t *testing.T, s *AuthService, tok string) time.Time {
	t.Helper()
	claims, err := s.parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		t.Fatalf("no exp: %v", err)
	}
	return exp.Time
}

// A QR handoff used to hand out a full 7-day access + 30-day refresh pair, so a
// photographed QR meant a month of control over the whole deck. The exchange now
// yields tokens bound to the one session, with short lifetimes.
func TestHandoffTokensAreSessionScoped(t *testing.T) {
	s := newTestSvc()
	access, refresh, err := s.GenerateHandoffTokens("agent-1")
	if err != nil {
		t.Fatal(err)
	}

	scope, err := s.VerifyAccess(access)
	if err != nil {
		t.Fatalf("scoped access should verify: %v", err)
	}
	if scope.SessionID != "agent-1" {
		t.Fatalf("scope = %q, want agent-1", scope.SessionID)
	}
	if got := time.Until(expOf(t, s, access)); got > HandoffAccessTTL+time.Minute {
		t.Errorf("access lives %v, want <= %v", got, HandoffAccessTTL)
	}
	if got := time.Until(expOf(t, s, refresh)); got > HandoffRefreshTTL+time.Minute {
		t.Errorf("refresh lives %v, want <= %v", got, HandoffRefreshTTL)
	}

	// Fail closed: any caller still on the unscoped check must reject it rather
	// than treat it as a full credential.
	if err := s.VerifyAccessToken(access); err == nil {
		t.Fatal("scoped token MUST NOT pass the unscoped VerifyAccessToken")
	}
	if _, err := s.VerifyAccess(refresh); err == nil {
		t.Fatal("scoped refresh token MUST NOT be usable as access")
	}
}

// Refreshing keeps the scope and can never outlive the refresh token, so the
// handoff grant has a hard end.
func TestRefreshKeepsHandoffScope(t *testing.T) {
	s := newTestSvc()
	_, refresh, err := s.GenerateHandoffTokens("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	access, err := s.RefreshAccessToken(refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	scope, err := s.VerifyAccess(access)
	if err != nil {
		t.Fatal(err)
	}
	if scope.SessionID != "agent-1" {
		t.Fatalf("refreshed scope = %q, want agent-1", scope.SessionID)
	}
	if expOf(t, s, access).After(expOf(t, s, refresh)) {
		t.Fatal("refreshed access outlives its refresh token")
	}
}

func TestFullTokenHasNoScope(t *testing.T) {
	s := newTestSvc()
	access, _ := s.GenerateToken()
	scope, err := s.VerifyAccess(access)
	if err != nil {
		t.Fatal(err)
	}
	if scope.SessionID != "" {
		t.Fatalf("full token got scope %q", scope.SessionID)
	}
}

func TestMiddlewareConfinesHandoffToken(t *testing.T) {
	s := NewAuthService(true, "pin", "1234", "", "test-secret-0123456789abcdef")
	scoped, _, err := s.GenerateHandoffTokens("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	full, _ := s.GenerateToken()

	h := Middleware(s)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(tok, method, target string) int {
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	allowed := [][2]string{
		{"GET", "/api/agents"},
		{"GET", "/api/agents/slash-commands"},
		{"GET", "/api/agents/agent-1"},
		{"POST", "/api/agents/agent-1/stop"},
		{"POST", "/api/agents/agent-1/route-turn"},
		{"GET", "/api/agents/agent-1/sessions/abc"},
		{"GET", "/api/files/read?agentId=agent-1&path=main.go"},
		{"PUT", "/api/files/write?agentId=agent-1"},
		{"GET", "/api/logs/agent-1"},
		{"GET", "/api/notifications"},
		{"GET", "/api/push/vapid"},
		{"POST", "/api/push/subscribe"},
	}
	for _, c := range allowed {
		if code := do(scoped, c[0], c[1]); code != http.StatusNoContent {
			t.Errorf("scoped %s %s = %d, want allowed", c[0], c[1], code)
		}
	}

	denied := [][2]string{
		{"POST", "/api/agents"},                 // create another agent
		{"DELETE", "/api/agents/agent-1"},       // delete the agent itself
		{"POST", "/api/agents/agent-1/handoff"}, // re-mint → endless extension
		{"POST", "/api/agents/agent-2/stop"},    // someone else's agent
		{"GET", "/api/agents/agent-2/sessions"}, // someone else's history
		{"GET", "/api/agents/agent-1x"},         // prefix lookalike
		{"POST", "/api/plugins/install"},
		{"GET", "/api/files/read?path=/etc"}, // no agent scope
		{"GET", "/api/files/read?agentId=agent-2&path=a"},
		{"GET", "/api/proxy?url=http://x"},
		{"POST", "/api/projects/create"},
		{"GET", "/api/projects/browse"},
		{"GET", "/api/logs"},
		{"GET", "/api/v2/runs"},
		{"POST", "/api/v2/runs"},
	}
	for _, c := range denied {
		if code := do(scoped, c[0], c[1]); code != http.StatusForbidden {
			t.Errorf("scoped %s %s = %d, want 403", c[0], c[1], code)
		}
	}

	// A full token is untouched by the scope rules.
	for _, c := range append(allowed, denied...) {
		if code := do(full, c[0], c[1]); code != http.StatusNoContent {
			t.Errorf("full %s %s = %d, want allowed", c[0], c[1], code)
		}
	}
}
