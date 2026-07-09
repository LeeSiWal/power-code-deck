# Changelog

All notable changes to this project are documented here.

## v0.2.4 — 보안 강화 (security hardening)

> **v0.2.3 이하는 아래 취약점이 있으므로 업그레이드가 필요합니다.** 특히 무인증(기본값) 모드에서 로컬에 열려 있으면 악성 웹페이지가 접근할 수 있었습니다.

### Security
- **WebSocket Origin 검증** — `/ws`가 모든 Origin을 허용하던 것을 허용 목록 기반 검증으로 교체. 임의의 웹페이지가 `ws://localhost/ws`에 붙어 터미널에 명령을 주입하는 drive-by 공격을 차단합니다(브라우저가 아닌 CLI 등 Origin 헤더가 없는 클라이언트는 허용).
- **WebSocket 토큰 상시 요구** — 무인증 모드에서도 `/ws`가 항상 유효한 토큰을 요구합니다. 로컬 브라우저는 `POST /api/auth/anonymous`(무인증 모드 + 로컬 Origin 한정)에서 익명 토큰을 발급받아 사용합니다. 기존 무인증 UX(로그인 화면 없음)는 그대로 유지됩니다.
- **파일 API 경로 검증 상시 적용** — `agentId`를 생략하면 경로 검증을 건너뛰어 임의 절대경로 read/write/delete/rename이 가능하던 문제를 수정. 이제 모든 파일 작업이 허용 base(에이전트 작업 디렉토리, 또는 워크스페이스 루트/홈 + 최근 프로젝트) 안으로 제한되며, `~/.ssh`·`~/.aws`·`~/.gnupg` 등 민감 디렉토리는 명시적으로 차단됩니다.
- **ValidatePath prefix 우회 버그 수정** — `/base`가 `/base-evil`도 통과시키던 `HasPrefix` 검사를 `filepath.Rel` 기반으로 교체하고, 아직 존재하지 않는 쓰기 경로는 가장 가까운 실제 부모를 심링크 해석해 base 이탈을 차단합니다.
- **Refresh 토큰의 access 통용 차단** — access 토큰에 `type:"access"` 클레임을 추가하고 인증·WS 검증에서 타입을 확인합니다. 30일짜리 refresh 토큰을 API 자격증명으로 쓸 수 없습니다(v0.2.4 이전 발급된 무타입 토큰은 만료까지 access로 허용하는 마이그레이션 유예 포함).
- **Host 헤더 검증(DNS rebinding 방지)** — Host가 localhost/127.0.0.1/`[::1]`(및 PUBLIC_URL/LAN_URL/BIND_HOST/`ALLOWED_HOSTS`) 허용 목록에 없으면 403. 모든 라우트에 전역 적용됩니다.

### Changed
- **Graceful shutdown** — `http.ListenAndServe` 대신 `http.Server`를 사용해 SIGINT/SIGTERM 시 5초 컨텍스트로 in-flight 요청을 정리한 뒤 DB를 닫습니다. 활성 PTY 세션은 의도적으로 유지(Detach ≠ Kill)되며 프로세스 종료와 함께 정리됩니다.
- `POWERCODEDECK_ALLOWED_HOSTS`(쉼표 구분) 환경변수 추가 — 리버스 프록시 도메인이나 커스텀 호스트로 접근할 때 Host 검증 허용 목록에 추가합니다.

### Tests
- `services/file_test.go` — ValidatePath 테이블 테스트(정상/`..` traversal/prefix 우회/심링크 이탈/미존재 쓰기 경로) 및 민감 디렉토리 차단.
- `auth/auth_test.go` — access/refresh 타입 분리 및 레거시 무타입 토큰 허용.
- `ws/hub_test.go`, `middleware/hostcheck_test.go` — Origin/Host 허용·차단.

## v0.2.3 — cgo-free, natively cross-compilable (incl. Windows .exe)

### Fixed
- **Native Windows: agents now launch.** Windows `CreateProcess` (used by ConPTY) can't run `.cmd`/`.bat`/`.ps1` shims directly, and npm-installed CLIs (`claude`, `gemini`, `codex`) are `.cmd` shims — so clicking Launch Agent silently did nothing. On Windows the engine now routes non-`.exe` commands through `cmd.exe /c`.
- Agent-launch failures are now surfaced to the user (alert) instead of only logged to the console, so a failed launch is no longer a silent "no-op".

### Changed
- **Visible data folder + shortcuts.** The install/data directory moved from the hidden `~/.powercodedeck` to **`~/PowerCodeDeck`** so non-developers can find it; existing installs are migrated automatically (the DB/.env move with it). On Windows the installer now creates a **Desktop + Start Menu shortcut** (launch and "데이터 폴더 열기" in Explorer) and a one-word **`pcd`** command, so no long WSL command is needed. The SQLite filename (`powercodedeck.db`) and bundle id are unchanged.
- **No more cgo / C toolchain.** SQLite driver switched from `mattn/go-sqlite3` (cgo) to **`modernc.org/sqlite`** (pure Go), and the PTY layer from `creack/pty` (Unix-only) to **`aymanbagabas/go-pty`** (Unix PTY on mac/Linux, **ConPTY on Windows**). `pcd` now builds with `CGO_ENABLED=0` — no gcc/build-essential required.
- `install.sh` no longer installs `build-essential`; it only needs `git`, `curl`, `ca-certificates`. Builds use `CGO_ENABLED=0`.

### Added
- **Terminal copy / paste.** xterm renders to a canvas, so its selection isn't a DOM selection and native Cmd+C copied nothing. Copy is now wired to **Cmd+C** (macOS) / **Ctrl+Shift+C**, with a floating **복사** button that appears while text is selected (also works on touch); paste to **Cmd+V** (native) / **Ctrl+Shift+V**. A bare Ctrl+C still sends SIGINT. Includes an execCommand fallback for non-secure contexts (LAN handoff over http).
- **Native Windows binary** — `make build-windows` produces `pcd.exe` (`GOOS=windows CGO_ENABLED=0`), a real PE32+ executable with no WSL and no cgo. The WSL installer remains the tested/recommended path until the native `.exe` is validated on Windows hardware.
- `Makefile` targets build with `CGO_ENABLED=0`; `make build-windows` for the native `.exe`.

## v0.2.2 — tmux-free session engine + easy Windows install

### Changed
- **Session engine refactor** — terminal/agent sessions now go through a single `SessionEngine` interface. The web/API/WebSocket layers no longer touch the session runtime directly. The invariant **"Detach is not Kill"** is enforced: a browser disconnect only detaches the viewer; the shell/Claude process keeps running. Only Kill / Restart / Delete end the process.
- **tmux removed** — PowerCodeDeck now uses its own in-process `InternalPtySessionEngine` (owns each session's PTY process directly via `creack/pty`, with a per-session scrollback ring buffer replayed on reconnect). tmux is no longer a runtime dependency, is no longer installed by `install.sh`, and `TmuxSessionEngine`/`tmux.go`/`pty.go` were deleted. mac/Linux run natively without tmux; native Windows (go-pty/ConPTY) is future work.
- `POWERCODEDECK_SESSION_ENGINE` is **deprecated** — the internal engine is always used; a set value logs a warning and is otherwise ignored.

### Added
- **One-line Windows installer** — `iwr -useb .../win-install.ps1 | iex` sets up WSL + Ubuntu (no interactive account — runs as root), reboots with confirmation and **auto-resumes after login**, then builds PowerCodeDeck. Falls back to **WSL1 when CPU virtualization is off**, and prints ASCII/English so consoles don't show `???`.
- `POWERCODEDECK_SESSION_SCROLLBACK_BYTES` (default `524288`) — per-session replay buffer size.
- `docs/session-engine.md` documenting the engine, the Detach≠Kill rule, server-restart behavior, and the future `pcd-sessiond` split.
- Beginner-friendly Windows install guidance in the README.

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
