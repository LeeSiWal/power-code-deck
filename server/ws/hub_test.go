package ws

import (
	"net/http"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	allowed := map[string]bool{
		"http://localhost:33033": true,
		"http://127.0.0.1:33033": true,
	}

	tests := []struct {
		name   string
		origin string // "" means no Origin header at all
		set    bool
		want   bool
	}{
		{"allowed localhost", "http://localhost:33033", true, true},
		{"allowed trailing slash", "http://localhost:33033/", true, true},
		{"allowed loopback ip", "http://127.0.0.1:33033", true, true},
		{"cross origin blocked", "https://evil.example.com", true, false},
		{"wrong port blocked", "http://localhost:9999", true, false},
		{"no origin header (CLI) allowed", "", false, true},
		{"empty origin header allowed", "", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/ws", nil)
			if tt.set {
				r.Header.Set("Origin", tt.origin)
			}
			if got := checkOrigin(allowed, r); got != tt.want {
				t.Fatalf("checkOrigin(origin=%q set=%v)=%v want %v", tt.origin, tt.set, got, tt.want)
			}
		})
	}
}
