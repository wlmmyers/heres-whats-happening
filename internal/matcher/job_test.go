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

func TestJob_FullRun_EmbedsScoresAndArchives(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "job-full@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Bowl", NormalizedName: "bowl",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "job-1",
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))

	fakeVec := make([]float32, 384)
	fakeVec[0] = 1.0
	emb := &fakeEmbedder{vec: fakeVec}

	job := matcher.NewJob(q, emb, matcher.Defaults())
	require.NoError(t, job.Run(ctx))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1 AND event_id = $2",
		userRow.ID, eventID).Scan(&n))
	require.Equal(t, 1, n)
}
