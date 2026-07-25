package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func defaultCityID(t *testing.T, q *store.Queries) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	row, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	return row.ID.String()
}

func TestSignup_Success(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@example.com",
		"password": "hunter22",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.AccessToken)
	require.Equal(t, "alice@example.com", resp.User.Email)

	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			found = true
			require.True(t, c.HttpOnly)
			require.NotEmpty(t, c.Value)
		}
	}
	require.True(t, found, "refresh_token cookie should be set")
}

func TestSignup_DuplicateEmailReturns409(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})

	send := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"email": "dup@example.com", "password": "hunter22"})
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	require.Equal(t, http.StatusCreated, send().Code)
	require.Equal(t, http.StatusConflict, send().Code)
}

func TestSignup_ShortPasswordReturns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})

	body, _ := json.Marshal(map[string]string{"email": "x@example.com", "password": "short"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func signupAndGetCity(t *testing.T) (*store.Queries, *auth.JWTSigner, string) {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	return q, signer, cityID
}

func TestLogin_Success(t *testing.T) {
	q, signer, _ := signupAndGetCity(t)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.AccessToken)
}

func TestLogin_WrongPassword(t *testing.T) {
	q, signer, _ := signupAndGetCity(t)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "nope"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLogin_UnknownEmail(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	body, _ := json.Marshal(map[string]string{"email": "no@example.com", "password": "whatever"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRefresh_Success(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	// signup to get a refresh cookie
	body, _ := json.Marshal(map[string]string{"email": "rf@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	// call /auth/refresh with the cookie
	req2 := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req2.AddCookie(refreshCookie)
	rec2 := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp))
	require.NotEmpty(t, resp.AccessToken)
}

func TestRefresh_NoCookie(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	rec := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRefresh_RevokedRejected(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "rev@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	// revoke directly via the query
	require.NoError(t, q.RevokeRefreshTokenByHash(context.Background(), auth.HashRefresh(refreshCookie.Value)))

	req2 := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req2.AddCookie(refreshCookie)
	rec2 := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec2, req2)
	require.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestLogout_RevokesAndClears(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "lo@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Mode: config.ConfirmationOff, Sender: &email.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	req2 := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req2.AddCookie(refreshCookie)
	rec2 := httptest.NewRecorder()
	handlers.Logout(q)(rec2, req2)
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// subsequent refresh should fail
	req3 := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req3.AddCookie(refreshCookie)
	rec3 := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec3, req3)
	require.Equal(t, http.StatusUnauthorized, rec3.Code)
}

// signupWith runs a signup in the given mode and returns the queries handle,
// the recorder, and the fake sender.
func signupWith(t *testing.T, mode config.ConfirmationMode, address string) (*store.Queries, *httptest.ResponseRecorder, *email.Fake) {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	fake := &email.Fake{}
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{
		Mode:       mode,
		Sender:     fake,
		APIBaseURL: "https://api.example.com",
		AppBaseURL: "https://app.example.com",
	})

	body, _ := json.Marshal(map[string]string{"email": address, "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return q, rec, fake
}

func confirmedOf(t *testing.T, q *store.Queries, address string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row, err := q.GetUserByEmail(ctx, address)
	require.NoError(t, err)
	return row.Confirmed
}

// extractConfirmLink pulls the confirm URL out of a plain-text mail body.
func extractConfirmLink(t *testing.T, text string) string {
	t.Helper()
	for _, f := range strings.Fields(text) {
		if strings.Contains(f, "/auth/confirm?token=") {
			return f
		}
	}
	t.Fatalf("no confirm link in message body: %q", text)
	return ""
}

func TestSignup_ModeOff_ConfirmedAndNoMail(t *testing.T) {
	q, rec, fake := signupWith(t, config.ConfirmationOff, "off@example.com")

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, confirmedOf(t, q, "off@example.com"))
	require.Empty(t, fake.Messages(), "off must send nothing — it is today's behavior byte for byte")
}

func TestSignup_ModeSend_ConfirmedAndMailSent(t *testing.T) {
	q, rec, fake := signupWith(t, config.ConfirmationSend, "send@example.com")

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, confirmedOf(t, q, "send@example.com"),
		"send must leave the user confirmed so nobody can be locked out")
	require.Len(t, fake.Messages(), 1)
	require.Equal(t, "send@example.com", fake.Last().To)
	require.Contains(t, fake.Last().Text, "https://api.example.com/auth/confirm?token=")
}

func TestSignup_ModeEnforce_UnconfirmedTokenRowAndMailSent(t *testing.T) {
	q, rec, fake := signupWith(t, config.ConfirmationEnforce, "enforce@example.com")

	require.Equal(t, http.StatusCreated, rec.Code)
	require.False(t, confirmedOf(t, q, "enforce@example.com"))
	require.Len(t, fake.Messages(), 1)

	// A token row exists and resolves by the hash of the token in the link.
	link := extractConfirmLink(t, fake.Last().Text)
	tok := link[strings.Index(link, "token=")+len("token="):]
	row, err := q.GetEmailConfirmationByHash(context.Background(), auth.HashRefresh(tok))
	require.NoError(t, err)
	require.False(t, row.ConsumedAt.Valid)
	require.True(t, row.ExpiresAt.Time.After(time.Now().Add(23*time.Hour)))
}

func TestSignup_ResponseCarriesConfirmed(t *testing.T) {
	_, rec, _ := signupWith(t, config.ConfirmationEnforce, "body@example.com")

	var resp struct {
		User struct {
			Confirmed bool `json:"confirmed"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.False(t, resp.User.Confirmed)
}

func TestSignup_SendFailureStillReturns201(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	fake := &email.Fake{Err: errors.New("ses is down")}
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{
		Mode: config.ConfirmationEnforce, Sender: fake,
		APIBaseURL: "https://api.example.com", AppBaseURL: "https://app.example.com",
	})

	body, _ := json.Marshal(map[string]string{"email": "sendfail@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	// The user still reaches /confirm-email, where resend is one click away.
	require.Equal(t, http.StatusCreated, rec.Code)
	require.False(t, confirmedOf(t, q, "sendfail@example.com"))
}

// The zero value of ConfirmationMode is "", not "off". A Server built without
// going through config.Load carries it, so the send decision must be an
// allowlist — treating "" as a sending mode would both send unintended mail and
// dereference a nil Sender.
func TestSignup_ZeroValueModeSendsNothingAndDoesNotPanic(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	// No Mode, no Sender — exactly what a bare Server literal produces.
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{})

	body, _ := json.Marshal(map[string]string{"email": "zerovalue@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, confirmedOf(t, q, "zerovalue@example.com"),
		"an unrecognized mode must behave like off, not enforce")
}
