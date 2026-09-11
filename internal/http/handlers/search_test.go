package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type searchBody struct {
	Results []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		StartsAt string `json:"starts_at"`
		ImageURL string `json:"image_url"`
		Segment  string `json:"segment"`
		Venue    struct {
			Name string `json:"name"`
		} `json:"venue"`
	} `json:"results"`
}

func searchRouter(q *store.Queries) http.Handler {
	r := chi.NewRouter()
	r.Get("/search/{cityId}/events", handlers.SearchEvents(q))
	return r
}

func seedSearchHandlerFixture(t *testing.T, q *store.Queries, ctx context.Context) string {
	t.Helper()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "The Bowl", NormalizedName: "the bowl",
	})
	require.NoError(t, err)
	_, err = q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "search-handler-1",
		Title:         "Midnight Orchard",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	return uuidFromPgCal(city.ID).String()
}

func doSearch(t *testing.T, h http.Handler, cityID, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/search/"+cityID+"/events?q="+rawQuery, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSearchEvents_ReturnsMatchingEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "midnight")
	require.Equal(t, http.StatusOK, rec.Code)

	var body searchBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 1)
	require.Equal(t, "Midnight Orchard", body.Results[0].Title)
	require.Equal(t, "The Bowl", body.Results[0].Venue.Name)
}

// rank is an internal ordering signal. Publishing it invites clients to
// threshold on it, which is exactly the mistake the cosine bands rule out.
func TestSearchEvents_DoesNotExposeRank(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "midnight")
	require.NotContains(t, rec.Body.String(), "rank")
}

func TestSearchEvents_RejectsShortAndEmptyQueries(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)
	h := searchRouter(q)

	for _, tc := range []struct{ name, query, code string }{
		{"empty", "", "bad_query"},
		{"whitespace only", "%20%20", "bad_query"},
		{"two runes", "mi", "query_too_short"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doSearch(t, h, cityID, tc.query)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), tc.code)
		})
	}
}

// Runes, not bytes: the floor must be enforced on rune count. Three CJK
// characters (9 bytes) alone can't tell a rune-counter from a byte-counter
// apart -- a UTF-8 string of 3 runes is always at least 3 bytes, so both
// implementations accept it identically. Two 4-byte emoji force the split:
// 2 runes but 8 bytes, so only a genuine rune-counter rejects it -- a
// byte-counter (len(raw) < 3) would wrongly accept it.
func TestSearchEvents_CountsRunesNotBytes(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)
	h := searchRouter(q)

	t.Run("three CJK runes, 9 bytes, accepted", func(t *testing.T) {
		rec := doSearch(t, h, cityID, "%E6%9D%B1%E4%BA%AC%E9%83%BD")
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("two 4-byte emoji, 2 runes, 8 bytes, rejected as too short", func(t *testing.T) {
		rec := doSearch(t, h, cityID, "%F0%9F%98%80%F0%9F%98%83")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), "query_too_short")
	})
}

func TestSearchEvents_RejectsBadCityID(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), "not-a-uuid", "midnight")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_city_id")
}

func TestSearchEvents_ReturnsEmptyArrayNotNull(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "zzzqqqxxx")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"results":[]`)
}
