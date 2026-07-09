package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"powercodedeck/auth"
	"powercodedeck/config"
)

type loginRequest struct {
	Pin      string `json:"pin"`
	Password string `json:"password"`
	Secret   string `json:"secret"`
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func Login(authSvc *auth.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		// Accept the credential from whichever field the client used.
		secret := req.Secret
		if secret == "" {
			secret = req.Pin
		}
		if secret == "" {
			secret = req.Password
		}

		if !authSvc.VerifyCredential(secret) {
			jsonError(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		accessToken, err := authSvc.GenerateToken()
		if err != nil {
			jsonError(w, "token generation failed", http.StatusInternalServerError)
			return
		}

		refreshToken, err := authSvc.GenerateRefreshToken()
		if err != nil {
			jsonError(w, "token generation failed", http.StatusInternalServerError)
			return
		}

		jsonResponse(w, tokenResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
		})
	}
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func Refresh(authSvc *auth.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		accessToken, err := authSvc.RefreshAccessToken(req.RefreshToken)
		if err != nil {
			jsonError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}

		jsonResponse(w, map[string]string{"accessToken": accessToken})
	}
}

// AnonymousToken issues access/refresh tokens in no-auth mode so the WebSocket
// (which now always authenticates) can be reached by the local browser without
// a login. It refuses when auth is enabled — use /login — and only serves
// callers with a local/allowed Origin, so a cross-origin drive-by page can't
// mint one (its cross-origin fetch also can't read the response thanks to CORS,
// and the Host guard already restricts who reaches this at all).
//
//	POST /api/auth/anonymous
func AnonymousToken(authSvc *auth.AuthService, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if authSvc.Enabled() {
			jsonError(w, "authentication is enabled; use login", http.StatusForbidden)
			return
		}
		if !isLocalOrigin(r, cfg) {
			jsonError(w, "forbidden origin", http.StatusForbidden)
			return
		}

		access, err := authSvc.GenerateToken()
		if err != nil {
			jsonError(w, "token generation failed", http.StatusInternalServerError)
			return
		}
		refresh, err := authSvc.GenerateRefreshToken()
		if err != nil {
			jsonError(w, "token generation failed", http.StatusInternalServerError)
			return
		}

		jsonResponse(w, tokenResponse{AccessToken: access, RefreshToken: refresh})
	}
}

// isLocalOrigin allows requests with no Origin header (same-origin navigations /
// non-browser clients) and requests whose Origin is in the configured allow-list.
func isLocalOrigin(r *http.Request, cfg *config.Config) bool {
	origin := strings.ToLower(strings.TrimRight(r.Header.Get("Origin"), "/"))
	if origin == "" {
		return true
	}
	for _, o := range cfg.AllowedOrigins() {
		if strings.ToLower(strings.TrimRight(o, "/")) == origin {
			return true
		}
	}
	return false
}
