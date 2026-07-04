package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Password hashing uses a stdlib-only salted, iterated SHA-256 KDF so the build
// stays dependency-free (no golang.org/x/crypto). The encoded form is
// "sha256$<iterations>$<saltHex>$<hashHex>". This can be upgraded to
// bcrypt/argon2 later without changing callers — VerifyPassword dispatches on
// the algorithm prefix.
const pwIterations = 100_000

// HashPassword returns an encoded hash for the given plaintext password.
func HashPassword(plain string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	sum := deriveKey(plain, salt, pwIterations)
	return fmt.Sprintf("sha256$%d$%s$%s", pwIterations, hex.EncodeToString(salt), hex.EncodeToString(sum))
}

// VerifyPassword reports whether plain matches the encoded hash.
func VerifyPassword(plain, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := deriveKey(plain, salt, iter)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func deriveKey(plain string, salt []byte, iter int) []byte {
	h := sha256.Sum256(append(append([]byte{}, salt...), []byte(plain)...))
	out := h[:]
	for i := 1; i < iter; i++ {
		next := sha256.Sum256(append(append([]byte{}, salt...), out...))
		out = next[:]
	}
	return out
}
