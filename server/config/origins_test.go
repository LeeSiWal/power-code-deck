package config

import (
	"net"
	"strings"
	"testing"
)

// hasOrigin is case-insensitive: the Origin header's case is the client's choice,
// while `has` in config_test.go stays exact for Host values.
func hasOrigin(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// hostAccepted mirrors middleware.allowedHost: exact host:port, or the bare
// hostname (which matches any port).
func hostAccepted(allowed []string, host string) bool {
	if hasOrigin(allowed, host) {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return hasOrigin(allowed, h)
	}
	return false
}

// The three guards — CORS, the DNS-rebinding HostCheck, and the WebSocket
// CheckOrigin — must agree. main.go's LAN comment records what happens when they
// do not: HostCheck auto-detected the LAN IP and let the page load, the Origin
// allow-list did not, and a LAN device sat on "Connecting…" with no token. Same
// class of failure, now reachable from any cross-origin client.
func TestGuardsAgreeOnEveryConfiguredOrigin(t *testing.T) {
	cfg := &Config{
		Port:        "33033",
		PublicURL:   "https://pcd.example.com",
		LanURL:      "http://192.168.1.50:33033",
		CORSOrigins: "http://localhost:5173",
	}
	hosts := cfg.AllowedHosts()
	for _, origin := range cfg.ClientOrigins() {
		if !hasOrigin(cfg.AllowedOrigins(), origin) {
			t.Errorf("origin %q is a client origin but AllowedOrigins rejects it", origin)
		}
		// A desktop shell origin is the SHELL's own document origin (Tauri serves
		// the bundled UI from tauri://localhost, or https://tauri.localhost on
		// Windows WebView2). It is never a Host the deck receives — requests still
		// carry the deck's real host — so requiring a HostCheck entry for it would
		// be wrong, not merely redundant.
		if hasOrigin(desktopShellOrigins, origin) {
			continue
		}
		host := hostFromURL(origin)
		if host == "" {
			t.Errorf("origin %q has no parseable host", origin)
			continue
		}
		if !hostAccepted(hosts, host) {
			t.Errorf("origin %q is allowed but HostCheck rejects its Host %q", origin, host)
		}
	}
}

// The desktop shell's origin must be allowed EXPLICITLY. ws.checkOrigin lets a
// request with no Origin header through (for the CLI), and a shell could ride that
// exemption — but then the exemption can never be tightened again.
func TestClientOriginsIncludeDesktopShell(t *testing.T) {
	cfg := &Config{Port: "33033"}
	origins := cfg.ClientOrigins()
	for _, want := range []string{"tauri://localhost", "https://tauri.localhost"} {
		if !hasOrigin(origins, want) {
			t.Errorf("ClientOrigins() missing desktop shell origin %q; got %v", want, origins)
		}
	}
}

// A loopback-only user must see no change at all from cross-origin support.
func TestLoopbackOnlyConfigStillAllowsLoopback(t *testing.T) {
	cfg := &Config{Port: "33033"}
	for _, want := range []string{
		"http://localhost:33033", "http://127.0.0.1:33033",
		"https://localhost:33033", "https://127.0.0.1:33033",
	} {
		if !hasOrigin(cfg.ClientOrigins(), want) {
			t.Errorf("loopback origin %q missing from ClientOrigins()", want)
		}
	}
	hosts := cfg.AllowedHosts()
	for _, want := range []string{"localhost:33033", "127.0.0.1:33033", "localhost", "127.0.0.1"} {
		if !hostAccepted(hosts, want) {
			t.Errorf("loopback host %q rejected by AllowedHosts()", want)
		}
	}
	// And nothing else leaked in beyond the shell origins.
	for _, o := range cfg.ClientOrigins() {
		if strings.Contains(o, "example.com") {
			t.Errorf("unconfigured origin leaked into ClientOrigins(): %q", o)
		}
	}
}
