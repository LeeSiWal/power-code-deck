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
| Real-account E2E (Claude/Codex/Antigravity executing a routed Run) | **NOT_RUN** — Claude/Codex not logged in in this environment; Antigravity not run to avoid spending quota and because its automatic use is policy-gated | — |
