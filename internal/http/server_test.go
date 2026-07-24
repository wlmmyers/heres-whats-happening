package http_test

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
	hs "github.com/wmyers/heres-whats-happening/internal/http"
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
		require.NotEqual(t, http.StatusTooManyRequests, resp.StatusCode, "request %d", i+1)
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
		require.NotEqual(t, http.StatusTooManyRequests, resp.StatusCode, "request %d", i+1)
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
