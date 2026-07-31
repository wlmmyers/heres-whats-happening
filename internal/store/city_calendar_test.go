package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// otherCity returns a second city, distinct from the default one. cities is not
// truncated between tests, so this must be idempotent across the whole run.
func otherCity(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO cities (slug, name, timezone)
		VALUES ('test-other-city', 'Other City', 'America/Los_Angeles')
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`).Scan(&id)
	require.NoError(t, err)
	return id
}

// seedCityEvent inserts a venue in cityID and one event at it, with no
// user_event_match row of any kind.
func seedCityEvent(t *testing.T, q *store.Queries, ctx context.Context, cityID pgtype.UUID, venueName, sourceEventID, title string, startsAt time.Time) pgtype.UUID {
	t.Helper()
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: cityID, Name: venueName, NormalizedName: venueName,
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: sourceEventID,
		Title:         title,
		Description:   "seeded",
		StartsAt:      pgtype.Timestamptz{Time: startsAt, Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	return eventID
}

func TestGetCityCalendarPage_ReturnsUnmatchedEventsAndExcludesOtherCities(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	away := otherCity(t, pool)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "city-1", "Home Show", time.Now().Add(48*time.Hour))
	seedCityEvent(t, q, ctx, away, "Away Hall", "city-2", "Away Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 21,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Home Show", rows[0].Title)
	require.Equal(t, "The Bowl", rows[0].VenueName)
}

// Past events stay out. Far-future ones no longer do: dropping from/to removed
// the upper bound, so the feed runs forward indefinitely.
func TestGetCityCalendarPage_ExcludesPastEventsButNotFarFutureOnes(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "past-1", "Past Show", time.Now().Add(-72*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "far-1", "Far Show", time.Now().Add(90*24*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "soon-1", "Soon Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 21,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "Soon Show", rows[0].Title)
	require.Equal(t, "Far Show", rows[1].Title)
}

func TestGetCityCalendarPage_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	live := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "live-1", "Live Show", time.Now().Add(48*time.Hour))
	gone := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "gone-1", "Archived Show", time.Now().Add(48*time.Hour))
	_, err = pool.Exec(ctx, `UPDATE events SET archived_at = NOW() WHERE id = $1`, gone)
	require.NoError(t, err)

	rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 21,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Live Show", rows[0].Title)
	require.Equal(t, live, rows[0].EventID)
}

// LIMIT caps the page, and the cursor resumes exactly after the last row of the
// previous one.
func TestGetCityCalendarPage_LimitAndCursorSeek(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	base := time.Now().Add(24 * time.Hour)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "p-1", "First", base)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "p-2", "Second", base.Add(time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "p-3", "Third", base.Add(2*time.Hour))

	first, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "First", first[0].Title)
	require.Equal(t, "Second", first[1].Title)

	last := first[len(first)-1]
	second, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:         home.ID,
		CursorStartsAt: last.StartsAt,
		CursorEventID:  last.EventID,
		PageLimit:      2,
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "Third", second[0].Title)
}

// The tiebreaker that justifies the composite cursor: three events sharing one
// starts_at must paginate without dropping or repeating any of them.
func TestGetCityCalendarPage_TiedStartsAtPaginateCleanly(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	tied := time.Now().Add(24 * time.Hour)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "tie-1", "Tie A", tied)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "tie-2", "Tie B", tied)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "tie-3", "Tie C", tied)

	seen := []string{}
	params := store.GetCityCalendarPageParams{CityID: home.ID, PageLimit: 2}
	for {
		rows, err := q.GetCityCalendarPage(ctx, params)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			seen = append(seen, r.Title)
		}
		last := rows[len(rows)-1]
		params.CursorStartsAt = last.StartsAt
		params.CursorEventID = last.EventID
	}

	require.ElementsMatch(t, []string{"Tie A", "Tie B", "Tie C"}, seen)
	require.Len(t, seen, 3, "no event may appear twice")
}
