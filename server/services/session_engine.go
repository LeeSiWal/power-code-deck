package services

import "time"

// SessionEngine is the boundary between PowerCodeDeck's web/API/hub layer and
// the terminal session runtime. Everything above it (handlers, WebSocket hub,
// agent service) talks to sessions ONLY through this interface — it must not
// reach for tmux or a raw PTY directly.
//
// The single most important rule this interface encodes:
//
//	Detach is NOT Kill.
//	  Detach — a viewer (browser/tab/mobile) leaves a session. The underlying
//	           shell/agent process keeps running.
//	  Kill   — the underlying process is terminated. Only Delete/Restart (or an
//	           explicit user action) may do this.
//
// The concrete implementation is [InternalPtySessionEngine]: `pcd` owns each
// session's process + PTY directly (go-pty: Unix PTY on mac/Linux, ConPTY on
// Windows), with no tmux. The interface is shaped so it can later be swapped for
// a RemoteSessionEngine client that talks to a separate `pcd-sessiond` daemon —
// with no change to the callers. See docs/session-engine.md.
type SessionEngine interface {
	// Create starts a new session's underlying process and registers it.
	Create(req CreateSessionRequest) (*SessionInfo, error)

	// Attach registers a viewer on an existing session and returns any
	// scrollback to replay. It NEVER starts or restarts the process.
	Attach(sessionID, viewerID string) (*AttachResult, error)
	// Detach removes a viewer. It NEVER kills the process.
	Detach(sessionID, viewerID string) error

	// Write forwards input bytes to the session's process.
	Write(sessionID string, data []byte) error
	// Ack reports that a viewer has processed n bytes of output, draining the
	// flow-control backlog so the (backpressured) read pump can resume.
	Ack(sessionID string, n int)
	// Resize adjusts the session's PTY window size.
	Resize(sessionID string, cols, rows int) error

	// Kill terminates the underlying process and drops the session.
	Kill(sessionID string) error
	// Restart kills the current process (if any) and starts a fresh one.
	Restart(sessionID string) error

	// HasSession reports whether the underlying process is currently alive.
	HasSession(sessionID string) bool
	// Get returns the last-known metadata for a session.
	Get(sessionID string) (*SessionInfo, error)
	// List returns all sessions the engine currently tracks.
	List() ([]SessionInfo, error)

	// SetOutputHandler installs the callback that receives every session's
	// output. The hub wires this to its per-session broadcast.
	SetOutputHandler(handler OutputHandler)
}

// OutputHandler receives a chunk of a session's terminal output.
type OutputHandler func(sessionID string, data []byte)

// Session status values. The internal engine distinguishes exited (process
// ended on its own) from killed (explicit user action) precisely.
const (
	SessionRunning = "running" // process is alive
	SessionExited  = "exited"  // process ended on its own
	SessionKilled  = "killed"  // terminated by an explicit user action
	SessionStopped = "stopped" // not alive (e.g. after a server restart)
	SessionUnknown = "unknown" // status could not be determined
)

// CreateSessionRequest describes a session to start. ID is chosen by the caller
// (PowerCodeDeck uses the agent id) so the session id == agent id everywhere.
type CreateSessionRequest struct {
	ID      string
	Type    string // shell | claude | antigravity | codex | custom
	Command string
	Args    []string
	Cwd     string
	Cols    int
	Rows    int
}

// SessionInfo is the engine's view of a session. It intentionally overlaps only
// with runtime concerns — durable agent metadata (name, color, …) stays in the
// AgentService/DB.
type SessionInfo struct {
	ID        string
	Type      string
	Command   string
	Args      []string
	Cwd       string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AttachResult carries scrollback to replay to a freshly-attached viewer
// (the engine's ring-buffer snapshot). Replay may be nil when there is nothing
// buffered yet.
type AttachResult struct {
	SessionID string
	Replay    []byte
}
