package services

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"time"
)

// Handoff redeem outcomes. These are surfaced to the client so the failure
// screen can explain what went wrong (without leaking the raw token).
var (
	ErrHandoffInvalid = errors.New("handoff token invalid")
	ErrHandoffExpired = errors.New("handoff token expired")
	ErrHandoffUsed    = errors.New("handoff token already used")
)

const handoffTimeLayout = "2006-01-02T15:04:05Z"

// HandoffToken is the persisted record for a one-time "Continue on Mobile" link.
// The raw token is never stored — only its SHA-256 hash.
type HandoffToken struct {
	ID        string
	SessionID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type HandoffService struct {
	db *sql.DB
}

func NewHandoffService(db *sql.DB) *HandoffService {
	return &HandoffService{db: db}
}

// Create issues a fresh one-time token bound to sessionID and returns the raw
// token (shown once, embedded in the QR) plus its record. Any earlier unused
// token for the same session is invalidated so only the newest QR works.
func (s *HandoffService) Create(sessionID, createdBy, clientIP, userAgent string, ttl time.Duration) (rawToken string, rec *HandoffToken, err error) {
	rawToken = generateHandoffToken()
	hash := hashHandoffToken(rawToken)

	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	// Invalidate any prior unused tokens for this session (Regenerate semantics).
	s.db.Exec(
		"UPDATE handoff_tokens SET expires_at = ? WHERE session_id = ? AND used_at IS NULL",
		now.Format(handoffTimeLayout), sessionID,
	)

	_, err = s.db.Exec(
		`INSERT INTO handoff_tokens (id, token_hash, session_id, created_at, expires_at, created_by, client_ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, hash, sessionID, now.Format(handoffTimeLayout), expiresAt.Format(handoffTimeLayout), createdBy, clientIP, userAgent,
	)
	if err != nil {
		return "", nil, err
	}

	return rawToken, &HandoffToken{
		ID:        id,
		SessionID: sessionID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, nil
}

// Redeem validates and consumes a raw token. On success it marks the token used
// (atomically, so a token can only ever be redeemed once) and returns the bound
// session id. It never returns the session id for an expired/used/invalid token.
func (s *HandoffService) Redeem(rawToken string) (sessionID string, err error) {
	hash := hashHandoffToken(rawToken)

	var expiresStr string
	var usedAt sql.NullString
	row := s.db.QueryRow(
		"SELECT session_id, expires_at, used_at FROM handoff_tokens WHERE token_hash = ?",
		hash,
	)
	if err := row.Scan(&sessionID, &expiresStr, &usedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrHandoffInvalid
		}
		return "", err
	}

	if usedAt.Valid && usedAt.String != "" {
		return "", ErrHandoffUsed
	}

	expiresAt, perr := time.Parse(handoffTimeLayout, expiresStr)
	if perr != nil || time.Now().UTC().After(expiresAt) {
		return "", ErrHandoffExpired
	}

	// Consume atomically — WHERE used_at IS NULL guards against a double redeem
	// racing two concurrent requests.
	res, err := s.db.Exec(
		"UPDATE handoff_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL",
		time.Now().UTC().Format(handoffTimeLayout), hash,
	)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrHandoffUsed
	}

	return sessionID, nil
}

// CleanupExpired removes tokens that are both expired and long past use, keeping
// the table small. Best-effort; errors are ignored by callers.
func (s *HandoffService) CleanupExpired() error {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(handoffTimeLayout)
	_, err := s.db.Exec("DELETE FROM handoff_tokens WHERE expires_at < ?", cutoff)
	return err
}

func generateHandoffToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return "hdo_" + base64.RawURLEncoding.EncodeToString(b)
}

func hashHandoffToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// DetectLANIP returns the server's first private IPv4 address (e.g. 192.168.x.x)
// so the QR modal can offer a same-Wi-Fi handoff URL. Returns "" if none found.
func DetectLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			return ip4.String()
		}
	}
	return ""
}
