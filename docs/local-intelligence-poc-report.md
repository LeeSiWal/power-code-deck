# CURRENT CODEX STATUS

**MOSTLY OPTIMIZED**

PowerCodeDeck's real Codex path uses `codex app-server --stdio` with the correct repository cwd, inherited user/Codex environment, structured event replay, approval handling, thread resume and explicit process lifecycle. The audit found and fixed both High issues: client-authoritative native launch context and unbounded app-server RPC waits. A real PowerCodeDeck `CodexDriver` live test completed successfully against authenticated Codex.

It is not rated fully optimized because native-capable agent creation still eagerly starts a fallback Codex TUI PTY before the default view launches a second app-server process, and live processes do not survive a PowerCodeDeck server restart.

## 1. Current Codex architecture

```text
User
→ React NativeChat
→ authenticated WebSocket native:open/native:input
→ Hub (server-authoritative agent/cwd/driver lookup)
→ NativeService (conversation/config/history lifecycle)
→ CodexDriver
→ codex app-server --stdio
→ repository cwd
→ JSON-RPC events/approvals/result
→ NativeService bounded history + Hub fan-out
→ React NativeChat
```

The explicit `?terminal` fallback instead uses Hub → `InternalPtySessionEngine` → go-pty/ConPTY → Codex TUI, with raw input, resize, flow control, UTF-8 boundary preservation, terminal-mode reconstruction and scrollback replay.

## 2. Codex optimization verdict

**MOSTLY OPTIMIZED** after fixes. See [codex-optimization-audit.md](codex-optimization-audit.md) for function-level evidence and the pre-fix `PARTIALLY OPTIMIZED` verdict.

## 3. Problems discovered

- Critical: none demonstrated.
- High, fixed: a browser could supply cwd/driver/resume identity for `native:open` instead of the server resolving the agent row.
- High, fixed: app-server RPC calls could wait forever.
- Medium, open: native-capable agent creation eagerly launches both fallback PTY and native runtimes.
- Medium, open: server restart ends in-flight native and PTY processes.
- Medium, open: app-server stderr is drained but not exposed because it may contain prompt/config material; exit and timeout states are exposed safely.
- Low, fixed: newly created agent cwd is now validated and stored as an absolute path.

## 4. Fixes made

- Added server-authoritative native launch resolution and regression coverage.
- Added stable absolute working-directory validation.
- Added bounded Codex RPC waits, timeout cleanup and independent process waiting.
- Added an opt-in live Codex driver smoke test (`PCD_LIVE_CODEX=1`) so mock/unit success is not mistaken for process/protocol success.

## 5. Local Intelligence implementation

The POC is additive; existing chat input is not intercepted.

- Runtime SQLite provider registry: name, type, base URL, model, timeout and enabled state.
- Ollama implementation with explicit remote URLs; localhost is not a default.
- Settings UI for saving/editing a provider and viewing each health stage.
- Health sequence: TCP reachability → `/api/tags` → configured model match → real `/api/generate` → measured latency.
- Deterministic repository candidate builder using `git status`, `git diff --stat`, `git log`, `git ls-files`, task/path terms and bounded file reads.
- Required context-pack sections: TASK, FILES, SYMBOLS, CALL FLOW, LIKELY CHANGE POINTS, TESTS and UNCERTAINTIES.
- Execution modes through `POST /api/intelligence/run`:
  - `CLOUD_ONLY`: sends the original task unchanged to the existing native session.
  - `LOCAL_PREPROCESS_CLOUD`: builds context, calls Ollama, validates reduction, then sends an advisory pack plus the original task. Codex may inspect any additional file.
  - `LOCAL_ONLY`: restricted to `summarize`, `explain`, `classify`, `log_analysis`, and `repository_question`.
- Hybrid local failure explicitly records the reason and falls back to the unchanged cloud task.
- SQLite traces store only state transitions and measurements. Prompts, repository contents, responses, credentials and environment values are not persisted.
- A native `result` event updates a dispatched trace to `CLOUD_COMPLETED` (or `CLOUD_COMPLETED_WITH_FALLBACK`). This proves a real turn boundary, not semantic task correctness.

Protected API surface:

```text
GET    /api/intelligence/providers
PUT    /api/intelligence/providers/{name}
DELETE /api/intelligence/providers/{name}
POST   /api/intelligence/providers/{name}/health
POST   /api/intelligence/run
GET    /api/intelligence/traces
GET    /api/intelligence/traces/{id}
```

## 6. Remote provider validation

Remote Mac Studio E2E: **BLOCKED**

- Provider: not supplied/configured in this environment
- Endpoint: not available
- Model: not available
- Health: not tested against a real remote Ollama
- Latency: not available

The actual API E2E used an intentionally invalid remote hostname and returned, without a crash:

```json
{
  "provider": "invalid-remote",
  "reachable": false,
  "apiHealthy": false,
  "modelAvailable": false,
  "generationTest": false,
  "latencyMs": 6,
  "errorCode": "LOCAL_PROVIDER_UNREACHABLE"
}
```

An alive-API/missing-model path is regression-tested and returns `LOCAL_MODEL_UNAVAILABLE`, but that test uses a controlled HTTP transport and is not claimed as remote E2E evidence.

## 7. Context measurement

Actual PowerCodeDeck repository measurement:

- Candidate files: 12
- Raw bytes: 114,496
- Raw estimated tokens: 28,322
- Optimized estimated tokens: **BLOCKED — no real local provider response**
- Reduction: **BLOCKED — no real local provider response**

`estimatedTokens` is `ceil(Unicode code points / 4)`. It is a deterministic provider-independent comparison estimate, not an exact Codex or Ollama tokenizer count. Ollama's returned `prompt_eval_count + eval_count` is recorded separately as `localTokens`.

## 8. Codex regression

- Existing `NativeService.Send` remains the default UI path.
- `CLOUD_ONLY` is explicitly tested to pass the original task byte-for-byte to the native driver.
- Native cwd/driver now come from the server row, while model/mode/effort remain persisted session choices.
- PTY/session, detach/reconnect, terminal rendering, authentication, file and PWA paths were not replaced.

## 9. E2E evidence

Real Codex CLI smoke:

- Codex: 0.146.0
- Authentication: ChatGPT login present
- Repository: `/home/siwal/code/power-code-deck`
- Sandbox: read-only
- Result: `PCD_CODEX_SMOKE_OK`
- Reported tokens: 4,423

Real PowerCodeDeck driver smoke:

- Path: `CodexDriver.Start` → initialize → thread start → turn start → assistant event → result event
- Duration (final run): 5.94 s
- Result: PASS (`PCD_CODEX_DRIVER_OK`)

Actual temporary-server Local Intelligence trace:

- Trace: `PCD-F519DD3AD4`
- Repository scan: 12 candidate files, 31,491 raw estimated tokens
- Provider request: attempted
- Failure: `LOCAL_PROVIDER_UNREACHABLE`
- Fallback: false because this run deliberately used `LOCAL_ONLY`
- Process crash: none

No trace is presented for a successful local response or hybrid completion because no reachable remote Local LLM was supplied.

## 10. Tests

Passed:

```text
GOCACHE=/tmp/pcd-go-cache go test ./...
npm run build
GOCACHE=/tmp/pcd-go-cache go build -o /tmp/pcd-poc .
PCD_LIVE_CODEX=1 PCD_LIVE_CODEX_CWD=/home/siwal/code/power-code-deck \
  go test ./services -run TestCodexDriverLiveSmoke -v -count=1
PCD_POC_REPO=/home/siwal/code/power-code-deck \
  go test ./services -run TestLiveRepositoryCandidateMeasurement -v
```

The initial `pnpm build` attempt was blocked by host pnpm 11 requiring Node ≥22.13 while the host has Node 20.20.2. The equivalent project `npm run build` used the existing dependencies and passed TypeScript plus Vite production compilation.

## 11. Known limitations

- Real remote Ollama inference, optimized context size and reduction ratio are unverified in this environment.
- `LOCAL_PREPROCESS_CLOUD` real E2E and real local-failure→cloud-completion E2E remain unverified without a reachable local endpoint and an active native session. The fallback policy itself has regression coverage.
- A native result proves turn completion, not that tests passed or the requested code change is semantically correct.
- Traces allow baseline/hybrid comparison but do not yet capture Codex's hidden internal context/token usage or post-turn changed-file snapshots.
- Only Ollama is implemented. OpenAI-compatible, MLX and vLLM are future provider additions.
- Context collection requires a Git repository and is capped at 24 files, 24 KiB per file and 256 KiB total.
- The eager PTY/native double-runtime remains to be addressed separately.

## 12. Next recommended milestone

Do not add automatic routing yet. First configure one real remote Ollama provider, run health until all four stages pass, then execute the same non-destructive repository task through `CLOUD_ONLY` and `LOCAL_PREPROCESS_CLOUD`. Promote the POC only if the local trace contains a real response, raw > optimized context, the Codex turn completes in both runs, and the task result remains equivalent. After that proof, the next useful work is manual routing in NativeChat and provider expansion; cost tracking and automatic routing should wait.

## Acceptance status

| Test | Status | Evidence |
|---|---|---|
| Remote Local LLM on another machine | BLOCKED | No endpoint supplied |
| Invalid endpoint | PASS | `LOCAL_PROVIDER_UNREACHABLE`, no crash, actual API E2E |
| Live endpoint, missing model | IMPLEMENTED / E2E BLOCKED | staged regression test; no real provider |
| Context pack from actual repository | INPUT PASS / OUTPUT BLOCKED | actual repository scan passed; local generation unavailable |
| Raw > optimized | BLOCKED | optimized result unavailable |
| Existing CLOUD_ONLY | PASS | unchanged-prompt regression + real Codex driver live smoke |
| LOCAL_PREPROCESS_CLOUD | IMPLEMENTED / E2E BLOCKED | no real local inference endpoint |
| Local failure fallback | PASS (regression) / E2E BLOCKED | fallback trace and unchanged cloud prompt tested; no real remote failure + completed Codex turn run |
