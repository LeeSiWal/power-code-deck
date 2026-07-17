# PowerCodeDeck Session Architecture

## Current phase

PowerCodeDeck routes all terminal/agent session management through a single
`SessionEngine` interface ([server/services/session_engine.go](../server/services/session_engine.go)).
There is **one implementation**: `InternalPtySessionEngine`
([code](../server/services/session_engine_internal.go)). `pcd` owns each
session's process + PTY directly (go-pty) and keeps per-session scrollback in
a bounded [ring buffer](../server/services/ring_buffer.go). **tmux is no longer
used or required** anywhere.

```
Browser / Mobile / iPad
        │
        ▼
      pcd                     Web UI · REST API · WebSocket gateway · Auth · QR Handoff
        │
        ▼
  SessionEngine               (interface — the only way sessions are touched)
        │
        ▼
  InternalPtySessionEngine    Persistent process · viewer attach/detach · ring buffer
        │
        ▼
  PTY  →  Claude / Codex / Shell
```

Handlers, the WebSocket hub, and the agent service call **only** `SessionEngine`.

> **History:** earlier versions ran each agent in a tmux session
> (`TmuxSessionEngine`). tmux was removed once the internal engine was verified;
> only the unused `tmux_session` DB column remains, kept to avoid a migration.

## Configuration

```env
POWERCODEDECK_SESSION_SCROLLBACK_BYTES=524288   # per-session replay buffer (512KB default)
```

`POWERCODEDECK_SESSION_ENGINE` is **deprecated**. If set to anything other than
`internal`, the server logs a warning and continues with the internal engine.

## The key rule: Detach is not Kill

This is the invariant the whole refactor exists to guarantee:

| Action  | Meaning                                                   | Ends the process? |
|---------|----------------------------------------------------------|-------------------|
| **Detach** | A viewer (browser tab / mobile / iPad) leaves a session | **No** |
| **Kill**   | The underlying shell/agent process is terminated        | **Yes** |

- A browser closing or a WebSocket dropping is a **Detach** — the Claude/shell
  process keeps running so you can reconnect (and hand off to mobile).
- **Kill** happens only on explicit user actions: Delete, Restart (kills the old
  process before starting a new one), or an explicit kill.

In `InternalPtySessionEngine`, Detach removes the viewer and — even when the
last viewer leaves — **never** closes the PTY or kills the process. Only `Kill`
does. This is covered by
[session_engine_internal_test.go](../server/services/session_engine_internal_test.go)
(`TestInternalDetachDoesNotKill`, `TestInternalKillAndRestart`,
`TestInternalNaturalExit`).

## Viewers

Each WebSocket connection is a **viewer** with a stable `viewerID`. Sessions
track their set of viewers, which lets the engine keep a session's streaming PTY
open while any viewer is attached and enables future Control Room views such as:

```
Session: power-code-deck
Viewers: Desktop, iPad
Status:  running
```

## Session status

```
running  — the process is alive
exited   — the process ended on its own
killed   — terminated by an explicit user action
stopped  — no longer alive (e.g. after a server restart)
unknown  — status could not be determined
```

The internal engine distinguishes `exited` (the process ended on its own) from
`killed` (an explicit user action) precisely, and reports `running` while the
process is alive.

## Scrollback / replay

`Attach` returns an `AttachResult{ Replay []byte }`. The internal engine returns
the session's ring-buffer snapshot, which the hub sends to the newly-attached
viewer before live output resumes — so a reconnecting browser/mobile sees the
current screen.

## Server restart behavior

PowerCodeDeck now owns PTY processes directly.

If the PowerCodeDeck **server process restarts**, live shell/Claude processes may
stop — session lifetime is currently tied to the `pcd` process. The app does
**not** auto-respawn agents after a restart; a stopped session stays `stopped`
and the user presses Restart to start a fresh one. A future version
(`pcd-sessiond`, below) will separate session lifetime from the web server so
sessions survive a `pcd` restart.

Browser disconnects are unaffected — those are viewer detaches, and the process
keeps running.

## Native builds (no cgo, no WSL)

The internal engine uses **go-pty** (Unix PTY on mac/Linux, **ConPTY on
Windows**) and the DB uses **pure-Go SQLite** (`modernc.org/sqlite`). There is no
cgo and no C toolchain requirement, so `pcd` cross-compiles to a native binary
on all three platforms:

```
make build            # host binary (pcd)
make build-windows    # native pcd.exe (no WSL, no cgo)
```

`GOOS=windows CGO_ENABLED=0 go build` produces a real `pcd.exe`. (The WSL-based
installer remains the tested/recommended path on Windows until the native `.exe`
is validated on Windows hardware.)

## Future phase: pcd-sessiond

To also survive a `pcd` restart, session ownership can move into a separate
daemon:

```
Browser → pcd → pcd-sessiond → PTY/ConPTY → Claude / Shell
```

- **pcd** — web server, API, auth, QR handoff, Control Room, file explorer.
- **pcd-sessiond** — persistent session daemon that owns PTY/ConPTY processes,
  ring buffers, and viewer attach/detach. `pcd` can restart without killing live
  sessions.

Reached via a third `SessionEngine` implementation, `RemoteSessionEngineClient`.

### Implementations

- `InternalPtySessionEngine` — **current** — in-process PTY, no tmux (mac/Linux
  today; go-pty/ConPTY for native Windows later)
- `TmuxSessionEngine` — **removed** — earlier tmux-backed implementation
- `RemoteSessionEngineClient` — **planned** — talks to `pcd-sessiond`

### Draft pcd-sessiond API

```http
POST /sessions            # create a session
GET  /sessions            # list sessions
GET  /sessions/:id        # session info
POST /sessions/:id/write  # send input
POST /sessions/:id/resize # resize PTY
POST /sessions/:id/kill   # terminate the process
POST /sessions/:id/restart
GET  /sessions/:id/stream # replay ring buffer, then live output;
                          # client disconnect = viewer Detach (process lives)
```

Security:

- `pcd-sessiond` binds to `127.0.0.1` only and is never exposed to the internet.
- `pcd` authenticates to it with an internal token.
- Transport may later become a Unix socket (Linux/macOS) or named pipe (Windows),
  with localhost TCP as a fallback.

### Draft configuration (not yet implemented)

```env
POWERCODEDECK_SESSIOND_ENABLED=false                    # opt into the remote daemon
POWERCODEDECK_SESSIOND_ADDR=http://127.0.0.1:33034
POWERCODEDECK_SESSIOND_TOKEN=
POWERCODEDECK_SESSION_SCROLLBACK_BYTES=524288
```

## Invariant to preserve, always

> **Detach is not Kill.** A viewer disconnecting must never terminate the
> underlying agent process.
