package services

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
)

// ApproveTokenStore hands out and checks the per-session secret that the
// permission bridge (`pcd mcp-approve`) presents when it asks the deck for a
// decision.
//
// Why a token at all, when the endpoint is loopback-only: anything running as this
// user can reach loopback. Without a token, any local process could raise fake
// approval prompts ("Bash: rm -rf …, approve?") on a real session and try to phish
// a tap, or answer for a session it doesn't own. The token scopes a bridge to the
// exactly one session it was spawned for.
type ApproveTokenStore interface {
	// Valid reports whether token belongs to sessionID. Comparison is
	// constant-time — a timing oracle on a loopback secret is still an oracle.
	Valid(sessionID, token string) bool
}

type approveTokens struct {
	mu     sync.RWMutex
	tokens map[string]string // sessionID -> token
}

func NewApproveTokenStore() *approveTokens {
	return &approveTokens{tokens: make(map[string]string)}
}

// Issue mints (or replaces) the token for a session and returns it. Called when a
// native session starts; the value goes to the bridge through the CLI's
// --mcp-config env block and never touches the browser.
func (s *approveTokens) Issue(sessionID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[sessionID] = tok
	s.mu.Unlock()
	return tok, nil
}

// Revoke drops a session's token — its process is gone, so its bridge must not be
// able to ask anything ever again.
func (s *approveTokens) Revoke(sessionID string) {
	s.mu.Lock()
	delete(s.tokens, sessionID)
	s.mu.Unlock()
}

func (s *approveTokens) Valid(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	s.mu.RLock()
	want := s.tokens[sessionID]
	s.mu.RUnlock()
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1
}
