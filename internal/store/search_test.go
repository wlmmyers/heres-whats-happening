package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// seedSearchFixture builds a small catalogue with the properties each test
// below probes: an accented title, a venue whose name is also a word, and one
// event that is past so the showable filter has something to exclude.
func seedSearchFixture(t *testing.T, q *store.Queries, ctx context.Context) pgtype.UUID {
	t.Helper()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)

	mkVenue := func(name, norm string) pgtype.UUID {
		id, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
			CityID: city.ID, Name: name, NormalizedName: norm,
		})
		require.NoError(t, err)
		return id
	}
	showbox := mkVenue("The Showbox", "the showbox")
	bowl := mkVenue("The Bowl", "the bowl")

	mkEvent := func(srcID, title string, venue pgtype.UUID, startsAt time.Time) pgtype.UUID {
		id, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: srcID,
			Title:         title,
			StartsAt:      pgtype.Timestamptz{Time: startsAt, Valid: true},
			VenueID:       venue,
		})
		require.NoError(t, err)
		return id
	}
	future := time.Now().Add(48 * time.Hour)
	mkEvent("s-accent", "Edén Muñoz: Como En Los Viejos Tiempos Tour", bowl, future)
	mkEvent("s-orchard", "Midnight Orchard", bowl, future)
	mkEvent("s-at-showbox", "Some Other Band", showbox, future)
	mkEvent("s-titled-showbox", "Showbox Allstars", bowl, future)
	mkEvent("s-past", "Midnight Orchard Farewell", bowl, time.Now().Add(-72*time.Hour))
	return city.ID
}

func search(t *testing.T, q *store.Queries, ctx context.Context, cityID pgtype.UUID, term string) []store.SearchEventsRow {
	t.Helper()
	rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
		Query: term, CityID: cityID, ResultLimit: 10,
	})
	require.NoError(t, err)
	return rows
}

func titles(rows []store.SearchEventsRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

// The regression test for the whole immutable_unaccent design.
func TestSearchEvents_FindsAccentedTitleWithoutAccentKeys(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "eden munoz")),
		"Edén Muñoz: Como En Los Viejos Tiempos Tour")
}

// Threshold 0.2, not the 0.3 default -- at 0.3 this returns nothing.
func TestSearchEvents_ToleratesOneCharacterTypo(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "midnite orchard")), "Midnight Orchard")
}

// Trigram similarity is word-order independent; plain ILIKE is not.
func TestSearchEvents_IsWordOrderIndependent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "orchard midnight")), "Midnight Orchard")
}

// The 0.6 venue weight exists for exactly this: a venue match must never
// outrank an event whose TITLE carries the term.
func TestSearchEvents_TitleMatchOutranksVenueMatch(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	rows := search(t, q, ctx, cityID, "showbox")
	require.NotEmpty(t, rows)
	require.Equal(t, "Showbox Allstars", rows[0].Title)
	require.Contains(t, titles(rows), "Some Other Band", "venue match still surfaces, just lower")
}

func TestSearchEvents_ExcludesPastEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.NotContains(t, titles(search(t, q, ctx, cityID, "midnight orchard")),
		"Midnight Orchard Farewell")
}

func TestSearchEvents_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	_, err := pool.Exec(ctx,
		`UPDATE events SET archived_at = NOW() WHERE title = 'Midnight Orchard'`)
	require.NoError(t, err)

	require.NotContains(t, titles(search(t, q, ctx, cityID, "midnight orchard")), "Midnight Orchard")
}

func TestSearchEvents_ScopesToCity(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	seedSearchFixture(t, q, ctx)

	otherCity := pgtype.UUID{Bytes: [16]byte{9, 9, 9, 9}, Valid: true}
	require.Empty(t, search(t, q, ctx, otherCity, "midnight orchard"))
}

func TestSearchEvents_RespectsResultLimit(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
		Query: "midnight orchard", CityID: cityID, ResultLimit: 1,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// Order must be total, or identical requests reorder and the dropdown flickers
// between keystrokes.
func TestSearchEvents_OrderIsStableAcrossIdenticalCalls(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	first := titles(search(t, q, ctx, cityID, "showbox"))
	for i := 0; i < 5; i++ {
		require.Equal(t, first, titles(search(t, q, ctx, cityID, "showbox")))
	}
}
