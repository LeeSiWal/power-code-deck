# Codex Optimization Audit

Audit date: 2026-08-16

Status: **PARTIALLY OPTIMIZED**

This verdict describes the repository before the blocking fixes recorded below. PowerCodeDeck has a strong transport and session foundation, but two launch-integrity failures prevent an `OPTIMIZED` verdict: the native Codex launch trusts client-supplied repository identity, and app-server RPC calls can wait forever.

## Evidence

### 1. Process launch

- Native Codex is launched by `services.CodexDriver.Start` as `codex app-server --stdio` using `exec.Command`; it is not parsed through a shell.
- `findAgentCommand("codex")` searches the server `PATH`, npm's global bin directory, and `~/.local/bin`, then selects the newest versioned installation when possible. This is stronger than relying on the service manager's often-minimal `PATH`.
- The terminal compatibility path is separate: `InternalPtySessionEngine.Create` resolves a CLI, creates a go-pty PTY/ConPTY, sets its size and starts the process.
- Evidence commands on the audited host:
  - `command -v codex` → `/home/siwal/.npm-global/bin/codex`
  - `codex --version` → `codex-cli 0.146.0`
  - `codex login status` → `Logged in using ChatGPT`
  - `codex app-server --help` confirms stdio transport support.

### 2. Working directory

- `CodexDriver.Start` sets `cmd.Dir = cfg.Cwd`; `threadParams` also sends the same `cwd` to `thread/start` or `thread/resume`.
- `AgentService.Create` expands a leading `~`, stores the resulting working directory on the agent row and supplies it to the PTY engine.
- The default UI reads `agent.workingDir` and sends it in `native:open`.
- **High defect:** `Hub.handleMessage(EventNativeOpen)` passes the browser's `payload.Cwd`, `payload.Driver`, and `payload.Resume` directly to `NativeService.Start` without resolving the authoritative agent row. A stale or altered client can therefore launch agent A in project B or resume an unrelated conversation. Correct behavior must derive repository and driver identity on the server and use the persisted conversation id.

### 3. Environment

- Both `CodexDriver.Start` and `InternalPtySessionEngine.Create` start from `os.Environ()`, preserving `HOME`, `CODEX_HOME`, credentials, Git settings supplied through environment variables, MCP configuration discovery and user-defined variables.
- `withAgentPath` adds npm-global and `~/.local/bin` without removing the inherited `PATH`.
- A host-available UTF-8 locale is added. The PTY path also supplies `TERM=xterm-256color`.
- Environment values are not logged by these paths. The audit command printed only a narrow allow-list of non-secret environment names.

### 4. PTY compatibility

- Native Codex does not depend on terminal emulation: JSON-RPC requests and newline-delimited JSON notifications travel over stdin/stdout.
- The explicit `?terminal` path uses go-pty/ConPTY, forwards raw input, applies resize, and preserves ANSI/DEC modes (alternate screen, mouse, application cursor keys and bracketed paste) for replay.
- `readPump` carries incomplete UTF-8 sequences across reads, answers terminal capability queries and applies byte-acknowledged backpressure.
- Paste submit and paste-only paths preserve bracketed-paste framing. Keyboard input is forwarded as raw bytes by the terminal surface.

### 5. Session persistence

- Native sessions live independently of WebSocket clients in `NativeService.sessions`. Events are retained as structured history (bounded to 2,000 events).
- The Codex thread id received from `system/init` is persisted on the agent row through `SetPersistence`; a later native open resumes that thread.
- PTY sessions live in the server-owned `InternalPtySessionEngine`, and each retains a bounded 512 KiB replay ring by default.
- Server process restart terminates live native and PTY processes. Codex conversation identity can resume, but an in-flight turn cannot. This limitation is documented for PTY sessions and applies to native process ownership as well.

### 6. Reconnect

- Browser close invokes viewer detach, not process kill. WebSocket reconnect queues early attach/open messages and reopens the current surface.
- Native reconnect replays structured history and pending approvals. PTY reconnect replays scrollback plus reconstructed DEC private modes.
- Device takeover is explicit: another device receives `native:evicted` or `terminal:evicted`; a tab on the same device may continue observing.

### 7. Error observability

- Native start/send/model/mode/interrupt failures become `native:error` events and are also logged server-side.
- PTY process state distinguishes running, exited and explicitly killed.
- **High defect:** `CodexDriver.call` blocks on a response channel with no deadline. A wedged app-server can hang native open or message delivery indefinitely.
- **Medium defect:** app-server stderr is drained into `io.Discard`. The user sees that startup failed but loses the child process exit status/detail needed to distinguish protocol failure from configuration failure.

### 8. Codex-specific integration

- PowerCodeDeck uses the supported app-server lifecycle: `initialize` → `initialized` → `thread/start|resume` → `turn/start`, with `turn/interrupt` and approval responses.
- Approval policy and sandbox are mapped per Codex thread. Codex approval requests are normalized into the shared approval broker, while command/file/MCP events are normalized into native chat events.
- Model and conversation id are persisted. Codex correctly hides unsupported Claude-only effort and session-option controls.
- **Medium overhead:** creating a Codex agent eagerly starts the Codex TUI PTY, then opening the default native view starts a second Codex app-server process. This does not duplicate prompts or cloud context by itself, but it wastes a process and makes one agent row represent two runtimes. PTY launch should eventually be lazy for native-capable presets.

### 9. Context/token behavior

- The current native path sends exactly the user's turn to Codex. PowerCodeDeck does not prepend repository snapshots, repeat file content, or impose a second agent/router context layer.
- Repository exploration remains Codex's responsibility. Consequently there is no PowerCodeDeck-added context overhead, but there is also no measured pre-processing or token-reduction facility before this POC.
- No existing code reports raw candidate context, optimized context, estimated tokens, reduction ratio, local-model latency or local fallback.

## Actual execution flow

Default native Codex:

```text
User
→ React NativeChat
→ authenticated WebSocket native:open/native:input
→ WebSocket Hub
→ NativeService
→ CodexDriver
→ codex app-server --stdio
→ thread bound to repository cwd
→ JSON-RPC notifications/events
→ NativeService bounded history + Hub fan-out
→ React NativeChat
```

Explicit terminal Codex (`?terminal`):

```text
User
→ React terminal surface
→ authenticated WebSocket terminal:attach/input/resize/ack
→ Hub write gate
→ InternalPtySessionEngine
→ go-pty/ConPTY
→ codex TUI in repository cwd
→ raw PTY output + ring buffer + flow control
→ WebSocket terminal:output
→ terminal renderer
```

Agent creation occurs through `POST /api/agents` → `handlers.CreateAgent` → `AgentService.Create` → `SessionEngine.Create`; for native-capable presets this currently creates the fallback TUI before the default native page starts app-server.

## Issues

### Critical

None demonstrated by code or baseline execution.

### High

1. Native Codex repository/driver/resume identity is trusted from the WebSocket payload rather than resolved from the agent row.
2. Codex JSON-RPC requests have no response deadline and can leave a session operation waiting forever.

### Medium

1. Codex app-server stderr is discarded, weakening failure diagnosis.
2. Native-capable agent creation eagerly launches a PTY runtime even though the default view launches a separate native runtime.
3. Live process continuity ends with the PowerCodeDeck server process; only conversation identity/history can be resumed.
4. Native history is bounded by event count rather than encoded byte size, so a small number of unusually large tool results can consume significant memory.

### Low

1. `AgentService.Create` relies on process start to reject a missing working directory rather than validating and reporting the directory error explicitly.
2. Codex app-server protocol compatibility is covered by normalization/unit tests but had no dedicated live smoke test in the normal test command.

## Recommended fixes

1. Resolve native cwd and driver from `AgentService.Get(agentId)` inside the Hub; ignore client resume identity and use persisted server state.
2. Add a bounded Codex RPC deadline, remove timed-out pending calls, and surface process exit status without logging prompts, credentials or environment contents.
3. Add regression tests for server-authoritative native launch metadata and RPC timeout cleanup.
4. Preserve `CLOUD_ONLY` as the unchanged `NativeService.Send` baseline. Add Local Intelligence as an opt-in service/API rather than inserting it invisibly into every turn.
5. After the POC, make the PTY runtime lazy for native-capable agents, with an explicit transition when `?terminal` is requested.

## Baseline verification

- `go test ./...`: all Go packages passed. The Go tool emitted a non-test cache-trim warning because the sandbox cannot write `$HOME/.cache/go-build/trim.txt`.
- `pnpm build`: blocked before compilation because the installed pnpm 11.6.0 requires Node ≥22.13 while the host has Node 20.20.2 (`node:sqlite` unavailable). This is an environment/toolchain mismatch, not a source compilation result, and is not counted as PASS.

## Blocking-fix verification

The two High findings were fixed after this pre-change audit:

1. `Hub.nativeLaunchIdentity` now resolves cwd and driver from the durable agent row, rejects non-native agents, validates that the stored directory is accessible, and passes an empty resume hint so `NativeService` uses server-persisted conversation identity. The regression test is `TestNativeLaunchIdentityComesFromAgentRow`.
2. `CodexDriver.call` now has a 30-second RPC deadline and removes timed-out requests. Process waiting is independent of stdout draining, so exit diagnosis cannot deadlock pending calls. The regression test is `TestCodexCallTimesOutAndRemovesPendingRequest`.
3. Newly created agents store a validated absolute cwd through `ResolveWorkingDir`, preventing a later server launch directory from reinterpreting a relative project path.

Post-fix Codex status: **MOSTLY OPTIMIZED**. The remaining duplicate eager PTY/native runtime and server-process continuity limits are Medium architectural work, not blockers for the opt-in POC.
