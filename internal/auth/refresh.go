package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const refreshBytes = 32

// GenerateRefresh returns a base64url-encoded 32-byte random token (43 chars, no padding).
func GenerateRefresh() (string, error) {
	buf := make([]byte, refreshBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashRefresh returns sha256(token) as raw bytes for storage as BYTEA.
func HashRefresh(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
