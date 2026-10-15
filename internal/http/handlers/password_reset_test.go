package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/pwhash"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// syncRunner runs the handlers' off-request mail work inline, so a test can
// assert on what was sent as soon as the handler returns.
func syncRunner(f func()) { f() }

func resetDeps(sender email.Sender) handlers.PasswordResetDeps {
	return handlers.PasswordResetDeps{
		Sender:       sender,
		AppBaseURL:   "https://app.example.com",
		EmailLimiter: ratelimit.NewMemory(3, time.Hour),
		Async:        syncRunner,
	}
}

// newResetUser creates an unconfirmed user whose password is "oldpassword".
func newResetUser(t *testing.T, address string) (*store.Queries, pgtype.UUID) {
	t.Helper()
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	hash, err := pwhash.Hash("oldpassword")
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: address, PasswordHash: hash, CityID: city.ID, Confirmed: false,
	})
	require.NoError(t, err)
	return q, row.ID
}

func postJSON(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func requestReset(t *testing.T, q *store.Queries, deps handlers.PasswordResetDeps, address string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": address})
	return postJSON(t, handlers.RequestPasswordReset(q, deps), string(body))
}

func submitReset(t *testing.T, q *store.Queries, deps handlers.PasswordResetDeps, token, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token, "password": password})
	return postJSON(t, handlers.ResetPassword(q, deps), string(body))
}

// resetTokenFrom pulls the token out of the reset link in a plain-text body.
func resetTokenFrom(t *testing.T, text string) string {
	t.Helper()
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "https://app.example.com/reset-password?token=") {
			u, err := url.Parse(f)
			require.NoError(t, err)
			return u.Query().Get("token")
		}
	}
	t.Fatalf("no reset link in message body: %q", text)
	return ""
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body.Error.Code
}

func TestRequestPasswordReset_KnownAddressMailsAnAppLink(t *testing.T) {
	q, _ := newResetUser(t, "known@example.com")
	fake := &email.Fake{}

	// Case and whitespace are normalized the same way login does.
	rec := requestReset(t, q, resetDeps(fake), "  Known@Example.com ")
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Len(t, fake.Messages(), 1)
	msg := fake.Last()
	require.Equal(t, "known@example.com", msg.To)
	require.NotEmpty(t, resetTokenFrom(t, msg.Text))
}

func TestRequestPasswordReset_UnknownAddressIsAnIndistinguishable204(t *testing.T) {
	q, _ := newResetUser(t, "someone@example.com")
	fake := &email.Fake{}

	rec := requestReset(t, q, resetDeps(fake), "nobody@example.com")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, rec.Body.String())
	require.Empty(t, fake.Messages())
}

func TestRequestPasswordReset_MalformedAddressIsA204(t *testing.T) {
	q, _ := newResetUser(t, "someone@example.com")
	fake := &email.Fake{}

	rec := requestReset(t, q, resetDeps(fake), "not-an-email")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, fake.Messages())
}

func TestRequestPasswordReset_BadJSONIs400(t *testing.T) {
	q, _ := newResetUser(t, "someone@example.com")
	rec := postJSON(t, handlers.RequestPasswordReset(q, resetDeps(&email.Fake{})), "{")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRequestPasswordReset_SendFailureIsStillA204(t *testing.T) {
	q, _ := newResetUser(t, "sendfail@example.com")
	fake := &email.Fake{Err: errors.New("ses is down")}

	rec := requestReset(t, q, resetDeps(fake), "sendfail@example.com")
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRequestPasswordReset_PerAddressLimitSilentlyStopsMail(t *testing.T) {
	q, _ := newResetUser(t, "flooded@example.com")
	fake := &email.Fake{}
	deps := resetDeps(fake)

	for range 5 {
		rec := requestReset(t, q, deps, "flooded@example.com")
		require.Equal(t, http.StatusNoContent, rec.Code, "over the limit must look the same")
	}
	require.Len(t, fake.Messages(), 3)
}

func TestRequestPasswordReset_NewRequestInvalidatesThePriorLink(t *testing.T) {
	q, _ := newResetUser(t, "twice@example.com")
	fake := &email.Fake{}
	deps := resetDeps(fake)

	requestReset(t, q, deps, "twice@example.com")
	oldTok := resetTokenFrom(t, fake.Last().Text)
	requestReset(t, q, deps, "twice@example.com")
	newTok := resetTokenFrom(t, fake.Last().Text)
	require.NotEqual(t, oldTok, newTok)

	rec := submitReset(t, q, deps, oldTok, "newpassword")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_token", errCode(t, rec))

	rec = submitReset(t, q, deps, newTok, "newpassword")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestResetPassword_HappyPath(t *testing.T) {
	q, uid := newResetUser(t, "happy@example.com")
	ctx := context.Background()
	fake := &email.Fake{}
	deps := resetDeps(fake)

	// A live session from before the reset.
	refreshRaw, err := auth.GenerateRefresh()
	require.NoError(t, err)
	_, err = q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    uid,
		TokenHash: auth.HashRefresh(refreshRaw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	requestReset(t, q, deps, "happy@example.com")
	tok := resetTokenFrom(t, fake.Last().Text)

	rec := submitReset(t, q, deps, tok, "newpassword")
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "happy@example.com", out.Email)

	user, err := q.GetUserByEmail(ctx, "happy@example.com")
	require.NoError(t, err)
	ok, err := pwhash.Verify("newpassword", user.PasswordHash)
	require.NoError(t, err)
	require.True(t, ok, "the new password must work")
	ok, err = pwhash.Verify("oldpassword", user.PasswordHash)
	require.NoError(t, err)
	require.False(t, ok, "the old password must not")
	require.True(t, user.Confirmed, "following the emailed link proves the address")

	_, err = q.GetActiveRefreshTokenByHash(ctx, auth.HashRefresh(refreshRaw))
	require.Error(t, err, "every existing session must be signed out")

	changed := fake.Last()
	require.Equal(t, "happy@example.com", changed.To)
	require.Equal(t, email.PasswordChangedMessage("happy@example.com").Subject, changed.Subject)
}

func TestResetPassword_TokenWorksOnce(t *testing.T) {
	q, _ := newResetUser(t, "once@example.com")
	fake := &email.Fake{}
	deps := resetDeps(fake)

	requestReset(t, q, deps, "once@example.com")
	tok := resetTokenFrom(t, fake.Last().Text)

	require.Equal(t, http.StatusOK, submitReset(t, q, deps, tok, "newpassword").Code)
	rec := submitReset(t, q, deps, tok, "otherpassword")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_token", errCode(t, rec))
}

func TestResetPassword_ExpiredTokenIsRejected(t *testing.T) {
	q, uid := newResetUser(t, "expired@example.com")
	raw, err := auth.GenerateRefresh()
	require.NoError(t, err)
	require.NoError(t, q.UpsertPasswordReset(context.Background(), store.UpsertPasswordResetParams{
		UserID:    uid,
		TokenHash: auth.HashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}))

	rec := submitReset(t, q, resetDeps(&email.Fake{}), raw, "newpassword")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_token", errCode(t, rec))
}

func TestResetPassword_UnknownOrMissingTokenIsRejected(t *testing.T) {
	q, _ := newResetUser(t, "unknown@example.com")
	deps := resetDeps(&email.Fake{})

	for _, tok := range []string{"not-a-real-token", ""} {
		rec := submitReset(t, q, deps, tok, "newpassword")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "invalid_token", errCode(t, rec))
	}
}

func TestResetPassword_WeakPasswordDoesNotSpendTheToken(t *testing.T) {
	q, _ := newResetUser(t, "weak@example.com")
	fake := &email.Fake{}
	deps := resetDeps(fake)

	requestReset(t, q, deps, "weak@example.com")
	tok := resetTokenFrom(t, fake.Last().Text)

	rec := submitReset(t, q, deps, tok, "short")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "weak_password", errCode(t, rec))

	require.Equal(t, http.StatusOK, submitReset(t, q, deps, tok, "longenough").Code)
}

func TestResetPassword_ChangedNoticeFailureStillSucceeds(t *testing.T) {
	q, _ := newResetUser(t, "noticefail@example.com")
	fake := &email.Fake{}
	deps := resetDeps(fake)

	requestReset(t, q, deps, "noticefail@example.com")
	tok := resetTokenFrom(t, fake.Last().Text)

	fake.Err = errors.New("ses is down")
	require.Equal(t, http.StatusOK, submitReset(t, q, deps, tok, "newpassword").Code)
}
