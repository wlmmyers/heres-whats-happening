package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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

func TestDeleteIcalToken_RemovesRow(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "ical-del@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID:    userRow.ID,
		TokenHash: []byte("hash-bytes"),
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID))
	mw := middleware.RequireAuth(signer)
	h := mw(handlers.DeleteIcalToken(q))

	req := httptest.NewRequest(http.MethodDelete, "/me/ical-token", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM ical_tokens WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 0, n)
}

func TestGetIcalFeed_ReturnsRFC5545(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	rawToken := "test-token-not-random-but-fine-for-test"
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID:    userID,
		TokenHash: auth.HashRefresh(rawToken),
	}))

	r := chi.NewRouter()
	r.Get("/ical/{token}", handlers.GetIcalFeed(q))

	req := httptest.NewRequest(http.MethodGet, "/ical/"+rawToken+".ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/calendar; charset=utf-8", rec.Result().Header.Get("Content-Type"))
	require.Contains(t, rec.Result().Header.Get("Cache-Control"), "max-age=3600")
	require.Equal(t, "PT1H", rec.Result().Header.Get("X-Published-Ttl"))

	body := rec.Body.String()
	require.Contains(t, body, "BEGIN:VCALENDAR")
	require.Contains(t, body, "BEGIN:VEVENT")
	require.Contains(t, body, "SUMMARY:PB Live")
	require.Contains(t, body, `LOCATION:The Bowl\, 100 Main St`)
	require.Contains(t, body, "END:VCALENDAR")
}

func TestGetIcalFeed_UnknownToken_404(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)

	r := chi.NewRouter()
	r.Get("/ical/{token}", handlers.GetIcalFeed(q))

	req := httptest.NewRequest(http.MethodGet, "/ical/nope-not-a-real-token.ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
