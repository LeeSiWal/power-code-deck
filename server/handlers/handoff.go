package handlers

import (
	"errors"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"powercodedeck/auth"
	"powercodedeck/config"
	"powercodedeck/services"

	"github.com/gorilla/mux"
)

// CreateHandoff issues a one-time "Continue on Mobile" token for a session and
// returns the QR-ready URLs. Requires the caller to be authorized for the API
// (the auth middleware already gates this route).
//
//	POST /api/agents/{id}/handoff
func CreateHandoff(handoffSvc *services.HandoffService, agentSvc *services.AgentService, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.HandoffEnabled {
			jsonError(w, "session handoff is disabled", http.StatusForbidden)
			return
		}

		sessionID := mux.Vars(r)["id"]
		if _, err := agentSvc.Get(sessionID); err != nil {
			jsonError(w, "session not found", http.StatusNotFound)
			return
		}

		ip := clientIP(r)
		ua := r.UserAgent()
		ttl := time.Duration(cfg.HandoffTokenTTL) * time.Second

		rawToken, rec, err := handoffSvc.Create(sessionID, "", ip, ua, ttl)
		if err != nil {
			jsonError(w, "failed to create handoff token", http.StatusInternalServerError)
			return
		}

		publicBase := cfg.PublicURL
		if publicBase == "" {
			publicBase = requestBaseURL(r)
		}
		publicURL := publicBase + "/handoff/" + rawToken

		var localURL string
		if cfg.LanHandoffEnabled {
			if base := lanBaseURL(cfg); base != "" {
				localURL = base + "/handoff/" + rawToken
			}
		}

		// Never log the raw token.
		log.Printf("handoff token created session=%s expires=%s ip=%s",
			sessionID, rec.ExpiresAt.Format(time.RFC3339), ip)

		warning := ""
		if !cfg.AuthEnabled && cfg.LanHandoffEnabled {
			warning = "PowerCodeDeck authentication is disabled and LAN handoff is enabled. " +
				"Anyone on the same network who can reach this URL may attempt to open handoff links. " +
				"Use one-time tokens, enable PIN/password auth, or keep the service behind VPN/Tailscale."
		}

		jsonResponse(w, map[string]interface{}{
			"token":       rawToken,
			"sessionId":   sessionID,
			"expiresAt":   rec.ExpiresAt.Format(time.RFC3339),
			"ttlSeconds":  cfg.HandoffTokenTTL,
			"publicUrl":   publicURL,
			"localUrl":    localURL,
			"lanEnabled":  cfg.LanHandoffEnabled,
			"authEnabled": cfg.AuthEnabled,
			"warning":     warning,
		})
	}
}

// RedeemHandoff validates a one-time token from a scanned QR, marks it used,
// issues a session-scoped handoff cookie, and redirects the mobile browser to
// the session. On failure it renders a friendly (KO + EN) error page.
//
//	GET /handoff/{token}
func RedeemHandoff(handoffSvc *services.HandoffService, agentSvc *services.AgentService, authSvc *auth.AuthService, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := mux.Vars(r)["token"]
		ip := clientIP(r)

		sessionID, err := handoffSvc.Redeem(token)
		if err != nil {
			reason := "invalid"
			switch {
			case errors.Is(err, services.ErrHandoffExpired):
				reason = "expired"
			case errors.Is(err, services.ErrHandoffUsed):
				reason = "already_used"
			case errors.Is(err, services.ErrHandoffInvalid):
				reason = "invalid"
			default:
				reason = "error"
			}
			log.Printf("handoff token rejected reason=%s ip=%s", reason, ip)
			renderHandoffError(w, reason)
			return
		}

		// The session must still be alive to attach to.
		if _, gerr := agentSvc.Get(sessionID); gerr != nil {
			log.Printf("handoff token rejected reason=session_gone session=%s ip=%s", sessionID, ip)
			renderHandoffError(w, "session_gone")
			return
		}

		// Issue a short-lived, session-scoped handoff cookie. Only meaningful
		// when PowerCodeDeck auth is enabled, but harmless otherwise.
		if cookie, cerr := authSvc.GenerateHandoffCookie(sessionID); cerr == nil {
			http.SetCookie(w, &http.Cookie{
				Name:     "handoff_session",
				Value:    cookie,
				Path:     "/",
				HttpOnly: true,
				Secure:   isHTTPS(r),
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int((30 * time.Minute).Seconds()),
			})
		}

		log.Printf("handoff token used session=%s ip=%s", sessionID, ip)
		http.Redirect(w, r, "/agents/"+sessionID+"?from=handoff", http.StatusFound)
	}
}

// HandoffExchange lets a redeemed mobile browser trade its session-scoped
// handoff cookie for normal access/refresh tokens, so the existing bearer/WS
// flow works unchanged. Only needed when PowerCodeDeck auth is enabled.
//
//	POST /api/auth/handoff/exchange
func HandoffExchange(authSvc *auth.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("handoff_session")
		if err != nil || c.Value == "" {
			jsonError(w, "no handoff session", http.StatusUnauthorized)
			return
		}
		sessionID, err := authSvc.VerifyHandoffCookie(c.Value)
		if err != nil {
			jsonError(w, "invalid handoff session", http.StatusUnauthorized)
			return
		}

		access, err := authSvc.GenerateToken()
		if err != nil {
			jsonError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}
		refresh, err := authSvc.GenerateRefreshToken()
		if err != nil {
			jsonError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{
			"accessToken":  access,
			"refreshToken": refresh,
			"sessionId":    sessionID,
		})
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func lanBaseURL(cfg *config.Config) string {
	if cfg.LanURL != "" {
		return cfg.LanURL
	}
	if ip := services.DetectLANIP(); ip != "" {
		return fmt.Sprintf("http://%s:%s", ip, cfg.Port)
	}
	return ""
}

func renderHandoffError(w http.ResponseWriter, reason string) {
	status := http.StatusGone
	if reason == "invalid" {
		status = http.StatusNotFound
	}

	detailEN := "This handoff link is invalid or expired."
	detailKO := "이어하기 링크가 만료되었거나 이미 사용되었습니다."
	switch reason {
	case "session_gone":
		detailEN = "That session is no longer available."
		detailKO = "해당 세션이 더 이상 존재하지 않습니다."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, handoffErrorHTML, html.EscapeString(detailKO), html.EscapeString(detailEN))
}

const handoffErrorHTML = `<!doctype html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PowerCodeDeck — Handoff</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         background:#0d1117; color:#e6edf3; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; padding:24px; }
  .card { max-width:420px; text-align:center; background:#161b22; border:1px solid #30363d;
          border-radius:16px; padding:32px 24px; }
  .icon { font-size:40px; margin-bottom:12px; }
  h1 { font-size:18px; margin:0 0 12px; }
  p { font-size:14px; line-height:1.6; color:#9da7b3; margin:6px 0; }
  .en { color:#6e7681; font-size:12px; }
  a.btn { display:inline-block; margin-top:20px; padding:10px 20px; border-radius:10px;
          background:#238636; color:#fff; text-decoration:none; font-size:14px; font-weight:600; }
</style>
</head>
<body>
  <div class="card">
    <div class="icon">🔗</div>
    <h1>이어하기 링크를 열 수 없습니다</h1>
    <p>%s</p>
    <p>PC 화면에서 <b>모바일에서 이어하기</b>를 다시 눌러 새 QR 코드를 생성해 주세요.</p>
    <p class="en">%s Please generate a new QR code from PowerCodeDeck.</p>
    <a class="btn" href="/">홈으로 이동 / Go home</a>
  </div>
</body>
</html>`
