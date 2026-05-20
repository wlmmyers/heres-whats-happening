package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
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
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q))

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
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q))

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
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q))

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
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
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
