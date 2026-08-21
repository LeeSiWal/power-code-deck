package middleware

import (
	"net/http"
	"strings"
)

// CORS takes the DERIVED client-origin list (config.ClientOrigins), not the raw
// CORS_ORIGINS env string. That distinction is the bug this signature fixes: the
// raw string omits PUBLIC_URL and LAN_URL, so a user reaching the deck through its
// public URL passed the Host and WebSocket guards and then failed the preflight.
// Same-origin use hid it, because a same-origin request needs no CORS header.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		// An Origin header never has a trailing slash; a configured base URL often
		// does. Normalize here so one stray slash can't silently reject a client.
		if o = strings.TrimRight(strings.TrimSpace(strings.ToLower(o)), "/"); o != "" {
			originSet[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if originSet[strings.ToLower(strings.TrimRight(origin, "/"))] || originSet["*"] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
