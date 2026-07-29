package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequireConfirmed_AllowsConfirmedUser(t *testing.T) {
	called := false
	h := RequireConfirmed()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ContextWithConfirmed(req.Context(), true))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestRequireConfirmed_RejectsUnconfirmedUserWith403(t *testing.T) {
	called := false
	h := RequireConfirmed()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ContextWithConfirmed(req.Context(), false))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, called)

	// The code is the contract the SPA's refresh-and-retry keys on.
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Equal(t, "confirmation_required", body.Error.Code)
}

func TestRequireConfirmed_RejectsWhenClaimAbsent(t *testing.T) {
	h := RequireConfirmed()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run without an authenticated context")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
