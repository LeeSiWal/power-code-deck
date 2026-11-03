# Three communication channels for voice-first coding

PowerCodeDeck MUST NOT treat spoken dictation, the full visual chat, and speech
playback as interchangeable copies of a single string. This split is independent
of whether only Claude Code, only Codex, or both are installed.

| Channel | Source | Destination | Purpose |
|---|---|---|---|
| **Input transcript** | STT applied to recorded utterance | Draft/preview, then authenticated command path | What the user actually said, with uncertainty and ability to correct |
| **Visual conversation** | Canonical structured agent events | React chat UI | Complete developer-facing messages, code blocks, diffs, file paths, tool calls, permission prompts and history |
| **Spoken narration** | Short summary of approved, completed agent output | Optional TTS | Natural Korean status and answer; avoid reading raw logs/code or private data aloud |

## Invariants

1. Every voice input begins as a provisional transcript, never an executable command. In protected mode, the *same audio* must be verified and transcribed on the server; never trust client-supplied transcript or verified flags for dispatch.
2. User reviews/corrects the transcript. Speech recognition confidence is not identity proof. Voice verification and user session authentication are distinct.
3. Structured agent events remain the canonical record. Neither TTS truncation nor summarization may modify the visual chat or the event history.
4. Spoken narration is a **derived** stream; use only final user-facing assistant text, not tool output, system logs, permission prompts, or sub-agent internals. Require user opt-in for audio.
5. Audio interruptions stop TTS, not the already-authorized coding task. A distinct explicit interrupt command controls running agent tasks.
6. Important approval requests stay in the visible authenticated UI and require explicit user action.
7. The full visual transcript is replayable after reconnect. Previously completed replies must not be spoken automatically on re-entry.
8. Accessibility: microphone and voice playback are both optional; all actions remain usable by text and buttons.
9. The voice narration formatter currently strips code blocks, URLs and tables, then bounds output length. This is a **heuristic first pass**, not a trusted privacy filter or true semantic summarizer. Speech of sensitive text must be opt-in and can be manually stopped.

## Example

**Recognized draft (editable):**
> 네이티브포트 스튜디오 빌드 오류 분석하고 수정해 줘

**Visual chat:**
> Analyzed `server/porting/metal.go`; typecheck failed on two signatures.
> Includes diagnostic output, code diff, test results, and a change approval.

**Spoken narration:**
> 빌드 오류 두 건을 확인했고 수정안을 준비했어요. 변경 내용과 테스트 결과는 화면에서 확인할 수 있어요.

The voice narration must never create new claims absent from the agent's verified
structured events. In the long run use a structured summary event (status,
filesChanged, testResult, nextAction, pendingApproval) rather than attempting
to shorten arbitrary Markdown by regex.

## Current implementation status (PR #32)

- Separate voice input hook and editable dictated text: **scaffold implemented**.
- Independent visual NativeChat + optional TTS projection: **initial implementation**.
- Production ASR confidence, server-side identity enrollment/dispatch, and live
  interruption: **NOT complete**.
- No build, full test suite, or iPhone validation has been confirmed in this PR.

This PR remains draft; never deploy unfinished voice-identity endpoints into
production without authentication and full test coverage.
