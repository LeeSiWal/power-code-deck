package config

import "testing"

func has(hosts []string, want string) bool {
	for _, h := range hosts {
		if h == want {
			return true
		}
	}
	return false
}

// TestAllowedHostsFromCORSOrigins is the regression test for the reverse-proxy
// breakage: a deployment that only set CORS_ORIGINS (its public domain) was
// rejected by the DNS-rebinding Host guard because AllowedHosts ignored
// CORS_ORIGINS. If you trust an Origin, you trust its Host.
func TestAllowedHostsFromCORSOrigins(t *testing.T) {
	cfg := &Config{
		Port:        "33033",
		BindHost:    "127.0.0.1",
		CORSOrigins: "https://pcd.example.com,http://localhost:33033",
	}

	hosts := cfg.AllowedHosts()

	if !has(hosts, "pcd.example.com") {
		t.Fatalf("bare CORS host must be allowed; got %v", hosts)
	}
	if !has(hosts, "pcd.example.com:33033") {
		t.Fatalf("CORS host with configured port must be allowed; got %v", hosts)
	}
	// Loopback stays allowed regardless.
	if !has(hosts, "localhost:33033") {
		t.Fatalf("loopback must remain allowed; got %v", hosts)
	}
}

// TestAllowedHostsDefaultLoopbackOnly confirms the security default is unchanged:
// with no CORS/PUBLIC_URL configured, only loopback hosts are accepted.
func TestAllowedHostsDefaultLoopbackOnly(t *testing.T) {
	cfg := &Config{Port: "33033", BindHost: "127.0.0.1"}
	for _, h := range cfg.AllowedHosts() {
		switch h {
		case "localhost", "127.0.0.1", "::1", "[::1]",
			"localhost:33033", "127.0.0.1:33033", "[::1]:33033":
			// loopback — expected
		default:
			t.Fatalf("unexpected non-loopback host in default config: %q", h)
		}
	}
}
