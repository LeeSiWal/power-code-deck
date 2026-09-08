// Legacy session API. New code should import internal/runtime/session directly.
package services

import "powercodedeck/internal/runtime/session"

type SessionEngine = session.SessionEngine
type OutputHandler = session.OutputHandler
type CreateSessionRequest = session.CreateSessionRequest
type SessionInfo = session.SessionInfo
type AttachResult = session.AttachResult

const (
	SessionRunning = session.SessionRunning
	SessionExited  = session.SessionExited
	SessionKilled  = session.SessionKilled
	SessionStopped = session.SessionStopped
	SessionUnknown = session.SessionUnknown
)
