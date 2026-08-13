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

// seedArtistWithImage creates a resolved artists row (status "ok") with an
// artist_images row also at status "ok", so GetArtistEnrichmentBatch surfaces
// a populated image section for it. Kept separate from seedCalendarFixture,
// which never sets headline_artist_id on its event, so as not to perturb any
// test built on top of that fixture.
func seedArtistWithImage(t *testing.T, q *store.Queries, ctx context.Context, nameKey, displayName, imageURL string) pgtype.UUID {
	t.Helper()
	artistID, err := q.UpsertArtist(ctx, store.UpsertArtistParams{
		NameKey:     nameKey,
		DisplayName: displayName,
		Status:      "ok",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertArtistImage(ctx, store.UpsertArtistImageParams{
		ArtistID: artistID,
		Status:   "ok",
		Url:      &imageURL,
	}))
	return artistID
}

// seedArtistWithNoEnrichment creates a resolved artists row whose image
// enrichment explicitly found nothing (status "none"). Used to pin the "only
// status=ok sections appear" rule through the real batch-load-and-attach path,
// not just buildArtist in isolation (already covered by artist_test.go).
func seedArtistWithNoEnrichment(t *testing.T, q *store.Queries, ctx context.Context, nameKey, displayName string) pgtype.UUID {
	t.Helper()
	artistID, err := q.UpsertArtist(ctx, store.UpsertArtistParams{
		NameKey:     nameKey,
		DisplayName: displayName,
		Status:      "ok",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertArtistImage(ctx, store.UpsertArtistImageParams{
		ArtistID: artistID,
		Status:   "none",
	}))
	return artistID
}

// TestGetMyCalendar_AttachesArtistEnrichmentWithImageFallback exercises Step 6
// wiring end-to-end for GetMyCalendar: it must batch-load enrichment for the
// page, hang it off the matching event as "artist", and fall the top-level
// image_url back to the artist photo since the event itself supplies none.
// seedCalendarFixture's own event never sets headline_artist_id, so it
// doubles here as the "no artist key at all" control in the same response —
// pinning the omitempty contract that keeps today's frontend payloads
// unchanged for events without a headline artist.
func TestGetMyCalendar_AttachesArtistEnrichmentWithImageFallback(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	artistID := seedArtistWithImage(t, q, ctx, "phoebe-bridgers-enrich-fallback", "Phoebe Bridgers", "https://img.example.com/pb.jpg")

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Enriched Hall", NormalizedName: "enriched hall",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:         src.ID,
		SourceEventID:    "enriched-1",
		Title:            "Enriched Show",
		Description:      "has a headline artist",
		StartsAt:         pgtype.Timestamptz{Time: time.Now().Add(50 * time.Hour), Valid: true},
		VenueID:          venueID,
		HeadlineArtistID: artistID,
		// ImageUrl intentionally left nil: the fallback should fill it in.
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userID,
		EventID:        eventID,
		Score:          0.9,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Raw map decode, not a typed struct: a typed *Artist field decodes to nil
	// both when the key is absent and (hypothetically) present-but-null, and
	// the whole point here is to pin that the key is genuinely absent for the
	// unenriched event, not just zero-valued.
	var raw struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Len(t, raw.Events, 2)

	var enriched, plain map[string]any
	for _, e := range raw.Events {
		switch e["title"] {
		case "Enriched Show":
			enriched = e
		case "PB Live":
			plain = e
		}
	}
	require.NotNil(t, enriched, "Enriched Show missing from response")
	require.NotNil(t, plain, "PB Live missing from response")

	artist, ok := enriched["artist"].(map[string]any)
	require.True(t, ok, "expected an artist object, got %#v", enriched["artist"])
	require.Equal(t, "Phoebe Bridgers", artist["name"])
	image, ok := artist["image"].(map[string]any)
	require.True(t, ok, "expected an image section, got %#v", artist["image"])
	require.Equal(t, "https://img.example.com/pb.jpg", image["url"])
	require.Equal(t, "https://img.example.com/pb.jpg", enriched["image_url"])

	_, hasArtist := plain["artist"]
	require.False(t, hasArtist, "PB Live has no headline_artist_id and must carry no artist key at all")
}

// TestGetMyCalendar_ArtistEnrichmentOmitsFailedSections pins the "only
// status=ok sections appear" rule through the real HTTP path: a resolved
// artist whose image enrichment explicitly found nothing must still produce
// an artist object (it has a name), but no image/bio/tour keys, and the
// image_url fallback must have nothing to contribute.
func TestGetMyCalendar_ArtistEnrichmentOmitsFailedSections(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	artistID := seedArtistWithNoEnrichment(t, q, ctx, "some-local-opener-enrich", "Some Local Opener")

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Opener Room", NormalizedName: "opener room",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:         src.ID,
		SourceEventID:    "unenriched-1",
		Title:            "Opener Show",
		Description:      "headline artist with no successful enrichment",
		StartsAt:         pgtype.Timestamptz{Time: time.Now().Add(51 * time.Hour), Valid: true},
		VenueID:          venueID,
		HeadlineArtistID: artistID,
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userID,
		EventID:        eventID,
		Score:          0.4,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	var target map[string]any
	for _, e := range raw.Events {
		if e["title"] == "Opener Show" {
			target = e
		}
	}
	require.NotNil(t, target, "Opener Show missing from response")

	artist, ok := target["artist"].(map[string]any)
	require.True(t, ok, "expected an artist object for a resolved artist, got %#v", target["artist"])
	require.Equal(t, "Some Local Opener", artist["name"])

	_, hasImage := artist["image"]
	require.False(t, hasImage, "image status=none must not produce an image section")
	_, hasBio := artist["bio"]
	require.False(t, hasBio, "no bio row at all must not produce a bio section")
	_, hasTour := artist["tour"]
	require.False(t, hasTour, "no tour row at all must not produce a tour section")

	_, hasImageURL := target["image_url"]
	require.False(t, hasImageURL, "neither the event nor the artist has an image, so the fallback has nothing to contribute")
}

// TestGetCityCalendar_AttachesArtistEnrichment pins Step 6 wiring for the city
// endpoint specifically. Each of the three handlers wires its own
// attachArtists call independently, so a dropped or misplaced call in this
// handler would not be caught by the GetMyCalendar tests above.
func TestGetCityCalendar_AttachesArtistEnrichment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	artistID := seedArtistWithImage(t, q, ctx, "city-enrich-artist", "City Enrich Artist", "https://img.example.com/city.jpg")

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "City Enrich Hall", NormalizedName: "city enrich hall",
	})
	require.NoError(t, err)
	_, err = q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:         src.ID,
		SourceEventID:    "city-enriched-1",
		Title:            "City Enriched Show",
		Description:      "seeded",
		StartsAt:         pgtype.Timestamptz{Time: time.Now().Add(52 * time.Hour), Valid: true},
		VenueID:          venueID,
		HeadlineArtistID: artistID,
	})
	require.NoError(t, err)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	var target map[string]any
	for _, e := range raw.Events {
		if e["title"] == "City Enriched Show" {
			target = e
		}
	}
	require.NotNil(t, target, "City Enriched Show missing from response")
	artist, ok := target["artist"].(map[string]any)
	require.True(t, ok, "expected an artist object, got %#v", target["artist"])
	require.Equal(t, "City Enrich Artist", artist["name"])
	require.Equal(t, "https://img.example.com/city.jpg", target["image_url"])
}

// TestGetEventByID_AttachesArtistEnrichment pins Step 6 wiring for the
// single-event endpoint, whose attachArtists call is wrapped differently
// (evs := []calendarEvent{ev}; attachArtists(...); ev = evs[0]) than the two
// paged handlers above, so it needs its own direct coverage.
func TestGetEventByID_AttachesArtistEnrichment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	artistID := seedArtistWithImage(t, q, ctx, "single-enrich-artist", "Single Enrich Artist", "https://img.example.com/single.jpg")

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Single Enrich Hall", NormalizedName: "single enrich hall",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:         src.ID,
		SourceEventID:    "single-enriched-1",
		Title:            "Single Enriched Show",
		Description:      "seeded",
		StartsAt:         pgtype.Timestamptz{Time: time.Now().Add(53 * time.Hour), Valid: true},
		VenueID:          venueID,
		HeadlineArtistID: artistID,
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userID,
		EventID:        eventID,
		Score:          0.6,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	req := httptest.NewRequest(http.MethodGet, "/events/"+uuidFromPgCal(eventID).String(), nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	artist, ok := raw["artist"].(map[string]any)
	require.True(t, ok, "expected an artist object, got %#v", raw["artist"])
	require.Equal(t, "Single Enrich Artist", artist["name"])
	require.Equal(t, "https://img.example.com/single.jpg", raw["image_url"])
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

// A user with no matched events must get {"events":[]}, not {"events":null}.
// This is a JSON-level assertion rather than require.Empty on a decoded slice
// because a decoded [] and null both satisfy require.Empty — the whole point
// here is to pin the wire shape, not just the length.
func TestGetMyCalendar_NoMatchedEventsReturnsEmptyList(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "no-matches@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, err)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userRow.ID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"events":[]}`, rec.Body.String())
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

	url := "/calendar/" + uuidFromPgCal(city.ID).String()
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

	url := "/calendar/" + uuidFromPgCal(city.ID).String()
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
	url := "/calendar/" + uuid.New().String()
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

func getCityCalendarPage(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID, cityID pgtype.UUID, cursor string) pagedCalendarResponse {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(cityID).String()
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

func TestGetCityCalendar_FirstPageCapsAt20AndReturnsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	resp := getCityCalendarPage(t, q, signer, userID, city.ID, "")

	require.Len(t, resp.Events, 20)
	require.NotEmpty(t, resp.NextCursor)
}

// A page that is exactly full but has nothing after it must not advertise a
// next page. Mirrors TestGetMyCalendar_ExactlyFullPageOmitsCursor for the city
// endpoint, whose page-boundary check (len(rows) > calendarPageSize) has no
// other test that lands on an exactly-full final page.
func TestGetCityCalendar_ExactlyFullPageOmitsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	// 19 + the fixture's 1 = exactly 20. The city endpoint counts every
	// showable city event regardless of match, same as GetMyCalendar counts
	// matched ones here.
	seedManyMatchedEvents(t, q, ctx, userID, 19)

	resp := getCityCalendarPage(t, q, signer, userID, city.ID, "")

	require.Len(t, resp.Events, 20)
	require.Empty(t, resp.NextCursor)
}

func TestGetCityCalendar_CursorWalksEveryEventExactlyOnce(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getCityCalendarPage(t, q, signer, userID, city.ID, cursor)
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

// The composite-cursor test at the HTTP layer: events sharing a start instant
// straddle the page boundary and must not be dropped or repeated.
func TestGetCityCalendar_TiedStartsAtStraddlingPageBoundary(t *testing.T) {
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

	// 25 events all at the same instant, so the page boundary at 20 falls in
	// the middle of the tie group.
	tied := time.Now().Add(24 * time.Hour)
	for i := 0; i < 25; i++ {
		_, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: fmt.Sprintf("tie-%03d", i),
			Title:         fmt.Sprintf("Tied %02d", i),
			Description:   "seeded",
			StartsAt:      pgtype.Timestamptz{Time: tied, Valid: true},
			VenueID:       venueID,
		})
		require.NoError(t, err)
	}

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getCityCalendarPage(t, q, signer, userID, city.ID, cursor)
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

func TestGetCityCalendar_BadCursor_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?cursor=!!!not-a-cursor!!!"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_cursor")
}

func TestGetCityCalendar_StaleFromToParamsAreIgnored(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?from=2026-01-01&to=2026-01-02"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1)
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
