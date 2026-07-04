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
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(7 * 24 * time.Hour).Unix(),
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

func (s *AuthService) VerifyToken(tokenStr string) error {
	if tokenStr == "" {
		return errors.New("empty token")
	}

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return err
	}
	if !token.Valid {
		return errors.New("invalid token")
	}
	return nil
}

func (s *AuthService) RefreshAccessToken(refreshTokenStr string) (string, error) {
	token, err := jwt.Parse(refreshTokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return "", err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", errors.New("invalid refresh token")
	}

	tokenType, _ := claims["type"].(string)
	if tokenType != "refresh" {
		return "", errors.New("not a refresh token")
	}

	return s.GenerateToken()
}
