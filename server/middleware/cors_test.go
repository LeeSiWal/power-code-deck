package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsOrigin(t *testing.T, allowed []string, origin string) string {
	t.Helper()
	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/api/agents", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Header().Get("Access-Control-Allow-Origin")
}

// The regression this exists for: CORS was wired with the raw CORS_ORIGINS env
// string while the WebSocket guard was wired with the derived list that also folds
// in PUBLIC_URL and LAN_URL. A user reaching the deck through PUBLIC_URL therefore
// passed the Host and WebSocket guards but failed the CORS preflight. Same-origin
// use hid it, because same-origin requests need no CORS header at all.
func TestCORSAllowsEveryDerivedOrigin(t *testing.T) {
	allowed := []string{
		"http://localhost:33033",
		"https://pcd.example.com", // from PUBLIC_URL
		"http://192.168.1.50:33033",
		"tauri://localhost",
	}
	for _, origin := range allowed {
		if got := corsOrigin(t, allowed, origin); got != origin {
			t.Errorf("Access-Control-Allow-Origin for %q = %q, want %q", origin, got, origin)
		}
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	if got := corsOrigin(t, []string{"http://localhost:33033"}, "https://evil.example.com"); got != "" {
		t.Errorf("unknown origin echoed back: %q", got)
	}
}

// A trailing slash on a configured origin must not silently stop matching — an
// Origin header never carries one, so the allow-list has to normalize.
func TestCORSIgnoresTrailingSlashInConfig(t *testing.T) {
	if got := corsOrigin(t, []string{"https://pcd.example.com/"}, "https://pcd.example.com"); got != "https://pcd.example.com" {
		t.Errorf("trailing-slash config broke matching: %q", got)
	}
}

func TestCORSWildcardStillWorks(t *testing.T) {
	if got := corsOrigin(t, []string{"*"}, "https://anything.test"); got != "https://anything.test" {
		t.Errorf("wildcard broke: %q", got)
	}
}
