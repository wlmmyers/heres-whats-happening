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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// uuidMust parses a uuid string into the 16-byte array pgtype.UUID expects.
func uuidMust(t *testing.T, s string) [16]byte {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return id
}

func signupForThreshold(t *testing.T, q *store.Queries, signer *auth.JWTSigner, cityID, email string) (string, string) {
	t.Helper()
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

func TestUpdateMatchThreshold_PersistsAndAccepts(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "th-ok@example.com")

	body, _ := json.Marshal(map[string]float64{"threshold": 0.45})
	req := httptest.NewRequest(http.MethodPatch, "/me/match-threshold", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.UpdateMatchThreshold(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var got *float64
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT score_threshold FROM users WHERE id=$1`,
		pgtype.UUID{Bytes: uuidMust(t, uid), Valid: true}).Scan(&got))
	require.NotNil(t, got)
	require.InDelta(t, 0.45, *got, 1e-9)
}

func TestUpdateMatchThreshold_RejectsOutOfRange(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, _ := signupForThreshold(t, q, signer, cityID, "th-bad@example.com")

	for _, v := range []float64{0.10, 0.75} {
		body, _ := json.Marshal(map[string]float64{"threshold": v})
		req := httptest.NewRequest(http.MethodPatch, "/me/match-threshold", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw := middleware.RequireAuth(signer)
		mw(handlers.UpdateMatchThreshold(q)).ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "threshold %v should be rejected", v)
	}
}
