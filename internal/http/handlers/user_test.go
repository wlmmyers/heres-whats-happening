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
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestGetMe_ReturnsCurrentUser(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	signupAndLogin := func(email string) (string, string) {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter22"})
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var resp struct {
			AccessToken string `json:"access_token"`
			User        struct{ ID, Email string }
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		return resp.AccessToken, resp.User.ID
	}

	access, _ := signupAndLogin("getme@example.com")

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMe(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "getme@example.com", out.Email)
}

func TestDeleteMe_SoftDeletes(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "del@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	req2 := httptest.NewRequest(http.MethodDelete, "/me", nil)
	req2.Header.Set("Authorization", "Bearer "+resp.AccessToken)
	rec2 := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.DeleteMe(q)).ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// verify deleted_at is set
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := q.GetUserByEmail(ctx, "del@example.com")
	require.Error(t, err) // soft-deleted users are filtered out
}

func TestGetMe_ReturnsResolvedThreshold(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "getme-th@example.com")

	// Default (NULL) → resolves to the global default (0.3).
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMe(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		ScoreThreshold float64 `json:"score_threshold"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.InDelta(t, 0.3, out.ScoreThreshold, 1e-9)

	// After setting a value, GetMe returns it.
	th := 0.5
	require.NoError(t, q.UpdateUserScoreThreshold(context.Background(), store.UpdateUserScoreThresholdParams{
		ID: pgtype.UUID{Bytes: uuidMust(t, uid), Valid: true}, ScoreThreshold: &th,
	}))
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/me", nil)
	req2.Header.Set("Authorization", "Bearer "+access)
	mw(handlers.GetMe(q)).ServeHTTP(rec2, req2)
	var out2 struct {
		ScoreThreshold float64 `json:"score_threshold"`
	}
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&out2))
	require.InDelta(t, 0.5, out2.ScoreThreshold, 1e-9)
}
