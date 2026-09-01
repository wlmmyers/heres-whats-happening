package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// goingEventRow mirrors the calendarEvent fields the going sidebar renders.
type goingEventRow struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	StartsAt string  `json:"starts_at"`
	Score    float64 `json:"score"`
	Venue    struct {
		Name string `json:"name"`
	} `json:"venue"`
	MatchedBecause struct {
		Performers []string `json:"performers"`
		Genres     []string `json:"genres"`
	} `json:"matched_because"`
}

// seedGoingEvent adds one event at the given offset from now and, when going is
// true, marks userID as going to it. Returns the event id.
func seedGoingEvent(t *testing.T, q *store.Queries, ctx context.Context, userID pgtype.UUID, sourceEventID, title string, offset time.Duration, going bool) pgtype.UUID {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Going Hall", NormalizedName: "going hall",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: sourceEventID,
		Title:         title,
		Description:   "seeded",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(offset), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	if going {
		require.NoError(t, q.AddGoing(ctx, store.AddGoingParams{UserID: userID, EventID: eventID}))
	}
	return eventID
}

func listGoingEvents(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID pgtype.UUID) []goingEventRow {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/event-going/events", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.ListGoingEvents(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out []goingEventRow
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	return out
}

// The sidebar renders title, venue and match score straight from this payload,
// so it has to carry the same shape the calendar endpoints emit -- not the bare
// ids GET /me/event-going returns.
func TestListGoingEvents_ReturnsFullEventRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)
	require.NoError(t, q.AddGoing(ctx, store.AddGoingParams{UserID: userID, EventID: eventID}))

	rows := listGoingEvents(t, q, signer, userID)

	require.Len(t, rows, 1)
	require.Equal(t, uuidFromPgCal(eventID).String(), rows[0].ID)
	require.Equal(t, "PB Live", rows[0].Title)
	require.Equal(t, "The Bowl", rows[0].Venue.Name)
	require.InDelta(t, 0.82, rows[0].Score, 0.01)
	require.Equal(t, []string{"Phoebe Bridgers"}, rows[0].MatchedBecause.Performers)
	require.NotEmpty(t, rows[0].StartsAt)
}

func TestListGoingEvents_ExcludesEventsNotMarkedGoing(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)
	require.NoError(t, q.AddGoing(ctx, store.AddGoingParams{UserID: userID, EventID: eventID}))
	seedGoingEvent(t, q, ctx, userID, "going-skip", "Not Going To This", 72*time.Hour, false)

	rows := listGoingEvents(t, q, signer, userID)

	require.Len(t, rows, 1)
	require.Equal(t, "PB Live", rows[0].Title)
}

// No time filter: a show the user already attended stays in the payload, which
// is what lets the client split the list into "Going" and "Went" without a
// second endpoint.
func TestListGoingEvents_IncludesPastEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedGoingEvent(t, q, ctx, userID, "going-past", "Last Month's Show", -30*24*time.Hour, true)

	rows := listGoingEvents(t, q, signer, userID)

	require.Len(t, rows, 1)
	require.Equal(t, "Last Month's Show", rows[0].Title)
}

// Ordered oldest-first so the client can bucket by date without re-sorting.
func TestListGoingEvents_OrderedByStartsAtAscending(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedGoingEvent(t, q, ctx, userID, "going-late", "Later Show", 96*time.Hour, true)
	seedGoingEvent(t, q, ctx, userID, "going-early", "Earlier Show", -24*time.Hour, true)

	rows := listGoingEvents(t, q, signer, userID)

	require.Len(t, rows, 2)
	require.Equal(t, "Earlier Show", rows[0].Title)
	require.Equal(t, "Later Show", rows[1].Title)
}

// An event with no user_event_match row still belongs in the list -- the user
// can mark going from the all-city calendar, which matches nothing.
func TestListGoingEvents_UnmatchedEventScoresZero(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedGoingEvent(t, q, ctx, userID, "going-unmatched", "City Show", 12*time.Hour, true)

	rows := listGoingEvents(t, q, signer, userID)

	require.Len(t, rows, 1)
	require.Equal(t, "City Show", rows[0].Title)
	require.Zero(t, rows[0].Score)
	require.Empty(t, rows[0].MatchedBecause.Performers)
}

// json.Marshal turns a nil slice into null, which the client would have to
// guard on every render; an empty going list must serialise as [].
func TestListGoingEvents_EmptyListSerialisesAsEmptyArray(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/event-going/events", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.ListGoingEvents(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[]`, rec.Body.String())
}

func TestListGoingEvents_Unauthenticated(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)

	req := httptest.NewRequest(http.MethodGet, "/me/event-going/events", nil)
	rec := httptest.NewRecorder()
	handlers.ListGoingEvents(q).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
