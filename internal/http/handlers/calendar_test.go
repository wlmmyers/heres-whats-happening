package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestGetMyCalendar_AttachesArtistEnrichment exercises Step 6 wiring
// end-to-end for GetMyCalendar: it must batch-load enrichment for the page and
// hang it off the matching event as "artist" — including the band photo under
// artist.image — WITHOUT touching the top-level image_url, which stays purely
// scraper-sourced (Wikimedia Commons images are predominantly CC-BY/CC-BY-SA,
// and the frontend does not render the credit block yet, so the photo is only
// ever served alongside its attribution under artist.image).
// seedCalendarFixture's own event never sets headline_artist_id, so it
// doubles here as the "no artist key at all" control in the same response —
// pinning the omitempty contract that keeps today's frontend payloads
// unchanged for events without a headline artist.
func TestGetMyCalendar_AttachesArtistEnrichment(t *testing.T) {
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
		// ImageUrl intentionally left nil: no scraper image AND no fallback,
		// so image_url must stay absent even though the artist has a photo.
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
	_, hasImageURL := enriched["image_url"]
	require.False(t, hasImageURL, "image_url must stay purely scraper-sourced; the band photo lives only under artist.image")

	_, hasArtist := plain["artist"]
	require.False(t, hasArtist, "PB Live has no headline_artist_id and must carry no artist key at all")
}

// TestGetMyCalendar_ArtistEnrichmentOmitsFailedSections pins the "only
// status=ok sections appear" rule through the real HTTP path: a resolved
// artist whose image enrichment explicitly found nothing must still produce
// an artist object (it has a name), but no image/bio/tour keys, and no
// top-level image_url either — there is no scraper-sourced image and no
// artist photo to have ever populated it.
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
	require.False(t, hasImageURL, "no scraper-sourced image exists, and image_url no longer falls back to the artist photo")
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
	image, ok := artist["image"].(map[string]any)
	require.True(t, ok, "expected an image section, got %#v", artist["image"])
	require.Equal(t, "https://img.example.com/city.jpg", image["url"])
	_, hasImageURL := target["image_url"]
	require.False(t, hasImageURL, "image_url must stay purely scraper-sourced, not fall back to the artist photo")
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
	image, ok := artist["image"].(map[string]any)
	require.True(t, ok, "expected an image section, got %#v", artist["image"])
	require.Equal(t, "https://img.example.com/single.jpg", image["url"])
	_, hasImageURL := raw["image_url"]
	require.False(t, hasImageURL, "image_url must stay purely scraper-sourced, not fall back to the artist photo")
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

// callMyCalendar issues one GET /me/calendar?<query> as userID and hands back
// the raw recorder, so error-status tests can assert on it as well as the
// success path. query is the bare query string, without the leading "?".
func callMyCalendar(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID pgtype.UUID, query string) *httptest.ResponseRecorder {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	target := "/me/calendar"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	return rec
}

// seedBoundEvents adds n matched events for userID one hour apart from base,
// titled "Bound 00".."Bound NN" in chronological order. Unlike
// seedManyMatchedEvents the caller picks base, because the starts_at tests need
// to name an exact event instant in the query string.
func seedBoundEvents(t *testing.T, q *store.Queries, ctx context.Context, userID pgtype.UUID, base time.Time, n int) {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Bound Hall", NormalizedName: "bound hall",
	})
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: fmt.Sprintf("bound-%03d", i),
			Title:         fmt.Sprintf("Bound %02d", i),
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

// titlesOf decodes a calendar response body and returns the event titles in
// order, which is what the starts_at tests actually assert on.
func titlesOf(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	out := make([]string, 0, len(resp.Events))
	for _, e := range resp.Events {
		out = append(out, e.Title)
	}
	return out
}

// starts_at is the cursor's plain-language sibling: a lower bound the client
// names outright, rather than an opaque position we handed it. The bound is
// strict, so an event starting exactly at the given instant is excluded — that
// is what makes "everything after this event" work when the client passes back
// the starts_at it already has for that event.
func TestGetMyCalendar_StartsAtReturnsOnlyStrictlyLaterEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	// Truncated to the second so the RFC3339 param names the boundary event's
	// instant exactly: it must be excluded by >, not by a stray microsecond.
	base := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	seedBoundEvents(t, q, ctx, userID, base, 4)

	// Bound 01's own instant. The fixture's PB Live sits at +48h, after them all.
	boundary := base.Add(time.Hour).Format(time.RFC3339)
	rec := callMyCalendar(t, q, signer, userID, "starts_at="+url.QueryEscape(boundary))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"Bound 02", "Bound 03", "PB Live"}, titlesOf(t, rec))
}

// starts_at bounds the feed; the cursor walks it. A filtered feed longer than
// one page still hands back a cursor, and that cursor alone carries the rest —
// the client does not (and must not) re-send starts_at with it.
func TestGetMyCalendar_StartsAtFirstPageReturnsCursorForTheRest(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	base := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	seedBoundEvents(t, q, ctx, userID, base, 25)

	// Excludes Bound 00 only: 24 Bound events + the fixture's PB Live = 25 left.
	rec := callMyCalendar(t, q, signer, userID, "starts_at="+url.QueryEscape(base.Format(time.RFC3339)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var first pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&first))
	require.Len(t, first.Events, 20)
	require.Equal(t, "Bound 01", first.Events[0].Title)
	require.NotEmpty(t, first.NextCursor)

	second := getMyCalendarPage(t, q, signer, userID, first.NextCursor)
	require.Len(t, second.Events, 5)
	require.Empty(t, second.NextCursor)
}

// Two ways of saying "start here" in one request is a contradiction we refuse
// to guess at: the cursor's position and the caller's bound can disagree, and
// silently honouring one would hand back a page the client did not ask for.
func TestGetMyCalendar_CursorAndStartsAtTogether_Returns422(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	// A real cursor from a real first page, so the rejection is unambiguously
	// about the combination and not about an unparseable token.
	cursor := getMyCalendarPage(t, q, signer, userID, "").NextCursor
	require.NotEmpty(t, cursor)

	startsAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec := callMyCalendar(t, q, signer, userID,
		"cursor="+url.QueryEscape(cursor)+"&starts_at="+url.QueryEscape(startsAt))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "cursor_and_starts_at")
}

func TestGetMyCalendar_BadStartsAt_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	rec := callMyCalendar(t, q, signer, userID, "starts_at=next+tuesday")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_starts_at")
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

// ---- segment filter --------------------------------------------------------

// seedSegmentedEvents creates one matched event per entry, titled by its key.
// A nil segment seeds the NULL case — every event that predates the column, and
// every event from a source that does not classify.
func seedSegmentedEvents(t *testing.T, q *store.Queries, ctx context.Context, userID pgtype.UUID, bySegment map[string]*string) {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Segment Hall", NormalizedName: "segment hall",
	})
	require.NoError(t, err)

	i := 0
	for title, seg := range bySegment {
		eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: "seg-" + title,
			Title:         title,
			Description:   "seeded",
			StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(time.Duration(72+i) * time.Hour), Valid: true},
			VenueID:       venueID,
			Segment:       seg,
		})
		require.NoError(t, err)
		require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
			UserID:         userID,
			EventID:        eventID,
			Score:          0.5,
			ScoreBreakdown: []byte(`{}`),
			ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}))
		i++
	}
}

func ptrTo(s string) *string { return &s }

func segmentFixture() map[string]*string {
	return map[string]*string{
		"Seg Music":     ptrTo("music"),
		"Seg Sports":    ptrTo("sports"),
		"Seg Theatre":   ptrTo("arts-theatre"),
		"Seg Unlabeled": nil,
	}
}

func TestGetMyCalendar_NoSegmentParamReturnsEverything(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedSegmentedEvents(t, q, ctx, userID, segmentFixture())

	got := titlesOf(t, callMyCalendar(t, q, signer, userID, ""))
	require.Subset(t, got, []string{"Seg Music", "Seg Sports", "Seg Theatre", "Seg Unlabeled"})
}

// The filter is inclusive of unclassified events by design: NULL means "we do
// not know", and hiding those would make the filter silently lose every event
// that predates the column.
func TestGetMyCalendar_SegmentFilterReturnsMatchingPlusNullSegments(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedSegmentedEvents(t, q, ctx, userID, segmentFixture())

	got := titlesOf(t, callMyCalendar(t, q, signer, userID, "segment=music"))
	require.Contains(t, got, "Seg Music")
	require.Contains(t, got, "Seg Unlabeled")
	require.NotContains(t, got, "Seg Sports")
	require.NotContains(t, got, "Seg Theatre")
}

func TestGetMyCalendar_SegmentFilterAcceptsEveryValidSlug(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	for _, seg := range []string{"music", "sports", "arts-theatre", "miscellaneous", "undefined", "film"} {
		rec := callMyCalendar(t, q, signer, userID, "segment="+seg)
		require.Equal(t, http.StatusOK, rec.Code, "segment=%s must be accepted", seg)
	}
}

// A typo must fail loudly. Passing it through would return only the NULL-segment
// events, which looks like a working filter with few results.
func TestGetMyCalendar_UnknownSegmentIs400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	for _, bad := range []string{"musci", "Music", "esports", "'; drop table events;--"} {
		rec := callMyCalendar(t, q, signer, userID, "segment="+url.QueryEscape(bad))
		require.Equal(t, http.StatusBadRequest, rec.Code, "segment=%q must be rejected", bad)
		require.Contains(t, rec.Body.String(), "bad_segment")
	}
}

// An empty value is the same as not filtering — a client clearing its dropdown
// should not have to drop the parameter.
func TestGetMyCalendar_EmptySegmentParamIsNoFilter(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedSegmentedEvents(t, q, ctx, userID, segmentFixture())

	got := titlesOf(t, callMyCalendar(t, q, signer, userID, "segment="))
	require.Subset(t, got, []string{"Seg Music", "Seg Sports", "Seg Unlabeled"})
}

func TestGetMyCalendar_ResponseCarriesSegment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedSegmentedEvents(t, q, ctx, userID, segmentFixture())

	rec := callMyCalendar(t, q, signer, userID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var raw struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	seen := map[string]any{}
	for _, e := range raw.Events {
		seen[e["title"].(string)] = e["segment"]
	}
	require.Equal(t, "sports", seen["Seg Sports"])
	// omitempty: an unclassified event carries no segment key at all.
	require.Nil(t, seen["Seg Unlabeled"])
}

func TestGetCityCalendar_SegmentFilterReturnsMatchingPlusNullSegments(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	seedSegmentedEvents(t, q, ctx, userID, segmentFixture())

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	target := "/calendar/" + uuidFromPgCal(city.ID).String() + "?segment=music"
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := titlesOf(t, rec)
	require.Contains(t, got, "Seg Music")
	require.Contains(t, got, "Seg Unlabeled")
	require.NotContains(t, got, "Seg Sports")
}

func TestGetCityCalendar_UnknownSegmentIs400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	target := "/calendar/" + uuidFromPgCal(city.ID).String() + "?segment=musci"
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_segment")
}

// The detail endpoint returns the same calendarEvent shape as the list ones, so
// it must carry segment too — a client that filters a list by segment and then
// opens one event should not find the field gone.
func TestGetEventByID_CarriesSegment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Detail Segment Hall", NormalizedName: "detail segment hall",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "detail-segment-1",
		Title:         "Detail Segment Show",
		Description:   "seeded",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(54 * time.Hour), Valid: true},
		VenueID:       venueID,
		Segment:       ptrTo("arts-theatre"),
	})
	require.NoError(t, err)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	r := chi.NewRouter()
	r.With(middleware.RequireAuth(signer)).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	req := httptest.NewRequest(http.MethodGet, "/events/"+uuidFromPgCal(eventID).String(), nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Equal(t, "arts-theatre", raw["segment"])
}

// ---- city calendar starts_at ----------------------------------------------

// callCityCalendar issues one GET /calendar/{cityId}?<query> as userID and
// hands back the raw recorder, so error-status tests can assert on it too.
// query is the bare query string, without the leading "?".
func callCityCalendar(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID, cityID pgtype.UUID, query string) *httptest.ResponseRecorder {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	target := "/calendar/" + uuidFromPgCal(cityID).String()
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	return rec
}

// web/src/api/calendar.ts sends starts_at to this endpoint on every uncursored
// page, exactly as it does for /me/calendar. Ignoring it made the city feed
// start from whatever was still showable rather than from the day the client
// asked for.
func TestGetCityCalendar_StartsAtReturnsOnlyStrictlyLaterEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	base := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	seedBoundEvents(t, q, ctx, userID, base, 4)

	// Bound 01's own instant, which must be excluded by > rather than >=.
	boundary := base.Add(time.Hour).Format(time.RFC3339)
	rec := callCityCalendar(t, q, signer, userID, city.ID, "starts_at="+url.QueryEscape(boundary))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"Bound 02", "Bound 03", "PB Live"}, titlesOf(t, rec))
}

// The bound applies to the first page only; the cursor carries the rest, so a
// client that began with starts_at pages on with the cursor alone.
func TestGetCityCalendar_StartsAtFirstPageReturnsCursorForTheRest(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	base := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	seedBoundEvents(t, q, ctx, userID, base, 25)

	// Excludes Bound 00 only: 24 Bound events + the fixture's PB Live = 25 left.
	rec := callCityCalendar(t, q, signer, userID, city.ID,
		"starts_at="+url.QueryEscape(base.Format(time.RFC3339)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var first pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&first))
	require.Len(t, first.Events, 20)
	require.Equal(t, "Bound 01", first.Events[0].Title)
	require.NotEmpty(t, first.NextCursor)

	second := getCityCalendarPage(t, q, signer, userID, city.ID, first.NextCursor)
	require.Len(t, second.Events, 5)
	require.Empty(t, second.NextCursor)
}

// Same contradiction, same refusal as /me/calendar: the cursor's position and
// the caller's bound can disagree, and honouring either silently returns a page
// the client did not ask for.
func TestGetCityCalendar_CursorAndStartsAtTogether_Returns422(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	seedBoundEvents(t, q, ctx, userID, time.Now().Add(24*time.Hour).UTC().Truncate(time.Second), 25)

	cursor := getCityCalendarPage(t, q, signer, userID, city.ID, "").NextCursor
	require.NotEmpty(t, cursor)

	startsAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec := callCityCalendar(t, q, signer, userID, city.ID,
		"cursor="+url.QueryEscape(cursor)+"&starts_at="+url.QueryEscape(startsAt))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "cursor_and_starts_at")
}

func TestGetCityCalendar_BadStartsAt_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	rec := callCityCalendar(t, q, signer, userID, city.ID, "starts_at=not-a-timestamp")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_starts_at")
}
