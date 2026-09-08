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
	if p.Broker == nil || p.Tokens == nil || id == "" || cwd == "" {
		return nil, fmt.Errorf("Run provider configuration is incomplete")
	}
	var driver native.Driver
	switch provider {
	case providers.Claude:
		if p.ApproveURL == "" || p.SelfPath == "" {
			return nil, fmt.Errorf("Claude Run approval bridge is unavailable")
		}
		token, err := p.Tokens.Issue(id)
		if err != nil {
			return nil, err
		}
		driver = NewClaudeDriver(ClaudeConfig{SessionID: id, Cwd: cwd, ApproveURL: p.ApproveURL, ApproveToken: token, SelfPath: p.SelfPath})
	case providers.Codex:
		driver = NewCodexDriver(CodexConfig{SessionID: id, Cwd: cwd, Broker: p.Broker})
	default:
		return nil, fmt.Errorf("unsupported Run provider %q", provider)
	}
	adapter, err := native.New(providers.Identity{ExecutionID: id, Provider: provider}, driver)
	if err != nil {
		p.Tokens.Revoke(id)
		return nil, err
	}
	return &runExecution{Execution: adapter, cleanup: func() { p.Tokens.Revoke(id); p.Broker.CancelSession(id) }}, nil
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
