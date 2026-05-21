package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCreateIcalToken_ReturnsURL(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "ical@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID))

	mw := middleware.RequireAuth(signer)
	h := mw(handlers.CreateIcalToken(q, "http://localhost:8080"))

	req := httptest.NewRequest(http.MethodPost, "/me/ical-token", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, strings.HasPrefix(resp.URL, "http://localhost:8080/ical/"))
	require.True(t, strings.HasSuffix(resp.URL, ".ics"))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM ical_tokens WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestCreateIcalToken_RotatesOnRepeat(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "ical-rotate@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID))

	mw := middleware.RequireAuth(signer)
	h := mw(handlers.CreateIcalToken(q, "http://localhost:8080"))

	send := func() string {
		req := httptest.NewRequest(http.MethodPost, "/me/ical-token", nil)
		req.Header.Set("Authorization", "Bearer "+accessTok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var resp struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		return resp.URL
	}
	first := send()
	second := send()
	require.NotEqual(t, first, second, "token must rotate on repeat POST")

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM ical_tokens WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 1, n)

	parts := strings.Split(strings.TrimSuffix(second, ".ics"), "/ical/")
	require.Len(t, parts, 2)
	secondToken := parts[1]

	row, err := q.GetIcalTokenByHash(ctx, auth.HashRefresh(secondToken))
	require.NoError(t, err)
	require.Equal(t, userRow.ID, row.UserID)

	var _ pgtype.UUID
}
