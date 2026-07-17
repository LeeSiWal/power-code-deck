package services

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// ClaudeDriver runs one Claude Code session as a structured stream instead of a
// terminal: no PTY, no VT emulation, no screen to replay. We feed it user turns on
// stdin as JSON and read events off stdout as JSON, and the UI renders those
// events directly.
//
// The whole reason the native track exists: everything a terminal has to
// reconstruct (widths, cursor, DEC modes, wrap state, scrollback) simply isn't
// part of this conversation.

// ClaudeConfig describes a native session to start.
type ClaudeConfig struct {
	SessionID string // our agent id — also what the bridge quotes back to us
	Cwd       string
	Model     string // "" = the CLI's default

	// ResumeID is Claude's own session_id, to continue a past conversation.
	ResumeID string

	// ApproveURL/ApproveToken wire the permission bridge. When empty, the driver
	// runs WITHOUT --permission-prompt-tool, which means the CLI silently denies
	// every permission-gated tool while still reporting the turn as a success.
	// That is a footgun, so the driver refuses to start rather than pretend.
	ApproveURL   string
	ApproveToken string

	// SelfPath is the pcd binary to spawn as the MCP permission server — normally
	// os.Executable(). It's a field so tests can point at a stub.
	SelfPath string
}

// ClaudeDriver owns the process and its two pipes.
type ClaudeDriver struct {
	cfg ClaudeConfig

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	events  chan *StreamEvent
	done    chan struct{}
	stopped bool

	// claudeSessionID is learned from system/init and is what --resume needs.
	claudeSessionID string
}

func NewClaudeDriver(cfg ClaudeConfig) *ClaudeDriver {
	return &ClaudeDriver{
		cfg:    cfg,
		events: make(chan *StreamEvent, 64),
		done:   make(chan struct{}),
	}
}

// buildArgs assembles the CLI invocation.
//
// --verbose is REQUIRED with --print + stream-json (the CLI rejects the pair
// without it). --permission-prompt-tool names the MCP tool the CLI calls to ask a
// human; mcp__pcd__approve is our bridge, served by this same binary.
func (d *ClaudeDriver) buildArgs() []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--mcp-config", d.mcpConfigJSON(),
		"--permission-prompt-tool", "mcp__pcd__approve",
	}
	if d.cfg.Model != "" {
		args = append(args, "--model", d.cfg.Model)
	}
	if d.cfg.ResumeID != "" {
		args = append(args, "--resume", d.cfg.ResumeID)
	}
	return args
}

// mcpConfigJSON points the CLI at this very binary as an MCP server. --mcp-config
// takes JSON files OR inline JSON strings, so there's no temp file to create,
// secure, or clean up — and the token never touches disk.
func (d *ClaudeDriver) mcpConfigJSON() string {
	cfg := map[string]any{"mcpServers": map[string]any{
		"pcd": map[string]any{
			"type":    "stdio",
			"command": d.cfg.SelfPath,
			"args":    []string{"mcp-approve"},
			"env": map[string]string{
				"PCD_APPROVE_URL":     d.cfg.ApproveURL,
				"PCD_APPROVE_TOKEN":   d.cfg.ApproveToken,
				"PCD_APPROVE_SESSION": d.cfg.SessionID,
			},
		},
	}}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// Start launches the CLI and begins streaming events. Events() is closed when the
// process exits.
func (d *ClaudeDriver) Start() error {
	if d.cfg.ApproveURL == "" || d.cfg.ApproveToken == "" {
		// Refuse rather than run in the mode where permission prompts are silently
		// denied and the turn still says "success" — a UI on top of that reports
		// green checks for work that never happened.
		return fmt.Errorf("claude driver: refusing to start without an approval bridge")
	}
	if d.cfg.SelfPath == "" {
		return fmt.Errorf("claude driver: SelfPath (the pcd binary) is required for the MCP bridge")
	}

	bin := "claude"
	if resolved := findAgentCommand("claude"); resolved != "" {
		bin = resolved
	}
	cmd := exec.Command(bin, d.buildArgs()...)
	cmd.Dir = d.cfg.Cwd
	locale := utf8Locale()
	cmd.Env = withAgentPath(append(os.Environ(),
		"LANG="+locale,
		"LC_ALL="+locale,
	))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Keep stderr out of the event stream: it carries the CLI's own diagnostics,
	// which are not protocol. Drain it so a chatty CLI can't fill the pipe and
	// wedge the process.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	go io.Copy(io.Discard, stderr)

	if err := cmd.Start(); err != nil {
		return err
	}

	d.mu.Lock()
	d.cmd = cmd
	d.stdin = stdin
	d.mu.Unlock()

	go d.readPump(stdout)
	return nil
}

// readPump turns stdout lines into events. A line we can't parse is skipped, not
// fatal: the CLI prints the occasional non-protocol line, and one bad line must
// not kill a live session.
func (d *ClaudeDriver) readPump(stdout io.ReadCloser) {
	defer close(d.events)
	defer close(d.done)

	sc := bufio.NewScanner(stdout)
	// A single event can carry a whole file's contents (a Write tool_use, or a
	// Read's tool_result), so the default 64KB line cap is far too small.
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := ParseStreamEvent(line)
		if err != nil {
			continue
		}
		if ev.Type == StreamTypeSystem && ev.Subtype == "init" && ev.SessionID != "" {
			d.mu.Lock()
			d.claudeSessionID = ev.SessionID
			d.mu.Unlock()
		}
		d.events <- ev
	}

	d.mu.Lock()
	cmd := d.cmd
	d.mu.Unlock()
	if cmd != nil {
		_ = cmd.Wait()
	}
}

// Events yields the session's stream. Closed when the process exits.
func (d *ClaudeDriver) Events() <-chan *StreamEvent { return d.events }

// Send delivers a user turn. Safe to call while the agent is mid-thought — the CLI
// queues it, which is what makes "type ahead" work in a chat UI.
func (d *ClaudeDriver) Send(text string) error {
	d.mu.Lock()
	stdin := d.stdin
	stopped := d.stopped
	d.mu.Unlock()
	if stdin == nil || stopped {
		return fmt.Errorf("claude driver: session is not running")
	}
	b, err := json.Marshal(NewUserText(text))
	if err != nil {
		return err
	}
	_, err = stdin.Write(append(b, '\n'))
	return err
}

// ClaudeSessionID is the CLI's own session id (from system/init) — what --resume
// takes. Empty until init lands.
func (d *ClaudeDriver) ClaudeSessionID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.claudeSessionID
}

// Stop ends the session. Closing stdin is the polite exit (the CLI finishes and
// exits on EOF); Kill is the fallback if it doesn't.
func (d *ClaudeDriver) Stop() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	stdin := d.stdin
	cmd := d.cmd
	d.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// Done closes when the process has exited and the stream is drained.
func (d *ClaudeDriver) Done() <-chan struct{} { return d.done }
