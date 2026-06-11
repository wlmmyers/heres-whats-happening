package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func postNotInterested(t *testing.T, q *store.Queries, signer *auth.JWTSigner, access, eventID string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"event_id": eventID})
	req := httptest.NewRequest(http.MethodPost, "/me/not-interested", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.AddNotInterested(q)).ServeHTTP(rec, req)
	return rec
}

func TestAddNotInterested_Idempotent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, eventID := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID))

	evStr := uuidFromPgCal(eventID).String()
	require.Equal(t, http.StatusNoContent, postNotInterested(t, q, signer, access, evStr).Code)
	require.Equal(t, http.StatusNoContent, postNotInterested(t, q, signer, access, evStr).Code)
}

func TestAddNotInterested_BadUUID(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, _ := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID))

	require.Equal(t, http.StatusBadRequest, postNotInterested(t, q, signer, access, "not-a-uuid").Code)
}

func TestAddNotInterested_UnknownEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, _ := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID))

	require.Equal(t, http.StatusBadRequest, postNotInterested(t, q, signer, access, uuid.NewString()).Code)
}

func TestResetNotInterested_Returns204(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, eventID := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID))

	require.Equal(t, http.StatusNoContent,
		postNotInterested(t, q, signer, access, uuidFromPgCal(eventID).String()).Code)

	req := httptest.NewRequest(http.MethodDelete, "/me/not-interested", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.ResetNotInterested(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}
