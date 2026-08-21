package services

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ModeCloudOnly            = "CLOUD_ONLY"
	ModeLocalPreprocessCloud = "LOCAL_PREPROCESS_CLOUD"
	ModeLocalOnly            = "LOCAL_ONLY"

	defaultProviderTimeoutMS = 180000
	defaultHybridTimeout     = 90 * time.Second
	ollamaContextTokens      = 65536
	ollamaKeepAlive          = "30m"
	healthProbeTokens        = 32
	contextPackTokens        = 1000

	ErrProviderUnreachable = "LOCAL_PROVIDER_UNREACHABLE"
	ErrModelUnavailable    = "LOCAL_MODEL_UNAVAILABLE"
	ErrLocalTimeout        = "LOCAL_TIMEOUT"
	ErrRequestCanceled     = "LOCAL_REQUEST_CANCELED"
	ErrGenerationFailed    = "LOCAL_GENERATION_FAILED"
	ErrContextBuild        = "CONTEXT_BUILD_FAILED"
	ErrCloudExecution      = "CLOUD_EXECUTION_FAILED"
	ErrNativeSession       = "NATIVE_SESSION_NOT_READY"
	ErrValidation          = "VALIDATION_FAILED"
)

// LocalProvider is intentionally small. The POC implements Ollama; the type
// field keeps the stored shape extensible without inventing a provider framework.
type LocalProvider struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	TimeoutMS int    `json:"timeoutMs"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type ProviderHealth struct {
	Provider       string `json:"provider"`
	Reachable      bool   `json:"reachable"`
	APIHealthy     bool   `json:"apiHealthy"`
	ModelAvailable bool   `json:"modelAvailable"`
	GenerationTest bool   `json:"generationTest"`
	LatencyMS      int64  `json:"latencyMs"`
	ErrorCode      string `json:"errorCode,omitempty"`
	Error          string `json:"error,omitempty"`
}

type ProviderRegistry struct {
	db         *sql.DB
	httpClient func(time.Duration) *http.Client
	dial       func(context.Context, string, string) (net.Conn, error)
}

func NewProviderRegistry(db *sql.DB) *ProviderRegistry {
	return &ProviderRegistry{
		db:         db,
		httpClient: func(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} },
		dial:       (&net.Dialer{}).DialContext,
	}
}

func validateProvider(p LocalProvider) (LocalProvider, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.Model = strings.TrimSpace(p.Model)
	if p.Name == "" || p.BaseURL == "" || p.Model == "" {
		return p, fmt.Errorf("name, baseUrl, and model are required")
	}
	if p.Type == "" {
		p.Type = "ollama"
	}
	if p.Type != "ollama" {
		return p, fmt.Errorf("provider type %q is not implemented in this POC", p.Type)
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return p, fmt.Errorf("baseUrl must be an absolute http(s) URL")
	}
	if u.User != nil {
		return p, fmt.Errorf("credentials in baseUrl are not allowed")
	}
	if p.TimeoutMS == 0 {
		p.TimeoutMS = defaultProviderTimeoutMS
	}
	if p.TimeoutMS < 100 || p.TimeoutMS > 300000 {
		return p, fmt.Errorf("timeoutMs must be between 100 and 300000")
	}
	return p, nil
}

func (r *ProviderRegistry) Upsert(p LocalProvider) (LocalProvider, error) {
	p, err := validateProvider(p)
	if err != nil {
		return p, err
	}
	_, err = r.db.Exec(`INSERT INTO local_ai_providers(name,type,base_url,model,timeout_ms,enabled,updated_at)
		VALUES(?,?,?,?,?,?,datetime('now')) ON CONFLICT(name) DO UPDATE SET
		type=excluded.type,base_url=excluded.base_url,model=excluded.model,
		timeout_ms=excluded.timeout_ms,enabled=excluded.enabled,updated_at=datetime('now')`,
		p.Name, p.Type, p.BaseURL, p.Model, p.TimeoutMS, p.Enabled)
	if err != nil {
		return p, err
	}
	return r.Get(p.Name)
}

func (r *ProviderRegistry) Get(name string) (LocalProvider, error) {
	var p LocalProvider
	err := r.db.QueryRow(`SELECT name,type,base_url,model,timeout_ms,enabled,updated_at
		FROM local_ai_providers WHERE name=?`, name).Scan(
		&p.Name, &p.Type, &p.BaseURL, &p.Model, &p.TimeoutMS, &p.Enabled, &p.UpdatedAt)
	return p, err
}

func (r *ProviderRegistry) List() ([]LocalProvider, error) {
	rows, err := r.db.Query(`SELECT name,type,base_url,model,timeout_ms,enabled,updated_at
		FROM local_ai_providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocalProvider{}
	for rows.Next() {
		var p LocalProvider
		if err := rows.Scan(&p.Name, &p.Type, &p.BaseURL, &p.Model, &p.TimeoutMS, &p.Enabled, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *ProviderRegistry) Delete(name string) error {
	res, err := r.db.Exec("DELETE FROM local_ai_providers WHERE name=?", name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func providerTimeout(p LocalProvider) time.Duration {
	return time.Duration(p.TimeoutMS) * time.Millisecond
}

func (r *ProviderRegistry) Health(ctx context.Context, name string) (h ProviderHealth) {
	p, err := r.Get(name)
	h = ProviderHealth{Provider: name}
	if err != nil {
		h.ErrorCode, h.Error = ErrProviderUnreachable, "provider not found"
		return h
	}
	if !p.Enabled {
		h.ErrorCode, h.Error = ErrProviderUnreachable, "provider is disabled"
		return h
	}
	started := time.Now()
	defer func() { h.LatencyMS = time.Since(started).Milliseconds() }()
	u, _ := url.Parse(p.BaseURL)
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	dialCtx, cancel := context.WithTimeout(ctx, providerTimeout(p))
	conn, err := r.dial(dialCtx, "tcp", net.JoinHostPort(u.Hostname(), port))
	cancel()
	if err != nil {
		h.ErrorCode, h.Error = classifyLocalError(err), conciseError(err)
		return h
	}
	h.Reachable = true
	_ = conn.Close()

	models, err := r.ollamaModels(ctx, p)
	if err != nil {
		h.ErrorCode, h.Error = classifyLocalError(err), conciseError(err)
		return h
	}
	h.APIHealthy = true
	for _, model := range models {
		if sameOllamaModel(model, p.Model) {
			h.ModelAvailable = true
			break
		}
	}
	if !h.ModelAvailable {
		h.ErrorCode, h.Error = ErrModelUnavailable, "configured model is not installed"
		return h
	}
	if _, _, err := r.ollamaGenerate(ctx, p, "Reply with OK only.", healthProbeTokens); err != nil {
		h.ErrorCode, h.Error = classifyLocalError(err), conciseError(err)
		return h
	}
	h.GenerationTest = true
	return h
}

func sameOllamaModel(a, b string) bool {
	if a == b {
		return true
	}
	return strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest")
}

func (r *ProviderRegistry) ollamaModels(ctx context.Context, p LocalProvider) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/api/tags", nil)
	resp, err := r.httpClient(providerTimeout(p)).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider API returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid provider API response: %w", err)
	}
	out := make([]string, 0, len(body.Models)*2)
	for _, m := range body.Models {
		out = append(out, m.Name, m.Model)
	}
	return out, nil
}

type ollamaGenerateResponse struct {
	Response        string `json:"response"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

func (r *ProviderRegistry) ollamaGenerate(ctx context.Context, p LocalProvider, prompt string, maxTokens int) (string, int, error) {
	body, _ := json.Marshal(map[string]any{
		"model": p.Model, "prompt": prompt, "stream": false,
		"keep_alive": ollamaKeepAlive,
		"options": map[string]any{
			"num_ctx": ollamaContextTokens, "num_predict": maxTokens, "temperature": 0,
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/api/generate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient(providerTimeout(p)).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var out ollamaGenerateResponse
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out)
	if resp.StatusCode == http.StatusNotFound || strings.Contains(strings.ToLower(out.Error), "model") {
		return "", 0, fmt.Errorf("%s: configured model is unavailable", ErrModelUnavailable)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("provider generation returned HTTP %d", resp.StatusCode)
	}
	if decodeErr != nil {
		return "", 0, fmt.Errorf("invalid generation response: %w", decodeErr)
	}
	if out.Error != "" {
		return "", 0, fmt.Errorf("provider generation failed")
	}
	if strings.TrimSpace(out.Response) == "" {
		return "", out.PromptEvalCount + out.EvalCount, fmt.Errorf("provider returned an empty generation")
	}
	return strings.TrimSpace(out.Response), out.PromptEvalCount + out.EvalCount, nil
}

func classifyLocalError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return ErrLocalTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrRequestCanceled
	}
	if strings.Contains(err.Error(), ErrModelUnavailable) {
		return ErrModelUnavailable
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ErrLocalTimeout
		}
		return ErrProviderUnreachable
	}
	return ErrGenerationFailed
}

func conciseError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

type CandidateContext struct {
	Text            string   `json:"-"`
	Files           []string `json:"files"`
	EstimatedTokens int      `json:"estimatedTokens"`
	Bytes           int      `json:"bytes"`
	Source          string   `json:"source"`
}

const (
	maxCandidateFiles  = 24
	maxFileBytes       = 12 * 1024
	maxFilesystemFiles = 5000
	maxFilesystemDepth = 12
	// Hybrid preprocessing is a latency-sensitive request, not a full repository
	// upload. Keep the evidence near 16k estimated tokens so a 30B local model can
	// finish before common reverse-proxy request deadlines and still leave ample
	// room for instructions and the generated context pack.
	maxContextBytes = 64 * 1024
)

// EstimateTokens is explicitly an estimate: Unicode code points / 4, rounded
// up. It is provider-independent and deterministic, not a claim about a model's
// tokenizer. Both sides of a reduction use the same algorithm.
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

func BuildCandidateContext(ctx context.Context, cwd, task string) (CandidateContext, error) {
	cwd, err := ResolveWorkingDir(cwd)
	if err != nil {
		return CandidateContext{}, err
	}
	status, _ := repoCommand(ctx, cwd, "status", "--short", "--untracked-files=all")
	diffStat, _ := repoCommand(ctx, cwd, "diff", "--stat", "--")
	logSummary, _ := repoCommand(ctx, cwd, "log", "-8", "--oneline", "--decorate=no")
	tracked, err := repoCommand(ctx, cwd, "ls-files")
	source := "git"
	if err != nil {
		paths, walkErr := filesystemProjectFiles(ctx, cwd)
		if walkErr != nil {
			return CandidateContext{}, fmt.Errorf("filesystem scan: %w", walkErr)
		}
		tracked = strings.Join(paths, "\n")
		status, diffStat, logSummary = "", "", ""
		source = "filesystem"
	}
	recent := ""
	if source == "git" {
		recent, _ = repoCommand(ctx, cwd, "log", "-8", "--name-only", "--pretty=format:")
	}

	changed := parseStatusPaths(status)
	keywords := taskKeywords(task)
	type scoredPath struct {
		path  string
		score int
	}
	scores := map[string]int{}
	for _, p := range changed {
		scores[p] += 100
	}
	for _, p := range nonEmptyLines(recent) {
		scores[p] += 20
	}
	for _, p := range nonEmptyLines(tracked) {
		low := strings.ToLower(p)
		for _, k := range keywords {
			if strings.Contains(low, k) {
				scores[p] += 10
			}
		}
		if _, ok := scores[p]; !ok && len(scores) < maxCandidateFiles {
			scores[p] = 1
		}
	}
	paths := make([]scoredPath, 0, len(scores))
	for p, score := range scores {
		if safeRepoRelativePath(cwd, p) {
			paths = append(paths, scoredPath{p, score})
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].score == paths[j].score {
			return paths[i].path < paths[j].path
		}
		return paths[i].score > paths[j].score
	})
	if len(paths) > maxCandidateFiles {
		paths = paths[:maxCandidateFiles]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "REPOSITORY: %s\nCONTEXT SOURCE: %s\nTASK TERMS: %s\n",
		filepath.Base(cwd), strings.ToUpper(source), strings.Join(keywords, ", "))
	if source == "git" {
		fmt.Fprintf(&b, "\nGIT STATUS\n%s\n\nDIFF STAT\n%s\n\nRECENT COMMITS\n%s\n", status, diffStat, logSummary)
	} else {
		b.WriteString("\nGit metadata unavailable; using a bounded filesystem scan.\n")
	}
	files := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if b.Len() >= maxContextBytes {
			break
		}
		file, err := os.Open(filepath.Join(cwd, filepath.FromSlash(candidate.path)))
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxFileBytes))
		_ = file.Close()
		if readErr != nil || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		header := "\nFILE: " + candidate.path + "\n"
		remaining := maxContextBytes - b.Len() - len(header) - 1
		if remaining <= 0 {
			break
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.WriteString(header)
		b.Write(data)
		b.WriteByte('\n')
		files = append(files, candidate.path)
	}
	text := b.String()
	return CandidateContext{Text: text, Files: files, EstimatedTokens: EstimateTokens(text), Bytes: len(text), Source: source}, nil
}

var intelligenceSkipDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "out": true, "coverage": true,
	".next": true, ".cache": true, ".output": true, ".turbo": true, ".git": true,
	"vendor": true, "target": true, "__pycache__": true, ".venv": true, "venv": true,
}

var intelligenceSkipFiles = map[string]bool{
	"package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"credentials.json": true, "service-account.json": true,
}

// filesystemProjectFiles is the non-Git fallback. It deliberately does not follow
// symlinks and excludes dependency/build trees, hidden paths, lockfiles, and common
// credential formats so an untracked project cannot accidentally send local secrets
// or generated noise to the configured inference provider.
func filesystemProjectFiles(ctx context.Context, cwd string) ([]string, error) {
	rootDepth := strings.Count(filepath.Clean(cwd), string(filepath.Separator))
	paths := make([]string, 0, maxCandidateFiles)
	err := filepath.WalkDir(cwd, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == cwd {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if intelligenceSkipDirs[name] || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxFilesystemDepth {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(name, ".") || unsafeIntelligenceFile(name) {
			return nil
		}
		rel, err := filepath.Rel(cwd, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		if len(paths) >= maxFilesystemFiles {
			return fs.SkipAll
		}
		return nil
	})
	return paths, err
}

func unsafeIntelligenceFile(name string) bool {
	lower := strings.ToLower(name)
	if intelligenceSkipFiles[lower] {
		return true
	}
	switch strings.ToLower(filepath.Ext(lower)) {
	case ".pem", ".key", ".p12", ".pfx", ".keystore", ".jks":
		return true
	default:
		return false
	}
}

func repoCommand(ctx context.Context, cwd string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", cwd}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\r\n"), err
}

func nonEmptyLines(s string) []string {
	out := []string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseStatusPaths(status string) []string {
	out := []string{}
	sc := bufio.NewScanner(strings.NewReader(status))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 3 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if _, after, ok := strings.Cut(p, " -> "); ok {
			p = after
		}
		p = strings.Trim(p, `"`)
		out = append(out, p)
	}
	return out
}

func taskKeywords(task string) []string {
	seen := map[string]bool{}
	out := []string{}
	fields := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')
	})
	for _, f := range fields {
		if utf8.RuneCountInString(f) < 3 || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func safeRepoRelativePath(cwd, p string) bool {
	if p == "" || filepath.IsAbs(p) {
		return false
	}
	full := filepath.Clean(filepath.Join(cwd, filepath.FromSlash(p)))
	rel, err := filepath.Rel(cwd, full)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type TraceEvent struct {
	At      string         `json:"at"`
	Stage   string         `json:"stage"`
	Status  string         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
}

type IntelligenceTrace struct {
	ID              string       `json:"id"`
	AgentID         string       `json:"agentId,omitempty"`
	Mode            string       `json:"mode"`
	Status          string       `json:"status"`
	Provider        string       `json:"provider,omitempty"`
	Model           string       `json:"model,omitempty"`
	RawTokens       int          `json:"rawEstimatedTokens"`
	OptimizedTokens int          `json:"optimizedEstimatedTokens"`
	LocalTokens     int          `json:"localTokens"`
	LatencyMS       int64        `json:"latencyMs"`
	Reduction       float64      `json:"reductionPercent"`
	ErrorCode       string       `json:"errorCode,omitempty"`
	Fallback        bool         `json:"fallback"`
	Events          []TraceEvent `json:"events"`
	CreatedAt       string       `json:"createdAt"`

	// What the CLOUD actually spent on the dispatched turn. This is the only
	// honest basis for a savings comparison: RawTokens is the candidate context
	// PowerCodeDeck assembled, which CLOUD_ONLY never sends, so RawTokens−
	// OptimizedTokens measures local compression, not saving.
	//
	// CloudUsageKnown is false when the driver reported no usage at all (Codex —
	// see codex_driver.go). The zeros below then mean "not measured", never
	// "measured as zero", and the UI must not present them as a number.
	CloudCostUSD         float64 `json:"cloudCostUsd"`
	CloudInputTokens     int     `json:"cloudInputTokens"`
	CloudOutputTokens    int     `json:"cloudOutputTokens"`
	CloudCacheReadTokens int     `json:"cloudCacheReadTokens"`
	CloudUsageKnown      bool    `json:"cloudUsageKnown"`
}

type IntelligenceRunRequest struct {
	AgentID   string `json:"agentId"`
	Task      string `json:"task"`
	Mode      string `json:"mode"`
	Provider  string `json:"provider"`
	Operation string `json:"operation,omitempty"`
}

type IntelligenceRunResult struct {
	Trace       IntelligenceTrace `json:"trace"`
	ContextPack string            `json:"contextPack,omitempty"`
	Files       []string          `json:"files,omitempty"`
	Dispatched  bool              `json:"cloudDispatched"`
}

type IntelligenceService struct {
	db            *sql.DB
	providers     *ProviderRegistry
	agents        *AgentService
	native        *NativeService
	hybridTimeout time.Duration
	mu            sync.Mutex
	pending       map[string]string // agent id -> trace id awaiting a native result event
}

func NewIntelligenceService(db *sql.DB, providers *ProviderRegistry, agents *AgentService, native *NativeService) *IntelligenceService {
	s := &IntelligenceService{
		db: db, providers: providers, agents: agents, native: native,
		hybridTimeout: defaultHybridTimeout, pending: make(map[string]string),
	}
	if native != nil {
		native.AddEventObserver(s.observeNativeEvent)
	}
	return s
}

func newTrace(req IntelligenceRunRequest) IntelligenceTrace {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return IntelligenceTrace{ID: "PCD-" + strings.ToUpper(hex.EncodeToString(b)), AgentID: req.AgentID, Mode: req.Mode, Status: "STARTED", CreatedAt: now}
}

func addTrace(t *IntelligenceTrace, stage, status string, details map[string]any) {
	t.Events = append(t.Events, TraceEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Stage: stage, Status: status, Details: details})
}

func (s *IntelligenceService) Run(ctx context.Context, req IntelligenceRunRequest) (IntelligenceRunResult, error) {
	req.Task = strings.TrimSpace(req.Task)
	if req.Mode == "" {
		req.Mode = ModeCloudOnly
	}
	result := IntelligenceRunResult{Trace: newTrace(req)}
	t := &result.Trace
	addTrace(t, "task_received", "OK", map[string]any{"mode": req.Mode})
	if req.Task == "" || req.AgentID == "" {
		t.Status, t.ErrorCode = "FAILED", ErrValidation
		addTrace(t, "validation", "FAILED", map[string]any{"reason": "agentId and task are required"})
		s.saveTrace(*t)
		return result, fmt.Errorf("agentId and task are required")
	}
	if req.Mode != ModeCloudOnly && req.Mode != ModeLocalPreprocessCloud && req.Mode != ModeLocalOnly {
		t.Status, t.ErrorCode = "FAILED", ErrValidation
		s.saveTrace(*t)
		return result, fmt.Errorf("unsupported execution mode")
	}
	agent, err := s.agents.Get(req.AgentID)
	if err != nil {
		t.Status, t.ErrorCode = "FAILED", ErrValidation
		s.saveTrace(*t)
		return result, fmt.Errorf("agent not found")
	}
	if req.Mode == ModeCloudOnly {
		return s.dispatchCloud(result, req.Task, req.Task, false)
	}
	if req.Mode == ModeLocalPreprocessCloud && (s.native == nil || !s.native.Running(req.AgentID)) {
		t.Status, t.ErrorCode = "FAILED", ErrNativeSession
		addTrace(t, "cloud_execution", "FAILED", map[string]any{
			"errorCode": ErrNativeSession, "reason": "native session is not ready",
		})
		s.saveTrace(*t)
		return result, fmt.Errorf("native session is not ready")
	}
	if req.Mode == ModeLocalOnly && !localOnlyAllowed(req.Operation) {
		t.Status, t.ErrorCode = "FAILED", ErrValidation
		addTrace(t, "validation", "FAILED", map[string]any{"reason": "LOCAL_ONLY operation is not allow-listed"})
		s.saveTrace(*t)
		return result, fmt.Errorf("LOCAL_ONLY is limited to summarize, explain, classify, log_analysis, or repository_question")
	}

	addTrace(t, "repository_scan", "STARTED", nil)
	candidate, err := BuildCandidateContext(ctx, agent.WorkingDir, req.Task)
	if err != nil {
		return s.localFailure(result, req, ErrContextBuild, err)
	}
	t.RawTokens = candidate.EstimatedTokens
	result.Files = candidate.Files
	addTrace(t, "repository_scan", "OK", map[string]any{
		"candidateFiles": len(candidate.Files), "rawEstimatedTokens": t.RawTokens,
		"contextBytes": candidate.Bytes, "source": candidate.Source,
	})

	p, err := s.providers.Get(req.Provider)
	if err != nil || !p.Enabled {
		return s.localFailure(result, req, ErrProviderUnreachable, fmt.Errorf("provider is missing or disabled"))
	}
	t.Provider, t.Model = p.Name, p.Model
	requestTimeout := providerTimeout(p)
	if req.Mode == ModeLocalPreprocessCloud {
		hybridTimeout := s.hybridTimeout
		if hybridTimeout <= 0 {
			hybridTimeout = defaultHybridTimeout
		}
		if requestTimeout > hybridTimeout {
			requestTimeout = hybridTimeout
		}
	}
	generationCtx, cancelGeneration := context.WithTimeout(ctx, requestTimeout)
	defer cancelGeneration()
	addTrace(t, "local_request", "STARTED", map[string]any{
		"provider": p.Name, "model": p.Model, "timeoutMs": requestTimeout.Milliseconds(),
	})
	prompt := contextPackPrompt(req.Task, candidate.Text)
	started := time.Now()
	pack, localTokens, err := s.providers.ollamaGenerate(generationCtx, p, prompt, contextPackTokens)
	t.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		return s.localFailure(result, req, classifyLocalError(err), err)
	}
	t.LocalTokens = localTokens
	addTrace(t, "local_response", "OK", map[string]any{"latencyMs": t.LatencyMS, "localTokens": localTokens})
	if !validContextPack(pack) {
		return s.localFailure(result, req, ErrValidation, fmt.Errorf("local response did not contain the required context-pack sections"))
	}
	t.OptimizedTokens = EstimateTokens(pack)
	if t.RawTokens <= 0 || t.OptimizedTokens >= t.RawTokens {
		return s.localFailure(result, req, ErrValidation, fmt.Errorf("context pack did not reduce estimated context"))
	}
	t.Reduction = float64(t.RawTokens-t.OptimizedTokens) * 100 / float64(t.RawTokens)
	result.ContextPack = pack
	addTrace(t, "context_measurement", "OK", map[string]any{
		"rawEstimatedTokens": t.RawTokens, "optimizedEstimatedTokens": t.OptimizedTokens,
		"reductionPercent": t.Reduction, "algorithm": "unicode_codepoints_divided_by_4_ceiling",
	})
	if req.Mode == ModeLocalOnly {
		t.Status = "SUCCESS"
		addTrace(t, "result", "SUCCESS", map[string]any{"cloudExecution": false})
		s.saveTrace(*t)
		return result, nil
	}
	cloudPrompt := "PowerCodeDeck generated the following LOCAL context pack. Treat it as advisory, verify it against the repository, and inspect additional files whenever needed.\n\n" + pack + "\n\nUSER TASK\n" + req.Task
	return s.dispatchCloud(result, cloudPrompt, req.Task, false)
}

func localOnlyAllowed(op string) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "summarize", "explain", "classify", "log_analysis", "repository_question":
		return true
	default:
		return false
	}
}

func contextPackPrompt(task, raw string) string {
	return `Create a compact repository context pack for a cloud coding agent.
Use only the supplied evidence. Prefer paths, symbols, call flow, change points, and tests over prose.
Return exactly these headings: TASK, FILES, SYMBOLS, CALL FLOW, LIKELY CHANGE POINTS, TESTS, UNCERTAINTIES.
Do not claim the cloud agent is restricted to these files. Keep the result much shorter than the evidence.

TASK
` + task + `

REPOSITORY EVIDENCE
` + raw
}

func validContextPack(pack string) bool {
	upper := strings.ToUpper(pack)
	for _, heading := range []string{"TASK", "FILES", "SYMBOLS", "CALL FLOW", "LIKELY CHANGE POINTS", "TESTS", "UNCERTAINTIES"} {
		if !strings.Contains(upper, heading) {
			return false
		}
	}
	return true
}

func (s *IntelligenceService) localFailure(result IntelligenceRunResult, req IntelligenceRunRequest, code string, err error) (IntelligenceRunResult, error) {
	t := &result.Trace
	t.ErrorCode = code
	addTrace(t, "local_processing", "FAILED", map[string]any{"errorCode": code, "reason": conciseError(err)})
	if req.Mode == ModeLocalPreprocessCloud {
		t.Fallback = true
		addTrace(t, "fallback", "CLOUD_ONLY", map[string]any{"reason": code})
		return s.dispatchCloud(result, req.Task, req.Task, true)
	}
	t.Status = "FAILED"
	s.saveTrace(*t)
	return result, err
}

func (s *IntelligenceService) dispatchCloud(result IntelligenceRunResult, prompt, displayTask string, fallback bool) (IntelligenceRunResult, error) {
	t := &result.Trace
	addTrace(t, "cloud_execution", "STARTED", map[string]any{"driver": "codex_or_claude_native"})
	if s.native == nil {
		t.Status, t.ErrorCode = "FAILED", ErrCloudExecution
		addTrace(t, "cloud_execution", "FAILED", map[string]any{
			"errorCode": ErrCloudExecution, "reason": "native service unavailable",
		})
		s.saveTrace(*t)
		return result, fmt.Errorf("native service unavailable")
	}
	// Persist and register before Send: a very fast driver can emit its result
	// synchronously enough to race code that registers only after Send returns.
	t.Status = "CLOUD_DISPATCHING"
	s.saveTrace(*t)
	s.mu.Lock()
	s.pending[t.AgentID] = t.ID
	s.mu.Unlock()
	if err := s.native.SendWithDisplayText(t.AgentID, prompt, displayTask); err != nil {
		s.mu.Lock()
		if s.pending[t.AgentID] == t.ID {
			delete(s.pending, t.AgentID)
		}
		s.mu.Unlock()
		t.Status, t.ErrorCode = "FAILED", ErrCloudExecution
		addTrace(t, "cloud_execution", "FAILED", map[string]any{
			"errorCode": ErrCloudExecution, "reason": conciseError(err),
		})
		s.saveTrace(*t)
		return result, err
	}
	result.Dispatched = true
	s.mu.Lock()
	stillPending := s.pending[t.AgentID] == t.ID
	s.mu.Unlock()
	if !stillPending {
		if completed, err := s.Trace(t.ID); err == nil {
			result.Trace = completed
		}
		return result, nil
	}
	if fallback {
		t.Status = "FALLBACK_CLOUD_DISPATCHED"
	} else {
		t.Status = "CLOUD_DISPATCHED"
	}
	addTrace(t, "cloud_execution", "DISPATCHED", nil)
	s.saveTrace(*t)
	return result, nil
}

func (s *IntelligenceService) observeNativeEvent(agentID string, ev *StreamEvent) {
	if ev == nil || ev.Type != StreamTypeResult {
		return
	}
	s.mu.Lock()
	traceID := s.pending[agentID]
	delete(s.pending, agentID)
	s.mu.Unlock()
	if traceID == "" {
		return
	}
	t, err := s.Trace(traceID)
	if err != nil {
		return
	}
	if t.Fallback {
		t.Status = "CLOUD_COMPLETED_WITH_FALLBACK"
	} else {
		t.Status = "CLOUD_COMPLETED"
	}
	details := map[string]any{"nativeTurnBoundary": true}
	if ev.Usage != nil {
		t.CloudUsageKnown = true
		t.CloudCostUSD = ev.TotalCostUSD
		t.CloudInputTokens = ev.Usage.InputTokens
		t.CloudOutputTokens = ev.Usage.OutputTokens
		t.CloudCacheReadTokens = ev.Usage.CacheReadInputTokens
		details["cloudCostUsd"] = t.CloudCostUSD
		details["cloudInputTokens"] = t.CloudInputTokens
		details["cloudOutputTokens"] = t.CloudOutputTokens
		details["cloudCacheReadTokens"] = t.CloudCacheReadTokens
	} else {
		// Recorded, not silently skipped: a reader of this trace must be able to
		// tell "the driver reports nothing" from "the turn was free".
		details["cloudUsageReported"] = false
	}
	addTrace(&t, "cloud_execution", "COMPLETED", details)
	s.saveTrace(t)
}

func (s *IntelligenceService) saveTrace(t IntelligenceTrace) {
	events, _ := json.Marshal(t.Events)
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO intelligence_traces
		(id,agent_id,mode,status,provider,model,raw_tokens,optimized_tokens,local_tokens,latency_ms,error_code,fallback,events_json,created_at,
		 cloud_cost_usd,cloud_input_tokens,cloud_output_tokens,cloud_cache_read_tokens,cloud_usage_known,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'))`, t.ID, t.AgentID, t.Mode, t.Status,
		t.Provider, t.Model, t.RawTokens, t.OptimizedTokens, t.LocalTokens, t.LatencyMS,
		t.ErrorCode, t.Fallback, string(events), t.CreatedAt,
		t.CloudCostUSD, t.CloudInputTokens, t.CloudOutputTokens, t.CloudCacheReadTokens, t.CloudUsageKnown)
}

func (s *IntelligenceService) Traces(limit int) ([]IntelligenceTrace, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,agent_id,mode,status,provider,model,raw_tokens,optimized_tokens,
		local_tokens,latency_ms,error_code,fallback,events_json,created_at,
		cloud_cost_usd,cloud_input_tokens,cloud_output_tokens,cloud_cache_read_tokens,cloud_usage_known
		FROM intelligence_traces ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IntelligenceTrace{}
	for rows.Next() {
		var t IntelligenceTrace
		var events string
		if err := rows.Scan(&t.ID, &t.AgentID, &t.Mode, &t.Status, &t.Provider, &t.Model,
			&t.RawTokens, &t.OptimizedTokens, &t.LocalTokens, &t.LatencyMS, &t.ErrorCode,
			&t.Fallback, &events, &t.CreatedAt,
			&t.CloudCostUSD, &t.CloudInputTokens, &t.CloudOutputTokens,
			&t.CloudCacheReadTokens, &t.CloudUsageKnown); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(events), &t.Events)
		if t.RawTokens > 0 && t.OptimizedTokens > 0 {
			t.Reduction = float64(t.RawTokens-t.OptimizedTokens) * 100 / float64(t.RawTokens)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *IntelligenceService) Trace(id string) (IntelligenceTrace, error) {
	var t IntelligenceTrace
	var events string
	err := s.db.QueryRow(`SELECT id,agent_id,mode,status,provider,model,raw_tokens,optimized_tokens,
		local_tokens,latency_ms,error_code,fallback,events_json,created_at,
		cloud_cost_usd,cloud_input_tokens,cloud_output_tokens,cloud_cache_read_tokens,cloud_usage_known
		FROM intelligence_traces WHERE id=?`, id).Scan(
		&t.ID, &t.AgentID, &t.Mode, &t.Status, &t.Provider, &t.Model, &t.RawTokens,
		&t.OptimizedTokens, &t.LocalTokens, &t.LatencyMS, &t.ErrorCode, &t.Fallback, &events, &t.CreatedAt,
		&t.CloudCostUSD, &t.CloudInputTokens, &t.CloudOutputTokens, &t.CloudCacheReadTokens, &t.CloudUsageKnown)
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal([]byte(events), &t.Events)
	if t.RawTokens > 0 && t.OptimizedTokens > 0 {
		t.Reduction = float64(t.RawTokens-t.OptimizedTokens) * 100 / float64(t.RawTokens)
	}
	return t, nil
}
