// Package antigravity runs a single Antigravity CLI headless turn as a provider execution.
// It does not install/authenticate the CLI, edit CLI policy, or auto-approve tools.
// Start prepares an installed command; Send starts exactly one process. A follow-up
// uses a new execution with the returned ConversationID as Config.ResumeID.
package antigravity

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"powercodedeck/internal/providers"
)

type Config struct {
	Command    string   // defaults to agy; executable, not a shell command string
	PrefixArgs []string // optional executable-wrapper arguments
	Cwd        string
	Model      string   // empty lets the installed CLI choose its configured model
	ResumeID   string   // explicit ID only; never implicitly resume another user's latest
	Env        []string // nil inherits the host environment; authentication stays with CLI
}

type Execution struct {
	identity                          providers.Identity
	cfg                               Config
	mu                                sync.Mutex
	ready, sent, stopped, interrupted bool
	command                           string
	conversationID                    string
	cmd                               *exec.Cmd
	stdout                            io.ReadCloser
	events                            chan providers.Event
	stop                              chan struct{}
}

func New(executionID string, cfg Config) (*Execution, error) {
	if executionID == "" {
		return nil, fmt.Errorf("Antigravity execution ID is required")
	}
	if cfg.ResumeID == "latest" {
		return nil, fmt.Errorf("Antigravity resume requires an explicit session ID")
	}
	if cfg.Cwd == "" {
		return nil, fmt.Errorf("Antigravity working directory is required")
	}
	cfg.PrefixArgs = append([]string(nil), cfg.PrefixArgs...)
	if cfg.Env != nil {
		cfg.Env = append([]string{}, cfg.Env...)
	}
	return &Execution{identity: providers.Identity{ExecutionID: executionID, Provider: providers.Antigravity}, cfg: cfg,
		conversationID: cfg.ResumeID, events: make(chan providers.Event, 64), stop: make(chan struct{})}, nil
}

func (e *Execution) Identity() providers.Identity { return e.identity }
func (e *Execution) Capabilities() providers.Capabilities {
	return providers.Capabilities{MultiTurn: false, ApprovalHandling: providers.CLISettings}
}
func (e *Execution) ConversationID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conversationID
}
func (e *Execution) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped || e.ready {
		return fmt.Errorf("Antigravity execution cannot be started again")
	}
	command := e.cfg.Command
	if command == "" {
		command = "agy"
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("Antigravity CLI is not installed or not on PATH: %w", err)
	}
	if runtime.GOOS == "windows" && (strings.HasSuffix(strings.ToLower(resolved), ".cmd") || strings.HasSuffix(strings.ToLower(resolved), ".bat")) {
		return fmt.Errorf("Antigravity on Windows requires the native agy.exe executable")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return err
	}
	cwd, err := filepath.Abs(e.cfg.Cwd)
	if err != nil {
		return err
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Antigravity working directory is not a directory")
	}
	e.command, e.cfg.Cwd, e.ready = resolved, cwd, true
	return nil
}

func (e *Execution) Send(prompt string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ready || e.sent || e.stopped {
		return fmt.Errorf("Antigravity execution accepts one prompt after Start")
	}
	args := append([]string(nil), e.cfg.PrefixArgs...)
	// One argv element preserves prompt whitespace and shell metacharacters.
	args = append(args, "--output-format", "stream-json", "--prompt", prompt)
	if e.cfg.Model != "" {
		args = append(args, "--model", e.cfg.Model)
	}
	if e.cfg.ResumeID != "" {
		args = append(args, "--conversation", e.cfg.ResumeID)
	}
	cmd := exec.Command(e.command, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = e.cfg.Cwd
	cmd.Env = e.cfg.Env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tail{limit: 8192}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		stdout.Close()
		return fmt.Errorf("start Antigravity: %w", err)
	}
	e.cmd, e.stdout, e.sent = cmd, stdout, true
	go e.pump(cmd, stdout, stderr)
	return nil
}

func (e *Execution) Next(ctx context.Context) (providers.Event, error) {
	if err := ctx.Err(); err != nil {
		return providers.Event{}, err
	}
	select {
	case <-ctx.Done():
		return providers.Event{}, ctx.Err()
	case event, ok := <-e.events:
		if !ok {
			return providers.Event{}, io.EOF
		}
		return event, nil
	}
}

func (e *Execution) Interrupt() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd == nil {
		return fmt.Errorf("Antigravity has no active turn")
	}
	e.interrupted = true
	err := e.cmd.Process.Kill()
	// Unblock stdout even if a child inherited it. The pump reaps the CLI.
	if e.stdout != nil {
		e.stdout.Close()
	}
	return err
}
func (e *Execution) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return
	}
	e.stopped = true
	close(e.stop)
	if e.cmd != nil {
		_ = e.cmd.Process.Kill()
		if e.stdout != nil {
			e.stdout.Close()
		}
	}
	if !e.sent {
		close(e.events)
	} // otherwise the pump owns channel closure
}
func (e *Execution) SetPermissionMode(mode string) error {
	if mode == "" {
		return nil
	}
	return fmt.Errorf("Antigravity headless provider uses configured CLI policy only; interactive approvals are not bridged")
}

// tail bounds diagnostic memory while keeping stderr out of the event stream.
type tail struct {
	data  []byte
	limit int
}

func (t *tail) Write(b []byte) (int, error) {
	n := len(b)
	t.data = append(t.data, b...)
	if len(t.data) > t.limit {
		t.data = append([]byte(nil), t.data[len(t.data)-t.limit:]...)
	}
	return n, nil
}

func (e *Execution) pump(cmd *exec.Cmd, stdout io.ReadCloser, stderr *tail) {
	defer close(e.events)
	defer stdout.Close()
	var sequence uint64
	emit := func(event providers.Event) bool {
		sequence++
		event.Identity = e.identity
		event.Sequence = sequence
		if event.ConversationID == "" {
			event.ConversationID = e.ConversationID()
		}
		select {
		case e.events <- event:
			return true
		case <-e.stop:
			return false
		}
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	var final *providers.Event
	decoder := decoder{tools: make(map[string]bool)}
	for scanner.Scan() {
		event, err := decoder.parse(scanner.Bytes())
		if err != nil {
			continue
		} // tolerate non-JSON CLI noise
		if event.ConversationID != "" {
			e.mu.Lock()
			e.conversationID = event.ConversationID
			e.mu.Unlock()
		}
		if event.Kind == providers.TurnFinished {
			copy := event
			final = &copy
			continue
		}
		if !emit(event) {
			break
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	e.mu.Lock()
	interrupted := e.interrupted
	e.cmd = nil
	e.stdout = nil
	e.mu.Unlock()
	// Emit exactly one outcome, after checking OS exit as well as CLI result.
	// Missing results, read errors or a nonzero exit must never masquerade as success.
	if final == nil {
		final = &providers.Event{Kind: providers.TurnFinished, Outcome: &providers.Outcome{Status: providers.CompletionFailed, IsError: true, Reason: "missing_result"}}
	}
	if interrupted {
		final.Outcome.IsError = true
		final.Outcome.Reason = "interrupted"
		final.Outcome.Status = providers.CompletionInterrupted
	} else if scanErr != nil {
		final.Outcome.IsError = true
		final.Outcome.Reason = "stream_error"
		final.Outcome.Status = providers.CompletionFailed
		final.Outcome.Text = scanErr.Error()
	} else if waitErr != nil {
		final.Outcome.IsError = true
		final.Outcome.Reason = "process_error"
		final.Outcome.Status = providers.CompletionFailed
		final.Outcome.Text = strings.TrimSpace(string(stderr.data))
		if final.Outcome.Text == "" {
			final.Outcome.Text = waitErr.Error()
		}
	}
	final.Outcome.Diagnostics = strings.TrimSpace(string(stderr.data))
	emit(*final)
}

var _ providers.Execution = (*Execution)(nil)
