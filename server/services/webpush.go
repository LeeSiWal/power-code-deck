package services

// Web Push wire crypto — RFC 8291 (message encryption, aes128gcm content-encoding)
// and RFC 8292 (VAPID). Implemented on the standard library plus the JWT dep we
// already carry, so the deck keeps its "no new dependencies" promise: no webpush
// module, no APNs/FCM SDK. Correctness is pinned by the RFC 8291 §5 test vector in
// webpush_test.go — this is the one place a silent bug means notifications simply
// never arrive, so it is tested, not trusted.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/hkdf"
)

// webpushKeys are the browser's ECDH public key (p256dh, 65-byte uncompressed) and
// its 16-byte auth secret, exactly as the PushManager subscription delivers them.
type webpushKeys struct {
	p256dh []byte
	auth   []byte
}

// encryptPayload implements RFC 8291 §3: it encrypts plaintext for a subscription
// using a fresh ephemeral key and salt. The returned bytes are the request body —
// RFC 8188 header (salt | rs | idlen | keyid) followed by one AES-128-GCM record.
func encryptPayload(uaPublic, authSecret, plaintext []byte) ([]byte, error) {
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	return encryptPayloadWith(uaPublic, authSecret, plaintext, asPriv, salt)
}

// encryptPayloadWith is the deterministic core, split out so the RFC test vector can
// pin the exact ephemeral key + salt. Never call this directly outside tests.
func encryptPayloadWith(uaPublic, authSecret, plaintext []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaPub, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("invalid p256dh key: %w", err)
	}
	shared, err := asPriv.ECDH(uaPub)
	if err != nil {
		return nil, err
	}
	asPublic := asPriv.PublicKey().Bytes() // 65-byte uncompressed point

	// RFC 8291 §3.4: the ECDH secret becomes the IKM, keyed by the auth secret, with
	// key_info binding both public keys so a captured message can't be replayed to a
	// different subscription.
	keyInfo := append([]byte("WebPush: info\x00"), uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm := hkdf32(authSecret, shared, keyInfo, 32)

	// RFC 8188 §2.2/2.3: content-encryption key and nonce from the per-message salt.
	cek := hkdf32(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf32(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: the plaintext followed by the 0x02 "last record" delimiter (RFC
	// 8188 §2.1). No extra padding — our payloads are tiny and single-record.
	record := make([]byte, 0, len(plaintext)+1)
	record = append(record, plaintext...)
	record = append(record, 0x02)
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	var body bytes.Buffer
	body.Write(salt)                                    // 16 octets
	_ = binary.Write(&body, binary.BigEndian, uint32(4096)) // record size
	body.WriteByte(byte(len(asPublic)))                 // keyid length (65)
	body.Write(asPublic)                                // keyid = as_public
	body.Write(ciphertext)
	return body.Bytes(), nil
}

// hkdf32 = HKDF-SHA256 with Extract(salt, secret) then Expand(info), truncated to n.
func hkdf32(salt, secret, info []byte, n int) []byte {
	r := hkdf.New(sha256.New, secret, salt, info)
	out := make([]byte, n)
	_, _ = io.ReadFull(r, out)
	return out
}

// --- VAPID (RFC 8292) -------------------------------------------------------

// generateVAPIDKeys creates a P-256 application-server keypair. The private key is
// returned PKCS#8/base64url for storage; the public key is the 65-byte uncompressed
// point base64url, which is both the `k=` VAPID param and the applicationServerKey
// a browser subscribes with.
func generateVAPIDKeys() (privB64, pubB64 string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	ecdhPub, err := priv.PublicKey.ECDH()
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(pkcs8),
		base64.RawURLEncoding.EncodeToString(ecdhPub.Bytes()), nil
}

// parseVAPIDPrivate restores the ECDSA signing key from its stored PKCS#8 form.
func parseVAPIDPrivate(privB64 string) (*ecdsa.PrivateKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(privB64)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("stored VAPID key is not ECDSA")
	}
	return ec, nil
}

// vapidAuthHeader builds the Authorization header value for one push endpoint. The
// JWT audience is the endpoint's origin (scheme://host), signed ES256 with the
// application-server key; the push service validates it against `k=`.
func vapidAuthHeader(endpoint string, priv *ecdsa.PrivateKey, pubB64, sub string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": u.Scheme + "://" + u.Host,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": sub,
	})
	signed, err := tok.SignedString(priv)
	if err != nil {
		return "", err
	}
	return "vapid t=" + signed + ", k=" + pubB64, nil
}

// sendWebPush encrypts and POSTs one notification. It returns the push service's
// HTTP status so the caller can prune a subscription the service reports as gone
// (404/410). ttl is seconds the service may hold the message for an offline device.
func sendWebPush(client *http.Client, endpoint string, keys webpushKeys, priv *ecdsa.PrivateKey, pubB64, sub string, payload []byte, ttl int) (int, error) {
	body, err := encryptPayload(keys.p256dh, keys.auth, payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	auth, err := vapidAuthHeader(endpoint, priv, pubB64, sub)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(ttl))
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
