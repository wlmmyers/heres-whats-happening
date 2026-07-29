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

func TestGetCityCalendarInRange_ReturnsUnmatchedEventsAndExcludesOtherCities(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	away := otherCity(t, pool)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "city-1", "Home Show", time.Now().Add(48*time.Hour))
	seedCityEvent(t, q, ctx, away, "Away Hall", "city-2", "Away Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
		CityID:     home.ID,
		StartsAt:   pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		StartsAt_2: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Home Show", rows[0].Title)
	require.Equal(t, "The Bowl", rows[0].VenueName)
}

func TestGetCityCalendarInRange_ExcludesPastAndOutOfRangeEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "past-1", "Past Show", time.Now().Add(-72*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "far-1", "Far Show", time.Now().Add(90*24*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "soon-1", "Soon Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
		CityID:     home.ID,
		StartsAt:   pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		StartsAt_2: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Soon Show", rows[0].Title)
}

func TestGetCityCalendarInRange_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	live := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "live-1", "Live Show", time.Now().Add(48*time.Hour))
	gone := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "gone-1", "Archived Show", time.Now().Add(48*time.Hour))
	_, err = pool.Exec(ctx, `UPDATE events SET archived_at = NOW() WHERE id = $1`, gone)
	require.NoError(t, err)

	rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
		CityID:     home.ID,
		StartsAt:   pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		StartsAt_2: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Live Show", rows[0].Title)
	require.Equal(t, live, rows[0].EventID)
}
