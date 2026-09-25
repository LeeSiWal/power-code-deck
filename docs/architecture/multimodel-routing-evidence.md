# Multi-model routing — provider evidence and support matrix

Reviewed 2026-09-25. This is an engineering record of what official pages say
and what the installed CLIs did on this machine. **It is not legal advice and
not an automated terms verdict.** The machine-readable part is
`server/internal/routing/policy_evidence.json` (compiled into the binary; users
cannot override it from `routing.json`).

Dates: "published" = announcement date, "updated" = page modification date,
"effective" = policy start. "undated" means the page shows none.

## Sources checked

| Ref | Page | Dates seen | What it says (short) | What it does not say |
| --- | --- | --- | --- | --- |
| G1 | developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals | effective 2026-06-18; updated 2026-09-02 | Consumer tiers (individuals, AI Pro, Ultra) stopped being served for Gemini Code Assist IDE extensions and Gemini CLI; migrate to Antigravity. Standard/Enterprise unaffected. | No retraction or reinstatement. Nothing on Gemini API-key/Vertex use of Gemini CLI. |
| G2 | developers.googleblog.com …transitioning-gemini-cli-to-antigravity-cli | published 2026-05-19 | Same cut-over date; Standard/Enterprise licences unchanged; recommends Antigravity CLI. | No retraction. |
| G3 | antigravity.google/docs/cli/install | undated | Browser sign-in; API key via `modelProvider: gemini` + `GEMINI_API_KEY`. | Usage rules. |
| G4 | antigravity.google/docs/cli/headless | undated | `-p`, `--output-format stream-json`, `--input-format stream-json` for a continuous process, `--conversation <id>`; events `init`/`step_update`/`result`; usage counters are **cumulative for the session**; unapprovable tools are soft-denied, exit 0, notice on stderr. | `--effort`; `denied_actions` in `result`. |
| G5 | antigravity.google/docs/plans | undated | After baseline quota, AI credits bill at Gemini Enterprise pricing; "AI Credit Overages" setting is Never / Always. | The default; whether the CLI/headless can read or set it. |
| G6 | antigravity.google/terms | undated | "using the Service in connection with products not provided by us" is listed under abuse; "Using third party software, tools, or services to access the Service (e.g. using OpenClaw with Antigravity OAuth) is a breach". | Whether launching the unmodified official `agy` from a personal tool is "third-party software … to access the Service". |
| G7 | antigravity.google/docs/models | undated | Gemini 3.x models plus Claude Sonnet/Opus 4.6 and GPT-OSS-120b; separate 5-hour/weekly limits for Gemini vs other models. | That these grant any Claude Code / Codex entitlement (they do not). |
| A1 | code.claude.com/docs/en/legal-and-compliance | undated | OAuth is for subscribers' ordinary use of Claude Code; third parties may not offer Claude.ai login or route requests through Free/Pro/Max credentials on behalf of users; an end user signing in to the unmodified binary with their own subscription is not prevented. Limits assume ordinary individual usage. | A definition of "ordinary, individual usage". |
| A3 | code.claude.com/docs/en/model-config | undated | Depending on plan, **Fable can bill to usage credits**; in `-p`/SDK mode Claude Code **never shows the consent prompt** and bills without asking. Some `[1m]` variants require usage credits on Pro. Billing/rate-limit errors never trigger fallback-model switching. | A general rule for other models when limits run out. |
| A4 | code.claude.com/docs/en/headless | undated | `--bare` does not use the subscription login and is planned to become the `-p` default; denials appear as `permission_denied` and `permission_denials`. | When `--bare` becomes default. |
| O1 | developers.openai.com/codex/auth → learn.chatgpt.com/docs/auth | undated | ChatGPT sign-in = subscription access; API key = standard API pricing; API keys are the recommended default for automation/CI; do not expose Codex in untrusted/public environments; `auth.json` is a credential. | Which wins if `OPENAI_API_KEY` is set alongside a ChatGPT login. |
| O2 | …/codex/app-server → learn.chatgpt.com/docs/app-server | undated | `initialize`, `model/list` (`supportedReasoningEfforts`), `thread/start`, `thread/resume`, `turn/start` (`model`, `effort`, …), `turn/interrupt`, `account/read`, `account/rateLimits/read`, approval requests. | Terms for third-party clients. |
| O3 | …/codex/config-advanced | undated | `--oss` with Ollama/LM Studio; `--local-provider`. | How `--oss` interacts with app-server threads. |
| R1–R3 | github.com/lm-sys/RouteLLM | last push 2024-08-10 | Routers `mf`, `sw_ranking` (OpenAI embeddings), `bert`, `causal_llm`; `Controller.route(prompt, router, threshold)`; strong iff score ≥ threshold. | Anything about CLIs, subscriptions, coding-agent difficulty. |

G8 (CLI reference) and G9 (migration) were also read; G8 lists slash commands
and settings, not launch flags — `--effort`, `--mode`, launch `--add-dir` and
`agy models` were found only in the installed CLI's `--help`.

## Installed versions on the development host (2026-09-25)

| CLI | Version | Discovery result through PowerCodeDeck's own probe |
| --- | --- | --- |
| claude | 2.1.239 | installed; `claude auth status` → not logged in **in this shell** (sandbox sets `ANTHROPIC_BASE_URL`, flagged as inherited billing env) |
| codex | codex-cli 0.154.0 | installed; `codex login status` → Not logged in (answer is on **stderr**); `model/list` returned 6 models with per-model efforts |
| agy | 1.1.28 → **1.2.11** | the CLI **auto-updated during the session**; headless flags unchanged; 1.2.10 changelog: headless runs ending on a model error now exit 3 instead of 0; `agy models` listed 14 models (Gemini, Claude, GPT-OSS); `--help` goes to **stderr** |
| gemini | 0.50.0 | installed; settings.json selected auth type is personal Google sign-in → `consumer_auth_discontinued` |
| RouteLLM | routellm 0.2.0, torch 2.14.0+cpu, transformers 5.17.0 | BERT checkpoint `routellm/bert_gpt4_augmented` runs offline on CPU |

The same host's production service may run with different logins; these rows
describe this shell only.

## Resulting defaults

| Adapter | Manual (user picks) | Automatic (routing picks) | Notes |
| --- | --- | --- | --- |
| Claude Code | allowed | allowed | Fable / `[1m]` / default-model profiles should set `extraUsageRisk`; gated unless `spend.allowExtraUsageRisk`. |
| Codex | allowed | allowed | ChatGPT sign-in = subscription; API-key sign-in = metered (blocked without `allowApiMetered`). Unattended/CI = review required. |
| Antigravity | allowed **with warning** (existing behavior preserved) | **policy_review_required → excluded** | Also billing `unknown` (overage setting unobservable). Existing v2 reviewer still launches Antigravity for every Run — see risks. |
| Gemini CLI | not executable | not executable | Optional enterprise/API compatibility path: status only (`not_implemented` executor). Consumer sign-in shown as ended, distinct from "not logged in". |
| Local (Ollama / OpenAI-compatible) | text tasks only | only for task kinds with `quality.<kind>.status = verified` | No tools/edits, so never selected for code Runs. |

## Unresolved (needs evidence, not code)

1. Whether Google considers PowerCodeDeck launching the official `agy` binary for
   its own user "third-party software … to access the Service" (G6). Until a
   written answer exists, automatic Antigravity routing stays off.
2. The Antigravity AI-credit overage default and whether headless runs respect
   "Never" (G5).
3. Which Claude models draw usage credits on the user's specific plan (A3 says
   "depending on your plan and seat tier").
4. Codex behavior when both a ChatGPT login and `OPENAI_API_KEY` are present (O1).
5. Whether `claude -p` without `--bare` keeps using the subscription login after
   `--bare` becomes the default (A4).
