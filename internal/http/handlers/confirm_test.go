package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
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

func resendPost(t *testing.T, q *store.Queries, uid pgtype.UUID, sender email.Sender) *httptest.ResponseRecorder {
	t.Helper()
	h := handlers.ResendConfirmation(q, testDeps(sender))
	req := httptest.NewRequest(http.MethodPost, "/auth/confirm/resend", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), uuid.UUID(uid.Bytes)))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestResendConfirmation_SendsFreshLinkAndInvalidatesThePrior(t *testing.T) {
	q, uid, oldTok := newUnconfirmedUser(t, "resend@example.com", 24*time.Hour)
	fake := &email.Fake{}

	rec := resendPost(t, q, uid, fake)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Len(t, fake.Messages(), 1)
	require.Equal(t, "resend@example.com", fake.Last().To)

	// The prior link stops working — one live token per user.
	_, err := q.GetEmailConfirmationByHash(context.Background(), auth.HashRefresh(oldTok))
	require.Error(t, err)

	// The new one resolves and is unconsumed.
	newLink := extractConfirmLink(t, fake.Last().Text)
	newTok := newLink[strings.Index(newLink, "token=")+len("token="):]
	row, err := q.GetEmailConfirmationByHash(context.Background(), auth.HashRefresh(newTok))
	require.NoError(t, err)
	require.False(t, row.ConsumedAt.Valid)
}

func TestResendConfirmation_AlreadyConfirmedIsANoOp204(t *testing.T) {
	q, uid, _ := newUnconfirmedUser(t, "already@example.com", 24*time.Hour)
	require.NoError(t, q.MarkUserConfirmed(context.Background(), uid))
	fake := &email.Fake{}

	rec := resendPost(t, q, uid, fake)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, fake.Messages(), "a confirmed user must not be re-mailed")
}

func TestResendConfirmation_SendFailureReturns500(t *testing.T) {
	q, uid, _ := newUnconfirmedUser(t, "resendfail@example.com", 24*time.Hour)

	// Unlike signup, the user explicitly asked for this — surface the failure
	// so the UI can say the mail did not go out.
	rec := resendPost(t, q, uid, &email.Fake{Err: errors.New("ses is down")})
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestResendConfirmation_NoUserInContextReturns401(t *testing.T) {
	q, _, _ := newUnconfirmedUser(t, "nouser@example.com", 24*time.Hour)

	h := handlers.ResendConfirmation(q, testDeps(&email.Fake{}))
	req := httptest.NewRequest(http.MethodPost, "/auth/confirm/resend", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
