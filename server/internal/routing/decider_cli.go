package routing

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirRunner runs an official CLI in a given directory with prompt on stdin.
type DirRunner interface {
	LookPath(name string) (string, error)
	RunIn(ctx context.Context, dir, bin string, args []string, stdin string) (stdout, stderr string, err error)
}

// CallOutput is one decider answer as the CLI reported it.
type CallOutput struct {
	Text          string
	Usage         *UsageRecord
	ObservedModel string
}

// CallError carries a class so decider failures never masquerade as
// executor failures.
type CallError struct {
	Class string
	Err   error
}

func (e *CallError) Error() string { return e.Class + ": " + e.Err.Error() }
func (e *CallError) Unwrap() error { return e.Err }

// DeciderCaller performs one bounded, tool-free decision call.
type DeciderCaller interface {
	Call(ctx context.Context, p Profile, prompt, schema string) (CallOutput, error)
}

// CLIDecider uses the installed official CLIs. It runs each call in a fresh,
// empty temporary directory — never the Run's workspace — and never touches
// global CLI settings or logins.
type CLIDecider struct {
	Runner    DirRunner
	Local     *LocalClient
	Endpoints map[string]LocalEndpoint
	TempRoot  string
}

func classify(detail string) string {
	switch {
	case rateRe.MatchString(detail):
		return "rate_limited"
	case authRe.MatchString(detail), strings.Contains(strings.ToLower(detail), "failed to authenticate"):
		return "auth"
	case entitlementRe.MatchString(detail):
		return "entitlement"
	}
	return "provider_error"
}

func (c *CLIDecider) Call(ctx context.Context, p Profile, prompt, schema string) (CallOutput, error) {
	if p.Adapter == AdapterLocal {
		return c.local(ctx, p, prompt)
	}
	dir, err := os.MkdirTemp(c.TempRoot, "pcd-decider-")
	if err != nil {
		return CallOutput{}, &CallError{"launch", err}
	}
	defer os.RemoveAll(dir)
	var bin string
	var args []string
	switch p.Adapter {
	case AdapterClaude:
		bin, err = c.Runner.LookPath("claude")
		args = ClaudeDeciderArgs(p, schema)
	case AdapterCodex:
		bin, err = c.Runner.LookPath("codex")
		schemaPath := filepath.Join(dir, ".pcd-verdict-schema.json")
		if err == nil {
			err = os.WriteFile(schemaPath, []byte(schema), 0600)
		}
		args = CodexDeciderArgs(p, dir, schemaPath)
	default:
		return CallOutput{}, &CallError{"launch", fmt.Errorf("adapter %s cannot decide", p.Adapter)}
	}
	if err != nil {
		return CallOutput{}, &CallError{"launch", err}
	}
	stdout, stderr, runErr := c.Runner.RunIn(ctx, dir, bin, args, prompt)
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return CallOutput{}, &CallError{"timeout", ctx.Err()}
		}
		return CallOutput{}, &CallError{"canceled", ctx.Err()}
	}
	var out CallOutput
	var perr error
	if p.Adapter == AdapterClaude {
		out, perr = ParseClaudeResult(stdout)
	} else {
		out, perr = ParseCodexExec(stdout)
	}
	if perr != nil {
		detail := perr.Error() + " " + stderr
		if runErr != nil {
			detail += " " + runErr.Error()
		}
		return out, &CallError{classify(detail), errors.New(Summarize(detail, 500))}
	}
	return out, nil
}

// ClaudeDeciderArgs: print mode, no built-in tools, no MCP servers, no
// user/project/local settings (hooks, plugins), no skills, no session file.
// Managed (enterprise policy) settings cannot be excluded by any flag.
func ClaudeDeciderArgs(p Profile, schema string) []string {
	args := []string{"-p", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--setting-sources", "", "--disable-slash-commands", "--no-session-persistence"}
	if schema != "" {
		args = append(args, "--json-schema", schema)
	}
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	if p.Effort != "" {
		args = append(args, "--effort", p.Effort)
	}
	return args
}

// CodexDeciderArgs: exec with a read-only sandbox in an empty directory,
// without user config (MCP servers, profiles) and without a session file.
// The shell tool itself cannot be removed — hence allowReadOnlyShell.
func CodexDeciderArgs(p Profile, dir, schemaPath string) []string {
	args := []string{"exec", "--json", "--sandbox", "read-only", "--ephemeral", "--ignore-user-config", "--skip-git-repo-check", "-C", dir, "--output-schema", schemaPath}
	if p.Model != "" {
		args = append(args, "-m", p.Model)
	}
	if p.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+p.Effort)
	}
	return append(args, "-")
}

// ParseClaudeResult reads `claude -p --output-format json`. is_error wins over
// subtype: an expired login is reported as subtype "success" + is_error.
func ParseClaudeResult(stdout string) (CallOutput, error) {
	var r struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            *struct {
			InputTokens              *int `json:"input_tokens"`
			OutputTokens             *int `json:"output_tokens"`
			CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
			OutputTokensDetails      *struct {
				ThinkingTokens *int `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
		ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &r); err != nil {
		return CallOutput{}, fmt.Errorf("unrecognized claude output: %v", err)
	}
	out := CallOutput{Text: r.Result}
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		out.Text = string(r.StructuredOutput)
	}
	models := make([]string, 0, len(r.ModelUsage))
	for m := range r.ModelUsage {
		models = append(models, m)
	}
	sortStrings(models)
	// Every model the call used, including background/auxiliary ones.
	out.ObservedModel = strings.Join(models, ",")
	if u := r.Usage; u != nil {
		// Claude reports cache reads/creation separately from input_tokens.
		out.Usage = &UsageRecord{Scope: "turn", Source: "reported", InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheRead: u.CacheReadInputTokens, CacheCreation: u.CacheCreationInputTokens}
		if u.OutputTokensDetails != nil {
			out.Usage.ThinkingTokens = u.OutputTokensDetails.ThinkingTokens
		}
	}
	if r.IsError {
		return out, errors.New(r.Result)
	}
	return out, nil
}

// ParseCodexExec reads `codex exec --json` JSONL: the last agent message is the
// answer; turn.completed carries usage (cached_input_tokens is a subset of
// input_tokens in Codex's accounting).
func ParseCodexExec(stdout string) (CallOutput, error) {
	var out CallOutput
	var failure string
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Item *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage *struct {
				InputTokens       *int `json:"input_tokens"`
				CachedInputTokens *int `json:"cached_input_tokens"`
				OutputTokens      *int `json:"output_tokens"`
			} `json:"usage"`
			Error   *struct{ Message string } `json:"error"`
			Message string                    `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "item.completed":
			if ev.Item != nil && ev.Item.Type == "agent_message" {
				out.Text = ev.Item.Text
			}
		case "turn.completed":
			if u := ev.Usage; u != nil {
				out.Usage = &UsageRecord{Scope: "turn", Source: "reported", InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheRead: u.CachedInputTokens}
			}
		case "turn.failed", "error":
			failure = ev.Message
			if ev.Error != nil && ev.Error.Message != "" {
				failure = ev.Error.Message
			}
		}
	}
	if failure != "" {
		return out, errors.New(failure)
	}
	if out.Text == "" {
		return out, errors.New("codex returned no final message")
	}
	return out, nil
}

func (c *CLIDecider) local(ctx context.Context, p Profile, prompt string) (CallOutput, error) {
	ep, ok := c.Endpoints[p.EndpointRef]
	if !ok || c.Local == nil {
		return CallOutput{}, &CallError{"launch", errors.New("local endpoint not configured")}
	}
	ex, err := NewLocalExecution("decide_"+time.Now().Format("150405.000000"), c.Local, ep, p.Model)
	if err != nil {
		return CallOutput{}, &CallError{"launch", err}
	}
	if err := ex.Send(prompt); err != nil {
		return CallOutput{}, &CallError{"launch", err}
	}
	for {
		ev, err := ex.Next(ctx)
		if err != nil {
			return CallOutput{}, &CallError{"provider_error", err}
		}
		if ev.Outcome != nil {
			if ev.Outcome.IsError {
				return CallOutput{}, &CallError{"provider_error", errors.New(ev.Outcome.Diagnostics)}
			}
			out := CallOutput{Text: ev.Outcome.Text, ObservedModel: ev.Model}
			if u := ev.Outcome.Usage; u != nil {
				in, o := u.InputTokens, u.OutputTokens
				out.Usage = &UsageRecord{Scope: "turn", Source: "reported", InputTokens: &in, OutputTokens: &o, Local: true}
			}
			return out, nil
		}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
