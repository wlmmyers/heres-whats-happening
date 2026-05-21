package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func uuidFromPgCal(u pgtype.UUID) uuid.UUID { return uuid.UUID(u.Bytes) }

func seedCalendarFixture(t *testing.T, q *store.Queries, ctx context.Context) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "calendar@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, err)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "The Bowl", NormalizedName: "the bowl",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "cal-1",
		Title:         "PB Live",
		Description:   "Indie rock",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userRow.ID,
		EventID:        eventID,
		Score:          0.82,
		ScoreBreakdown: []byte(`{"matched_performers":["Phoebe Bridgers"],"matched_genres":["indie"]}`),
	}))
	return userRow.ID, eventID
}

func TestGetMyCalendar_ReturnsMatchedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID))

	from := time.Now().Add(-time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from="+from+"&to="+to, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []struct {
			ID    string  `json:"id"`
			Title string  `json:"title"`
			Score float64 `json:"score"`
			Venue struct {
				Name string `json:"name"`
			} `json:"venue"`
			MatchedBecause struct {
				Performers []string `json:"performers"`
				Genres     []string `json:"genres"`
			} `json:"matched_because"`
		} `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1)
	require.Equal(t, "PB Live", resp.Events[0].Title)
	require.InDelta(t, 0.82, resp.Events[0].Score, 0.01)
	require.Equal(t, "The Bowl", resp.Events[0].Venue.Name)
	require.Equal(t, []string{"Phoebe Bridgers"}, resp.Events[0].MatchedBecause.Performers)
	require.Equal(t, []string{"indie"}, resp.Events[0].MatchedBecause.Genres)
}

func TestGetMyCalendar_DateRangeFiltering(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID))

	from := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(14 * 24 * time.Hour).UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from="+from+"&to="+to, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(t, resp.Events)
}

func TestGetMyCalendar_MissingDates_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID))

	req := httptest.NewRequest(http.MethodGet, "/me/calendar", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetEventByID_MatchedEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID))

	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	url := "/events/" + uuidFromPgCal(eventID).String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID    string  `json:"id"`
		Title string  `json:"title"`
		Score float64 `json:"score"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "PB Live", resp.Title)
	require.InDelta(t, 0.82, resp.Score, 0.01)
}

func TestGetEventByID_UnmatchedEvent_ScoreIsZero(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "lone@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Q", NormalizedName: "q",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "unmatched-1",
		Title:         "Unmatched",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID))
	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	req := httptest.NewRequest(http.MethodGet, "/events/"+uuidFromPgCal(eventID).String(), nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Title string  `json:"title"`
		Score float64 `json:"score"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "Unmatched", resp.Title)
	require.Equal(t, 0.0, resp.Score)
}

func TestGetEventByID_NotFound(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "nf@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID))

	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	req := httptest.NewRequest(http.MethodGet, "/events/00000000-0000-0000-0000-000000000000", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
