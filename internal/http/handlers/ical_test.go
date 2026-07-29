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
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID), true)

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
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID), true)

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

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID), true)
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

// A date-only event is stored as local midnight. It must stay in the feed for
// the whole day it happens on — not vanish an hour after midnight, hours before
// the show actually starts.
func TestGetIcalFeed_KeepsDateOnlyEventLaterToday(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Neumos", NormalizedName: "neumos",
	})
	// Midnight-local today; the show is this evening.
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "ical-tbd-1", Title: "Date Only Show",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(-12 * time.Hour), Valid: true},
		TimeTbd:  true,
		VenueID:  venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID: userID, EventID: eventID, Score: 0.9,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))

	rawToken := "ical-tbd-token-not-random-but-fine"
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID: userID, TokenHash: auth.HashRefresh(rawToken),
	}))

	r := chi.NewRouter()
	r.Get("/ical/{token}", handlers.GetIcalFeed(q))
	req := httptest.NewRequest(http.MethodGet, "/ical/"+rawToken+".ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "SUMMARY:Date Only Show")
}

// An event that has started but is still running stays in the feed.
func TestGetIcalFeed_KeepsInProgressEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Showbox", NormalizedName: "showbox",
	})
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "ical-running-1", Title: "In Progress Show",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID: userID, EventID: eventID, Score: 0.9,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))

	rawToken := "ical-running-token-not-random-but-fine"
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID: userID, TokenHash: auth.HashRefresh(rawToken),
	}))

	r := chi.NewRouter()
	r.Get("/ical/{token}", handlers.GetIcalFeed(q))
	req := httptest.NewRequest(http.MethodGet, "/ical/"+rawToken+".ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "SUMMARY:In Progress Show")
}

// The feed must still drop events that are genuinely finished.
func TestGetIcalFeed_DropsFinishedEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Tractor", NormalizedName: "tractor",
	})
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "ical-done-1", Title: "Finished Show",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(-8 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID: userID, EventID: eventID, Score: 0.9,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))

	rawToken := "ical-done-token-not-random-but-fine"
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID: userID, TokenHash: auth.HashRefresh(rawToken),
	}))

	r := chi.NewRouter()
	r.Get("/ical/{token}", handlers.GetIcalFeed(q))
	req := httptest.NewRequest(http.MethodGet, "/ical/"+rawToken+".ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "SUMMARY:Finished Show")
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
