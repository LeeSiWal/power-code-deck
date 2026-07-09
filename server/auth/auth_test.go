package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newTestSvc() *AuthService {
	return NewAuthService(false, "none", "", "", "test-secret-0123456789abcdef")
}

// TestAccessRefreshSeparation is the regression test for the vuln where a 30-day
// refresh token was accepted as an API/WS credential.
func TestAccessRefreshSeparation(t *testing.T) {
	s := newTestSvc()

	access, err := s.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := s.GenerateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}

	if err := s.VerifyAccessToken(access); err != nil {
		t.Fatalf("access token should verify: %v", err)
	}
	if err := s.VerifyAccessToken(refresh); err == nil {
		t.Fatal("refresh token MUST NOT pass VerifyAccessToken")
	}

	// The refresh flow itself still works and yields a usable access token.
	newAccess, err := s.RefreshAccessToken(refresh)
	if err != nil {
		t.Fatalf("RefreshAccessToken(refresh) should succeed: %v", err)
	}
	if err := s.VerifyAccessToken(newAccess); err != nil {
		t.Fatalf("refreshed access token should verify: %v", err)
	}
	// An access token must not be usable where a refresh token is expected.
	if _, err := s.RefreshAccessToken(access); err == nil {
		t.Fatal("access token MUST NOT be usable as a refresh token")
	}
}

// TestLegacyNoTypeTokenAcceptedAsAccess covers the migration allowance: tokens
// minted before v0.2.4 have no "type" claim and must still authenticate.
func TestLegacyNoTypeTokenAcceptedAsAccess(t *testing.T) {
	s := newTestSvc()

	claims := jwt.MapClaims{
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyAccessToken(legacy); err != nil {
		t.Fatalf("legacy no-type token should be accepted as access: %v", err)
	}
}

func TestVerifyAccessTokenRejectsGarbage(t *testing.T) {
	s := newTestSvc()
	if err := s.VerifyAccessToken(""); err == nil {
		t.Fatal("empty token must fail")
	}
	if err := s.VerifyAccessToken("not.a.jwt"); err == nil {
		t.Fatal("malformed token must fail")
	}

	// A token signed with a different secret must fail.
	other := NewAuthService(false, "none", "", "", "a-completely-different-secret")
	tok, _ := other.GenerateToken()
	if err := s.VerifyAccessToken(tok); err == nil {
		t.Fatal("token signed with a foreign secret must fail")
	}
}
