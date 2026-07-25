package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

const (
	welcomeURL = "https://app.example.com/?welcome=true"
	errorURL   = "https://app.example.com/?confirmerror=true"
)

func testDeps(sender email.Sender) handlers.ConfirmationDeps {
	return handlers.ConfirmationDeps{
		Mode:       config.ConfirmationEnforce,
		Sender:     sender,
		APIBaseURL: "https://api.example.com",
		AppBaseURL: "https://app.example.com",
	}
}

// newUnconfirmedUser creates an unconfirmed user with a confirmation token and
// returns the queries handle, the user id, and the raw token.
func newUnconfirmedUser(t *testing.T, address string, expiresIn time.Duration) (*store.Queries, pgtype.UUID, string) {
	t.Helper()
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: address, PasswordHash: "x", CityID: city.ID, Confirmed: false,
	})
	require.NoError(t, err)

	raw, err := auth.GenerateRefresh()
	require.NoError(t, err)
	require.NoError(t, q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID:    row.ID,
		TokenHash: auth.HashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(expiresIn), Valid: true},
	}))
	return q, row.ID, raw
}

func confirmGet(t *testing.T, q *store.Queries, token string) *httptest.ResponseRecorder {
	t.Helper()
	h := handlers.ConfirmEmail(q, testDeps(&email.Fake{}))
	req := httptest.NewRequest(http.MethodGet, "/auth/confirm?token="+token, nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestConfirmEmail_HappyPathFlipsConfirmedAndRedirectsToWelcome(t *testing.T) {
	q, uid, tok := newUnconfirmedUser(t, "happy@example.com", 24*time.Hour)

	rec := confirmGet(t, q, tok)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, welcomeURL, rec.Header().Get("Location"))

	row, err := q.GetUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.True(t, row.Confirmed)
}

func TestConfirmEmail_ConsumedReplayStillRedirectsToWelcome(t *testing.T) {
	q, _, tok := newUnconfirmedUser(t, "replay@example.com", 24*time.Hour)

	// First fetch: the corporate mail scanner (Outlook SafeLinks) prefetching.
	require.Equal(t, welcomeURL, confirmGet(t, q, tok).Header().Get("Location"))

	// Second fetch: the human actually clicking. Telling them their link failed
	// would be a lie — they are confirmed.
	rec := confirmGet(t, q, tok)
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, welcomeURL, rec.Header().Get("Location"))
}

func TestConfirmEmail_ExpiredTokenRedirectsToError(t *testing.T) {
	q, uid, tok := newUnconfirmedUser(t, "expired@example.com", -1*time.Hour)

	rec := confirmGet(t, q, tok)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, errorURL, rec.Header().Get("Location"))

	row, err := q.GetUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.False(t, row.Confirmed, "an expired link must not confirm")
}

func TestConfirmEmail_UnknownTokenRedirectsToError(t *testing.T) {
	q, _, _ := newUnconfirmedUser(t, "unknown@example.com", 24*time.Hour)

	rec := confirmGet(t, q, "not-a-real-token")

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, errorURL, rec.Header().Get("Location"))
}

func TestConfirmEmail_MissingTokenRedirectsToError(t *testing.T) {
	q, _, _ := newUnconfirmedUser(t, "notoken@example.com", 24*time.Hour)

	h := handlers.ConfirmEmail(q, testDeps(&email.Fake{}))
	req := httptest.NewRequest(http.MethodGet, "/auth/confirm", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, errorURL, rec.Header().Get("Location"))
}

func TestConfirmEmail_NeverReturnsJSON(t *testing.T) {
	q, _, tok := newUnconfirmedUser(t, "nojson@example.com", 24*time.Hour)

	for _, token := range []string{tok, "bogus"} {
		rec := confirmGet(t, q, token)
		require.Equal(t, http.StatusFound, rec.Code)
		require.NotContains(t, rec.Header().Get("Content-Type"), "application/json")
	}
}
