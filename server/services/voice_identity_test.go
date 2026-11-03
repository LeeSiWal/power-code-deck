package services

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

type testSpeakerVerifier struct {
	match bool
	err   error
}

func (v *testSpeakerVerifier) Enroll(_ context.Context, samples [][]byte) ([]byte, error) {
	if v.err != nil {
		return nil, v.err
	}
	return []byte("fake-test-embedding"), nil
}

func (v *testSpeakerVerifier) Match(_ context.Context, _, sample []byte) (bool, error) {
	if v.err != nil {
		return false, v.err
	}
	return v.match, nil
}

func TestVoiceIdentityDefaultsOffAndMissingVerifierFailsClosed(t *testing.T) {
	s := NewVoiceIdentityService(nil)
	if s.Enabled() {
		t.Fatal("voice protection must default off")
	}
	if err := s.Enable(); !errors.Is(err, ErrVoiceUnavailable) {
		t.Fatalf("expected verifier unavailable, got %v", err)
	}
	if ok, err := s.Verify(context.Background(), []byte("audio")); ok || !errors.Is(err, ErrVoiceUnavailable) {
		t.Fatalf("unverified audio accepted: %v %v", ok, err)
	}
}

func TestVoiceIdentityEnrollmentVerificationAndDeletion(t *testing.T) {
	v := &testSpeakerVerifier{match: true}
	s := NewVoiceIdentityService(v)
	if err := s.Enable(); !errors.Is(err, ErrVoiceNotEnrolled) {
		t.Fatalf("must enroll before enabling, got %v", err)
	}
	if err := s.Enroll(context.Background(), [][]byte{[]byte("a"), []byte("b")}); err == nil {
		t.Fatal("accepted fewer than three samples")
	}
	if err := s.Enroll(context.Background(), [][]byte{[]byte("a"), []byte("b"), []byte("c")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Verify(context.Background(), []byte("sample")); !ok || err != nil {
		t.Fatalf("expected enrolled speaker, got %v %v", ok, err)
	}
	v.match = false
	if ok, err := s.Verify(context.Background(), []byte("other")); ok || !errors.Is(err, ErrVoiceMismatch) {
		t.Fatalf("unknown speaker accepted: %v %v", ok, err)
	}
	v.err = errors.New("model unavailable")
	if ok, err := s.Verify(context.Background(), []byte("sample")); ok || err == nil {
		t.Fatal("model failure must not authorize")
	}
	s.DeleteProfile()
	if s.Enabled() {
		t.Fatal("deleting voice profile must disable voice protection")
	}
	if ok, _ := s.Verify(context.Background(), []byte("sample")); ok {
		t.Fatal("deleted profile accepted")
	}
}

func TestVoiceIdentityTicketBoundAndOneTime(t *testing.T) {
	s := NewVoiceIdentityService(&testSpeakerVerifier{match: true})
	_ = s.Enroll(context.Background(), [][]byte{[]byte("a"), []byte("b"), []byte("c")})
	if err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("prompt"))
	ticket := VoiceTicket{UserID: "user", AgentID: "agent-claude", Digest: digest, ExpiresAt: time.Now().Add(10*time.Second)}
	if err := s.StoreTicket("random-id", ticket); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeTicket("random-id", "user", "agent-codex", digest); !errors.Is(err, ErrVoiceMismatch) {
		t.Fatalf("other agent accepted: %v", err)
	}
	if err := s.ConsumeTicket("random-id", "user", "agent-claude", digest); !errors.Is(err, ErrVoiceConsumed) {
		t.Fatalf("ticket replay accepted: %v", err)
	}
	if err := s.StoreTicket("second-id", ticket); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeTicket("second-id", "user", "agent-claude", digest); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeTicket("second-id", "user", "agent-claude", digest); !errors.Is(err, ErrVoiceConsumed) {
		t.Fatalf("ticket replay accepted: %v", err)
	}
	if err := s.StoreTicket("expired", VoiceTicket{UserID: "user", AgentID: "agent-claude", Digest: digest, ExpiresAt: time.Now().Add(-time.Second)}); err == nil {
		t.Fatal("accepted expired ticket")
	}
	s.Disable()
	if err := s.StoreTicket("disabled", ticket); err == nil {
		t.Fatal("accepted ticket while disabled")
	}
}
