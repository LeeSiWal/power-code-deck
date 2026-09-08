package pty

import "powercodedeck/internal/runtime/session"

// LaunchSpec is a prepared process invocation. Command must be executable by
// go-pty (normally an absolute path; Windows script shims must be resolved by
// the caller). Env follows os/exec semantics: nil inherits the host environment.
// Cwd and terminal dimensions come from the session request.
type LaunchSpec struct {
	Command string
	Args    []string
	Env     []string
}

// PrepareLaunch resolves a request before a PTY is opened. It is called again
// on Restart and may be called concurrently for different sessions. Returning
// an error creates no PTY, process or registered session. Implementations own
// CLI discovery, platform shims, environment and installation policy.
type PrepareLaunch func(session.CreateSessionRequest) (LaunchSpec, error)

var _ session.SessionEngine = (*Engine)(nil)
