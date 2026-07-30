package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	addr := "100 Main St"
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "The Bowl", NormalizedName: "the bowl", Address: &addr,
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
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))
	return userRow.ID, eventID
}

func TestGetMyCalendar_ReturnsMatchedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)

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

// seedManyMatchedEvents adds n matched events for userID, one hour apart
// starting 24h out, titled "Event 00".."Event NN" in chronological order.
func seedManyMatchedEvents(t *testing.T, q *store.Queries, ctx context.Context, userID pgtype.UUID, n int) {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Paging Hall", NormalizedName: "paging hall",
	})
	require.NoError(t, err)

	base := time.Now().Add(24 * time.Hour)
	for i := 0; i < n; i++ {
		eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: fmt.Sprintf("page-%03d", i),
			Title:         fmt.Sprintf("Event %02d", i),
			Description:   "seeded",
			StartsAt:      pgtype.Timestamptz{Time: base.Add(time.Duration(i) * time.Hour), Valid: true},
			VenueID:       venueID,
		})
		require.NoError(t, err)
		require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
			UserID:         userID,
			EventID:        eventID,
			Score:          0.5,
			ScoreBreakdown: []byte(`{}`),
			ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}))
	}
}

type pagedCalendarResponse struct {
	Events []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"events"`
	NextCursor string `json:"next_cursor"`
}

// getMyCalendarPage issues one request and decodes it. cursor may be "".
func getMyCalendarPage(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID pgtype.UUID, cursor string) pagedCalendarResponse {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/me/calendar"
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

func TestGetMyCalendar_FirstPageCapsAt20AndReturnsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	resp := getMyCalendarPage(t, q, signer, userID, "")

	require.Len(t, resp.Events, 20)
	require.NotEmpty(t, resp.NextCursor)
}

func TestGetMyCalendar_CursorWalksEveryEventExactlyOnce(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	// 25 seeded here + the 1 from seedCalendarFixture = 26 total.
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getMyCalendarPage(t, q, signer, userID, cursor)
		require.LessOrEqual(t, len(resp.Events), 20)
		for _, e := range resp.Events {
			require.False(t, seenIDs[e.ID], "event %s returned twice", e.ID)
			seenIDs[e.ID] = true
			total++
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	require.Equal(t, 26, total)
}

func TestGetMyCalendar_LastPageOmitsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	// Exactly one event exists, so the first page is also the last.
	resp := getMyCalendarPage(t, q, signer, userID, "")

	require.Len(t, resp.Events, 1)
	require.Empty(t, resp.NextCursor)
}

// A page that is exactly full but has nothing after it must not advertise a
// next page. This is what the fetch-21-return-20 trick buys us.
func TestGetMyCalendar_ExactlyFullPageOmitsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	// 19 + the fixture's 1 = exactly 20.
	seedManyMatchedEvents(t, q, ctx, userID, 19)

	resp := getMyCalendarPage(t, q, signer, userID, "")

	require.Len(t, resp.Events, 20)
	require.Empty(t, resp.NextCursor)
}

func TestGetMyCalendar_BadCursor_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?cursor=!!!not-a-cursor!!!", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_cursor")
}

// The frontend still sends from/to until it is updated separately. Those params
// must be ignored, not rejected.
func TestGetMyCalendar_StaleFromToParamsAreIgnored(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from=2026-01-01&to=2026-01-02", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	// The window would have excluded it; pagination ignores the window.
	require.Len(t, resp.Events, 1)
}

// The composite cursor's whole purpose: events sharing a start instant must
// straddle the page boundary without being dropped or repeated. Every other
// pagination test here spaces events an hour apart and would still pass with a
// starts_at-only cursor; this one would not.
func TestGetMyCalendar_TiedStartsAtStraddlingPageBoundary(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Tie Hall", NormalizedName: "tie hall",
	})
	require.NoError(t, err)

	// 25 matched events at one instant, so the page boundary at 20 falls inside
	// the tie group.
	tied := time.Now().Add(24 * time.Hour)
	for i := 0; i < 25; i++ {
		eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: fmt.Sprintf("mytie-%03d", i),
			Title:         fmt.Sprintf("Tied %02d", i),
			Description:   "seeded",
			StartsAt:      pgtype.Timestamptz{Time: tied, Valid: true},
			VenueID:       venueID,
		})
		require.NoError(t, err)
		require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
			UserID:         userID,
			EventID:        eventID,
			Score:          0.5,
			ScoreBreakdown: []byte(`{}`),
			ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}))
	}

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getMyCalendarPage(t, q, signer, userID, cursor)
		for _, e := range resp.Events {
			require.False(t, seenIDs[e.ID], "event %s returned twice across a tie", e.ID)
			seenIDs[e.ID] = true
			total++
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	// 25 tied + the fixture's 1.
	require.Equal(t, 26, total)
}

func TestGetEventByID_MatchedEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)

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

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID), true)
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
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID), true)

	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	req := httptest.NewRequest(http.MethodGet, "/events/00000000-0000-0000-0000-000000000000", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetMyCalendar_ExcludesNotInterested(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)

	require.NoError(t, q.AddNotInterested(ctx, store.AddNotInterestedParams{
		UserID:  userID,
		EventID: eventID,
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	from := time.Now().Add(-time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from="+from+"&to="+to, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(t, resp.Events) // dismissed event is hidden
}

// Companion regression test: clearing the list makes the event visible again.
// Proves ClearNotInterested actually deletes the row. Passes once the filter exists.
func TestGetMyCalendar_ResetRestoresEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)

	require.NoError(t, q.AddNotInterested(ctx, store.AddNotInterestedParams{
		UserID:  userID,
		EventID: eventID,
	}))
	require.NoError(t, q.ClearNotInterested(ctx, userID))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	from := time.Now().Add(-time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from="+from+"&to="+to, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1) // event visible again after reset
}

// cityRouter wires the handler the way server.go does, so {cityId} resolves.
func cityRouter(q *store.Queries, signer *auth.JWTSigner) *chi.Mux {
	r := chi.NewRouter()
	r.With(middleware.RequireAuth(signer)).Get("/calendar/{cityId}", handlers.GetCityCalendar(q))
	return r
}

func TestGetCityCalendar_ReturnsUnmatchedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	// A second event with NO user_event_match row at all.
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Side Room", NormalizedName: "side room",
	})
	_, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "city-unmatched",
		Title:         "Unmatched Show",
		Description:   "nobody matched this",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	from := time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(30 * 24 * time.Hour).UTC().Format("2006-01-02")

	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?from=" + from + "&to=" + to
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []struct {
			Title          string  `json:"title"`
			Score          float64 `json:"score"`
			MatchedBecause struct {
				Performers []string `json:"performers"`
				Genres     []string `json:"genres"`
			} `json:"matched_because"`
		} `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	titles := []string{}
	for _, e := range resp.Events {
		titles = append(titles, e.Title)
	}
	// Both the matched event and the unmatched one.
	require.ElementsMatch(t, []string{"PB Live", "Unmatched Show"}, titles)
	for _, e := range resp.Events {
		require.Zero(t, e.Score, e.Title)
		require.Empty(t, e.MatchedBecause.Performers, e.Title)
		require.Empty(t, e.MatchedBecause.Genres, e.Title)
	}
}

// The endpoint is deliberately city-wide, not caller-specific: a not-interested
// event still appears. Guards against someone "helpfully" adding the filter.
func TestGetCityCalendar_IncludesNotInterestedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	require.NoError(t, q.AddNotInterested(ctx, store.AddNotInterestedParams{
		UserID: userID, EventID: eventID,
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	from := time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(30 * 24 * time.Hour).UTC().Format("2006-01-02")

	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?from=" + from + "&to=" + to
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1)
	require.Equal(t, "PB Live", resp.Events[0].Title)
}

func TestGetCityCalendar_UnknownCityReturnsEmptyList(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuid.New().String() + "?from=2026-01-01&to=2026-12-31"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"events":[]}`, rec.Body.String())
}

func TestGetCityCalendar_BadCityID_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/calendar/not-a-uuid?from=2026-01-01&to=2026-12-31", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_city_id")
}

func TestGetCityCalendar_MissingDates_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing_range")
}

func TestGetCityCalendar_NoToken_Returns401(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/calendar/"+uuid.New().String()+"?from=2026-01-01&to=2026-12-31", nil)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
