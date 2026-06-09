package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

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
	h := ingest.NewInterestHandler(q)
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

	h := ingest.NewInterestHandler(q)
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
