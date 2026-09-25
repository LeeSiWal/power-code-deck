// Legacy session API. New code should import internal/runtime/session directly.
package services

import "powercodedeck/internal/runtime/session"

type SessionEngine = session.SessionEngine
type OutputHandler = session.OutputHandler
type CreateSessionRequest = session.CreateSessionRequest
type SessionInfo = session.SessionInfo
type AttachResult = session.AttachResult

// Only the states this package still names itself remain aliased; the rest were
// retired once no caller depended on them.
const SessionExited = session.SessionExited
