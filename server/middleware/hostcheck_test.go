package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostCheck(t *testing.T) {
	// localhost:33033 (bare "localhost" allows any port), plus an explicit domain.
	mw := HostCheck([]string{"localhost", "127.0.0.1", "::1", "pcd.example.com"})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name string
		host string
		want int
	}{
		{"loopback with port", "localhost:33033", http.StatusOK},
		{"loopback other port", "localhost:8080", http.StatusOK},
		{"loopback ip", "127.0.0.1:33033", http.StatusOK},
		{"configured domain", "pcd.example.com", http.StatusOK},
		{"rebinding attacker host", "attacker.example.org", http.StatusForbidden},
		{"bare unknown host", "example.net:33033", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+"/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("Host=%q got %d want %d", tt.host, rec.Code, tt.want)
			}
		})
	}
}
