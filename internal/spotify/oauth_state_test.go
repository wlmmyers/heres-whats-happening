package spotify

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSealOpen_RoundTrip(t *testing.T) {
	key := []byte("test-key-test-key-test-key-32xx")
	cookie, err := SealOAuthState(key, "the-state", "the-verifier", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, cookie)

	state, verifier, err := OpenOAuthState(key, cookie)
	require.NoError(t, err)
	require.Equal(t, "the-state", state)
	require.Equal(t, "the-verifier", verifier)
}

func TestOpen_ExpiredRejected(t *testing.T) {
	key := []byte("test-key-test-key-test-key-32xx")
	cookie, err := SealOAuthState(key, "s", "v", -time.Minute)
	require.NoError(t, err)
	_, _, err = OpenOAuthState(key, cookie)
	require.Error(t, err)
}

func TestOpen_TamperedRejected(t *testing.T) {
	key := []byte("test-key-test-key-test-key-32xx")
	cookie, err := SealOAuthState(key, "s", "v", time.Minute)
	require.NoError(t, err)
	// flip one character of the cookie value
	bad := cookie[:len(cookie)-1] + "X"
	_, _, err = OpenOAuthState(key, bad)
	require.Error(t, err)
}
