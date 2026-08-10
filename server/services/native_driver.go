package services

// NativeDriver is the small common surface shared by Claude's stream-json mode
// and Codex app-server. Both keep a conversation alive, accept turns, emit the
// UI's normalized StreamEvents, and can interrupt the active turn.
type NativeDriver interface {
	Start() error
	Events() <-chan *StreamEvent
	Send(string) error
	Interrupt() error
	ConversationID() string
	Stop()
	// SetPermissionMode changes the permission mode of the running session in place.
	// An error means "I can't do that without being restarted" — NativeService takes
	// that as permission to fall back to a restart, which is what every driver did
	// unconditionally before.
	SetPermissionMode(mode string) error
}
