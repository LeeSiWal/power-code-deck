package auth

import "net/http"

func WSAuth(authSvc *AuthService, r *http.Request) error {
	token := r.URL.Query().Get("token")
	return authSvc.VerifyAccessToken(token)
}
