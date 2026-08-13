package services

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
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

	// PermissionMode is the CLI's own vocabulary: "" (default) | acceptEdits | plan.
	// It is NOT the deck's mode id — callers map through cliPermissionMode first, which
	// keeps the two server-owned modes (auto, 전체 허용) off the command line. Changed on
	// a live session with SetPermissionMode.
	PermissionMode string

	// Effort is the CLI's --effort level: low | medium | high | xhigh | max. It sets how
	// deeply Claude thinks and, with it, how many tokens a turn costs. "" means the deck
	// hasn't been told a preference, and the driver pins DefaultEffort instead of letting
	// the CLI default (xhigh) stand — see normalizeEffort.
	Effort string

	// Options are the per-agent session settings (extra directories, spend cap,
	// auto-compaction window, fallback model). Already normalized by the caller.
	Options NativeOptions

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

// has reports whether the CLI understands a flag. Unknown capabilities (no probe, or
// a probe that failed) answer yes, preserving the behaviour from before the probe.
func (d *ClaudeDriver) has(flag string) bool {
	if d.supports == nil {
		return true
	}
	return d.supports[flag]
}

// tailBuffer keeps the last maxTail bytes written to it and discards the rest, so a
// chatty CLI can neither fill the pipe nor grow this without bound.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const maxTail = 4096

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > maxTail {
		t.buf = t.buf[len(t.buf)-maxTail:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// DefaultEffort is what a session runs at until someone chooses otherwise. The CLI's
// own default is xhigh — the level tuned for the hardest agentic work — which bills
// every routine turn at the top of the scale. high is the documented minimum for
// intelligence-sensitive work and the better resting place for a mixed session.
const DefaultEffort = "high"

// EffortLevels are the values --effort accepts, cheapest first. Exported so the API can
// hand the client the same list rather than the UI keeping its own copy that drifts.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// normalizeEffort maps anything the client sends onto a level the CLI accepts. An
// unknown value becomes the default rather than reaching the command line, where it
// would fail the spawn — a bad dropdown value must not be able to break session start.
func normalizeEffort(effort string) string {
	for _, level := range EffortLevels {
		if effort == level {
			return effort
		}
	}
	return DefaultEffort
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

	// supports is the set of long flags this CLI build advertises, probed at Start.
	// nil means "couldn't tell" — everything is passed, as before the probe existed.
	supports map[string]bool

	// stderrTail keeps the CLI's last diagnostics. When the process refuses its own
	// arguments it says so on stderr and exits; without this the deck saw a session
	// that started and vanished with no reason recorded anywhere.
	stderrTail *tailBuffer
	// exited closes when the process is reaped; exitErr is why.
	exited  chan struct{}
	exitErr error

	// claudeSessionID is learned from system/init and is what --resume needs.
	claudeSessionID string
	// capabilities come from init: what this CLI build understands.
	capabilities []string
	interruptSeq int

	// ctrlSeq numbers control_requests; ctrlWaiters parks the caller of each one
	// until the CLI answers it, keyed by request id.
	ctrlSeq     int
	ctrlWaiters map[string]chan ControlResponse
}

func NewClaudeDriver(cfg ClaudeConfig) *ClaudeDriver {
	return &ClaudeDriver{
		cfg:         cfg,
		events:      make(chan *StreamEvent, 64),
		done:        make(chan struct{}),
		ctrlWaiters: make(map[string]chan ControlResponse),
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
		// Token-level deltas (stream_event/content_block_delta/text_delta). Without
		// this the CLI only emits whole assistant messages, so the UI sits silent
		// through a long answer and then dumps it at once.
		"--include-partial-messages",
		// NOTE: no --replay-user-messages. NativeService records each user turn in
		// history itself, the moment it's sent (native_service.go Send) — so the
		// user's half survives a reconnect AND lands in the right order. The CLI's
		// echo could arrive after the reply started, flipping the on-screen order,
		// and would double-print alongside our own record.
	}
	// The approve bridge goes on in EVERY mode. Without it the CLI has nobody to ask,
	// so a permission-gated tool is denied outright while the turn still reports
	// success — from the deck that reads as "작업이 승인 요청도 없이 스킵됐다".
	//
	// This used to be omitted for bypassPermissions, because passing that flag AND a
	// prompt tool makes the CLI route to the prompt tool and then deny. The fix is not
	// to drop the bridge but to never pass the flag: 전체 허용 is enforced server-side
	// (cliPermissionMode → "", autoDecide → allow), so the contradiction never arises.
	args = append(args,
		"--mcp-config", d.mcpConfigJSON(),
		"--permission-prompt-tool", "mcp__pcd__approve",
	)
	if d.cfg.Model != "" {
		args = append(args, "--model", d.cfg.Model)
	}
	// Always pinned, never omitted: leaving --effort off doesn't mean "no effort", it
	// means the CLI's own default (xhigh), which is the most expensive level and wrong
	// for the routine turns that make up most of a session.
	if d.has("--effort") {
		args = append(args, "--effort", normalizeEffort(d.cfg.Effort))
	}
	// Sub-agent text arrives tagged with parent_tool_use_id, which the client renders
	// under the calling Task instead of in the main thread. Always on rather than a
	// setting: it changes only what the CLI hands us, never what the model does, so it
	// costs no tokens — and without it the deck's sub-agent panel can only ever show
	// that a sub-agent ran, not what it found.
	if d.has("--forward-subagent-text") {
		args = append(args, "--forward-subagent-text")
	}
	// Extra roots beyond cwd. Passed as one variadic flag — the CLI stops collecting
	// at the next "--" token, and every path is absolute and vetted by Normalize.
	if dirs := d.cfg.Options.AddDirs; len(dirs) > 0 && d.has("--add-dir") {
		args = append(args, "--add-dir")
		args = append(args, dirs...)
	}
	if budget := d.cfg.Options.MaxBudgetUSD; budget > 0 && d.has("--max-budget-usd") {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(budget, 'f', -1, 64))
	}
	// Always pinned, like --effort and for the same reason: omitting it isn't "no
	// compaction policy", it's the CLI's own — which lets context grow to the full 1M
	// window, so every request in a long session re-reads up to a million tokens.
	if d.has("--autocompact") {
		if ac := d.cfg.Options.Autocompact; ac != "" {
			args = append(args, "--autocompact", ac)
		} else {
			args = append(args, "--autocompact", DefaultAutocompact)
		}
	}
	if fm := d.cfg.Options.FallbackModel; fm != "" && d.has("--fallback-model") {
		args = append(args, "--fallback-model", fm)
	}
	// Permission mode (Shift+Tab in the TUI): default | acceptEdits | plan |
	// bypassPermissions. "" leaves the CLI default (our approve bridge gates tools).
	if d.cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", d.cfg.PermissionMode)
	}
	if d.cfg.ResumeID != "" {
		args = append(args, "--resume", d.cfg.ResumeID)
	}
	// AskUserQuestion cannot show an interactive prompt in headless stream-json mode:
	// the CLI fails it immediately with an error ("Answer questions?") and, left to
	// itself, Claude treats that as "no answer" and PROCEEDS on a guess within the same
	// turn — so the deck's answer buttons, tapped a moment later, land on a question
	// that's already closed and the pick appears to do nothing. This guidance tells
	// Claude the error is expected and to STOP and wait, so the user's selection (sent
	// as the next turn) is the authoritative answer. (Verified: without it, a real
	// session ended the turn on a guess right after the failed ask.)
	args = append(args, "--append-system-prompt", askUserQuestionGuidance)
	return args
}

// askUserQuestionGuidance keeps AskUserQuestion usable in the deck. The tool stays
// enabled (its questions/options drive the deck's answer buttons); this only changes
// how Claude reacts to the tool's unavoidable error in headless mode.
const askUserQuestionGuidance = "IMPORTANT — AskUserQuestion in this environment: calling the AskUserQuestion tool " +
	"returns an error such as \"Answer questions?\" because this session cannot render an interactive prompt. " +
	"This error is EXPECTED and does NOT mean the user declined or that anything failed. When you need a " +
	"decision from the user, call AskUserQuestion once and then STOP and end your turn immediately — do not " +
	"retry it, do not guess a default, and do not proceed with any further work. The user is shown your " +
	"questions as buttons and will send their choice as their next message; wait for it."

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
	// Ask this CLI what it understands BEFORE building the command line. The deck and
	// the CLI ship separately, so a flag the deck knows may be one this build has never
	// heard of — and an unknown flag isn't ignored, it kills the process on startup.
	d.mu.Lock()
	d.supports = supportedFlags(bin)
	d.mu.Unlock()

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
	// Keep stderr out of the event stream — it carries the CLI's own diagnostics, which
	// are not protocol — but KEEP THE TAIL. Discarding it (as this used to) is what let
	// a CLI rejecting one of our flags look like a session that started and vanished:
	// the reason was printed, then thrown away, and the deck had nothing to report.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	d.stderrTail = &tailBuffer{}
	go io.Copy(d.stderrTail, stderr)

	if err := cmd.Start(); err != nil {
		return err
	}

	d.mu.Lock()
	d.cmd = cmd
	d.stdin = stdin
	d.exited = make(chan struct{})
	exited := d.exited
	d.mu.Unlock()

	go func() {
		err := cmd.Wait()
		d.mu.Lock()
		d.exitErr = err
		d.mu.Unlock()
		close(exited)
	}()

	// A process that dies on its own arguments dies at once. Waiting a beat here turns
	// that into a real Start error, which the caller already knows how to handle — it
	// logs it, tells the user, and retries without --resume. Without this the failure
	// was invisible: Start succeeded, the session was registered, and the pump quietly
	// removed it a moment later, so the next message reported "session is not running"
	// with nothing anywhere saying why.
	select {
	case <-exited:
		detail := d.stderrTail.String()
		if detail == "" {
			detail = "no output on stderr"
		}
		d.mu.Lock()
		exitErr := d.exitErr
		d.mu.Unlock()
		return fmt.Errorf("claude exited immediately (%v): %s", exitErr, detail)
	case <-time.After(startupGrace):
	}

	go d.readPump(stdout)
	return nil
}

// startupGrace is how long Start waits to see whether the CLI rejects its own command
// line. Argument errors are immediate; anything slower (loading a large transcript,
// for instance) is a healthy start and must not be delayed by more than this.
const startupGrace = 700 * time.Millisecond

// readPump turns stdout lines into events. A line we can't parse is skipped, not
// fatal: the CLI prints the occasional non-protocol line, and one bad line must
// not kill a live session.
func (d *ClaudeDriver) readPump(stdout io.ReadCloser) {
	defer close(d.events)
	defer close(d.done)
	defer d.releaseControlWaiters()

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
			d.capabilities = ev.Capabilities
			d.mu.Unlock()
		}
		// Hand a control_response to whoever is waiting on it BEFORE the event goes
		// to the fan-out: events is buffered but finite, and a stalled consumer must
		// not be able to wedge a waiting SetPermissionMode.
		if ev.Type == StreamTypeControlResponse && ev.Response != nil {
			d.deliverControl(*ev.Response)
		}
		d.events <- ev
	}

	// The process is reaped by the goroutine Start launched — waiting again here would
	// race it and return "Wait was already called".
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

// Interrupt stops the turn in progress. The CLI answers with a control_response;
// we don't wait for it — the visible effect (the stream stopping) is the ack that
// matters to a user, and blocking here would stall the caller for a round trip.
//
// Refuses when the CLI didn't advertise interrupt support, rather than writing a
// line it will ignore and letting the user believe they stopped something.
func (d *ClaudeDriver) Interrupt() error {
	d.mu.Lock()
	stdin := d.stdin
	stopped := d.stopped
	caps := d.capabilities
	d.interruptSeq++
	id := fmt.Sprintf("int-%d", d.interruptSeq)
	d.mu.Unlock()

	if stdin == nil || stopped {
		return fmt.Errorf("claude driver: session is not running")
	}
	supported := false
	for _, c := range caps {
		if c == "interrupt_receipt_v1" {
			supported = true
		}
	}
	if !supported {
		return fmt.Errorf("claude driver: this CLI build does not support interrupt")
	}
	b, err := json.Marshal(NewInterruptRequest(id))
	if err != nil {
		return err
	}
	_, err = stdin.Write(append(b, '\n'))
	return err
}

// setPermissionModeTimeout bounds the wait for the CLI's control_response. The CLI
// answers this frame immediately (it is not queued behind the turn), so a wait this
// long only ever expires on a build that ignores the frame entirely — and then the
// caller falls back to a restart rather than believing a switch that never happened.
const setPermissionModeTimeout = 5 * time.Second

// SetPermissionMode changes a LIVE session's permission mode without restarting it.
//
// This is the whole point: the mode used to be applied by killing the process and
// resuming, which threw away whatever turn was in flight — change your mind mid-task
// and the task stopped. The CLI accepts the change on the same stdin pipe instead.
//
// mode is the CLI's vocabulary ("" → "default", acceptEdits, plan). Server-owned
// modes (auto, 전체 허용) must be mapped through cliPermissionMode first; asking the
// CLI for bypassPermissions here is refused unless it was launched with
// --dangerously-skip-permissions.
//
// It waits for the CLI's answer rather than firing and forgetting: a refusal that we
// reported as success would leave the deck showing one mode while the session runs
// another.
func (d *ClaudeDriver) SetPermissionMode(mode string) error {
	d.mu.Lock()
	stdin := d.stdin
	stopped := d.stopped
	d.ctrlSeq++
	id := fmt.Sprintf("set-mode-%d", d.ctrlSeq)
	wait := make(chan ControlResponse, 1)
	d.ctrlWaiters[id] = wait
	d.mu.Unlock()

	if stdin == nil || stopped {
		d.forgetControl(id)
		return fmt.Errorf("claude driver: session is not running")
	}
	b, err := json.Marshal(NewSetPermissionModeRequest(id, mode))
	if err != nil {
		d.forgetControl(id)
		return err
	}
	if _, err := stdin.Write(append(b, '\n')); err != nil {
		d.forgetControl(id)
		return err
	}

	timer := time.NewTimer(setPermissionModeTimeout)
	defer timer.Stop()
	select {
	case resp, ok := <-wait:
		if !ok {
			return fmt.Errorf("claude driver: session ended before the mode change was acknowledged")
		}
		if resp.Subtype != "success" {
			msg := resp.Error
			if msg == "" {
				msg = resp.Subtype
			}
			return fmt.Errorf("claude driver: the CLI refused the mode change: %s", msg)
		}
		d.mu.Lock()
		d.cfg.PermissionMode = mode
		d.mu.Unlock()
		return nil
	case <-timer.C:
		d.forgetControl(id)
		return fmt.Errorf("claude driver: this CLI build did not answer set_permission_mode")
	}
}

// deliverControl routes one control_response to its waiter. An id nobody waits on is
// dropped: Interrupt fires and forgets, so its response is expected to be orphaned.
func (d *ClaudeDriver) deliverControl(resp ControlResponse) {
	d.mu.Lock()
	w := d.ctrlWaiters[resp.RequestID]
	delete(d.ctrlWaiters, resp.RequestID)
	d.mu.Unlock()
	if w != nil {
		w <- resp
	}
}

// releaseControlWaiters frees everyone still waiting when the process exits, so a
// dead session reports "it ended" at once instead of after the full timeout.
func (d *ClaudeDriver) releaseControlWaiters() {
	d.mu.Lock()
	waiters := d.ctrlWaiters
	d.ctrlWaiters = make(map[string]chan ControlResponse)
	d.mu.Unlock()
	for _, w := range waiters {
		close(w)
	}
}

func (d *ClaudeDriver) forgetControl(id string) {
	d.mu.Lock()
	delete(d.ctrlWaiters, id)
	d.mu.Unlock()
}

// ClaudeSessionID is the CLI's own session id (from system/init) — what --resume
// takes. Empty until init lands.
func (d *ClaudeDriver) ClaudeSessionID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.claudeSessionID
}

func (d *ClaudeDriver) ConversationID() string { return d.ClaudeSessionID() }

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
