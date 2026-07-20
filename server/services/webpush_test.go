package services

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

// TestEncryptPayloadRFC8291 uses the RFC 8291 §5 example inputs (receiver keys, auth
// secret, sender ephemeral key, salt) and proves correctness the way a browser does:
// it DECRYPTS our output with the receiver's private key and recovers the exact
// plaintext. This is stronger than string-matching the RFC's ciphertext (and doesn't
// depend on transcribing it): a byte wrong anywhere and the GCM open fails. If this
// breaks, notifications would encrypt to something no browser can read — a silent,
// invisible failure — so it is the guardrail for the whole push feature.
func TestEncryptPayloadRFC8291(t *testing.T) {
	b64 := base64.RawURLEncoding
	dec := func(s string) []byte {
		v, err := b64.DecodeString(s)
		if err != nil {
			t.Fatalf("decode %q: %v", s, err)
		}
		return v
	}

	plaintext := []byte("When I grow up, I want to be a watermelon")
	auth := dec("BTBZMqHH6r4Tts7J_aSIgg")
	uaPublic := dec("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	uaPrivRaw := dec("q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94")
	asPrivRaw := dec("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	salt := dec("DGv6ra1nlYgDCS1FRnbzlw")

	asPriv, err := ecdh.P256().NewPrivateKey(asPrivRaw)
	if err != nil {
		t.Fatalf("as private key: %v", err)
	}
	body, err := encryptPayloadWith(uaPublic, auth, plaintext, asPriv, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// The salt at the front must be the one we passed (RFC 8188 header), and the
	// keyid must carry the sender's public point — a quick structural anchor before
	// the real proof (decrypt) below.
	if !bytes.Equal(body[:16], salt) {
		t.Fatalf("body does not start with the salt")
	}

	got := decryptRFC8291(t, body, uaPublic, uaPrivRaw, auth)
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip mismatch\n got: %q\nwant: %q", got, plaintext)
	}
}

// decryptRFC8291 is the receiver side of RFC 8291 — what a browser's push service
// does — used only to verify our encryption. It parses the aes128gcm body, redoes
// the key agreement with the receiver's private key, and opens the single record.
func decryptRFC8291(t *testing.T, body, uaPublic, uaPrivRaw, auth []byte) []byte {
	t.Helper()
	salt := body[:16]
	idlen := int(body[20])
	asPublic := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]
	_ = binary.BigEndian.Uint32(body[16:20]) // rs — unused for a single record

	uaPriv, err := ecdh.P256().NewPrivateKey(uaPrivRaw)
	if err != nil {
		t.Fatalf("ua private key: %v", err)
	}
	asPub, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatalf("as public key: %v", err)
	}
	shared, err := uaPriv.ECDH(asPub)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	keyInfo := append([]byte("WebPush: info\x00"), uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm := hkdf32(auth, shared, keyInfo, 32)
	cek := hkdf32(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf32(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("gcm open failed — encryption is wrong: %v", err)
	}
	// Strip the RFC 8188 padding delimiter (0x02 for the last record).
	if n := len(record); n > 0 && record[n-1] == 0x02 {
		record = record[:n-1]
	}
	return record
}

// TestEncryptPayloadProductionRoundTrip proves the REAL production path end to end:
// a simulated browser subscription (fresh ECDH keypair + auth, base64url-encoded like
// a real PushManager gives us), fed through decodeB64 + encryptPayload (random
// ephemeral + salt), then decrypted with the subscription's private key. If a browser
// couldn't read our messages, this fails — closing the gap the fixed-vector test
// leaves (random keys, the decodeB64 path).
func TestEncryptPayloadProductionRoundTrip(t *testing.T) {
	uaPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authRaw := make([]byte, 16)
	if _, err := rand.Reader.Read(authRaw); err != nil {
		t.Fatal(err)
	}
	// Encode exactly as a browser subscription would deliver them.
	p256dhB64 := base64.RawURLEncoding.EncodeToString(uaPriv.PublicKey().Bytes())
	authB64 := base64.RawURLEncoding.EncodeToString(authRaw)

	plaintext := []byte(`{"title":"작업 완료","body":"에이전트가 끝났어요"}`)
	body, err := encryptPayload(decodeB64(p256dhB64), decodeB64(authB64), plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got := decryptRFC8291(t, body, uaPriv.PublicKey().Bytes(), uaPriv.Bytes(), authRaw)
	if !bytes.Equal(got, plaintext) {
		t.Errorf("production round-trip mismatch\n got: %q\nwant: %q", got, plaintext)
	}
}

// TestVAPIDKeysRoundTrip checks a generated keypair serializes and parses back, and
// signs a header whose parts are well-formed.
func TestVAPIDKeysRoundTrip(t *testing.T) {
	privB64, pubB64, err := generateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if pubB64 == "" {
		t.Fatal("empty public key")
	}
	// The public key must be a 65-byte uncompressed P-256 point.
	pub, err := base64.RawURLEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != 65 || pub[0] != 0x04 {
		t.Fatalf("public key not a 65-byte uncompressed point: len=%d err=%v", len(pub), err)
	}
	priv, err := parseVAPIDPrivate(privB64)
	if err != nil {
		t.Fatalf("parse private: %v", err)
	}
	h, err := vapidAuthHeader("https://push.example.com/xyz", priv, pubB64, "mailto:admin@example.com")
	if err != nil {
		t.Fatalf("auth header: %v", err)
	}
	if len(h) < len("vapid t=, k=")+10 || h[:8] != "vapid t=" {
		t.Fatalf("malformed auth header: %q", h)
	}
}
