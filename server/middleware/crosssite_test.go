package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func crossSiteStatus(t *testing.T, allowed []string, method, host string, headers map[string]string) int {
	t.Helper()
	h := CrossSiteGuard(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, "http://"+host+"/api/agents", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// The drive-by this exists for: any website POSTing to a no-auth deck on
// localhost to start a command.
func TestCrossSiteGuardRefusesForeignOrigin(t *testing.T) {
	allowed := []string{"http://localhost:33033", "https://pcd.example.com"}
	for name, hdr := range map[string]map[string]string{
		"foreign origin":       {"Origin": "https://evil.example"},
		"sandboxed null":       {"Origin": "null"},
		"fetch metadata only":  {"Sec-Fetch-Site": "cross-site"},
		"lookalike host":       {"Origin": "http://localhost:33033.evil.example"},
		"public url as suffix": {"Origin": "https://evil.pcd.example.com"},
	} {
		if got := crossSiteStatus(t, allowed, http.MethodPost, "localhost:33033", hdr); got != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, got)
		}
	}
}

func TestCrossSiteGuardAllowsOwnClients(t *testing.T) {
	allowed := []string{"http://localhost:33033", "https://pcd.example.com/"}
	cases := []struct {
		name, method, host string
		hdr                map[string]string
	}{
		{"configured origin", http.MethodPost, "localhost:33033", map[string]string{"Origin": "http://localhost:33033", "Sec-Fetch-Site": "same-origin"}},
		{"public url, trailing slash in config", http.MethodDelete, "pcd.example.com", map[string]string{"Origin": "https://pcd.example.com"}},
		// A LAN address HostCheck accepted but nobody listed as an origin.
		{"same host as request", http.MethodPut, "192.168.1.5:33033", map[string]string{"Origin": "http://192.168.1.5:33033"}},
		{"non-browser client", http.MethodPost, "127.0.0.1:33033", nil},
		{"safe method from anywhere", http.MethodGet, "localhost:33033", map[string]string{"Origin": "https://evil.example"}},
	}
	for _, c := range cases {
		if got := crossSiteStatus(t, allowed, c.method, c.host, c.hdr); got != http.StatusOK {
			t.Errorf("%s: status %d, want 200", c.name, got)
		}
	}
}

func TestCrossSiteGuardWildcard(t *testing.T) {
	if got := crossSiteStatus(t, []string{"*"}, http.MethodPost, "localhost:33033", map[string]string{"Origin": "https://anything.test"}); got != http.StatusOK {
		t.Errorf("wildcard origin refused: %d", got)
	}
}
