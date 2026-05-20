package matcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestArchiver_MarksStaleEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "AV", NormalizedName: "av",
	})

	staleID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "arch-1", Title: "Stale",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	_, err := pool.Exec(ctx, `UPDATE events SET last_seen_at = NOW() - INTERVAL '10 days' WHERE id = $1`, staleID)
	require.NoError(t, err)

	freshID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "arch-2", Title: "Fresh",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})

	step := matcher.NewArchiver(q)
	require.NoError(t, step.Run(ctx))

	stale, err := q.GetEventByID(ctx, staleID)
	require.NoError(t, err)
	require.True(t, stale.ArchivedAt.Valid, "stale event should be archived")

	fresh, err := q.GetEventByID(ctx, freshID)
	require.NoError(t, err)
	require.False(t, fresh.ArchivedAt.Valid, "fresh event should not be archived")
}
