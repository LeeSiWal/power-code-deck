# Voice Identity & Command Authorization (proposed design)

Status: **design only** — not yet a deployed voice-verification feature. This is an extension of [voice-coding-mvp.md](./voice-coding-mvp.md). Applies identically to Claude-only, Codex-only, or dual-provider deployments.

## Default behavior

- Feature flag: `voiceIdentity.enabled=false` until the user explicitly enrolls and enables it.
- If enabled, only voice segments that pass **speaker verification** can issue **coding commands**. Unknown, ambiguous, overlapping, replay-suspected, or verification-unavailable audio is **not dispatched** to an agent.
- When an unrecognized speaker talks, optional conversational response can be allowed **only in a separate, read-only conversational mode**; no tools, workspace reads, secrets, or repository context may be exposed. Default: refuse command, provide a generic explanation. **Do not** feed unverified speech to the actual coding-agent session.
- Microphone permission is separate from verified identity. Being signed in is separate from being speaker-verified. Speaker verification is **not strong authentication**: replay, synthesized voices and noisy environments can defeat or degrade it.
- Existing authenticated user session, CSRF protection, existing per-tool approvals, and privileged-action checks remain mandatory. Voice identity must never reduce these controls. Sensitive actions (push, delete, deploy, credentials, exfiltration, permission changes) require explicit authenticated confirmation, ideally device passkey/biometric user presence, not just a spoken 'yes'.
- If feature disabled, ordinary typed input and existing approved voice dictation work as they do today. The enabled setting must never block typed fallback; typed requests still use normal auth and approval policy.

## Trust boundary / flow

```text
microphone / VAD / overlap detection
   ↓ short utterance, speech-quality check
speaker verifier (registered enrollment template, server-side)
   ├─ match + sufficient quality + optional liveness/replay signal
   │    ↓ speaker-verified command claim (short-lived, bound to user/session/utterance)
   │    ASR → editable transcript → explicit send → existing authorization + native driver
   │                                             ├── Claude Code (if available)
   │                                             └── Codex (if available)
   ├─ unknown/mismatch/overlap/low-confidence → reject executable command
   │    └── optional isolated non-tool conversational reply (no project information)
   └─ error/no enrollment → fail closed for voice coding, show typed alternative
```

**Important:** A browser-only speaker comparison is not a security boundary; browsers are untrusted and can be modified. Any security-sensitive decision must be enforced on the server. For a browser UI, server must bind a short-lived verification result to the specific utterance and session and check it again before invoking a native agent. Even this is a secondary control, not proof of identity. Never accept a client-supplied `verified: true` boolean.

## Enrollment and storage

1. Authenticate on device before enrollment; disclose biometric data processing and obtain explicit opt-in.
2. Record several varied prompted utterances in quiet and moderately noisy conditions; check microphone quality and segment duration.
3. Derive speaker embeddings locally on the trusted server, encrypt at rest with owner-specific access controls, and delete raw enrollment audio by default.
4. Offer a **delete voice profile** control and retention settings, re-enrollment and recovery using existing account authentication, not voice alone.
5. Limit enrollment/verifications by rate, audit security decisions without writing raw audio/transcripts to general logs. Avoid including speaker embeddings or utterance fingerprints in telemetry.

Candidate offline modules (subject to benchmarking and license review): ECAPA-TDNN for embeddings, VAD for speech detection, optional diarization for multi-speaker segments, anti-spoof/replay detection as additional defense. Thresholds must be calibrated on real Korean utterances, target iPhone microphones, noise and playback conditions; do **not** hard-code a purported universal accuracy number.

## Explicit settings / UX

- `Voice protection: Off / Registered speaker only` (off by default).
- `Unrecognized speakers: Ignore / Generic read-only reply` (ignore or generic refusal default; no coding or access to private context).
- Enroll / re-enroll / delete voice profile with clear consent and account auth.
- Display `Listening`, `Checking speaker`, `Verified`, `Not verified`, `Try again`, and `Microphone unavailable` states, without exposing speaker similarity scores to unauthorized parties.
- Indicate when command routing is blocked. Allow keyboard alternative and manual authentication recovery.
- Spoken response must not disclose private repository details when the speaker is not verified.

## Threats and mitigations

| Threat | Mitigation / residual risk |
| --- | --- |
| Another person or TV speaks | VAD + speaker match + overlap detection; false accepts remain possible |
| Replay or voice clone | Challenge response and anti-spoof model can reduce, **not eliminate**, risk; sensitive actions require device confirmation |
| Injection through transcript / ASR mistakes | User preview + explicit send; command parsing never bypasses existing approval policies |
| Client forges a verification result | Server-side enforcement, attested server-issued utterance/session-scoped result; no bare client flag |
| Noisy/mixed voices | Fail closed and ask to repeat or type |
| Provider unavailable | Explicit capability state; Claude-only and Codex-only remain fully supported |
| Stolen voice profile | Encryption, access control, delete/re-enroll, retention limits |
| Web app disconnected | No voice command executed from unsent or unverified audio; existing authorized tasks continue independently |

## Phased delivery and gates

- **Stage A (now):** document requirements + threat model; leave current native voice dictation behavior unchanged.
- **Stage B:** implement opt-in enrollment UI + audited server-side profile store and verifier SPI, with mock verifier tests before connecting a real model.
- **Stage C:** benchmark speaker verification + spoof rejection on-device/microphone scenarios and wire enforcement into server's native-turn dispatch. If enabled and verifier fails, no agent runs.
- **Stage D:** isolated generic conversational mode (if enabled), replay resistance, biometric/passkey approvals for sensitive actions, and optional continuous listening.

### Acceptance tests

1. Claude-only + enrolled speaker: normal voice coding flow without Codex dependency.
2. Codex-only + enrolled speaker: normal flow without Claude dependency.
3. Unknown/mismatched/replayed/overlapping/low-quality speaker: no tool calls or Git writes; no private repository context leak.
4. Forged client verification field cannot authorize command.
5. Recognized speaker can request work but cannot bypass normal destructive-action approvals.
6. Speaker verifier disabled, unreachable, or throws error while protection **on**: fail closed for voice coding; typed alternative survives.
7. Protection **off**: existing voice-dictation UX remains available; all standard authentication/approval controls still apply.
8. Delete profile immediately revokes matching; normal login/passkey recovery still works.
