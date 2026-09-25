# Multi-model routing, escalation and handoff (PowerCodeDeck 2.0)

Branch `codex/powercodedeck-2-runtime-foundation`, started from `2afb980`
(2026-09-25). Evidence and the provider support matrix:
[multimodel-routing-evidence.md](multimodel-routing-evidence.md).

## What it does

Inside one v2 Run (the *logical session*), each attempt can run on a different
**execution profile** — adapter + model + effort + account/billing path. Routing
picks the profile (or the user does), records why, carries the previous
attempt's file changes and a provenance-labelled handoff to the next executor,
and applies bounded escalation only on verified evidence (failed host checks).
Existing checks (`diff_check`, `.powercodedeck/checks.json`, independent review)
remain the only success signal.

Not done, by design: transparent per-token proxying, moving hidden reasoning/KV
state between vendors, auth workarounds, reuse of consumer OAuth, account pooling.

## Architecture (reused vs new)

| Piece | Where | Notes |
| --- | --- | --- |
| Policy core (tiers, statuses, profiles, filter, rules, selection, escalation, handoff, state machine, store, local endpoints) | `server/internal/routing` | No process, no HTTP, no credentials. |
| Discovery | `routing/probe.go`, `services/routing_runner.go` | Only `claude auth status`, `codex login status`, `codex app-server` `model/list`, `agy --help`/`models`, `--version`; Gemini reads only `selectedType` from settings.json. Env var **names** that can switch billing are reported. |
| Run ↔ routing | `server/internal/routing/runroute` | Coordinator: decide → record → handoff → `Worker.StartWith` → classify result → next action. |
| Worker additions | `internal/orchestration/launch.go`, `worker.go` | `StartWith(run, Launch)` (model/effort per attempt), checkpoint patch + manifest (tracked + untracked, sensitive untracked excluded, via a temporary index), inheritance into the next worktree, `handoff.md`, quiescence scan, attempt report, `Interrupt`. Legacy `Start` records exactly what it did before. |
| Drivers | `services/run_providers.go`, `codex_driver.go`, `providers/antigravity` | Model/effort pass-through only (`turn/start.effort`, agy `--effort`). |
| HTTP | `handlers/routing.go` | Under the existing authenticated `/api` router, only with `PCD_V2_ENABLED=1`. |
| UI | `client/src/components/runs/RoutingPanel.tsx`, `settings/RoutingMatrix.tsx` | Runs detail panel + Settings matrix. |
| RouteLLM | `tools/routellm-sidecar/` | Optional Python loopback sidecar; real `Controller.route`. |

### Status dimensions

`installation`, `auth`, `entitlement`, `billing`, `technical`, `policy`,
`health` (per adapter) and `quality` (per profile and task kind) are separate.
Unknown values stay `unknown`. Each exclusion has its own reason code
(`not_installed`, `not_authenticated`, `consumer_auth_discontinued`,
`model_not_listed`, `effort_unsupported`, `billing_unknown`,
`billing_not_allowed`, `extra_usage_risk`, `inherited_billing_env`,
`not_implemented`, `technical_unsupported`, `policy_review_required`,
`policy_blocked`, `rate_limited`, `quality_unverified`, `tier_not_mapped`,
`needs_tools`, `context_too_small`, `pinned_elsewhere`, …).
Automatic-only gates (unknown billing, extra-usage risk, policy review) become
**warnings** on an explicit manual choice; known metered/credit paths stay
blocked until opted in.

A CLI version change is flagged (`needsRevalidation`) until someone presses
"확인함" in Settings (`POST /api/v2/routing/adapters/{id}/validated`), which
also drops cached router answers. This happened for real during development:
`agy` auto-updated from 1.1.28 to 1.2.11.

### Tiers are policy, not rankings

`VERY_EASY … ULTRA` are the minimum level a task needs. A profile lists the
tiers it serves; several tiers on one profile are shown as-is; generated
"CLI default" profiles claim no tiers and are manual-only. Nothing invents a
model or effort to fill a tier. If no allowed profile serves the floor, nothing
runs and the Run waits with the reasons (no silent downgrade).

### Selection

1. Filter every profile through every gate (all reasons kept).
2. Rule floor (`RuleTier`): keywords/size/files (Korean and English), capped for
   text tasks, raised above the tier of a prior **quality** failure only.
3. Among eligible profiles ≥ floor: lowest adequate tier, + stickiness for the
   current profile, + small bonus for verified quality.
4. RouteLLM: consulted only for a configured `(weak, strong)` pair whose weak
   side is the rule choice; skipped on escalation; single candidate → no call.
   **Its verdict changes the choice only if the pair's `calibration` is
   `evaluated:<eval-id>`**; otherwise it is recorded as advisory. See
   measurements below for why that is the default.

### Failure classes and actions

| Class | Source | Action (Auto + `autoEscalate`) |
| --- | --- | --- |
| quality | provider success + failed host checks, quiescence verified | escalate (floor above failed tier) |
| availability | 429 / quota / overloaded in provider diagnostics | failover; the whole quota bucket is cooled (Retry-After or 5 min) |
| auth / entitlement | 401 / not logged in / model access | wait for the user; bucket marked |
| environment | failure before the provider ran (git, dirty source, missing binary) | stop (`blocked_environment`) |
| permission | denials / safety refusal | stop — never routed around |
| unknown side effect | interrupted/unknown outcome or unverified quiescence | `reconcile`; never automatic |
| canceled | user cancel | stop; never resumed |

Limits: `maxSwitchesPerRun`, `maxAttemptsPerRun`, `maxRunMinutes`; pins make
any profile change wait for the user. Shadow and Manual never continue alone.

### Switching state machine

`idle → routing → (handoff) → running → quiescing → checkpointed → routing …`
plus `waiting_policy`, `waiting_billing`, `waiting_user`, `blocked_environment`,
`reconcile`, `failed`, `canceled`, `succeeded`. Every transition bumps an epoch
and is logged in the same transaction; a router answer computed under an old
epoch, a duplicate/late attempt report, or a start racing a cancel is rejected.
One worker slot + one unfinished routing attempt per Run = one writer. After an
attempt, PowerCodeDeck scans `/proc/*/cwd` until no process remains in the
attempt workspace; only then is a checkpoint taken and an automatic next
attempt allowed. Where no scan exists (macOS/Windows) the attempt reports
"unverified" and goes to `reconcile`. Server restart moves in-flight routing
states to `reconcile` and closes unfinished attempts as unknown.

### Handoff

Each attempt runs in a fresh worktree at the pinned base, so native sessions are
**not** resumed across attempts (bindings are recorded with `resumable=false`;
`PlanContinuation` would choose native resume only for an exact
adapter+account+workspace binding). Instead the next attempt gets:

- the previous attempt's `checkpoint.patch` applied to its worktree (fails the
  attempt if it does not apply cleanly);
- `handoff.md`: verbatim goal, extracted constraints, file manifest with
  hashes (sensitive files by path only), excluded sensitive untracked files,
  failing check output (redacted), the previous model's final message marked
  untrusted, next action, unchanged permission scope.

The mandatory part must fit ¾ of the target context (or 24 KiB when unknown);
otherwise the Run waits (`context_overflow`). Optional evidence is trimmed
first. No LLM summarizes anything.

## Lightweight LLM decider (`strategy: "commercial_llm"`) — added 2026-09-25

A second strategy next to `rules` and `routellm`. PCD asks a signed-in, allowed
profile to *propose* one executor among candidates the rules already allowed.
It does not code, run tools, explore the repository, spawn agents, change
requirements, policy, budgets or endpoints, or launch other CLIs. PCD
validates the answer and launches.

### Roles

Profiles carry `roles`: `decider`, `executor`, `reviewer`, `planner`,
`summarizer`. Empty = `executor`+`reviewer` (old meaning); `decider` is never
implied. One profile may hold several roles, and one subscription can supply all of
them — roles never require another service. Auxiliary roles have their own
context and permissions: the decider runs in an empty temporary directory
with no write access to the workspace; reviewers/planners run read-only
(Claude `--permission-mode plan`, Codex read-only sandbox, Antigravity
`plan`+`--sandbox`).

### When the decider is NOT called (in this order)

1. `strategy` is not `commercial_llm`, or mode is Off.
2. No executor passes the gates → nothing runs; reasons are shown.
3. Manual choice or profile pin → launch directly.
4. Only one *distinct* executor (aliases with the same adapter/model/effort/
   quota bucket count once) → launch directly.
5. Availability failover with a rule-determined fallback.
6. Rules are clear: no tie between distinct candidates at the top rule score.
7. A valid cached verdict for the same question (cleared on refresh,
   validation, any health/auth change, config change).
8. No allowed decider profile. If all candidates are local, no commercial
   decider is ever called (`local_only_no_commercial_decider`).
9. Shadow mode: only with per-Run approval ("이 Run에서 상용 판단 Shadow
   허용") and within `decider.shadowMaxCallsPerDay`.

The user can force one consultation with "판단 다시 요청 후 실행". Skips are
shown as optimizations, not errors.

### Choosing the decider (deterministic, never by another model)

Pinned `decider.profile` if eligible → otherwise eligible decider-role profiles
with `quality.decide.status = verified` first, then by measured p50 latency from
past calls (unmeasured last). Gates: install/auth/billing/policy/health, the
optional `decider.adapters` allowlist, and tool control:

| Adapter | One-shot call | Tool-free? |
| --- | --- | --- |
| Claude | `claude -p --output-format json --tools "" --strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources "" --disable-slash-commands --no-session-persistence --json-schema …` (prompt on stdin) | built-in tools, MCP, user/project/local settings (hooks, plugins) and skills off by official flags; **managed policy settings cannot be excluded**. Flags accepted by 2.1.239/2.1.282 (verified by an unauthenticated run); a successful live call is not verified. |
| Codex | `codex exec --json --sandbox read-only --ephemeral --ignore-user-config --skip-git-repo-check -C <empty dir> --output-schema <file> -` | **No** — the shell tool stays (read-only sandbox). Excluded unless `decider.allowReadOnlyShell: true`. |
| Local | chat completion to an allowlisted endpoint | yes; only with `quality.decide = verified` |
| Antigravity / Gemini | not implemented as decider | — |

Only one decider fallback, and only the explicitly configured
`decider.fallback`. Failures never escalate to a more expensive decider.

### Contract

Input (built by code): request kind `pcd.routing.decision` (a Go constant — user
text cannot set it), goal (redacted, ≤4 KB, marked as data), stage,
constraints, files, rule floor, current profile, candidates with measured
capabilities and per-kind quality (`unverified` stated as such), switch/attempt
budget, evidence items with refs. Output: one JSON object
`{"action","profile_id","reason_code","evidence_refs","needs"}` with actions
`dispatch | keep_current | need_context | abstain`, closed `reason_code` and
`needs` sets, profile ids and evidence refs that exist in the request,
`MaxOutputBytes` (default 2048). Unknown fields (commands, endpoints, budgets,
policy flags, confidence) → rejected. Bounds: `maxFormatRetries` (default 1),
`maxContextRounds` (default 1; PCD adds only evidence it already recorded),
per-call timeout (default 60 s) and 2× that overall.

A valid verdict **changes the executor only in Auto mode and only when the
decider profile has `quality.decide.status: "verified"`**; otherwise it is
recorded as advisory. No profile ships verified — start in Shadow on chosen
Runs. After deciding, the selected profile is re-checked right before launch
(auth/quota/policy may have changed); a stale epoch (cancel, settings change,
concurrent start) discards the decision.

### Failures

Decider failures are recorded as decider calls, not executor failures. A decider
429 cools the whole quota bucket (executor profiles on the same account too)
and the rule decision is recomputed; an auth failure invalidates discovery.
Invalid output/timeout only affect decider stats.

### Hidden calls now under the same policy

The v2 reviewer and planner used to be a fixed Antigravity execution for every
Run. They now resolve through the same gates: role pin → any allowed profile
with that role (a different provider first) → the Run's own provider (the user
chose it for this Run). Antigravity is reached only for Runs the user started on
Antigravity. When nothing is allowed, the review check fails with
`reviewer_unavailable` → class `review_blocked` → waits for the user (no
escalation, never success). Same-provider review is labelled "fresh context —
not a cross-vendor review". Reviewers still do not have OS-enforced read-only
file access in every adapter: Codex's sandbox enforces it, Claude plan mode and
Antigravity plan are CLI policy, and the existing post-review fingerprint
check detects mutations but does not prevent them.

### Usage by role

`GET /api/v2/runs/{id}/routing` returns `usage` for decision, execution (first
attempt), retry (later attempts), review and local, plus `commercialUsage`
(everything except local). Each reported value is counted once; `complete` is
false when any call in a role did not report usage. These are provider-reported
tokens, not bills; cache-read semantics differ per provider (Claude reports it
separately, Codex inside input), so cache reads are never added to input.

### Examples for the eight Claude/Codex/Local combinations

`docs/examples/routing/decider-*.json` (validated by
`TestDeciderExamplesEightCombinations`; "none" = any file on a host with nothing
installed):

| Claude | Codex | Local | File | Behavior (MEDIUM code task) |
| --- | --- | --- | --- | --- |
| – | – | – | any | no candidates, reasons shown, nothing enabled |
| ✓ | – | – | `decider-claude-only.json` | rules clear → Claude executes, no decider call; Claude reviews (same provider) |
| – | ✓ | – | `decider-codex-only.json` | rules clear → Codex executes; Codex decider allowed only because `allowReadOnlyShell` |
| ✓ | ✓ | – | `decider-claude-codex.json` | tie between services → one Claude decider call (Codex decider excluded: shell) |
| – | – | ✓ | `decider-local-only.json` | code task: no executor (text-only local); never a commercial call |
| ✓ | – | ✓ | `decider-claude-local.json` | as Claude only; local is a text summarizer, not a code executor |
| – | ✓ | ✓ | `decider-codex-local.json` | as Codex only |
| ✓ | ✓ | ✓ | `decider-all.json` | as Claude+Codex |

### Turning it off

Set `strategy` to `rules` (or `routellm`) in routing.json, or per Run in the
panel ("판단 전략"). Off mode makes no decider call; spend and policy gates stay
in force in every mode.

## Configuration

`routing.json` path: `PCD_ROUTING_CONFIG`, else `<user config dir>/powercodedeck/routing.json`
(Linux: `~/.config/powercodedeck/routing.json`). Absent file = mode `off`.
An invalid file keeps routing off and shows the error in the UI. The browser
can change only a Run's mode and pins; spend opt-ins, profiles, endpoints and
the sidecar URL are server-side only.

Examples (all validated by `TestDocumentedExamples`) in
[`docs/examples/routing/`](../examples/routing):

| File | Scenario |
| --- | --- |
| `codex-only.json` | one subscription, two effort profiles |
| `claude-only-single-profile.json` | one profile serving every tier |
| `codex-plus-claude.json` | two subscriptions, Auto with bounded escalation, gated Fable profile, advisory RouteLLM pair |
| `all-with-local.json` | Codex + Claude + Antigravity (manual only) + LAN Ollama (text only, unverified) |
| `local-only.json` | local model only (text tasks; code Runs are refused) |
| `google-only-gated.json` | Antigravity only: Auto selects nothing (policy review + unknown billing); manual use still possible with warnings |

Spend policy keys (all default `false`): `allowApiMetered`, `allowPlanCredits`,
`allowPurchasedCredits`, `allowPromoCredits`, `allowExtraUsageRisk`,
`allowUnknownBilling`; `budgetKind` is `best_effort` or `soft` — hard caps are
rejected because stopping a process cannot reverse usage already recorded.

Local endpoints: `kind` `ollama` or `openai`; loopback is always allowed,
private addresses need `allowPrivate`, plain HTTP to a non-loopback host needs
`allowInsecureHttp`; link-local and cloud-metadata addresses are refused at
dial time (after DNS); redirects are not followed; no proxy; optional bearer
token from `tokenEnv`. A local endpoint is labelled with its host, never
"offline".

## Using it

1. Start the server with `PCD_V2_ENABLED=1` (and optionally `PCD_ROUTING_CONFIG`).
2. Settings → "모델 라우팅 · 실행기 상태": installed/auth/billing/technical/
   health per CLI, per-profile manual and automatic eligibility with reasons,
   policy scope status. "다시 확인" re-probes after you log in with a CLI.
3. Runs → a single-task Run → "모델 라우팅" panel:
   - Mode: Off (legacy start button), Manual, Shadow, Auto.
   - Provider/profile pin.
   - "미리보기" shows the decision without recording it.
   - Start with a chosen profile or automatic selection.
   - While running: "다음 경계에서 전환" (next attempt) or "지금 전환"
     (interrupts; the next attempt starts only after verified quiescence and
     inherits the partial changes).
   - The decision (rule floor, source, reasons, RouteLLM score/checkpoint), the
     excluded candidates and one ordered history of transitions and attempts
     (requested vs observed model, class, action, timing breakdown, reported
     usage with scope; unreported usage shows "미보고", never 0).
   - "이 Run의 라우팅 기록 삭제" removes routing history for the Run.
   Planned (multi-task) Runs are not routed yet.

API (authenticated): `GET /api/v2/routing`, `POST /api/v2/routing/refresh`,
`POST /api/v2/routing/adapters/{adapter}/validated`,
`GET|PUT|DELETE /api/v2/runs/{id}/routing`, `GET /api/v2/runs/{id}/routing/preview?profile=`,
`POST /api/v2/runs/{id}/routing/start {"profile":""}`,
`POST /api/v2/runs/{id}/routing/switch {"profile":"…","when":"boundary|now"}`.

## RouteLLM sidecar

```bash
cd tools/routellm-sidecar
uv venv --python 3.12 .venv
VIRTUAL_ENV=.venv uv pip install --index-strategy unsafe-best-match --extra-index-url https://download.pytorch.org/whl/cpu -r requirements.txt
.venv/bin/python sidecar.py --allow-download --self-test   # first run downloads the BERT checkpoint (~1.1 GB)
.venv/bin/python sidecar.py --port 8765                     # afterwards runs offline
```

- Loads `routellm/bert_gpt4_augmented` through the real `Controller`; each
  request calls `Controller.route` once and reads the score from the same
  forward pass. `mf`/`sw_ranking` (OpenAI embeddings) are refused; a
  placeholder `OPENAI_API_KEY` + unroutable `OPENAI_BASE_URL` are set because
  routellm 0.2.0 builds an OpenAI client at import.
- `--device auto|cpu|cuda|mps`: upstream `BERTRouter` never moves tensors and
  calls `.numpy()` directly, so non-CPU devices use a device-correct copy of
  the same formula. Only CPU was run here.
- Binds 127.0.0.1; another host requires `--token-env`. Prompt text is never
  logged. PowerCodeDeck side: 1.5 s timeout, circuit breaker (3 failures →
  60 s), strict response schema, choice/score consistency check, cache keyed
  by config fingerprint + pair + threshold + text, cleared on refresh,
  validation, 429.
- Measured on this host (CPU, 16 cores): import+load ≈ 14 s wall, model load
  1.6–1.7 s, RSS ≈ 1.4 GB, per request p50 45 ms / p95 90 ms (n = 18, warm).

## Measurements (2026-09-25)

Pre-registered before running: RouteLLM may drive code-task Auto only if its
score separates hard (HIGH/ULTRA) from easy (VERY_EASY/EASY) tasks with
AUC ≥ 0.75 on `server/internal/routing/testdata/router_eval.jsonl`
(18 tasks, Korean and English, labels written before any score was seen).

| Measure | Result |
| --- | --- |
| RouteLLM BERT hard-vs-easy AUC | **0.458** (n_hard 6, n_easy 8) → not eligible; pairs stay advisory |
| Rule floor exact tier | 13/18, under-routed 2, over-routed 3 — **biased**: the same author wrote labels and rules |
| Router latency | p50 45 ms, p95 90 ms |

Not measured: task success rates, commercial token/usage savings, end-to-end
completion time with real models, local-model quality. No comparison run
(A = fixed commercial profile, B = rules, C = RouteLLM) with real providers was
performed; it needs authenticated CLIs and consumes the user's quota.

## Validation commands

```bash
cd server
go test ./...                                         # default CI: no provider, no network
go test -race ./internal/routing/... ./internal/orchestration/ ./handlers/
PCD_ROUTING_PROBE_LIVE=1 go test ./services -run TestRoutingProbeLive -v            # real CLI status commands, no inference
PCD_ROUTELLM_URL=http://127.0.0.1:8765 go test ./internal/routing -run TestRouterEvalLive -v
PCD_ROUTELLM_URL=http://127.0.0.1:8765 go test ./internal/routing/runroute -run TestCoordinatorWithRealSidecar -v
cd ../tools/routellm-sidecar && python3 -m unittest test_sidecar
cd ../../client && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/vite build
```

A/B/C comparison procedure (not yet run): for each task in a fixed set, create
three Runs from the same commit with identical prompts — A: Manual with the
chosen commercial profile; B: Auto with `routellm.enabled=false`; C: Auto with
the pair marked `evaluated:<id>` — then compare Run success (host checks),
`GET /api/v2/runs/{id}/routing` attempt timings and reported usage. Keep each
Run in its own worktree (default) and do not change criteria after viewing.

## Turning it off / rollback

- Per Run: mode Off. Globally: delete or rename `routing.json` (mode off) — the
  legacy Start button and legacy artifacts are unchanged.
- Without `PCD_V2_ENABLED=1` none of the routes or tables are touched.
- Tables are additive (`v2_routing_runs`, `_decisions`, `_transitions`,
  `_bindings`, `_attempts`, `_versions`); reverting the code leaves them unused.
  Routed attempts add artifacts `checkpoint.patch`, `checkpoint.json`,
  `handoff`, `inherited_checkpoint` under the existing work root.
- The sidecar is a separate optional process; stopping it only makes pairs
  advisory-unavailable (rule choice kept).

## Test coverage map (prompt §15)

| # | Status | Where |
| --- | --- | --- |
| 1, 3, 4, 6, 7, 9, 21, 22, 23 | fixture tests pass | `routing_test.go`, `examples_test.go` |
| 2, 5 | fixtures pass; live probe showed `consumer_auth_discontinued` on this host | `TestGeminiConsumerAuthDistinct`, `TestRoutingProbeLive` |
| 8 | fixture + live `model/list` (6 models); `turn/start.effort` has **no** fake-app-server test | `TestCodexModelListAndEffort` |
| 10 | existing Antigravity wire/denial/cumulative-usage fixtures + new `--effort` argv check | `providers/antigravity` |
| 11 | model/effort per attempt: pass; native resume across attempts: not used by design (fresh worktrees) | `TestRoutedAttemptsCarryWorkAcrossProviders`, `TestPlanContinuation` |
| 12, 13, 14 | fake-process pass (Codex→Claude with inherited edits and handoff); return-to-provider delta only unit-tested | `runroute` tests, `TestHandoffBundle` |
| 15, 24, 27 | fake-process pass: switch-now, single writer, cancel never restarts; UI pin/switch buttons exercised only partly in the browser | `TestSwitchNowKeepsOneWriter`, `TestCancelNeverRestarts` |
| 16, 28 | restart recovery + reopen DB pass; network drop mid-attempt not specifically tested | `TestStoreFencingAndRecovery` |
| 17 | pass | `TestStaleResultIgnored`, store duplicate test |
| 18, 19 | pass (overflow, trimming, secrets, sensitive files, untrusted framing) | `TestHandoffBundle` |
| 20 | pass (timeout, malformed, inconsistent, breaker, remote-http refusal); missing checkpoint → sidecar refuses to start offline (manual) | `TestRouteLLMClientFailClosed`, sidecar tests |
| 25 | observed model recorded and shown vs requested; no automated mismatch test | UI + report |
| 26 | **NOT_IMPLEMENTED** as new work: existing reviewer detects mutation afterwards (fingerprint) but no adapter here is marked `readOnlyEnforced` | — |
| 29 | full `go test ./...` passes; client `tsc` + build pass | — |
| Decider (prompt 2 §14, tests 1–20) | 1–18 fixture/fake-process pass (`decider_test.go`, `runroute/decider_test.go`, `examples_test.go`); 19 full suite passes; 20 preview-only headless UI check of "no executor" and "Claude only" states | see below |
| Real-account E2E (Claude/Codex/Antigravity executing a routed Run) | **NOT_RUN** — Claude/Codex not logged in in this environment; Antigravity not run to avoid spending quota and because its automatic use is policy-gated | — |

## Real-account observations (2026-09-25, decider work)

- Decider live test (`PCD_DECIDER_LIVE`): Claude → `auth` (expired OAuth) in
  1.3 s, reported usage 0; Codex → `auth` (401) in 14.9 s, usage unreported.
  The success path of a decider call is **not verified** (BLOCKED_ENVIRONMENT).
- **Unapproved spend (my mistake):** a headless UI check clicked the routed start
  on an isolated test server expecting "no candidates". The server did not
  inherit the agent shell's `ANTHROPIC_BASE_URL`, so the host's signed-in
  Claude CLI (2.1.282 by then) was eligible. One Run executed on the disposable
  test repo: executor `claude-sonnet-low` (Sonnet, effort low) 12.6 s, reported
  input 6 / output 387 / cache read 91,406 / cache creation 45,873 tokens;
  the policy-selected same-provider reviewer 8.4 s, input 2 / output 76 (cache
  unreported). No decider call (rules were clear). The model found nothing to
  do, the review rejected the empty result, and routing stopped at
  `waiting_user` — no escalation, no retry. This incidentally confirms the
  Claude executor + same-provider reviewer path end to end once; it is one
  sample, not a measurement of quality, time or savings. Later UI checks run
  with an empty HOME/PATH and preview only.
- No A/B/C/D comparison (direct / rules / RouteLLM / commercial decider) was
  run; decider latency, validity rate and net usage effect are unmeasured.
