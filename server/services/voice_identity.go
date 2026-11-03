package services

import (
	"context"
	"errors"
	"sync"
	"time"
)

// VoiceIdentity is a secondary guard for voice-originated turns, NOT a replacement
// for account authentication, passkeys, or tool approvals. Never take a
// client-supplied "verified" flag as proof of speaker identity.
var (
	ErrVoiceNotEnrolled = errors.New("voice identity is not enrolled")
	ErrVoiceUnavailable = errors.New("voice verifier unavailable")
	ErrVoiceMismatch  = errors.New("speaker could not be verified")
	ErrVoiceExpired   = errors.New("voice verification expired")
	ErrVoiceConsumed  = errors.New("voice verification already consumed")
)

// SpeakerVerifier is implemented by a trusted server-side speaker model.
// Raw audio never needs to be written to the general application logs.
type SpeakerVerifier interface {
	Enroll(ctx context.Context, samples [][]byte) ([]byte, error)
	Match(ctx context.Context, enrolledTemplate, sample []byte) (bool, error)
}

// VoiceTicket is deliberately opaque. It is consumed once and scoped to the
// user, agent, utterance digest, and a short deadline.
type VoiceTicket struct {
	UserID    string
	AgentID   string
	Digest    [32]byte
	ExpiresAt time.Time
}

type VoiceIdentityService struct {
	mu       sync.Mutex
	verifier SpeakerVerifier
	enabled  bool
	template []byte
	tickets  map[string]VoiceTicket
	now      func() time.Time
}

// NewVoiceIdentityService starts OFF and has no verifier. This intentional
// default never pretends to authenticate a voice without a deployed model.
func NewVoiceIdentityService(verifier SpeakerVerifier) *VoiceIdentityService {
	return &VoiceIdentityService{
		verifier: verifier,
		tickets: make(map[string]VoiceTicket),
		now:     time.Now,
	}
}

// Enrolled reports whether a temporary profile is present. Persistence is not
// implemented; a restart clears it and voice protection becomes unavailable.
func (s *VoiceIdentityService) Enrolled() bool {
 s.mu.Lock()
 defer s.mu.Unlock()
 return len(s.template)>0
}

func (s *VoiceIdentityService) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// Enable cannot activate protection without an enrolled profile and verifier.
// A production persistence layer must encrypt enrolled templates at rest.
func (s *VoiceIdentityService) Enable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verifier == nil {
		return ErrVoiceUnavailable
	}
	if len(s.template) == 0 {
		return ErrVoiceNotEnrolled
	}
	s.enabled = true
	return nil
}

func (s *VoiceIdentityService) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = false
	clear(s.tickets)
}

func (s *VoiceIdentityService) DeleteProfile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.template)
	s.template = nil
	s.enabled = false
	clear(s.tickets)
}

// Enroll receives consented audio from an authenticated call-site. Callers
// must enforce enrollment permissions and request limits. The raw samples are
// intentionally not retained in this service.
func (s *VoiceIdentityService) Enroll(ctx context.Context, samples [][]byte) error {
	if s.verifier == nil {
		return ErrVoiceUnavailable
	}
	if len(samples) < 3 {
		return ErrVoiceNotEnrolled
	}
	for _, sample := range samples {
		if len(sample) == 0 {
			return ErrVoiceMismatch
		}
	}
	tpl, err := s.verifier.Enroll(ctx, samples)
	if err != nil {
		return err
	}
	if len(tpl) == 0 {
		return ErrVoiceMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.template = append([]byte(nil), tpl...)
	clear(s.tickets)
	return nil
}

// Verify checks an audio utterance against the enrolled speaker. No success
// ticket is minted from a transcript, a client boolean, or similarity score.
func (s *VoiceIdentityService) Verify(ctx context.Context, audio []byte) (bool, error) {
	s.mu.Lock()
	enabled := s.enabled
	template := append([]byte(nil), s.template...)
	verifier := s.verifier
	s.mu.Unlock()
	if !enabled {
		return false, ErrVoiceUnavailable
	}
	if verifier == nil {
		return false, ErrVoiceUnavailable
	}
	if len(template) == 0 {
		return false, ErrVoiceNotEnrolled
	}
	if len(audio) == 0 {
		return false, ErrVoiceMismatch
	}
	match, err := verifier.Match(ctx, template, audio)
	if err != nil {
		return false, err
	}
	if !match {
		return false, ErrVoiceMismatch
	}
	return true, nil
}

// StoreTicket is called by a *trusted* verified-audio endpoint after Verify,
// using a crypto-random ID. It must never be exposed to untrusted callers as
// a generic "mark verified" endpoint.
func (s *VoiceIdentityService) StoreTicket(id string, t VoiceTicket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.verifier == nil || len(s.template) == 0 || id == "" ||
		t.UserID == "" || t.AgentID == "" || !t.ExpiresAt.After(s.now()) ||
		t.ExpiresAt.After(s.now().Add(30*time.Second)) {
		return ErrVoiceUnavailable
	}
	s.tickets[id] = t
	return nil
}

// ConsumeTicket binds verification to exactly one session, user, and payload.
// A mismatch always consumes the token, preventing retries with other text.
func (s *VoiceIdentityService) ConsumeTicket(id, userID, agentID string, digest [32]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	delete(s.tickets, id)
	if !s.enabled || !ok {
		return ErrVoiceConsumed
	}
	if !s.now().Before(t.ExpiresAt) {
		return ErrVoiceExpired
	}
	if t.UserID != userID || t.AgentID != agentID || t.Digest != digest {
		return ErrVoiceMismatch
	}
	return nil
}
