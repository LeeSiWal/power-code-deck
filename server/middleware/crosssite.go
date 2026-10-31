package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// CrossSiteGuard refuses state-changing requests that a browser sent on behalf of
// another site (CSRF). With auth off, nothing else stands between a page on any
// website and POST /api/agents — and that endpoint starts an arbitrary command.
// CORS does not help: a `mode: "no-cors"` POST still reaches the handler, the
// attacker just can't read the answer.
//
// Browsers always attach Origin to a POST/PUT/PATCH/DELETE, so the rule is:
//   - Origin present → it must be one of the client origins, or the request's own
//     host (same-origin; HostCheck has already vetted r.Host). "null" — what a
//     sandboxed iframe or a data: page sends — never matches.
//   - no Origin, but Sec-Fetch-Site says cross-site → refused.
//   - neither header → not a browser (the CLI, our own child processes); allowed,
//     because CSRF needs a browser carrying the victim's ambient credentials.
//
// Safe methods pass untouched: GET must not change state anywhere in this API.
func CrossSiteGuard(allowedOrigins []string) func(http.Handler) http.Handler {
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(strings.ToLower(o)), "/"); o != "" {
			originSet[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) || sameSiteRequest(originSet, r) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, `{"error":"cross-site request refused"}`, http.StatusForbidden)
		})
	}
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func sameSiteRequest(originSet map[string]bool, r *http.Request) bool {
	origin := strings.ToLower(strings.TrimRight(r.Header.Get("Origin"), "/"))
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	if origin == "null" {
		return false
	}
	if originSet["*"] || originSet[origin] {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}
