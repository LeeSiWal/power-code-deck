package auth

import (
	"net/http"
	"strings"
)

func Middleware(authSvc *AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// No-auth mode: let every request through.
			if !authSvc.Enabled() {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
				return
			}

			scope, err := authSvc.VerifyAccess(parts[1])
			if err != nil {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			if scope.SessionID != "" && !scopeAllows(scope.SessionID, r) {
				// 403, not 401: the token is fine, it just can't reach this. A 401
				// would make the client throw its tokens away.
				http.Error(w, `{"error":"this handoff token only covers its own session"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// scopeAllows is the allow-list for a session-scoped (QR handoff) token. It
// covers what the handed-off session page needs and nothing that manages the
// deck: no creating/deleting agents, no other agents, no plugins, projects, v2
// runs, routing, proxy, or file access outside the session's own repo.
func scopeAllows(sid string, r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	get := r.Method == http.MethodGet || r.Method == http.MethodHead

	if rest, ok := strings.CutPrefix(path, "/api/agents/"); ok {
		id, sub, _ := strings.Cut(rest, "/")
		if id == "slash-commands" && sub == "" {
			return get
		}
		if id != sid {
			return false
		}
		switch {
		case sub == "" && r.Method == http.MethodDelete:
			return false // deleting the agent is management, not driving it
		case sub == "handoff":
			return false // re-minting a handoff would extend the grant forever
		}
		return true
	}
	if strings.HasPrefix(path, "/api/files/") {
		// File handlers confine paths to the named agent's working dir; without
		// an agentId they fall back to every known base, so require ours.
		return r.URL.Query().Get("agentId") == sid
	}

	switch path {
	case "/api/agents", "/api/notifications", "/api/push/vapid", "/api/logs/" + sid:
		return get
	case "/api/push/subscribe", "/api/push/unsubscribe":
		return r.Method == http.MethodPost
	}
	return false
}
