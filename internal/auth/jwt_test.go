package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSignAndVerifyAccess_RoundTrip(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	gotUID, err := signer.VerifyAccess(tok)
	require.NoError(t, err)
	require.Equal(t, uid, gotUID)
}

func TestVerifyAccess_ExpiredRejected(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", -time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid)
	require.NoError(t, err)
	_, err = signer.VerifyAccess(tok)
	require.Error(t, err)
}

func TestVerifyAccess_TamperedRejected(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	_, err := signer.VerifyAccess("not.a.token")
	require.Error(t, err)
}
