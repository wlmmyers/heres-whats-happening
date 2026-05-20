package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
)

func TestRequireAuth_AllowsValidToken(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid)
	require.NoError(t, err)

	called := false
	h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := UserIDFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, uid, got)
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestRequireAuth_MissingHeaderRejected(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_BadTokenRejected(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer not.a.token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
