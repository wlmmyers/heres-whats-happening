package matcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestEmbedUsers_EmbedsUsersWithChangedInterests(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "user-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_artist",
		Value:           "Phoebe Bridgers",
		NormalizedValue: "phoebe bridgers",
		Weight:          1.0,
	}))
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_genre",
		Value:           "indie",
		NormalizedValue: "indie",
		Weight:          0.9,
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.2
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewUserEmbedder(q, emb)
	require.NoError(t, step.Run(ctx))

	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
	require.Contains(t, emb.calls[0][0], "indie")

	// Second run should not re-embed.
	require.NoError(t, step.Run(ctx))
	require.Len(t, emb.calls, 1) // still 1
}

func TestEmbedUsers_IncludesTrackArtists(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "track-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_artist",
		Value:           "Phoebe Bridgers",
		NormalizedValue: "phoebe bridgers",
		Weight:          1.0,
	}))
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_track_artist",
		Value:           "boygenius",
		NormalizedValue: "boygenius",
		Weight:          0.8,
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.2
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewUserEmbedder(q, emb)
	require.NoError(t, step.Run(ctx))

	require.Len(t, emb.calls, 1)
	// Both the top artist and the track artist appear in the "Top artists" text.
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
	require.Contains(t, emb.calls[0][0], "boygenius")
}

func TestEmbedUsers_IncludesSavedSongArtists(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "saved-embed@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	// Only a saved-song artist — no top-artist row. It should still surface in
	// the "Top artists" text (saved-song artists are folded into the artist set).
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_saved_song_artist",
		Value:           "Lucy Dacus",
		NormalizedValue: "lucy dacus",
		Weight:          1.0,
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.2
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewUserEmbedder(q, emb)
	require.NoError(t, step.Run(ctx))

	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Lucy Dacus")
}

func TestEmbedUser_EmbedsSingleUserWithSpotifyOnly(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "embed-one@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)
	// Spotify interest only — no manual tag. Must still embed.
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID:          userRow.ID,
		Kind:            "spotify_top_artist",
		Value:           "Phoebe Bridgers",
		NormalizedValue: "phoebe bridgers",
		Weight:          1.0,
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.2
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewUserEmbedder(q, emb)

	require.NoError(t, step.EmbedUser(ctx, userRow.ID))
	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
}

func TestEmbedUser_SkipsUserWithNoInterests(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "embed-none@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	emb := &fakeEmbedder{vec: make([]float32, 384)}
	step := matcher.NewUserEmbedder(q, emb)

	require.NoError(t, step.EmbedUser(ctx, userRow.ID))
	require.Len(t, emb.calls, 0) // empty text → no embed call
}
