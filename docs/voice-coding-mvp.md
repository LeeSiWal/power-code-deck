# Voice Coding — Requirements & Architecture (MVP)

Status: design + initial scaffold. Existing native Claude/Codex drivers remain the source of truth for execution; this document does not claim voice integration is already shipped.

## Non-negotiable: independent provider support

PowerCodeDeck MUST run voice coding with **Claude Code only**, **Codex only**, or **both**. Neither the voice interface nor task execution may require a second provider to be installed, authenticated, reachable, or subscribed. Provider handoff is an *optional enhancement*, never a prerequisite.

| Available | Conversation | Execution | Handoff |
|---|---|---|---|
| Claude only | Yes | Claude | Unavailable, safely disabled |
| Codex only | Yes | Codex | Unavailable, safely disabled |
| Both | Yes | User-selected/auto policy | Optional |
| Neither | Voice transcript + explanatory UI | Disabled with actionable install/setup state | Disabled |

## Product requirements

- Push-to-talk as MVP, followed by hands-free full-duplex speech with interruption/barge-in. Korean first.
- Voice transcript is editable before dispatch; no automatically executed partial ASR hypotheses.
- Spoken and written responses convey the same source-grounded agent events. Summarize structured tool progress, not raw ANSI terminal bytes.
- Reuse existing session IDs, native structured drivers, approval policies, and server persistence; reconnect resumes observation of a running task.
- Explicit consent for destructive shell actions, external publish/push, and broad filesystem operations. A voice command never bypasses approval.
- Project/repository scope is explicit. Prefer isolated Git worktrees per concurrent agent; PR/merge remain deliberate actions.
- Per-provider discovery and error states: absent Claude should not block Codex and vice versa. Switch only to installed/authorized providers; never silently swap after a failure.
- Separate microphone capture, transcription, command interpretation, agent dispatch, event summarization, and speech playback. Keep API/CLI credentials server-side.
- On iOS Safari/browser, detect actual microphone/STT/TTS capability and present a keyboard fallback where unsupported; require HTTPS and user gesture as relevant.
- Support voice pause/mute and transcript-only mode. Never require audio playback to operate the coding agent.

## Proposed architecture

```text
Mobile/desktop React UI
  mic + transcript editor + push-to-talk + TTS controls
       │ finalized text only
       ▼
Voice session controller (client, then server persisted)
       │ selects project + existing session + provider
       ▼
PowerCodeDeck authenticated API / WebSocket
       │ native structured turn + approval events
       ▼
Agent adapter registry + capability discovery
       ├─ Claude Code driver (independent)
       └─ Codex driver (independent)
       │
       ▼
Existing run/session engine → scoped workspace/worktree → Git/GitHub
       │
       └─ structured progress / completion / approval → concise speech rendering
```

## MVP execution sequence

1. Add isolated browser voice-input hook with capability checks, interim transcript, final transcript, errors, and cleanup.
2. Integrate voice control into existing NativeChat text-composer. User reviews recognized text and explicitly sends using the **existing** send path.
3. Add optional text-to-speech of short completed assistant responses with mute and interruption. Avoid reading raw tool logs.
4. Add provider-capability inventory to UI and test Claude-only, Codex-only, both, and neither.
5. Add server-owned conversation/intent ledger and reconnect recovery. Retain provider session IDs; do not invent cross-provider shared histories.
6. Add explicit cross-provider handoff with summarized context and isolated worktrees, without making it a dependency for single-provider operation.
7. Upgrade to streaming audio/barge-in using a pluggable STT/TTS backend after mobile Safari and latency testing.

## Acceptance checks

- Claude-only: speech → editable Korean transcript → Claude driver → response; no Codex code path required.
- Codex-only: identical flow with no Claude installation/authentication.
- Both: optional provider selection, continued voice UI, isolated concurrent execution.
- Neither/unsupported browser/microphone denied: useful explanatory state + keyboard path.
- Interrupted/closed browser: underlying authorized run continues; reopening recovers events without repeating commands.
- Audio playback interrupted when user starts talking; no duplicate sends.
- Existing auth, approvals, and Git safety checks remain enforced for voice turns.

## Implementation note

A browser-native SpeechRecognition implementation is only a progressive enhancement; it is not a dependable cross-browser or offline guarantee. For production Korean dictation and low-latency real-time voice, add a pluggable STT/TTS backend and do not store raw audio by default.
