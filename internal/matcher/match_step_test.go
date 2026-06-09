package matcher_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestMatchStep_TrackArtistMatchesPerformer(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "trackartist@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	// Only a track artist — no top-artist row for this name. It should still
	// match an event performer (track artists are folded into the artist set).
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_track_artist",
		Value: "boygenius", NormalizedValue: "boygenius", Weight: 1.0,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V3", NormalizedName: "v3",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "match-tm-track-1",
		Title:         "boygenius Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "boygenius", NormalizedName: "boygenius",
	}))
	// Matching embeddings push the pair over the threshold; the assertion below
	// is about the performer match coming from the track artist, not the score.
	vec := make([]float32, 384)
	vec[0] = 1.0
	pv := pgvector.NewVector(vec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID: userRow.ID, InterestEmbedding: &pv,
	}))
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID: eventID, Embedding: &pv,
	}))

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	row := pool.QueryRow(ctx,
		`SELECT score_breakdown FROM user_event_match WHERE user_id = $1 AND event_id = $2`,
		userRow.ID, eventID)
	var breakdown []byte
	require.NoError(t, row.Scan(&breakdown))
	var bd struct {
		MatchedPerformers []string `json:"matched_performers"`
	}
	require.NoError(t, json.Unmarshal(breakdown, &bd))
	require.Contains(t, bd.MatchedPerformers, "boygenius")
}

func TestMatchStep_SavedSongArtistMatchesPerformer(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "savedartist@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	// Only a saved-song artist — no top-artist row for this name. It should still
	// match an event performer (saved-song artists are folded into the artist set).
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_saved_song_artist",
		Value: "Lucy Dacus", NormalizedValue: "lucy dacus", Weight: 1.0,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V4", NormalizedName: "v4",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "match-tm-saved-1",
		Title:         "Lucy Dacus Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Lucy Dacus", NormalizedName: "lucy dacus",
	}))
	vec := make([]float32, 384)
	vec[0] = 1.0
	pv := pgvector.NewVector(vec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID: userRow.ID, InterestEmbedding: &pv,
	}))
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID: eventID, Embedding: &pv,
	}))

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	row := pool.QueryRow(ctx,
		`SELECT score_breakdown FROM user_event_match WHERE user_id = $1 AND event_id = $2`,
		userRow.ID, eventID)
	var breakdown []byte
	require.NoError(t, row.Scan(&breakdown))
	var bd struct {
		MatchedPerformers []string `json:"matched_performers"`
	}
	require.NoError(t, json.Unmarshal(breakdown, &bd))
	require.Contains(t, bd.MatchedPerformers, "Lucy Dacus")
}

func matchCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID, eventID pgtype.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1 AND event_id = $2",
		userID, eventID).Scan(&n))
	return n
}

func TestMatchStep_PrunesDroppedMatch(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "prune@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	// Two artist interests: one drives event A, the other event B.
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Radiohead", NormalizedValue: "radiohead", Weight: 1.0,
	}))
	// User embedding e_u = [1,0,0,...].
	userVec := make([]float32, 384)
	userVec[0] = 1.0
	uv := pgvector.NewVector(userVec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID: userRow.ID, InterestEmbedding: &uv,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	// Event embedding e_ev = [0,1,0,...] is orthogonal to the user → embedScore
	// 0.5 → 0.4*0.5 = 0.2. With Defaults() an artist match adds 0.6*(1.0/3.0) =
	// 0.2 → total 0.4 > 0.3 threshold; without the artist match only 0.2 < 0.3.
	eventVec := make([]float32, 384)
	eventVec[1] = 1.0
	ev := pgvector.NewVector(eventVec)

	eventA, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "prune-a", Title: "PB Live",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventA, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{ID: eventA, Embedding: &ev}))

	eventB, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "prune-b", Title: "Radiohead Live",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventB, PerformerName: "Radiohead", NormalizedName: "radiohead",
	}))
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{ID: eventB, Embedding: &ev}))

	step := matcher.NewMatchStep(q, matcher.Defaults())

	// Run 1: both events match (artist + embedding).
	require.NoError(t, step.Run(ctx))
	require.Equal(t, 1, matchCount(t, pool, ctx, userRow.ID, eventA))
	require.Equal(t, 1, matchCount(t, pool, ctx, userRow.ID, eventB))

	// User drops "Phoebe Bridgers" → event A loses its artist match and falls
	// below threshold; "Radiohead" (event B) stays above.
	_, err := pool.Exec(ctx,
		"DELETE FROM user_interests WHERE user_id = $1 AND normalized_value = $2",
		userRow.ID, "phoebe bridgers")
	require.NoError(t, err)

	// Run 2: event A is pruned; event B remains.
	require.NoError(t, step.Run(ctx))
	require.Equal(t, 0, matchCount(t, pool, ctx, userRow.ID, eventA), "dropped match should be pruned")
	require.Equal(t, 1, matchCount(t, pool, ctx, userRow.ID, eventB), "still-matching event should remain")
}

func TestMatchStep_NoActiveUsers_DoesNotPrune(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "noprune@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "VN", NormalizedName: "vn",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "noprune-1", Title: "Future Show",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	// Pre-seed a match with an old computed_at.
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID: userRow.ID, EventID: eventID, Score: 0.9,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour).UTC(), Valid: true},
	}))
	// Soft-delete the user so the run loads zero active users.
	_, err := pool.Exec(ctx, "UPDATE users SET deleted_at = NOW() WHERE id = $1", userRow.ID)
	require.NoError(t, err)

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	// Zero users loaded → stale-prune skipped → the seeded match survives (the
	// event is still upcoming, so DeleteObsoleteMatches leaves it too).
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 1, n)
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

func TestMatchStep_PerUserThresholdExcludesPair(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userRow, eventID := seedMatchablePair(t, q, "peruser-th@example.com", "th-evt-1")

	// The pair scores ~0.6 (string 0.6*0.333 + embedding 0.4*1.0). A threshold
	// of 0.7 must exclude it; the check is `score <= threshold`.
	th := 0.7
	require.NoError(t, q.UpdateUserScoreThreshold(ctx, store.UpdateUserScoreThresholdParams{
		ID: userRow.ID, ScoreThreshold: &th,
	}))

	require.NoError(t, matcher.NewMatchStep(q, matcher.Defaults()).Run(ctx))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_event_match WHERE user_id=$1 AND event_id=$2`,
		userRow.ID, eventID).Scan(&n))
	require.Equal(t, 0, n, "pair below the user's threshold must not be written")
}

func TestMatchStep_SingleUserFilterScopesToOneUser(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userA, eventID := seedMatchablePair(t, q, "scopeA@example.com", "scope-evt-1")
	userB := addMatchingUser(t, q, "scopeB@example.com", eventID)

	require.NoError(t, matcher.NewMatchStepForUser(q, matcher.Defaults(), userA.ID).Run(ctx))

	var nA, nB int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_event_match WHERE user_id=$1`, userA.ID).Scan(&nA))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_event_match WHERE user_id=$1`, userB.ID).Scan(&nB))
	require.Equal(t, 1, nA, "scoped user A should be matched")
	require.Equal(t, 0, nB, "user B must be untouched by a single-user run")
}

// seedMatchablePair creates a user (Spotify top-artist "Phoebe Bridgers" +
// unit embedding) and an upcoming event with the same performer + identical
// embedding, so Score() returns ~0.6. Returns the user row and event id.
func seedMatchablePair(t *testing.T, q *store.Queries, email, srcEventID string) (store.CreateUserRow, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: email, PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))
	uvVec := make([]float32, 384)
	uvVec[0] = 1.0
	uv := pgvector.NewVector(uvVec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID: userRow.ID, InterestEmbedding: &uv,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: srcEventID,
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	evVec := make([]float32, 384)
	evVec[0] = 1.0
	ev := pgvector.NewVector(evVec)
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID: eventID, Embedding: &ev,
	}))
	return userRow, eventID
}

// addMatchingUser creates a second user that also matches the given event's
// performer, so a global run would write a match for them.
func addMatchingUser(t *testing.T, q *store.Queries, email string, eventID pgtype.UUID) store.CreateUserRow {
	t.Helper()
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: email, PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))
	uvVec := make([]float32, 384)
	uvVec[0] = 1.0
	uv := pgvector.NewVector(uvVec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID: userRow.ID, InterestEmbedding: &uv,
	}))
	return userRow
}
