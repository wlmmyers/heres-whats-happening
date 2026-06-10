package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type fakeEmbedder struct {
	calls [][]string
	vec   []float32
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	f.calls = append(f.calls, inputs)
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}

func newFakeEmbedder() *fakeEmbedder {
	v := make([]float32, 384)
	for i := range v {
		v[i] = 0.1
	}
	return &fakeEmbedder{vec: v}
}

func pgtypeUUIDToString(t *testing.T, u pgtype.UUID) string {
	t.Helper()
	return uuid.UUID(u.Bytes).String()
}

func TestInterestHandler_WritesSpotifyArtistsAndGenres(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "interest-handler@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	msg := events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
			{Name: "MUNA", Rank: 2},
		},
		SpotifyTopGenres: []events.SpotifyTopItem{
			{Name: "indie rock", Rank: 1},
			{Name: "indie pop", Rank: 2},
		},
		SpotifySavedSongArtists: []events.SpotifyTopItem{
			{Name: "Lucy Dacus", Rank: 1},
			{Name: "Julien Baker", Rank: 2},
			{Name: "boygenius", Rank: 3},
		},
		FetchedAt: time.Now(),
	}
	body, _ := json.Marshal(&msg)
	h := ingest.NewInterestHandler(q, nil)
	require.NoError(t, h.Handle(ctx, body))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	genres, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_genre",
	})
	require.NoError(t, err)
	require.Len(t, genres, 2)

	saved, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_saved_song_artist",
	})
	require.NoError(t, err)
	require.Len(t, saved, 3)
}

func TestInterestHandler_ReplaceSemantics(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "replace@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	h := ingest.NewInterestHandler(q, nil)
	first := events.InterestMessage{
		UserID:            pgtypeUUIDToString(t, userRow.ID),
		SpotifyTopArtists: []events.SpotifyTopItem{{Name: "A", Rank: 1}, {Name: "B", Rank: 2}},
		FetchedAt:         time.Now(),
	}
	body1, _ := json.Marshal(&first)
	require.NoError(t, h.Handle(ctx, body1))

	second := events.InterestMessage{
		UserID:            pgtypeUUIDToString(t, userRow.ID),
		SpotifyTopArtists: []events.SpotifyTopItem{{Name: "C", Rank: 1}},
		FetchedAt:         time.Now(),
	}
	body2, _ := json.Marshal(&second)
	require.NoError(t, h.Handle(ctx, body2))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "C", rows[0].Value)
}

func TestInterestHandler_OnlyEmbed_DoesNotTouchRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "only-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	// A manual tag the handler must NOT delete.
	_, err = q.CreateManualInterest(ctx, store.CreateManualInterestParams{
		UserID:          userRow.ID,
		Value:           "Indie Rock",
		NormalizedValue: "indie rock",
	})
	require.NoError(t, err)

	emb := newFakeEmbedder()
	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpOnlyEmbed,
	})
	h := ingest.NewInterestHandler(q, emb)
	require.NoError(t, h.Handle(ctx, body))

	// Embedded once.
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Indie Rock")
	// Manual tag still present.
	tags, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "manual_tag",
	})
	require.NoError(t, err)
	require.Len(t, tags, 1)
}

func TestInterestHandler_Replace_AlsoEmbeds(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "replace-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	emb := newFakeEmbedder()
	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, emb)
	require.NoError(t, h.Handle(ctx, body))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
}

func TestInterestHandler_NilEmbedder_StillReplaces(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "nil-emb@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "MUNA", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, nil)
	require.NoError(t, h.Handle(ctx, body))

	rows, err := q.ListInterestsByUserAndKind(ctx, store.ListInterestsByUserAndKindParams{
		UserID: userRow.ID,
		Kind:   "spotify_top_artist",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestInterestHandler_ReplaceEmbedAndMatch_WritesMatchRow(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "embed-match@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "embed-match-1",
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	eventVec := make([]float32, 384)
	for i := range eventVec {
		eventVec[i] = 0.1
	}
	ev := pgvector.NewVector(eventVec)
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID: eventID, Embedding: &ev,
	}))

	emb := newFakeEmbedder()
	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, emb)
	require.NoError(t, h.Handle(ctx, body))

	var score float64
	err = pool.QueryRow(ctx,
		`SELECT score FROM user_event_match WHERE user_id = $1 AND event_id = $2`,
		userRow.ID, eventID).Scan(&score)
	require.NoError(t, err)
	require.Greater(t, score, 0.3)
}

func TestInterestHandler_NilEmbedder_WritesNoMatchRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "nil-emb-match@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V2", NormalizedName: "v2",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "nil-emb-match-1",
		Title:         "PB Live 2",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))

	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, nil)
	require.NoError(t, h.Handle(ctx, body))

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_event_match WHERE user_id = $1`,
		userRow.ID).Scan(&count))
	require.Equal(t, 0, count)
}
