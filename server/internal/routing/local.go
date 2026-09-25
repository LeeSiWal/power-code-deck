package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"powercodedeck/internal/providers"
)

// LocalEndpoint is an operator-configured model server. Endpoints come only from
// the server-side routing.json, never from a browser request.
type LocalEndpoint struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Kind  string `json:"kind"` // ollama | openai (OpenAI-compatible /v1)
	Model string `json:"model,omitempty"`
	// AllowPrivate permits RFC1918/ULA addresses (a LAN GPU box). Loopback is
	// always allowed; link-local and cloud metadata addresses never are.
	AllowPrivate bool `json:"allowPrivate,omitempty"`
	// AllowInsecureHTTP permits plain HTTP to a non-loopback host.
	AllowInsecureHTTP bool   `json:"allowInsecureHttp,omitempty"`
	TokenEnv          string `json:"tokenEnv,omitempty"`
	TimeoutSeconds    int    `json:"timeoutSeconds,omitempty"`
	// LocalNetOnly (set on endpoints added from Settings) refuses anything but
	// this machine, private networks and Tailscale — checked at dial time, after
	// DNS — so a URL typed in the browser can never send code to the internet.
	LocalNetOnly bool `json:"localNetOnly,omitempty"`
}

func (e LocalEndpoint) Validate() error {
	if !idPattern.MatchString(e.ID) {
		return fmt.Errorf("local endpoint: invalid id %q", e.ID)
	}
	if e.Kind != "ollama" && e.Kind != "openai" {
		return fmt.Errorf("local endpoint %s: kind must be ollama or openai", e.ID)
	}
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("local endpoint %s: invalid url", e.ID)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("local endpoint %s: scheme must be http or https", e.ID)
	}
	if e.LocalNetOnly {
		if ip := net.ParseIP(u.Hostname()); ip != nil && !IsLocalNetIP(ip) {
			return fmt.Errorf("local endpoint %s: only this machine, private networks and Tailscale are allowed", e.ID)
		}
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) && !e.AllowInsecureHTTP {
		return fmt.Errorf("local endpoint %s: plain http to a remote host needs allowInsecureHttp", e.ID)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if err := checkIP(ip, e.AllowPrivate); err != nil {
			return fmt.Errorf("local endpoint %s: %w", e.ID, err)
		}
	}
	return nil
}

func (e LocalEndpoint) hostLabel() string {
	u, err := url.Parse(e.URL)
	if err != nil {
		return "?"
	}
	return u.Host
}

var metadataIPs = []net.IP{net.ParseIP("169.254.169.254"), net.ParseIP("fd00:ec2::254"), net.ParseIP("100.100.100.200")}

// tailscaleNet is Tailscale's address range (RFC 6598 shared space).
var tailscaleNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// IsLocalNetIP reports loopback, private (RFC 1918 / ULA, which includes
// Tailscale's IPv6 fd7a:115c:a1e0::/48) and Tailscale IPv4 addresses.
func IsLocalNetIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || tailscaleNet.Contains(ip)
}

func checkIP(ip net.IP, allowPrivate bool) error {
	for _, m := range metadataIPs {
		if ip.Equal(m) {
			return errors.New("cloud metadata address refused")
		}
	}
	switch {
	case ip.IsLoopback():
		return nil
	case ip.IsUnspecified(), ip.IsMulticast(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast():
		return errors.New("link-local/unspecified/multicast address refused")
	case ip.IsPrivate():
		if !allowPrivate {
			return errors.New("private address requires allowPrivate")
		}
	}
	return nil
}

// LocalClient enforces the address policy at dial time (after DNS), so a
// hostname that later resolves to a metadata address is still refused.
type LocalClient struct {
	mu      sync.Mutex
	clients map[string]*http.Client
	Getenv  func(string) string
}

func NewLocalClient() *LocalClient {
	return &LocalClient{clients: map[string]*http.Client{}, Getenv: os.Getenv}
}

func (c *LocalClient) client(e LocalEndpoint) *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	// The key includes everything the dialer enforces, so an endpoint edited in
	// Settings never keeps a client built for its old address policy.
	key := fmt.Sprintf("%s|%v|%v|%d", e.ID, e.AllowPrivate, e.LocalNetOnly, e.TimeoutSeconds)
	if h, ok := c.clients[key]; ok {
		return h
	}
	allow, localOnly := e.AllowPrivate, e.LocalNetOnly
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return errors.New("unresolved address refused")
		}
		if err := checkIP(ip, allow); err != nil {
			return err
		}
		if localOnly && !IsLocalNetIP(ip) {
			return errors.New("only this machine, private networks and Tailscale are allowed")
		}
		return nil
	}}
	timeout := time.Duration(e.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 10*time.Minute {
		timeout = 120 * time.Second
	}
	h := &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{DialContext: dialer.DialContext, Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 90 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	c.clients[key] = h
	return h
}

func (c *LocalClient) do(ctx context.Context, e LocalEndpoint, method, path string, body any, out any) error {
	if err := e.Validate(); err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(e.URL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.TokenEnv != "" && c.Getenv != nil {
		if t := c.Getenv(e.TokenEnv); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	resp, err := c.client(e).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("endpoint returned %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// Stream sends one request to the endpoint through the same guarded client (the
// address checks run at dial time; redirects are not followed) and returns the
// response for the caller to read and close — the OSS bridge relays streams.
func (c *LocalClient) Stream(ctx context.Context, e LocalEndpoint, method, path string, body []byte) (*http.Response, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(e.URL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.TokenEnv != "" && c.Getenv != nil {
		if t := c.Getenv(e.TokenEnv); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	return c.client(e).Do(req)
}

func (c *LocalClient) Models(ctx context.Context, e LocalEndpoint) ([]ModelInfo, error) {
	var out []ModelInfo
	if e.Kind == "ollama" {
		var r struct {
			Models []struct{ Name string } `json:"models"`
		}
		if err := c.do(ctx, e, http.MethodGet, "/api/tags", nil, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			out = append(out, ModelInfo{ID: m.Name, Vendor: "local"})
		}
		return out, nil
	}
	var r struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := c.do(ctx, e, http.MethodGet, "/v1/models", nil, &r); err != nil {
		return nil, err
	}
	for _, m := range r.Data {
		out = append(out, ModelInfo{ID: m.ID, Vendor: "local"})
	}
	return out, nil
}

// LocalExecution is a single-turn, text-only providers.Execution. It has no
// tools and cannot edit files; routing only offers it for KindText work.
type LocalExecution struct {
	id     providers.Identity
	client *LocalClient
	ep     LocalEndpoint
	model  string
	mu     sync.Mutex
	events chan providers.Event
	cancel context.CancelFunc
	sent   bool
}

func NewLocalExecution(executionID string, c *LocalClient, e LocalEndpoint, model string) (*LocalExecution, error) {
	if executionID == "" || c == nil {
		return nil, errors.New("local execution: id and client required")
	}
	if model == "" {
		model = e.Model
	}
	if model == "" {
		return nil, errors.New("local execution: model required")
	}
	return &LocalExecution{id: providers.Identity{ExecutionID: executionID, Provider: providers.ID(AdapterLocal)}, client: c, ep: e, model: model, events: make(chan providers.Event, 4)}, nil
}

func (l *LocalExecution) Identity() providers.Identity { return l.id }
func (l *LocalExecution) Capabilities() providers.Capabilities {
	return providers.Capabilities{MultiTurn: false, ApprovalHandling: providers.CLISettings}
}
func (l *LocalExecution) Start() error           { return nil }
func (l *LocalExecution) ConversationID() string { return "" }
func (l *LocalExecution) SetPermissionMode(string) error {
	return errors.New("local text execution has no tools")
}
func (l *LocalExecution) Interrupt() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
	return nil
}
func (l *LocalExecution) Stop() { _ = l.Interrupt() }

func (l *LocalExecution) Send(text string) error {
	l.mu.Lock()
	if l.sent {
		l.mu.Unlock()
		return errors.New("local execution is single-turn")
	}
	l.sent = true
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.mu.Unlock()
	go func() {
		defer close(l.events)
		defer cancel()
		answer, usage, model, err := l.complete(ctx, text)
		out := &providers.Outcome{Status: providers.CompletionSuccess, Text: answer, Usage: usage}
		if err != nil {
			out = &providers.Outcome{Status: providers.CompletionFailed, IsError: true, Reason: "local_error", Diagnostics: Summarize(err.Error(), 2000)}
			if ctx.Err() != nil {
				out.Status = providers.CompletionInterrupted
			}
		} else {
			l.events <- providers.Event{Identity: l.id, Sequence: 1, Kind: providers.Message, Role: "assistant", Model: model, Blocks: []providers.Block{{Kind: providers.Text, Text: answer}}}
		}
		l.events <- providers.Event{Identity: l.id, Sequence: 2, Kind: providers.TurnFinished, Model: model, Outcome: out}
	}()
	return nil
}

func (l *LocalExecution) Next(ctx context.Context) (providers.Event, error) {
	select {
	case ev, ok := <-l.events:
		if !ok {
			return providers.Event{}, io.EOF
		}
		return ev, nil
	case <-ctx.Done():
		return providers.Event{}, ctx.Err()
	}
}

func (l *LocalExecution) complete(ctx context.Context, text string) (string, *providers.Usage, string, error) {
	msgs := []map[string]string{{"role": "user", "content": text}}
	if l.ep.Kind == "ollama" {
		var r struct {
			Model   string `json:"model"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			PromptEvalCount *int `json:"prompt_eval_count"`
			EvalCount       *int `json:"eval_count"`
		}
		if err := l.client.do(ctx, l.ep, http.MethodPost, "/api/chat", map[string]any{"model": l.model, "messages": msgs, "stream": false}, &r); err != nil {
			return "", nil, "", err
		}
		// Missing counters mean "not reported", which is not the same as zero.
		var u *providers.Usage
		if r.PromptEvalCount != nil && r.EvalCount != nil {
			u = &providers.Usage{Scope: providers.TurnUsage, InputTokens: *r.PromptEvalCount, OutputTokens: *r.EvalCount}
		}
		return r.Message.Content, u, r.Model, nil
	}
	var r struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := l.client.do(ctx, l.ep, http.MethodPost, "/v1/chat/completions", map[string]any{"model": l.model, "messages": msgs}, &r); err != nil {
		return "", nil, "", err
	}
	if len(r.Choices) == 0 {
		return "", nil, "", errors.New("no choices returned")
	}
	var u *providers.Usage
	if r.Usage != nil {
		u = &providers.Usage{Scope: providers.TurnUsage, InputTokens: r.Usage.PromptTokens, OutputTokens: r.Usage.CompletionTokens}
	}
	return r.Choices[0].Message.Content, u, r.Model, nil
}
