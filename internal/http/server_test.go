package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/email"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
	"github.com/wmyers/heres-whats-happening/internal/observability"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestServer_EndToEnd_SignupLoginMe(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	s := &hs.Server{
		DB:            pool,
		Queries:       q,
		JWTSigner:     signer,
		RefreshTTL:    time.Hour,
		DefaultCityID: uuid.UUID(city.ID.Bytes).String(),
	}
	mux := s.Router()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// signup
	body, _ := json.Marshal(map[string]string{"email": "e2e@example.com", "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var su struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&su))
	resp.Body.Close()
	require.NotEmpty(t, su.AccessToken)

	// GET /me
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+su.AccessToken)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var me struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&me))
	resp2.Body.Close()
	require.Equal(t, "e2e@example.com", me.Email)
}

// newTestServer starts a server backed by the test database. Each call builds a
// fresh Router, so rate-limit buckets never leak between tests.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	old := observability.Default
	observability.Default = observability.NewEmitter(&bytes.Buffer{})
	t.Cleanup(func() { observability.Default = old })

	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	s := &hs.Server{
		DB:            pool,
		Queries:       q,
		JWTSigner:     auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL:    time.Hour,
		DefaultCityID: uuid.UUID(city.ID.Bytes).String(),
	}
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv
}

func TestServer_IcalFeedIsRateLimited(t *testing.T) {
	srv := newTestServer(t)

	// The ical feed limit is 60/min. An unknown token 404s after one indexed
	// lookup — which is exactly the cheap-to-send, not-free-to-serve request
	// the limit exists to cap.
	for i := range 60 {
		resp, err := http.Get(srv.URL + "/ical/not-a-real-token.ics")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Get(srv.URL + "/ical/not-a-real-token.ics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

func TestServer_ReadyzIsRateLimited(t *testing.T) {
	srv := newTestServer(t)

	for i := range 30 {
		resp, err := http.Get(srv.URL + "/readyz")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestServer_LogoutIsRateLimited(t *testing.T) {
	srv := newTestServer(t)

	for i := range 30 {
		resp, err := http.Post(srv.URL+"/auth/logout", "application/json", nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusNoContent, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Post(srv.URL+"/auth/logout", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

// /healthz is the ALB health check target. Rate limiting it would let a
// request flood fail the health check and cycle otherwise-healthy tasks.
func TestServer_HealthzIsNeverRateLimited(t *testing.T) {
	srv := newTestServer(t)

	// Well past every other limit in the router.
	for i := range 200 {
		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i+1)
	}
}

func TestServer_RefreshIsRateLimited(t *testing.T) {
	s := &hs.Server{
		JWTSigner:  auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL: time.Hour,
	}
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	// The refresh limit is 30/min. Without a cookie each call short-circuits to
	// 401 without a DB round trip.
	for i := range 30 {
		resp, err := http.Post(srv.URL+"/auth/refresh", "application/json", nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Post(srv.URL+"/auth/refresh", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

// signupFor creates a user on srv, confirms them, and returns an access token
// that carries confirmed:true.
//
// Signup always creates an unconfirmed user, and every route outside /me,
// resend, and delete sits behind RequireConfirmed — so a caller that just wants
// "an authenticated user" needs the confirmation done for it, or it gets 403s
// that have nothing to do with what it is testing. The confirmed claim is baked
// into the access token at signing time, hence the fresh login rather than
// reusing the signup token.
func signupFor(t *testing.T, srv *httptest.Server, address string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": address, "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	confirmInDB(t, address)
	return loginFor(t, srv, address)
}

// confirmInDB flips users.confirmed without going through the emailed link.
// testdb.MustOpen is a per-process singleton, so this shares the pool the
// server under test is using.
func confirmInDB(t *testing.T, address string) {
	t.Helper()
	q := store.New(testdb.MustOpen(t))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	row, err := q.GetUserByEmail(ctx, address)
	require.NoError(t, err)
	require.NoError(t, q.MarkUserConfirmed(ctx, row.ID))
}

// loginFor mints a fresh access token for an existing user.
func loginFor(t *testing.T, srv *httptest.Server, address string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": address, "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.AccessToken)
	return out.AccessToken
}

// authedTestServer returns a running server plus an access token for a fresh
// user. newTestServer is defined in Task 5.
func authedTestServer(t *testing.T, email string) (*httptest.Server, string) {
	t.Helper()
	srv := newTestServer(t)
	return srv, signupFor(t, srv, email)
}

func doAuthed(t *testing.T, srv *httptest.Server, method, path, token string) int {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// authed_write is 30/min and stacks on the 120/min authed net, so it trips first.
func TestServer_AuthedWriteIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "write-limit@example.com")

	for i := range 30 {
		code := doAuthed(t, srv, http.MethodDelete, "/me/not-interested", token)
		require.Equal(t, http.StatusNoContent, code, "request %d", i+1)
	}

	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", token))
}

// The group-wide net covers routes with no bucket of their own, like GET /me.
func TestServer_AuthedNetIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "net-limit@example.com")

	for i := range 120 {
		code := doAuthed(t, srv, http.MethodGet, "/me", token)
		require.Equal(t, http.StatusOK, code, "request %d", i+1)
	}

	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodGet, "/me", token))
}

// Two users must not share a budget — this is the whole point of user keying.
func TestServer_AuthedLimitIsPerUser(t *testing.T) {
	srv, alice := authedTestServer(t, "alice-limit@example.com")

	for i := range 30 {
		require.Equal(t, http.StatusNoContent,
			doAuthed(t, srv, http.MethodDelete, "/me/not-interested", alice),
			"request %d", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", alice))

	// A second user on the same server and the same source IP.
	bob := signupFor(t, srv, "bob-limit@example.com")

	require.Equal(t, http.StatusNoContent,
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", bob),
		"bob must have his own budget despite sharing alice's IP")
}

// ical_token is 10/hour and stacks on the net, so it trips well before it.
func TestServer_IcalTokenMintingIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "ical-token-limit@example.com")

	for i := range 10 {
		code := doAuthed(t, srv, http.MethodPost, "/me/ical-token", token)
		require.Equal(t, http.StatusCreated, code, "request %d", i+1)
	}

	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodPost, "/me/ical-token", token))
}

// spotify_exchange is 10/hour. limitEvery records the token before invoking
// the handler, so the 429 arrives on the 11th request even though the
// handler itself can never succeed here (authedTestServer wires a nil
// Spotify client) — so only the rate-limit boundary is asserted, not the
// handler's own status.
func TestServer_SpotifyExchangeIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "spotify-exchange-limit@example.com")

	for i := range 10 {
		code := doAuthed(t, srv, http.MethodPost, "/integrations/spotify/exchange", token)
		require.NotEqual(t, http.StatusTooManyRequests, code, "request %d", i+1)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/integrations/spotify/exchange", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

// newConfirmationTestServer starts a server with the mailer wired up and
// returns it alongside the fake sender.
func newConfirmationTestServer(t *testing.T) (*httptest.Server, *email.Fake) {
	t.Helper()

	old := observability.Default
	observability.Default = observability.NewEmitter(&bytes.Buffer{})
	t.Cleanup(func() { observability.Default = old })

	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	fake := &email.Fake{}
	s := &hs.Server{
		DB:            pool,
		Queries:       q,
		JWTSigner:     auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL:    time.Hour,
		DefaultCityID: uuid.UUID(city.ID.Bytes).String(),
		EmailSender:   fake,
		AppBaseURL:    "https://app.example.com",
		APIBaseURL:    "https://api.example.com",
	}
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv, fake
}

// signupOn signs up and returns the access token.
func signupOn(t *testing.T, srv *httptest.Server, address string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": address, "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.AccessToken
}

func authedGet(t *testing.T, srv *httptest.Server, path, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// confirmLinkFrom pulls the confirm URL out of a plain-text mail body.
// server_test.go is package http_test and cannot see the handlers' copy.
func confirmLinkFrom(t *testing.T, text string) string {
	t.Helper()
	for _, f := range strings.Fields(text) {
		if strings.Contains(f, "/auth/confirm?token=") {
			return f
		}
	}
	t.Fatalf("no confirm link in message body: %q", text)
	return ""
}

// guardedRoutes are authenticated routes behind the confirmation gate. All
// return 200 for a confirmed user, so the only thing separating 403 from 200 is
// the gate.
var guardedRoutes = []string{
	"/me/manual-interests",
	"/me/calendar",
	"/calendar/00000000-0000-0000-0000-000000000000",
}

func TestServer_UnconfirmedIsGatedOffGuardedRoutes(t *testing.T) {
	srv, _ := newConfirmationTestServer(t)
	tok := signupOn(t, srv, "gated@example.com")

	for _, route := range guardedRoutes {
		require.Equal(t, http.StatusForbidden, authedGet(t, srv, route, tok), route)
	}
}

func TestServer_ExemptRoutesStayReachable(t *testing.T) {
	srv, _ := newConfirmationTestServer(t)
	tok := signupOn(t, srv, "exempt@example.com")

	// What an unconfirmed user needs to get confirmed, or to leave.
	require.Equal(t, http.StatusOK, authedGet(t, srv, "/me", tok))

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/auth/confirm/resend", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestServer_ConfirmLinkEndToEnd(t *testing.T) {
	srv, fake := newConfirmationTestServer(t)
	tok := signupOn(t, srv, "e2e-confirm@example.com")
	require.Equal(t, http.StatusForbidden, authedGet(t, srv, guardedRoutes[0], tok))

	// Follow the emailed link against this server, not the configured API host.
	require.Len(t, fake.Messages(), 1)
	link := confirmLinkFrom(t, fake.Last().Text)
	path := link[strings.Index(link, "/auth/confirm"):]

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noRedirect.Get(srv.URL + path)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "https://app.example.com/?welcome=true&email=e2e-confirm%40example.com", resp.Header.Get("Location"))

	// The old token still carries confirmed:false — that is the bounded stale
	// case the SPA self-heals by refreshing.
	require.Equal(t, http.StatusForbidden, authedGet(t, srv, guardedRoutes[0], tok))

	// A freshly minted one passes. This is the other half of the gate: without
	// it, a gate that rejected everyone would still satisfy every 403 assertion
	// above.
	fresh := loginFor(t, srv, "e2e-confirm@example.com")
	for _, route := range guardedRoutes {
		require.Equal(t, http.StatusOK, authedGet(t, srv, route, fresh), route)
	}
}
