package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"time"
)

// LocalSpeakerVerifier sends WAV audio only to a loopback-bound speaker embedding
// sidecar. This service does not expose a public inference endpoint.
type LocalSpeakerVerifier struct {
	endpoint string
	client   *http.Client
	threshold float64
}

func NewLocalSpeakerVerifier(endpoint string, threshold float64) (*LocalSpeakerVerifier, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("speaker sidecar must be an HTTP loopback origin")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("speaker sidecar may only use loopback")
	}
	if threshold <= 0 || threshold >= 1 {
		return nil, errors.New("speaker similarity threshold must be between 0 and 1")
	}
	// Disallow redirects: a compromised sidecar must not redirect voice samples
	// to a remote URL. Resolve localhost through an explicit loopback dial.
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", u.Port()))
		}},
	}
	return &LocalSpeakerVerifier{endpoint: endpoint, client: client, threshold: threshold}, nil
}

func (v *LocalSpeakerVerifier) embedding(ctx context.Context, wav []byte) ([]float64, error) {
	if len(wav) < 44 || len(wav) > 4*1024*1024 {
		return nil, ErrVoiceMismatch
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint+"/embed", bytes.NewReader(wav))
	if err != nil { return nil, err }
	req.Header.Set("Content-Type", "audio/wav")
	resp, err := v.client.Do(req)
	if err != nil { return nil, fmt.Errorf("%w: %v", ErrVoiceUnavailable, err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return nil, ErrVoiceUnavailable }
	var result struct { Embedding []float64 `json:"embedding"` }
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 16*1024)).Decode(&result); err != nil {
		return nil, ErrVoiceUnavailable
	}
	if len(result.Embedding) < 64 || len(result.Embedding) > 1024 { return nil, ErrVoiceUnavailable }
	return normalizeSpeakerEmbedding(result.Embedding)
}

func normalizeSpeakerEmbedding(values []float64) ([]float64, error) {
	var length float64
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) { return nil, ErrVoiceUnavailable }
		length += v * v
	}
	if length < 1e-12 { return nil, ErrVoiceUnavailable }
	length = math.Sqrt(length)
	out := make([]float64, len(values))
	for i, v := range values { out[i] = v / length }
	return out, nil
}

func (v *LocalSpeakerVerifier) Enroll(ctx context.Context, samples [][]byte) ([]byte, error) {
	if len(samples) < 3 || len(samples) > 10 { return nil, ErrVoiceMismatch }
	var avg []float64
	for _, sample := range samples {
		embedding, err := v.embedding(ctx, sample)
		if err != nil { return nil, err }
		if avg == nil { avg = make([]float64, len(embedding)) }
		if len(avg) != len(embedding) { return nil, ErrVoiceUnavailable }
		for i, x := range embedding { avg[i] += x }
	}
	normalized, err := normalizeSpeakerEmbedding(avg)
	if err != nil { return nil, err }
	return json.Marshal(normalized)
}

func (v *LocalSpeakerVerifier) Match(ctx context.Context, template, sample []byte) (bool, error) {
	var enrolled []float64
	if err := json.Unmarshal(template, &enrolled); err != nil { return false, ErrVoiceUnavailable }
	stored, err := normalizeSpeakerEmbedding(enrolled)
	if err != nil { return false, err }
	current, err := v.embedding(ctx, sample)
	if err != nil { return false, err }
	if len(stored) != len(current) { return false, ErrVoiceUnavailable }
	var similarity float64
	for i := range stored { similarity += stored[i] * current[i] }
	return similarity >= v.threshold, nil
}
