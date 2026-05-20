package spotify

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewVerifier_RFC7636Length(t *testing.T) {
	v, err := NewVerifier()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(v), 43)
	require.LessOrEqual(t, len(v), 128)
}

func TestNewVerifier_UniquePerCall(t *testing.T) {
	a, err := NewVerifier()
	require.NoError(t, err)
	b, err := NewVerifier()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestChallenge_MatchesSpec(t *testing.T) {
	verifier := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH" // 44 chars
	h := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(h[:])

	got := Challenge(verifier)
	require.Equal(t, want, got)
	require.False(t, strings.Contains(got, "="), "padding must be stripped")
}
