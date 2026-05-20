// Package spotify implements the OAuth + Web API client for Spotify.
// This file: PKCE helpers per RFC 7636.
package spotify

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// NewVerifier returns a fresh PKCE code_verifier: a base64url-encoded 48-byte
// random string (64 chars without padding, within the 43..128 spec range).
func NewVerifier() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Challenge returns the PKCE code_challenge for a verifier using the S256 method.
func Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
