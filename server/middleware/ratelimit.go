package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type visitor struct {
	count    int
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{visitors: map[string]*visitor{}, limit: limit, window: window}
	// Cleanup old entries periodically
	go func() {
		for {
			time.Sleep(window)
			rl.mu.Lock()
			for k, v := range rl.visitors {
				if time.Since(v.lastSeen) > window {
					delete(rl.visitors, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

// allow counts one request from key and reports whether it is within the limit.
func (rl *RateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	v := rl.visitors[key]
	if v == nil {
		v = &visitor{}
		rl.visitors[key] = v
	}
	if now.Sub(v.lastSeen) > rl.window {
		v.count = 0
	}
	v.count++
	v.lastSeen = now
	return v.count <= rl.limit
}

// clientKey is the peer's address without its port. Keyed on IP:port, every new
// connection got a fresh budget, so a PIN guesser only had to reconnect.
func clientKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientKey(r), time.Now()) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
