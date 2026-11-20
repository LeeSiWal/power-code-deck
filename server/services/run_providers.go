package services

import (
	"fmt"
	"sync"

	"powercodedeck/internal/providers"
	"powercodedeck/internal/providers/native"
)

// RunProviders is an application boundary around legacy drivers. It owns no
// legacy agent/session records and uses a separate approval broker and tokens.
type RunProviders struct {
	Broker     *PermissionBroker
	Tokens     *approveTokens
	ApproveURL string
	SelfPath   string
}

func (p *RunProviders) New(provider providers.ID, id, cwd string) (providers.Execution, error) {
	return p.NewWith(provider, id, cwd, "", "")
}

// NewWith launches a Run execution with an explicit model and effort (a routing
// profile). Empty values keep the CLI's configured defaults.
func (p *RunProviders) NewWith(provider providers.ID, id, cwd, model, effort string) (providers.Execution, error) {
	return p.NewRole(provider, id, cwd, model, effort, "")
}

// NewRole is NewWith plus a read-only mode for auxiliary roles: "plan" maps to
// Claude's plan permission mode and Codex's read-only sandbox. "" keeps the
// implementation defaults.
func (p *RunProviders) NewRole(provider providers.ID, id, cwd, model, effort, mode string) (providers.Execution, error) {
	return p.NewRoleResume(provider, id, cwd, model, effort, mode, "")
}

// NewRoleResume is NewRole that continues the provider conversation named by
// resume (Claude session_id / Codex threadId). "" starts a fresh conversation.
func (p *RunProviders) NewRoleResume(provider providers.ID, id, cwd, model, effort, mode, resume string) (providers.Execution, error) {
	driver, err := p.roleDriver(provider, id, cwd, model, effort, mode, resume)
	if err != nil {
		return nil, err
	}
	adapter, err := native.New(providers.Identity{ExecutionID: id, Provider: provider}, driver)
	if err != nil {
		p.Tokens.Revoke(id)
		return nil, err
	}
	return &runExecution{Execution: adapter, cleanup: func() { p.Tokens.Revoke(id); p.Broker.CancelSession(id) }}, nil
}

func (p *RunProviders) roleDriver(provider providers.ID, id, cwd, model, effort, mode, resume string) (native.Driver, error) {
	if p.Broker == nil || p.Tokens == nil || id == "" || cwd == "" {
		return nil, fmt.Errorf("Run provider configuration is incomplete")
	}
	switch provider {
	case providers.Claude:
		if p.ApproveURL == "" || p.SelfPath == "" {
			return nil, fmt.Errorf("Claude Run approval bridge is unavailable")
		}
		token, err := p.Tokens.Issue(id)
		if err != nil {
			return nil, err
		}
		return NewClaudeDriver(ClaudeConfig{SessionID: id, Cwd: cwd, ApproveURL: p.ApproveURL, ApproveToken: token, SelfPath: p.SelfPath, Model: model, Effort: effort, PermissionMode: mode, ResumeID: resume}), nil
	case providers.Codex:
		return NewCodexDriver(CodexConfig{SessionID: id, Cwd: cwd, Broker: p.Broker, Model: model, Effort: effort, Mode: mode, ResumeID: resume}), nil
	}
	return nil, fmt.Errorf("unsupported Run provider %q", provider)
}

type runExecution struct {
	providers.Execution
	once    sync.Once
	cleanup func()
}

func (e *runExecution) Stop() {
	e.once.Do(func() {
		e.cleanup()
		e.Execution.Stop()
	})
}
