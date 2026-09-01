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
	access, _ := signer.SignAccess(uuidFromPgCal(userID), true)

	evStr := uuidFromPgCal(eventID).String()
	require.Equal(t, http.StatusNoContent, postNotInterested(t, q, signer, access, evStr).Code)
	require.Equal(t, http.StatusNoContent, postNotInterested(t, q, signer, access, evStr).Code)
}

func TestAddNotInterested_BadUUID(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, _ := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID), true)

	require.Equal(t, http.StatusBadRequest, postNotInterested(t, q, signer, access, "not-a-uuid").Code)
}

func TestAddNotInterested_UnknownEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, _ := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID), true)

	require.Equal(t, http.StatusBadRequest, postNotInterested(t, q, signer, access, uuid.NewString()).Code)
}

func TestResetNotInterested_Returns204(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	userID, eventID := seedCalendarFixture(t, q, context.Background())
	access, _ := signer.SignAccess(uuidFromPgCal(userID), true)

	require.Equal(t, http.StatusNoContent,
		postNotInterested(t, q, signer, access, uuidFromPgCal(eventID).String()).Code)

	req := httptest.NewRequest(http.MethodDelete, "/me/not-interested", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.ResetNotInterested(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// getNotInterested reads the list the web client filters its calendar with.
func getNotInterested(t *testing.T, q *store.Queries, signer *auth.JWTSigner, access string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/me/not-interested", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.ListNotInterested(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var ids []string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&ids))
	return ids
}

// Since the calendar endpoints stopped filtering dismissals out server-side,
// this list is the only thing standing between a hidden event and the user
// seeing it again -- so both halves of the round trip are pinned here: the
// dismissal shows up, and the reset really deletes the row rather than just
// answering 204.
func TestListNotInterested_ReflectsAddAndReset(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)
	access, _ := signer.SignAccess(uuidFromPgCal(userID), true)

	require.Empty(t, getNotInterested(t, q, signer, access))

	evStr := uuidFromPgCal(eventID).String()
	require.Equal(t, http.StatusNoContent, postNotInterested(t, q, signer, access, evStr).Code)
	require.Equal(t, []string{evStr}, getNotInterested(t, q, signer, access))

	require.NoError(t, q.ClearNotInterested(ctx, userID))
	require.Empty(t, getNotInterested(t, q, signer, access))
}

// A dismissed event that has already happened drops off the list on its own:
// the calendar will not show it again either way, and keeping it would grow the
// payload forever.
func TestListNotInterested_OmitsPastEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	access, _ := signer.SignAccess(uuidFromPgCal(userID), true)

	pastID := seedGoingEvent(t, q, ctx, userID, "ni-past", "Last Month's Show", -30*24*time.Hour, false)
	require.NoError(t, q.AddNotInterested(ctx, store.AddNotInterestedParams{
		UserID:  userID,
		EventID: pastID,
	}))

	require.Empty(t, getNotInterested(t, q, signer, access))
}
