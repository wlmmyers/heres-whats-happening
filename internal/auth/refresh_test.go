package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateRefresh_RandomAnd32Bytes(t *testing.T) {
	a, err := GenerateRefresh()
	require.NoError(t, err)
	b, err := GenerateRefresh()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	// base64url encoding of 32 bytes is 43 chars without padding
	require.Len(t, a, 43)
}

func TestHashRefresh_DeterministicSHA256(t *testing.T) {
	want := sha256.Sum256([]byte("abc"))
	got := HashRefresh("abc")
	require.Equal(t, hex.EncodeToString(want[:]), hex.EncodeToString(got))
}
