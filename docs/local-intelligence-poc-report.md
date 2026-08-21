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
- **Execution model: a background job, not a request handler.** `POST /api/intelligence/run` validates
  synchronously (bad input is still a `400` inside the request), then answers `202 Accepted` with a
  `RUNNING` trace and runs on a context of the server's own. The HTTP request context is deliberately
  never handed to the service. Progress is broadcast on the `intelligence:trace` WebSocket event —
  every persisted transition, plus a closing emission that carries the generated context pack and file
  list, which are never written to the database. `POST /api/intelligence/traces/{id}/cancel` stops a run
  (`204`; `404` once it has finished); a cancelled run ends `LOCAL_REQUEST_CANCELED` and is the one
  failure that does **not** fall back to the cloud — nobody should be billed for a turn they just
  stopped. A local *timeout* still falls back, because nobody asked for that one to end.
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
POST   /api/intelligence/run                    → 202 + RUNNING trace
GET    /api/intelligence/traces
GET    /api/intelligence/traces/{id}
POST   /api/intelligence/traces/{id}/cancel     → 204 / 404
```

WebSocket (server → client): `intelligence:trace`

## 6. Remote provider validation

Remote Mac Studio E2E: **PASSED** (2026-08-21).

- Provider: `Mac Studio`
- Endpoint: `http://192.168.1.22:11434`
- Model: `qwen3-coder:30b`
- Health: all four stages pass — reachable, API healthy, model available, real generation test

```json
{
  "provider": "Mac Studio", "reachable": true, "apiHealthy": true,
  "modelAvailable": true, "generationTest": true, "latencyMs": 2340
}
```

The earlier "BLOCKED" status in this section was already stale when it was written: the provider was registered and 18 traces existed. It is replaced by the measured run below.

The unreachable path is unchanged and still returns, without a crash:

```json
{
  "provider": "invalid-remote", "reachable": false, "apiHealthy": false,
  "modelAvailable": false, "generationTest": false, "latencyMs": 6,
  "errorCode": "LOCAL_PROVIDER_UNREACHABLE"
}
```

An alive-API/missing-model path is regression-tested and returns `LOCAL_MODEL_UNAVAILABLE`, using a controlled HTTP transport rather than a live remote.

## 7. Context measurement

Measured on this repository through a real Claude native session, one fresh session per run
(a reused session carries prior turns as input tokens and would inflate every run after the first).

- Candidate files: 12
- Raw estimated tokens: **16,071** — the 64 KiB budget (§11) caps this; the 28,322 recorded earlier
  was produced under the old 256 KiB budget and is no longer reachable
- Optimized estimated tokens: **863 – 1,010** (real `qwen3-coder:30b` responses)
- Local compression: **~94%** of the candidate context
- Local latency: 17.5 – 46.2 s

`estimatedTokens` is `ceil(Unicode code points / 4)`. It is a deterministic, provider-independent
comparison estimate — not an exact tokenizer count. Ollama's `prompt_eval_count + eval_count` is
recorded separately as `localTokens`.

**Compression is not saving.** The candidate context is assembled by PowerCodeDeck and is never sent
in `CLOUD_ONLY`, which forwards the user's task byte-for-byte. What the cloud actually spent is
measured separately — see §7a.

## 7a. What the cloud actually spent (2026-08-21)

The question this section exists to answer: does `LOCAL_PREPROCESS_CLOUD` reduce what the
cloud charges? Five runs per mode, same repository, same non-destructive task
("Explain how the approval flow works in this repository"), one fresh Claude session per run.

| median of 5 | `CLOUD_ONLY` | `LOCAL_PREPROCESS_CLOUD` |
|---|---|---|
| cloud cost | **$1.1604** | **$1.2661** (+9.1%) |
| cache read | 788,104 | 820,821 |
| output tokens | 6,709 | 7,323 |
| local latency added | 0 s | 13 – 46 s |

**Verdict: no measurable saving.** The `CLOUD_ONLY` runs alone spanned $0.94 – $1.37 (±20%),
so the +9.1% median gap sits inside the noise — the two most expensive runs of the whole set
were `CLOUD_ONLY`, not hybrid. What hybrid does add, reliably, is 13-46 seconds of local
preprocessing.

### Why: the bill is (prefix × steps), and the pack touches neither

Fitting the ten runs to `cost = a·cache_read + b·output` lands within ±$0.037 (3%):

```
cost ≈ $0.96/Mtok · cache_read  +  $63/Mtok · output
```

Two measurements make that concrete:

- **The cached prefix is 24,052 tokens.** A turn that uses no tools at all ("reply with: ok")
  costs **$0.179** — 16,682 cache-creation + 24,052 cache-read tokens for a 4-token answer.
  That is the floor of any turn.
- **A real turn made 25 tool calls** (measured, `cloud_tool_calls`; the earlier
  `cache_read / prefix` estimate bounded it at 36). Every call re-reads the conversation,
  so cost tracks prefix × steps — and steps varied 2× between runs of the same task.

Non-cached input measured **22 – 28 tokens on every real run**. The context pack (≈1,010 tokens)
is therefore ~0.1% of what is billed: **the pack is not what costs money, and shrinking it
cannot be what saves money.** A 96% "compression" of a candidate context that `CLOUD_ONLY`
never sends changes nothing on the invoice.

Worse, the pack is **additive by construction**. The cloud wrapper says "verify it against the
repository, and inspect additional files whenever needed", and the local prompt is told "do not
claim the cloud agent is restricted to these files". Both are deliberate correctness choices —
a 30B model's pack will miss files, and a cloud agent forbidden to look would answer wrongly.
The cost is that the pack adds leads to follow instead of removing work: hybrid's cache reads
did not drop.

### What this means for the plan

Per the measurement plan's own criterion ("무승부/패배 = 스펙 §5 전체가 재검토 대상"), the LLM
axis as wired does not pay for itself:

- **`LOCAL_ONLY` stays.** It is the only proven saving, and it is total: no cloud turn, $1.16 → $0.
  Its limit is the operation allow-list, and its open risk is that a *wrong* local answer looks
  exactly like a right one — there is no correctness check today, only a structural one
  (`validContextPack` checks headings).
- **`LOCAL_PREPROCESS_CLOUD` should not be a default.** Measured benefit zero, measured cost
  13-46 s per turn plus the deadline/fallback/cancel machinery it requires.
- **The lever is step count, not prompt size.** 25 steps × a growing conversation is the bill.
  Trimming the prefix is capped low: of the 24k, CLAUDE.md is ~440 tokens and PowerCodeDeck's own
  additions are small — most of it is Claude Code's own system prompt and built-in tool
  definitions, which PowerCodeDeck does not control.
- **The one hybrid variant still worth testing** is substitutive rather than advisory: answer from
  the pack and open at most N more files. That trades correctness for cost explicitly, so it
  should be scoped to tasks where the pack is provably sufficient. Untested as of this writing.

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

- Local answer *quality* has never been measured. `validContextPack` checks that seven headings are
  present, not that the content is right, and `LOCAL_ONLY` output has no correctness check at all.
  This is the open risk of routing more work to the local model.
- The comparison covers one repository, one task, one cloud model. It shows that this wiring does not
  save; it does not show that no wiring could.
- A native result proves turn completion, not that tests passed or the requested code change is semantically correct.
- Traces now record what the cloud spent (cost, input/output, cache read, cache creation) and how many
  tool calls the turn made. Codex still reports no usage at all, so its traces carry
  `cloud_usage_known=false` rather than a fabricated zero.
- Only Ollama is implemented. OpenAI-compatible, MLX and vLLM are future provider additions.
- Context collection requires a Git repository and is capped at 24 files, 12 KiB per file and 64 KiB total.
  The budget was cut from 256 KiB so a 30B local model can finish inside common reverse-proxy deadlines.
- The eager PTY/native double-runtime remains to be addressed separately.
- Local latency itself is unchanged: 38-59 seconds on a successful run. Moving the run off the request
  removes the failure it caused, not the wait. Verified end-to-end against the real binary: a run whose
  HTTP client disconnects immediately still completes (`SUCCESS`, 6,018 ms local latency, 3,944 → 28
  estimated tokens), an explicit cancel ends it as `LOCAL_REQUEST_CANCELED` with no cloud dispatch, and
  an unreachable provider still ends as `LOCAL_PROVIDER_UNREACHABLE`.

## 12. Next recommended milestone

The milestone this section used to describe is **done**: a real remote provider passes all four
health stages, and the same task was run five times per mode through a real Claude session. The
result is in §7a — hybrid preprocessing shows no measurable saving.

So the next work is *not* more of this axis:

1. **Demote `LOCAL_PREPROCESS_CLOUD` from a default** to an experiment. It costs 13-46 s per turn
   for a benefit that measurement cannot find.
2. **Measure local answer quality** before routing anything to `LOCAL_ONLY` automatically. Today the
   human picks the mode, and that choice is the only safeguard against a confidently wrong local
   answer; automating routing removes it while nothing checks correctness.
3. **Attack step count, not prompt size** — 25 tool calls per question, varying 2× run to run, is
   what the bill is made of. The one hybrid variant still worth a test is substitutive ("answer from
   the pack, open at most N more files"), scoped to tasks where the pack is provably sufficient.

## Acceptance status

| Test | Status | Evidence |
|---|---|---|
| Remote Local LLM on another machine | **PASS** | `Mac Studio` / `192.168.1.22` / `qwen3-coder:30b`, four health stages, 2,340 ms (§6) |
| Invalid endpoint | PASS | `LOCAL_PROVIDER_UNREACHABLE`, no crash, actual API E2E |
| Live endpoint, missing model | IMPLEMENTED / E2E BLOCKED | staged regression test; a live missing-model run was not performed |
| Context pack from actual repository | **PASS** | 16,071 raw → 636-1,010 optimized estimated tokens from real `qwen3-coder:30b` responses |
| Raw > optimized | **PASS** | ~94% compression, 5 hybrid runs (§7) |
| Existing CLOUD_ONLY | PASS | unchanged-prompt regression + real Codex driver live smoke |
| LOCAL_PREPROCESS_CLOUD | **PASS (works) / FAILS ITS PURPOSE** | 5 real runs complete end to end; median cloud cost +9.1% vs `CLOUD_ONLY`, inside that mode's own ±20% spread — no saving (§7a) |
| Local failure fallback | PASS (regression) / E2E PASS for timeout | `TestStartStillHonorsLocalTimeout`; a live dead provider ends `LOCAL_PROVIDER_UNREACHABLE` |
| Cloud spend is measured, not inferred | **PASS** | `cloud_cost_usd`, `cloud_input/output_tokens`, `cloud_cache_read/creation_tokens`, `cloud_tool_calls` on every trace; Codex reports none and says so |
