package auth

import (
	"crypto/subtle"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthService struct {
	enabled      bool
	method       string // "none" | "pin" | "password"
	pin          string
	passwordHash string
	jwtSecret    []byte
}

func NewAuthService(enabled bool, method, pin, passwordHash, jwtSecret string) *AuthService {
	if method == "" {
		method = "none"
	}
	return &AuthService{
		enabled:      enabled,
		method:       method,
		pin:          pin,
		passwordHash: passwordHash,
		jwtSecret:    []byte(jwtSecret),
	}
}

// Enabled reports whether PowerCodeDeck enforces its own authentication.
func (s *AuthService) Enabled() bool { return s.enabled }

// Method returns the configured auth method: none, pin, or password.
func (s *AuthService) Method() string { return s.method }

// VerifyCredential checks a submitted secret (PIN or password) against the
// configured method. Returns true in no-auth mode.
func (s *AuthService) VerifyCredential(secret string) bool {
	switch s.method {
	case "pin":
		return s.pin != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(s.pin)) == 1
	case "password":
		return VerifyPassword(secret, s.passwordHash)
	default:
		return true
	}
}

func (s *AuthService) GenerateToken() (string, error) {
	claims := jwt.MapClaims{
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(7 * 24 * time.Hour).Unix(),
		"type": "access",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

func (s *AuthService) GenerateRefreshToken() (string, error) {
	claims := jwt.MapClaims{
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(30 * 24 * time.Hour).Unix(),
		"type": "refresh",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// parse validates a token's signature + expiry and returns its claims. It does
// NOT check the token's purpose — callers must verify the "type" claim.
func (s *AuthService) parse(tokenStr string) (jwt.MapClaims, error) {
	if tokenStr == "" {
		return nil, errors.New("empty token")
	}
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// VerifyAccessToken validates a token AND requires it to be an access token, so
// a 30-day refresh token (or a session-scoped handoff token) can never be used
// as an API/WebSocket credential.
//
// Migration: access tokens issued before v0.2.4 carried no "type" claim; they
// are still accepted until they expire.
// TODO(v0.3.0): drop the legacy no-type ("") allowance below.
func (s *AuthService) VerifyAccessToken(tokenStr string) error {
	claims, err := s.parse(tokenStr)
	if err != nil {
		return err
	}
	switch t, _ := claims["type"].(string); t {
	case "access", "":
		return nil
	default:
		return errors.New("not an access token")
	}
}

// GenerateHandoffCookie mints a short-lived JWT scoped to a single session. It
// is set as an httpOnly cookie after a one-time handoff token is redeemed, so
// the mobile browser can exchange it for normal access tokens without ever
// seeing a PIN/password. It is NOT full app access — it names one session.
func (s *AuthService) GenerateHandoffCookie(sessionID string) (string, error) {
	claims := jwt.MapClaims{
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(30 * time.Minute).Unix(),
		"type":      "handoff",
		"sessionId": sessionID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// VerifyHandoffCookie validates a handoff cookie and returns the session id it
// is bound to.
func (s *AuthService) VerifyHandoffCookie(cookie string) (string, error) {
	claims, err := s.parse(cookie)
	if err != nil {
		return "", err
	}
	if t, _ := claims["type"].(string); t != "handoff" {
		return "", errors.New("not a handoff cookie")
	}
	sessionID, _ := claims["sessionId"].(string)
	if sessionID == "" {
		return "", errors.New("handoff cookie missing session")
	}
	return sessionID, nil
}

// Lifetimes of the tokens a QR handoff exchanges for. Deliberately far shorter
// than a login's 7d/30d: whoever scans (or photographs) the QR gets these.
const (
	HandoffAccessTTL  = 12 * time.Hour
	HandoffRefreshTTL = 24 * time.Hour
)

// Token types of the session-scoped pair. They are distinct from "access" /
// "refresh" on purpose so a caller still using the unscoped VerifyAccessToken
// fails closed instead of treating one as a full credential.
const (
	typeSessionAccess  = "session-access"
	typeSessionRefresh = "session-refresh"
)

// Scope is what an access token may touch. The zero value is full access; a
// non-empty SessionID confines the bearer to that one session (agent).
type Scope struct {
	SessionID string
}

// GenerateHandoffTokens mints the access/refresh pair a redeemed handoff is
// exchanged for: bound to one session and short-lived.
func (s *AuthService) GenerateHandoffTokens(sessionID string) (access, refresh string, err error) {
	if sessionID == "" {
		return "", "", errors.New("handoff tokens need a session")
	}
	now := time.Now()
	refresh, err = s.sign(typeSessionRefresh, sessionID, now, now.Add(HandoffRefreshTTL))
	if err != nil {
		return "", "", err
	}
	access, err = s.sign(typeSessionAccess, sessionID, now, now.Add(HandoffAccessTTL))
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

func (s *AuthService) sign(typ, sessionID string, iat, exp time.Time) (string, error) {
	claims := jwt.MapClaims{
		"iat":       iat.Unix(),
		"exp":       exp.Unix(),
		"type":      typ,
		"sessionId": sessionID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
}

// VerifyAccess validates an API/WebSocket credential and returns its scope:
// full for a normal access token, one session for a handoff-derived one.
func (s *AuthService) VerifyAccess(tokenStr string) (Scope, error) {
	claims, err := s.parse(tokenStr)
	if err != nil {
		return Scope{}, err
	}
	switch t, _ := claims["type"].(string); t {
	case "access", "": // "" = legacy, see VerifyAccessToken
		return Scope{}, nil
	case typeSessionAccess:
		sid, _ := claims["sessionId"].(string)
		if sid == "" {
			return Scope{}, errors.New("session token missing session")
		}
		return Scope{SessionID: sid}, nil
	default:
		return Scope{}, errors.New("not an access token")
	}
}

func (s *AuthService) RefreshAccessToken(refreshTokenStr string) (string, error) {
	claims, err := s.parse(refreshTokenStr)
	if err != nil {
		return "", err
	}
	switch t, _ := claims["type"].(string); t {
	case "refresh":
		return s.GenerateToken()
	case typeSessionRefresh:
		// Keep the scope, and never outlive the refresh token: the handoff grant
		// ends when its refresh token does, however often it is refreshed.
		sid, _ := claims["sessionId"].(string)
		if sid == "" {
			return "", errors.New("session token missing session")
		}
		now := time.Now()
		exp := now.Add(HandoffAccessTTL)
		if rexp, err := claims.GetExpirationTime(); err == nil && rexp != nil && rexp.Time.Before(exp) {
			exp = rexp.Time
		}
		return s.sign(typeSessionAccess, sid, now, exp)
	default:
		return "", errors.New("not a refresh token")
	}
}
