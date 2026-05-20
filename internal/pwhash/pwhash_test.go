package pwhash

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashAndVerify_RoundTrip(t *testing.T) {
	h, err := Hash("hunter2")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$"))
	ok, err := Verify("hunter2", h)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestVerify_WrongPasswordRejected(t *testing.T) {
	h, err := Hash("hunter2")
	require.NoError(t, err)
	ok, err := Verify("nope", h)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerify_MalformedHash(t *testing.T) {
	_, err := Verify("x", "not-a-real-hash")
	require.Error(t, err)
}

func TestHash_UniqueSaltsProduceDifferentOutputs(t *testing.T) {
	a, err := Hash("same")
	require.NoError(t, err)
	b, err := Hash("same")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}
