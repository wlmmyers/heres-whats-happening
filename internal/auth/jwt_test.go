package auth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSignAndVerifyAccess_RoundTrip(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid, true)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	gotUID, _, err := signer.VerifyAccess(tok)
	require.NoError(t, err)
	require.Equal(t, uid, gotUID)
}

func TestVerifyAccess_ExpiredRejected(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", -time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid, true)
	require.NoError(t, err)
	_, _, err = signer.VerifyAccess(tok)
	require.Error(t, err)
}

func TestVerifyAccess_TamperedRejected(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	_, _, err := signer.VerifyAccess("not.a.token")
	require.Error(t, err)
}

func TestSignAccess_RoundTripsConfirmedClaim(t *testing.T) {
	s := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()

	for _, confirmed := range []bool{true, false} {
		tok, err := s.SignAccess(uid, confirmed)
		require.NoError(t, err)

		gotID, gotConfirmed, err := s.VerifyAccess(tok)
		require.NoError(t, err)
		require.Equal(t, uid, gotID)
		require.Equal(t, confirmed, gotConfirmed)
	}
}

// The claim is exactly as forgeable as the subject: both ride in the same
// HS256-signed payload. Rewriting either invalidates the MAC.
func TestVerifyAccess_RejectsTamperedConfirmedClaim(t *testing.T) {
	s := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	tok, err := s.SignAccess(uuid.New(), false)
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	tampered := bytes.Replace(payload, []byte(`"confirmed":false`), []byte(`"confirmed":true`), 1)
	require.NotEqual(t, payload, tampered, "payload must contain the confirmed claim")
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(tampered) + "." + parts[2]

	_, _, err = s.VerifyAccess(forged)
	require.Error(t, err)
}

func TestVerifyAccess_RejectsAlgNone(t *testing.T) {
	s := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"sub":"` + uuid.New().String() + `","confirmed":true,"exp":9999999999}`))

	_, _, err := s.VerifyAccess(header + "." + payload + ".")
	require.Error(t, err)
}
