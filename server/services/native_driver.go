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
}
