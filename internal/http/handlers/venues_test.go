package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

type venuesResp struct {
	Venues []struct {
		ID                 string   `json:"id"`
		Name               string   `json:"name"`
		Address            string   `json:"address"`
		Lat                *float64 `json:"lat"`
		Lng                *float64 `json:"lng"`
		WebsiteURL         string   `json:"website_url"`
		UpcomingEventCount int      `json:"upcoming_event_count"`
		NextEventAt        string   `json:"next_event_at"`
	} `json:"venues"`
}

// venuesRouter wires the handler the way server.go does, so {cityId} resolves.
func venuesRouter(q *store.Queries, signer *auth.JWTSigner) *chi.Mux {
	r := chi.NewRouter()
	r.With(middleware.RequireAuth(signer)).Get("/venues/{cityId}", handlers.ListCityVenues(q))
	return r
}

func callVenues(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID pgtype.UUID, cityID string) *httptest.ResponseRecorder {
	t.Helper()
	return callVenuesQuery(t, q, signer, userID, cityID, "")
}

func callVenuesQuery(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID pgtype.UUID, cityID, query string) *httptest.ResponseRecorder {
	t.Helper()
	tok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	target := "/venues/" + cityID
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	venuesRouter(q, signer).ServeHTTP(rec, req)
	return rec
}

func seedVenueEvent(t *testing.T, q *store.Queries, ctx context.Context, venueID pgtype.UUID, key string, startsAt time.Time) pgtype.UUID {
	t.Helper()
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	id, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: key,
		Title:         key,
		StartsAt:      pgtype.Timestamptz{Time: startsAt, Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	return id
}

func TestListCityVenues_ReturnsVenuesWithEventData(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	// Seeds "The Bowl" (100 Main St) with one event 48h out.
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	lat, lng, site := 47.6, -122.3, "https://annex.example.com"
	annex, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "annex", NormalizedName: "annex",
		Lat: &lat, Lng: &lng, WebsiteUrl: &site,
	})
	require.NoError(t, err)
	soon := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	seedVenueEvent(t, q, ctx, annex, "annex-1", soon)
	seedVenueEvent(t, q, ctx, annex, "annex-2", soon.Add(24*time.Hour))
	// Over: has event data, but nothing upcoming.
	seedVenueEvent(t, q, ctx, annex, "annex-past", time.Now().Add(-72*time.Hour))

	rec := callVenues(t, q, signer, userID, uuidFromPgCal(city.ID).String())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp venuesResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Venues, 2)

	// annex has more upcoming events, so it leads.
	a, b := resp.Venues[0], resp.Venues[1]
	require.Equal(t, "annex", a.Name)
	require.Equal(t, uuidFromPgCal(annex).String(), a.ID)
	require.Equal(t, 2, a.UpcomingEventCount)
	require.Equal(t, soon.Format(time.RFC3339), a.NextEventAt)
	require.Equal(t, site, a.WebsiteURL)
	require.NotNil(t, a.Lat)
	require.InDelta(t, lat, *a.Lat, 1e-9)
	require.InDelta(t, lng, *a.Lng, 1e-9)

	require.Equal(t, "The Bowl", b.Name)
	require.Equal(t, "100 Main St", b.Address)
	require.Equal(t, 1, b.UpcomingEventCount)
}

func TestListCityVenues_ExcludesVenuesWithoutLiveEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	// No events at all.
	_, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Empty Room", NormalizedName: "empty room",
	})
	require.NoError(t, err)
	// Only an archived event.
	gone, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Gone Club", NormalizedName: "gone club",
	})
	require.NoError(t, err)
	evID := seedVenueEvent(t, q, ctx, gone, "gone-1", time.Now().Add(48*time.Hour))
	_, err = pool.Exec(ctx, `UPDATE events SET archived_at = NOW() WHERE id = $1`, evID)
	require.NoError(t, err)
	// Only events that are over.
	old, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Old Hall", NormalizedName: "old hall",
	})
	require.NoError(t, err)
	seedVenueEvent(t, q, ctx, old, "old-1", time.Now().Add(-72*time.Hour))

	rec := callVenues(t, q, signer, userID, uuidFromPgCal(city.ID).String())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp venuesResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	names := []string{}
	for _, v := range resp.Venues {
		names = append(names, v.Name)
	}
	require.Equal(t, []string{"The Bowl"}, names)
}

func TestListCityVenues_UnknownCityReturnsEmptyList(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	rec := callVenues(t, q, signer, userID, uuid.New().String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"venues":[]}`, rec.Body.String())
}

func TestListCityVenues_BadCityID_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	rec := callVenues(t, q, signer, userID, "not-a-uuid")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_city_id")
}

func TestListCityVenues_NoToken_Returns401(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/venues/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	venuesRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// segment narrows the counts with the calendar's rule (NULL-segment events
// count under every value), and drops venues left with nothing in it.
func TestListCityVenues_SegmentFiltersCountsAndVenues(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	// "Segment Hall": one each of music, sports, arts-theatre and unlabeled,
	// ordered by map iteration, so the earliest is not known here.
	seedSegmentedEvents(t, q, ctx, userID, segmentFixture())

	arena, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Arena", NormalizedName: "arena",
	})
	require.NoError(t, err)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	_, err = q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "arena-sports",
		Title:         "Arena Game",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(30 * time.Hour), Valid: true},
		VenueID:       arena,
		Segment:       ptrTo("sports"),
	})
	require.NoError(t, err)

	counts := func(query string) map[string]map[string]any {
		rec := callVenuesQuery(t, q, signer, userID, uuidFromPgCal(city.ID).String(), query)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var raw struct {
			Venues []map[string]any `json:"venues"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		out := map[string]map[string]any{}
		for _, v := range raw.Venues {
			out[v["name"].(string)] = v
		}
		return out
	}

	all := counts("")
	require.EqualValues(t, 4, all["Segment Hall"]["upcoming_event_count"])
	require.EqualValues(t, 1, all["Arena"]["upcoming_event_count"])

	music := counts("segment=music")
	require.EqualValues(t, 2, music["Segment Hall"]["upcoming_event_count"], "music + unlabeled")
	require.NotContains(t, music, "Arena", "a venue with no upcoming music is hidden")
}

func TestListCityVenues_UnknownSegmentIs400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	rec := callVenuesQuery(t, q, signer, userID, uuidFromPgCal(city.ID).String(), "segment=musci")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_segment")
}

// Busiest first, whatever the names; equal counts fall back to case-insensitive
// alphabetical so the order is stable.
func TestListCityVenues_OrdersByUpcomingCountThenName(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	// Seeds "The Bowl" with one upcoming event.
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	seed := func(name string, n int) {
		v, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
			CityID: city.ID, Name: name, NormalizedName: strings.ToLower(name),
		})
		require.NoError(t, err)
		for i := 0; i < n; i++ {
			seedVenueEvent(t, q, ctx, v, fmt.Sprintf("%s-%d", name, i), time.Now().Add(time.Duration(24+i)*time.Hour))
		}
	}
	seed("Zed Club", 3)
	seed("middle room", 2)
	seed("Alpha Bar", 2)

	rec := callVenues(t, q, signer, userID, uuidFromPgCal(city.ID).String())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp venuesResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	names := []string{}
	for _, v := range resp.Venues {
		names = append(names, v.Name)
	}
	require.Equal(t, []string{"Zed Club", "Alpha Bar", "middle room", "The Bowl"}, names)
}
