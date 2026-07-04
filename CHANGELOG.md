# Changelog

All notable changes to this project are documented here.

## v0.2.0 — PowerCodeDeck Renewal

### Changed
- Renamed **AgentDeck → PowerCodeDeck**. Binary is now `pcd`.
- Introduced version management (`server/version`, startup banner, `pcd version`, `/api/auth/health`).
- Changed first-run authentication setup. Authentication is now **optional and disabled by default**.
- Added support for new `POWERCODEDECK_*` environment variables while keeping `AGENTDECK_*` compatibility.
- Unified terminal input around a **single interactive terminal** (from the earlier CHAT/RAW dual mode). The terminal handles commands, arrow-key menus, y/n approvals, Tab, Esc, and Ctrl+C directly.

### Added
- Device-aware **Prompt Bar** for Korean / long / multi-line prompts. Text is composed in a native textarea (correct IME composition) and pasted into the current terminal — **Send** adds Enter, **Paste** does not, plus **Clear** and **터미널 조작** (focus terminal). It never interprets Claude state or handles approvals; it only pastes text.
  - **Desktop**: optional overlay, toggled by the Prompt button or Cmd/Ctrl+K / Cmd/Ctrl+P; Esc closes.
  - **Mobile / iPad (touch)**: always shown and collapsible (never fully closed), because typing Korean directly into xterm splits the jamo (ㅇㅏㄴ instead of 안).
- On-screen PTY control-key bar (arrows, Enter, Esc, Tab, ⇧Tab, y/n, Ctrl+C/D) on desktop and mobile, so interactive CLI menus work without a physical keyboard.
- One-time hint when Korean is typed straight into the terminal on touch devices, pointing the user to the Prompt Bar.
- First-run authentication selection: **none**, **PIN**, or **password** (interactive wizard when run in a TTY).
- Startup security warning when authentication is disabled.
- `GET /api/health` (alias of `/api/auth/health`) exposing `appName`, `version`, `authEnabled`, `authMethod`.
- Password authentication with a dependency-free salted, iterated SHA-256 hash.
- Roadmap entry for **v0.3.0 Control Room**.

### Security
- Authentication is disabled by default for local / proxied deployments.
- Secrets (PIN / password) are never printed to the startup log.
- Passwords are stored hashed, never in plaintext.
- Added documentation warning not to expose PowerCodeDeck directly to the public internet.

### Compatibility
- Existing `AGENTDECK_*` environment variables remain supported; `POWERCODEDECK_*` wins when both are set.
- A legacy `.env` with only `AGENTDECK_PIN` is treated as PIN authentication.
- Data locations (`~/.agentdeck/`, `agentdeck.db`) are unchanged so existing installs keep their agents and settings.

## v0.1.0 — AgentDeck (MVP)
- Initial release: multi-agent web terminal for Claude Code / Gemini CLI / Codex CLI, file explorer, dashboard, PIN authentication with an auto-generated PIN.
