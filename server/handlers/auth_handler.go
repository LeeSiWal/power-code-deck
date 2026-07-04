package handlers

import (
	"encoding/json"
	"net/http"

	"powercodedeck/auth"
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
