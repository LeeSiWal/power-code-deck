# Changelog

All notable changes to this project are documented here.

## Unreleased — Session engine

### Changed
- **Session engine refactor** — terminal/agent sessions now go through a single `SessionEngine` interface. The web/API/WebSocket layers no longer touch the session runtime directly. The invariant **"Detach is not Kill"** is enforced: a browser disconnect only detaches the viewer; the shell/Claude process keeps running. Only Kill / Restart / Delete end the process.
- **tmux removed** — PowerCodeDeck now uses its own in-process `InternalPtySessionEngine` (owns each session's PTY process directly via `creack/pty`, with a per-session scrollback ring buffer replayed on reconnect). tmux is no longer a runtime dependency, is no longer installed by `install.sh`, and `TmuxSessionEngine`/`tmux.go`/`pty.go` were deleted. mac/Linux run natively without tmux; native Windows (go-pty/ConPTY) is future work.
- `POWERCODEDECK_SESSION_ENGINE` is **deprecated** — the internal engine is always used; a set value logs a warning and is otherwise ignored.

### Added
- `POWERCODEDECK_SESSION_SCROLLBACK_BYTES` (default `524288`) — per-session replay buffer size.
- `docs/session-engine.md` documenting the engine, the Detach≠Kill rule, server-restart behavior, and the future `pcd-sessiond` split.

### Notes
- If the `pcd` **server process** restarts, live sessions may stop (session lifetime is tied to the server for now); agents are not auto-respawned — press Restart. The legacy `tmux_session` DB column is kept but unused.

## v0.2.1 — Session Handoff

### Added
- **Session Handoff (Continue on Mobile)** — hand off an active terminal or Claude session from desktop to mobile / iPad by scanning a one-time QR code, attaching to the same tmux session.
  - One-time handoff tokens: **SHA-256 hashed** (raw tokens are never stored in the database), **10-minute TTL** by default, **single-use**, and **session-bound**.
  - New `POST /api/agents/:id/handoff` API to mint a handoff token/QR, and `GET /handoff/:token` redeem endpoint.
  - Session-scoped handoff cookie set on redeem so the mobile client lands on the correct session.
  - LAN + public URL support: QR encodes `POWERCODEDECK_PUBLIC_URL` (proxy/domain) or `POWERCODEDECK_LAN_URL` (same Wi-Fi) as configured.
  - Configurable server bind host via `POWERCODEDECK_BIND_HOST` (default `127.0.0.1`; set `0.0.0.0` for LAN handoff).
  - Mobile **Prompt Bar auto-expands** on handoff arrival for Korean / long prompts.
- New environment variables: `POWERCODEDECK_PUBLIC_URL`, `POWERCODEDECK_HANDOFF_ENABLED` (default `true`), `POWERCODEDECK_HANDOFF_TOKEN_TTL_SECONDS` (default `600`), `POWERCODEDECK_LAN_HANDOFF_ENABLED` (default `false`), `POWERCODEDECK_LAN_URL`, `POWERCODEDECK_BIND_HOST` (default `127.0.0.1`).

### Security
- Raw handoff tokens are never persisted — only their SHA-256 hash is stored and compared.
- Tokens expire (default 10 min), are single-use, and are bound to a specific session.
- Documentation warns against exposing PowerCodeDeck directly without authentication — especially when auth is disabled **and** LAN handoff is enabled — recommending PIN/password auth, Caddy + Authelia, Tailscale, VPN, or an SSH tunnel.

### Compatibility
- All new handoff variables honor the legacy `AGENTDECK_*` prefix as well; `POWERCODEDECK_*` wins when both are set.

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
