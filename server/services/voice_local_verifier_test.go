package services

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalSpeakerVerifierRejectsNonLoopback(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1:18743",
		"http://example.com:18743",
		"http://192.168.1.20:18743",
		"http://127.0.0.1:18743/path",
		"http://127.0.0.1:18743?redirect=true",
	} {
		if _, err := NewLocalSpeakerVerifier(endpoint, 0.7); err == nil {
			t.Fatalf("accepted unsafe sidecar endpoint %q", endpoint)
		}
	}
	if _, err := NewLocalSpeakerVerifier("http://127.0.0.1:18743", 0.7); err != nil {
		t.Fatalf("rejected valid loopback sidecar: %v", err)
	}
}

func TestNormalizeSpeakerEmbedding(t *testing.T) {
	if _, err := normalizeSpeakerEmbedding([]float64{0, 0}); !errors.Is(err, ErrVoiceUnavailable) {
		t.Fatalf("zero vector accepted: %v", err)
	}
	if _, err := normalizeSpeakerEmbedding([]float64{math.NaN()}); !errors.Is(err, ErrVoiceUnavailable) {
		t.Fatalf("non-finite vector accepted: %v", err)
	}
	a, err := normalizeSpeakerEmbedding([]float64{3, 4})
	if err != nil { t.Fatal(err) }
	if math.Abs(a[0]-0.6) > 1e-9 || math.Abs(a[1]-0.8) > 1e-9 {
		t.Fatalf("incorrect normalized vector: %v", a)
	}
}

func TestLocalSpeakerVerifierFailuresNeverAuthorize(t *testing.T) {
	// A sidecar returning malformed or tiny vectors must fail closed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float64{1, 0}})
	}))
	defer server.Close()
	verifier, err := NewLocalSpeakerVerifier(server.URL, 0.7)
	if err != nil { t.Fatal(err) }
	audio := make([]byte, 64)
	_, err = verifier.Match(context.Background(), []byte("[1,0]"), audio)
	if !errors.Is(err, ErrVoiceUnavailable) {
		t.Fatalf("malformed response not rejected: %v", err)
	}
	if _, err := verifier.Enroll(context.Background(), [][]byte{audio, audio, audio}); !errors.Is(err, ErrVoiceUnavailable) {
		t.Fatalf("invalid embedding was accepted for enrollment: %v", err)
	}
}
