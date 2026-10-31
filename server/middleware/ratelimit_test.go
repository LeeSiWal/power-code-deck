package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestRateLimitIgnoresSourcePort(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	codes := []int{}
	// Each attempt from a new connection (new source port) — same client.
	for port := 50000; port < 50005; port++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:" + strconv.Itoa(port)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[2] != 200 || codes[3] != http.StatusTooManyRequests || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("new ports must share one budget, got %v", codes)
	}
	// A different client is unaffected.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "203.0.113.8:50000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("other client limited: %d", w.Code)
	}
}

func TestRateLimitConcurrent(t *testing.T) {
	rl := NewRateLimiter(1000, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				rl.allow("10.0.0.1", time.Now())
			}
		}()
	}
	wg.Wait()
	if c := rl.visitors["10.0.0.1"].count; c != 1000 {
		t.Fatalf("lost updates: count=%d, want 1000", c)
	}
}
