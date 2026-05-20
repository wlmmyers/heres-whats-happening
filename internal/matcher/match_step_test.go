package matcher_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestMatchStep_WritesAboveThresholdRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "matchstep@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))
	userVec := make([]float32, 384)
	userVec[0] = 1.0
	uv := pgvector.NewVector(userVec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID:                userRow.ID,
		InterestEmbedding: &uv,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "match-tm-1",
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	eventVec := make([]float32, 384)
	eventVec[0] = 1.0
	ev := pgvector.NewVector(eventVec)
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID:        eventID,
		Embedding: &ev,
	}))

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	row := pool.QueryRow(ctx,
		`SELECT score, score_breakdown FROM user_event_match WHERE user_id = $1 AND event_id = $2`,
		userRow.ID, eventID)
	var score float64
	var breakdown []byte
	require.NoError(t, row.Scan(&score, &breakdown))
	require.Greater(t, score, 0.3)

	var bd map[string]any
	require.NoError(t, json.Unmarshal(breakdown, &bd))
	require.Contains(t, bd, "string_score")
	require.Contains(t, bd, "embedding_score")
	require.Contains(t, bd, "matched_performers")
}

func TestMatchStep_BelowThresholdSkipped(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "belowthresh@example.com", PasswordHash: "stub", CityID: city.ID,
	})

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V2", NormalizedName: "v2",
	})
	_, _ = q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "match-tm-2",
		Title:         "Unrelated",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 0, n)
}
