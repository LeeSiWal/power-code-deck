package routing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Runner executes one official CLI status command. It is injected so tests use
// recorded output and so the application can reuse its PATH resolution.
type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, bin string, args []string, stdin string) (stdout string, err error)
}

// Prober discovers adapter status. It only runs documented status/list
// commands; it never reads OAuth/token files, keyrings, or makes paid calls.
type Prober struct {
	Runner    Runner
	Getenv    func(string) string
	HomeDir   string
	Timeout   time.Duration
	Now       func() time.Time
	Endpoints []LocalEndpoint
	HTTP      *LocalClient

	mu    sync.Mutex
	cache []AdapterStatus
	at    time.Time
}

// Env var names that can move a CLI onto a metered or rerouted billing path.
// Only names are reported, never values.
var billingEnv = map[string][]string{
	AdapterClaude:      {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"},
	AdapterCodex:       {"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL"},
	AdapterAntigravity: {"GEMINI_API_KEY"},
	AdapterGemini:      {"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI"},
}

// Invalidate forces the next Probe to re-run (login/logout, CLI update,
// config change).
// SetEndpoints replaces the probed local endpoints (Settings edits) and drops
// the cached results so the next probe sees them.
func (p *Prober) SetEndpoints(eps []LocalEndpoint) {
	p.mu.Lock()
	p.Endpoints = append([]LocalEndpoint(nil), eps...)
	p.cache = nil
	p.mu.Unlock()
}

func (p *Prober) Invalidate() {
	p.mu.Lock()
	p.cache = nil
	p.mu.Unlock()
}

// Probe returns cached results younger than maxAge.
func (p *Prober) Probe(ctx context.Context, maxAge time.Duration) []AdapterStatus {
	now := p.now()
	p.mu.Lock()
	if p.cache != nil && now.Sub(p.at) < maxAge {
		out := p.cache
		p.mu.Unlock()
		return out
	}
	endpoints := append([]LocalEndpoint(nil), p.Endpoints...)
	p.mu.Unlock()
	var wg sync.WaitGroup
	out := make([]AdapterStatus, 4, 4+len(endpoints))
	for i, f := range []func(context.Context) AdapterStatus{p.claude, p.codex, p.antigravity, p.gemini} {
		wg.Add(1)
		go func(i int, f func(context.Context) AdapterStatus) {
			defer wg.Done()
			out[i] = f(ctx)
		}(i, f)
	}
	wg.Wait()
	for _, e := range endpoints {
		out = append(out, p.local(ctx, e))
	}
	p.mu.Lock()
	p.cache, p.at = out, now
	p.mu.Unlock()
	return out
}

func (p *Prober) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Prober) run(ctx context.Context, bin string, args ...string) (string, error) {
	t := p.Timeout
	if t <= 0 {
		t = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	return p.Runner.Run(ctx, bin, args, "")
}

func (p *Prober) base(id, name string) (AdapterStatus, string, bool) {
	now := p.now()
	s := AdapterStatus{AdapterID: id, Auth: Observation{Value: Unknown}, Entitlement: Observation{Value: Unknown}, Billing: Observation{Value: Unknown}, Health: Observation{Value: Unknown}, Technical: Observation{Value: Supported, Source: "adapter"}}
	for _, k := range billingEnv[id] {
		if p.getenv(k) != "" {
			s.InheritedEnv = append(s.InheritedEnv, k)
		}
	}
	bin, err := p.Runner.LookPath(name)
	if err != nil {
		s.Installation = Observed(NotInstalled, "PATH", name+" not found", now)
		return s, "", false
	}
	s.Executable = bin
	s.Installation = Observed(Installed, "PATH", bin, now)
	return s, bin, true
}

func (p *Prober) getenv(k string) string {
	if p.Getenv != nil {
		return p.Getenv(k)
	}
	return os.Getenv(k)
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+[\w.+-]*`)

func (p *Prober) claude(ctx context.Context) AdapterStatus {
	s, bin, ok := p.base(AdapterClaude, "claude")
	if !ok {
		return s
	}
	if out, err := p.run(ctx, bin, "--version"); err == nil {
		s.Version = versionRe.FindString(out)
	}
	out, err := p.run(ctx, bin, "auth", "status")
	now := p.now()
	if err != nil && strings.TrimSpace(out) == "" {
		s.Auth = Observed(Unknown, "claude auth status", "status command failed", now)
		return s
	}
	var st struct {
		LoggedIn    bool   `json:"loggedIn"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &st) != nil {
		s.Auth = Observed(Unknown, "claude auth status", "unrecognized output", now)
		return s
	}
	s.AuthMethod = claudeAuthMethod(st.AuthMethod, st.APIProvider)
	if !st.LoggedIn {
		s.Auth = Observed(NotAuthenticated, "claude auth status", "loggedIn=false", now)
		return s
	}
	s.Auth = Observed(Authenticated, "claude auth status", "authMethod="+st.AuthMethod+" apiProvider="+st.APIProvider, now)
	switch s.AuthMethod {
	case "subscription":
		// Included usage, except paths the docs say can draw usage credits
		// without a prompt in -p mode; profiles carry that separately.
		s.Billing = Observed(BillingSubscription, "claude auth status", "subscription sign-in; some models may bill usage credits (see extraUsageRisk)", now)
	case "api_key", "cloud":
		s.Billing = Observed(BillingAPIMetered, "claude auth status", st.AuthMethod, now)
	}
	return s
}

// claudeAuthMethod normalizes the CLI's reported method. Unknown spellings stay
// unknown instead of being guessed.
func claudeAuthMethod(method, provider string) string {
	m := strings.ToLower(method)
	if provider != "" && provider != "firstParty" {
		return "cloud"
	}
	switch {
	case m == "" || m == "none":
		return ""
	case strings.Contains(m, "api") && strings.Contains(m, "key"), m == "apikey", strings.Contains(m, "token") && !strings.Contains(m, "oauth"):
		return "api_key"
	case strings.Contains(m, "claude.ai"), strings.Contains(m, "oauth"), strings.Contains(m, "subscription"), strings.Contains(m, "max"), strings.Contains(m, "pro"):
		return "subscription"
	}
	return "unknown:" + method
}

func (p *Prober) codex(ctx context.Context) AdapterStatus {
	s, bin, ok := p.base(AdapterCodex, "codex")
	if !ok {
		return s
	}
	if out, err := p.run(ctx, bin, "--version"); err == nil {
		s.Version = versionRe.FindString(out)
	}
	now := p.now()
	out, _ := p.run(ctx, bin, "login", "status")
	line := strings.ToLower(strings.TrimSpace(out))
	switch {
	case strings.Contains(line, "not logged in"):
		s.Auth = Observed(NotAuthenticated, "codex login status", "not logged in", now)
	case strings.Contains(line, "chatgpt"):
		s.Auth, s.AuthMethod = Observed(Authenticated, "codex login status", "ChatGPT sign-in", now), "subscription"
		s.Billing = Observed(BillingSubscription, "codex login status", "ChatGPT plan; API-key auth would use API pricing", now)
	case strings.Contains(line, "api key"):
		s.Auth, s.AuthMethod = Observed(Authenticated, "codex login status", "API key", now), "api_key"
		s.Billing = Observed(BillingAPIMetered, "codex login status", "API key sign-in uses API pricing", now)
	default:
		s.Auth = Observed(Unknown, "codex login status", "unrecognized output", now)
	}
	// model/list is app-server metadata, not inference.
	if models, err := p.codexModels(ctx, bin); err == nil {
		s.Models = models
	} else {
		s.Technical = Observed(Partial, "codex app-server model/list", err.Error(), now)
	}
	return s
}

// codexModels runs one short app-server session: initialize → model/list.
func (p *Prober) codexModels(ctx context.Context, bin string) ([]ModelInfo, error) {
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"powercodedeck-probe","version":"0"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"model/list","params":{}}` + "\n"
	t := p.Timeout
	if t <= 0 {
		t = 15 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	var out string
	var err error
	if u, ok := p.Runner.(interface {
		RunUntil(context.Context, string, []string, string, func(string) bool) (string, error)
	}); ok {
		out, err = u.RunUntil(cctx, bin, []string{"app-server"}, in, func(s string) bool { return strings.Contains(s, `"id":2`) })
	} else {
		out, err = p.Runner.Run(cctx, bin, []string{"app-server"}, in)
	}
	if out == "" && err != nil {
		return nil, err
	}
	return ParseCodexModelList(out)
}

// ParseCodexModelList reads app-server NDJSON and returns the id=2 result.
// Unknown fields are ignored; a missing result is an error, not an empty list.
func ParseCodexModelList(ndjson string) ([]ModelInfo, error) {
	sc := bufio.NewScanner(strings.NewReader(ndjson))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage           `json:"id"`
			Error  *struct{ Message string } `json:"error"`
			Result *struct {
				Data []struct {
					ID                        string `json:"id"`
					Model                     string `json:"model"`
					DisplayName               string `json:"displayName"`
					Hidden                    bool   `json:"hidden"`
					IsDefault                 bool   `json:"isDefault"`
					DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
					SupportedReasoningEfforts []struct {
						ReasoningEffort string `json:"reasoningEffort"`
					} `json:"supportedReasoningEfforts"`
				} `json:"data"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || string(msg.ID) != "2" {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("model/list: %s", msg.Error.Message)
		}
		if msg.Result == nil {
			return nil, fmt.Errorf("model/list: no result")
		}
		var out []ModelInfo
		for _, m := range msg.Result.Data {
			if m.Hidden {
				continue
			}
			mi := ModelInfo{ID: m.ID, DisplayName: m.DisplayName, DefaultEffort: m.DefaultReasoningEffort, IsDefault: m.IsDefault, Vendor: "openai"}
			if mi.ID == "" {
				mi.ID = m.Model
			}
			for _, e := range m.SupportedReasoningEfforts {
				mi.Efforts = append(mi.Efforts, e.ReasoningEffort)
			}
			out = append(out, mi)
		}
		return out, nil
	}
	return nil, fmt.Errorf("model/list: no response")
}

func (p *Prober) antigravity(ctx context.Context) AdapterStatus {
	s, bin, ok := p.base(AdapterAntigravity, "agy")
	if !ok {
		return s
	}
	now := p.now()
	if out, err := p.run(ctx, bin, "--version"); err == nil {
		s.Version = versionRe.FindString(out)
	}
	// An IDE launcher named agy would not offer headless print/stream-json.
	help, _ := p.run(ctx, bin, "--help")
	if !strings.Contains(help, "--print") || !strings.Contains(help, "--output-format") {
		s.Installation = Observed(WrongBinary, "agy --help", "headless print/stream-json flags not offered by this executable", now)
		return s
	}
	out, err := p.run(ctx, bin, "models")
	if models := ParseAgyModels(out); len(models) > 0 {
		s.Models = models
		// There is no documented auth-status command; a successful model fetch
		// is the evidence used, and it is labeled as such.
		s.Auth = Observed(Authenticated, "agy models", "model list fetched", now)
	} else if err != nil {
		s.Auth = Observed(Unknown, "agy models", "model list unavailable", now)
	}
	// The AI-credit overage setting (Never/Always) cannot be read from the CLI.
	s.Billing = Observed(Unknown, "G5", "AI-credit overage setting is not observable from the CLI", now)
	return s
}

// ParseAgyModels reads `agy models` output: "<id>\t<display name>" lines.
func ParseAgyModels(out string) []ModelInfo {
	var models []ModelInfo
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || id == "" || strings.ContainsAny(id, " :") {
			continue
		}
		vendor := "google"
		switch {
		case strings.HasPrefix(id, "claude-"):
			vendor = "anthropic"
		case strings.HasPrefix(id, "gpt-"):
			vendor = "openai"
		}
		models = append(models, ModelInfo{ID: id, DisplayName: strings.TrimSpace(name), Vendor: vendor})
	}
	return models
}

func (p *Prober) gemini(ctx context.Context) AdapterStatus {
	s, bin, ok := p.base(AdapterGemini, "gemini")
	if !ok {
		return s
	}
	now := p.now()
	if out, err := p.run(ctx, bin, "--version"); err == nil {
		s.Version = versionRe.FindString(out)
	}
	s.Technical = Observed(NotImplemented, "adapter", "Gemini CLI is an optional enterprise/API compatibility path; no execution adapter is connected", now)
	switch {
	case p.getenv("GOOGLE_GENAI_USE_VERTEXAI") != "" || p.getenv("GOOGLE_CLOUD_PROJECT") != "":
		s.AuthMethod = "enterprise"
		s.Auth = Observed(Unknown, "environment", "Vertex/enterprise variables present; sign-in not verified", now)
		s.Billing = Observed(BillingAPIMetered, "environment", "Google Cloud billing", now)
	case p.getenv("GEMINI_API_KEY") != "":
		s.AuthMethod = "api_key"
		s.Auth = Observed(Unknown, "environment", "GEMINI_API_KEY present; not validated", now)
		s.Billing = Observed(BillingAPIMetered, "environment", "Gemini API key billing", now)
	default:
		// Only the selected auth type is read from settings.json — a config
		// value, not a credential.
		switch geminiSelectedAuth(p.HomeDir) {
		case "oauth-personal", "login-with-google":
			s.AuthMethod = "consumer_oauth"
			s.Auth = Observed(ConsumerAuthDiscontinued, "G1/G2", "personal Google sign-in for Gemini CLI ended 2026-06-18; use Antigravity CLI or an enterprise/API path", now)
		default:
			s.Auth = Observed(Unknown, "settings.json", "auth type not determined", now)
		}
	}
	return s
}

func geminiSelectedAuth(home string) string {
	if home == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil || len(b) > 1<<20 {
		return ""
	}
	var v struct {
		SelectedAuthType string `json:"selectedAuthType"`
		Security         struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	if v.Security.Auth.SelectedType != "" {
		return v.Security.Auth.SelectedType
	}
	return v.SelectedAuthType
}

func (p *Prober) local(ctx context.Context, e LocalEndpoint) AdapterStatus {
	now := p.now()
	s := AdapterStatus{AdapterID: AdapterLocal + ":" + e.ID, Executable: e.URL, Auth: Observation{Value: Unknown}, Entitlement: Observation{Value: Unknown},
		Billing: Observed(BillingLocal, "config", "operator-run endpoint "+e.hostLabel(), now), Technical: Observed(Partial, "adapter", "text-only chat; no tools or file edits", now), Health: Observation{Value: Unknown}}
	if p.HTTP == nil {
		s.Installation = Observed(Unknown, "config", "no HTTP client", now)
		return s
	}
	models, err := p.HTTP.Models(ctx, e)
	if err != nil {
		s.Installation = Observed(Unknown, "endpoint", err.Error(), now)
		s.Health = Observed(Degraded, "endpoint", err.Error(), now)
		return s
	}
	s.Installation = Observed(Installed, "endpoint", e.hostLabel(), now)
	s.Health = Observed(Healthy, "endpoint", fmt.Sprintf("%d models listed", len(models)), now)
	s.Models = models
	return s
}
