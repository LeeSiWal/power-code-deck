package middleware

import (
	"net"
	"net/http"
	"strings"
)

// HostCheck rejects requests whose Host header isn't in the allow-list. This
// blocks DNS-rebinding attacks, where a malicious site resolves an
// attacker-controlled domain to 127.0.0.1 to reach a no-auth PowerCodeDeck from
// the victim's browser. Non-loopback hosts are only accepted when explicitly
// configured (PUBLIC_URL / LAN_URL / BIND_HOST / ALLOWED_HOSTS).
//
// A bare hostname in the allow-list matches that host on any port, so changing
// the port never locks anyone out.
func HostCheck(allowedHosts []string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowedHosts))
	for _, h := range allowedHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			set[h] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowedHost(set, r.Host) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "forbidden host", http.StatusForbidden)
		})
	}
}

// allowedHost matches the full "host:port" or, failing that, the bare hostname
// (so an allowed host is reachable on any port).
func allowedHost(set map[string]bool, host string) bool {
	host = strings.ToLower(host)
	if host == "" {
		return false
	}
	if set[host] {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return set[h]
	}
	return false
}
