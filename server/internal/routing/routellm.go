package routing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// RouterVerdict is one RouteLLM answer for one configured pair.
type RouterVerdict struct {
	RequestID  string  `json:"requestId"`
	Router     string  `json:"router"`
	Score      float64 `json:"score"`  // RouteLLM strong-win-rate for this input
	Choice     string  `json:"choice"` // "strong" | "weak" from Controller.route
	Threshold  float64 `json:"threshold"`
	Checkpoint string  `json:"checkpoint"`
	Package    string  `json:"package"`
	Device     string  `json:"device"`
	LatencyMS  int64   `json:"latencyMs"`
	Cached     bool    `json:"cached"`
}

var ErrRouterUnavailable = errors.New("router unavailable")

// RouteLLMClient talks to the loopback sidecar. It fails closed: any error,
// timeout, oversized or malformed answer returns ErrRouterUnavailable and the
// caller keeps its rule-based floor. It never falls back to an external API.
type RouteLLMClient struct {
	cfg     RouteLLMConfig
	http    *http.Client
	token   string
	maxIn   int
	mu      sync.Mutex
	cache   map[string]cachedVerdict
	fails   int
	openTil time.Time
	now     func() time.Time
}

type cachedVerdict struct {
	v   RouterVerdict
	exp time.Time
}

const (
	breakerThreshold = 3
	breakerCooldown  = 60 * time.Second
	cacheTTL         = 30 * time.Minute
	maxCacheEntries  = 512
)

// NewRouteLLMClient refuses a non-loopback URL unless a token env var is
// configured and the scheme is https.
func NewRouteLLMClient(cfg RouteLLMConfig) (*RouteLLMClient, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("routellm: invalid url")
	}
	token := ""
	if cfg.TokenEnv != "" {
		token = os.Getenv(cfg.TokenEnv)
	}
	if !isLoopbackHost(u.Hostname()) && (u.Scheme != "https" || token == "") {
		return nil, fmt.Errorf("routellm: a non-loopback sidecar requires https and tokenEnv")
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 1500 * time.Millisecond
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &RouteLLMClient{cfg: cfg, http: client, token: token, maxIn: 8 * 1024, cache: map[string]cachedVerdict{}, now: time.Now}, nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Score asks RouteLLM for one pair. cacheKey must include everything that
// makes the answer valid (config fingerprint, pair, threshold, checkpoint).
func (c *RouteLLMClient) Score(ctx context.Context, text string, pair RouterPair, fingerprint string) (RouterVerdict, error) {
	if c == nil {
		return RouterVerdict{}, ErrRouterUnavailable
	}
	if len(text) > c.maxIn {
		text = clip(text, c.maxIn)
	}
	sum := sha256.Sum256([]byte(fingerprint + "\x00" + c.cfg.Router + "\x00" + pair.Weak + "\x00" + pair.Strong + "\x00" + fmt.Sprint(pair.Threshold) + "\x00" + text))
	key := hex.EncodeToString(sum[:])
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && c.now().Before(e.exp) {
		c.mu.Unlock()
		v := e.v
		v.Cached = true
		return v, nil
	}
	if c.now().Before(c.openTil) {
		c.mu.Unlock()
		return RouterVerdict{}, fmt.Errorf("%w: circuit open", ErrRouterUnavailable)
	}
	c.mu.Unlock()

	v, err := c.call(ctx, text, pair)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fails++
		if c.fails >= breakerThreshold {
			c.openTil = c.now().Add(breakerCooldown)
			c.fails = 0
		}
		return RouterVerdict{}, fmt.Errorf("%w: %v", ErrRouterUnavailable, err)
	}
	c.fails = 0
	if len(c.cache) >= maxCacheEntries {
		c.cache = map[string]cachedVerdict{}
	}
	c.cache[key] = cachedVerdict{v: v, exp: c.now().Add(cacheTTL)}
	return v, nil
}

// Invalidate drops cached answers (profile, auth, billing or policy changed).
func (c *RouteLLMClient) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cache = map[string]cachedVerdict{}
	c.mu.Unlock()
}

func (c *RouteLLMClient) call(ctx context.Context, text string, pair RouterPair) (RouterVerdict, error) {
	id := "rt_" + rand.Text()
	body, _ := json.Marshal(map[string]any{"requestId": id, "router": c.cfg.Router, "text": text, "threshold": pair.Threshold})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+"/v1/route", bytes.NewReader(body))
	if err != nil {
		return RouterVerdict{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", id)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	start := c.now()
	resp, err := c.http.Do(req)
	if err != nil {
		return RouterVerdict{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RouterVerdict{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 16*1024))
	d.DisallowUnknownFields()
	var v RouterVerdict
	if err := d.Decode(&v); err != nil {
		return RouterVerdict{}, fmt.Errorf("malformed response: %w", err)
	}
	if v.RequestID != id || v.Router != c.cfg.Router || (v.Choice != "strong" && v.Choice != "weak") || v.Score < 0 || v.Score > 1 || v.Threshold != pair.Threshold || v.Checkpoint == "" {
		return RouterVerdict{}, fmt.Errorf("response failed validation")
	}
	// Controller.route semantics: strong iff score >= threshold. A sidecar that
	// disagrees with its own score is not trusted.
	if (v.Score >= pair.Threshold) != (v.Choice == "strong") {
		return RouterVerdict{}, fmt.Errorf("choice inconsistent with score")
	}
	v.LatencyMS = c.now().Sub(start).Milliseconds()
	return v, nil
}

// Health reports the sidecar's /v1/health without routing anything.
func (c *RouteLLMClient) Health(ctx context.Context) (map[string]any, error) {
	if c == nil {
		return nil, ErrRouterUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.URL+"/v1/health", nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
