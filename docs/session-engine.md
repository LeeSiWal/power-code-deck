# PowerCodeDeck Session Architecture

## Current phase

PowerCodeDeck routes all terminal/agent session management through a single
`SessionEngine` interface ([server/services/session_engine.go](../server/services/session_engine.go)).
The current implementation, `TmuxSessionEngine`
([server/services/session_engine_tmux.go](../server/services/session_engine_tmux.go)),
still uses **tmux + a PTY** internally — but the web/API/WebSocket layers no
longer depend on tmux directly.

```
Browser / Mobile / iPad
        │
        ▼
      pcd            Web UI · REST API · WebSocket gateway · Auth · QR Handoff
        │
        ▼
  SessionEngine      (interface — the only way sessions are touched)
        │
        ▼
  TmuxSessionEngine  Persistent session · viewer attach/detach · PTY streaming
        │
        ▼
  tmux + PTY  →  Claude / Gemini / Codex / Shell
```

Handlers, the WebSocket hub, and the agent service call **only**
`SessionEngine`. They never import tmux, never call `ptySvc.Close`, and never
handle a tmux session name.

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

In `TmuxSessionEngine`, Detach tears down the `tmux attach` streaming PTY when
the last viewer leaves, but **never** runs `tmux kill-session`. Only `Kill`
does. This is covered by
[session_engine_tmux_test.go](../server/services/session_engine_tmux_test.go)
(`TestDetachDoesNotKill`, `TestMultiViewerDetach`, `TestRestartKeepsSessionAlive`).

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

The tmux engine currently maps these onto tmux-session existence
(`running` / `stopped`); a richer in-process engine will distinguish
`exited` vs `killed` precisely.

## Scrollback / replay

`Attach` returns an `AttachResult{ Replay []byte }`. The tmux engine leaves
`Replay` nil and relies on tmux redrawing the pane on attach. A future
in-process engine returns its ring-buffer snapshot here, which the hub sends to
the newly-attached viewer before live output resumes.

## Future phase: in-process PTY

The next implementation, `InternalPtySessionEngine`, will drop tmux and own the
child process + PTY directly (via `creack/pty` on Unix, `go-pty`/ConPTY on
Windows), keeping a scrollback ring buffer. This makes PowerCodeDeck run
**natively on Windows/macOS/Linux with no tmux and no WSL**. Because callers
already talk only to `SessionEngine`, this is a drop-in replacement.

Trade-off (accepted): without a separate daemon, sessions do not survive a
`pcd` **server restart** — they survive browser disconnects, which is what
handoff needs.

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

### Planned implementations

- `TmuxSessionEngine` — **current**
- `InternalPtySessionEngine` — in-process PTY/ConPTY, no tmux
- `RemoteSessionEngineClient` — talks to `pcd-sessiond`

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
POWERCODEDECK_SESSION_ENGINE=tmux            # tmux | internal | remote
POWERCODEDECK_SESSIOND_ENABLED=false
POWERCODEDECK_SESSIOND_ADDR=http://127.0.0.1:33034
POWERCODEDECK_SESSIOND_TOKEN=
POWERCODEDECK_SESSION_SCROLLBACK_BYTES=524288
```

## Invariant to preserve, always

> **Detach is not Kill.** A viewer disconnecting must never terminate the
> underlying agent process.
