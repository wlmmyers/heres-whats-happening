package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// patchShowSetlists drives the handler behind RequireAuth the way the router does.
func patchShowSetlists(t *testing.T, q *store.Queries, signer *auth.JWTSigner, access string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/me/show-setlists", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.UpdateShowSetlists(q)).ServeHTTP(rec, req)
	return rec
}

func readShowSetlists(t *testing.T, pool *pgxpool.Pool, uid string) bool {
	t.Helper()
	var got bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT show_setlists FROM users WHERE id=$1`,
		pgtype.UUID{Bytes: uuidMust(t, uid), Valid: true}).Scan(&got))
	return got
}

func TestUpdateShowSetlists_EnablesForUser(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "ss-on@example.com")

	require.False(t, readShowSetlists(t, pool, uid), "new users must start opted out")

	body, _ := json.Marshal(map[string]bool{"show_setlists": true})
	rec := patchShowSetlists(t, q, signer, access, body)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, readShowSetlists(t, pool, uid))
}

func TestUpdateShowSetlists_DisablesAgain(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "ss-off@example.com")

	on, _ := json.Marshal(map[string]bool{"show_setlists": true})
	require.Equal(t, http.StatusNoContent, patchShowSetlists(t, q, signer, access, on).Code)
	require.True(t, readShowSetlists(t, pool, uid))

	off, _ := json.Marshal(map[string]bool{"show_setlists": false})
	require.Equal(t, http.StatusNoContent, patchShowSetlists(t, q, signer, access, off).Code)
	require.False(t, readShowSetlists(t, pool, uid), "false must persist, not be treated as absent")
}

func TestUpdateShowSetlists_RejectsBadJSON(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, _ := signupForThreshold(t, q, signer, cityID, "ss-badjson@example.com")

	rec := patchShowSetlists(t, q, signer, access, []byte(`{"show_setlists":`))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateShowSetlists_RequiresAuth(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	body, _ := json.Marshal(map[string]bool{"show_setlists": true})
	req := httptest.NewRequest(http.MethodPatch, "/me/show-setlists", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.UpdateShowSetlists(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// An absent field must not be read as `false` — that would silently opt a user
// back out on any malformed client request.
func TestUpdateShowSetlists_RejectsMissingField(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "ss-missing@example.com")

	on, _ := json.Marshal(map[string]bool{"show_setlists": true})
	require.Equal(t, http.StatusNoContent, patchShowSetlists(t, q, signer, access, on).Code)

	rec := patchShowSetlists(t, q, signer, access, []byte(`{}`))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.True(t, readShowSetlists(t, pool, uid), "rejected request must leave the setting alone")
}
