package matcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScore_ArtistAndGenreMatch(t *testing.T) {
	user := UserProfile{
		SpotifyArtists: []NormalizedInterest{
			{Value: "Phoebe Bridgers", Normalized: "phoebe bridgers", Weight: 1.0},
		},
		SpotifyGenres: []NormalizedInterest{
			{Value: "indie", Normalized: "indie", Weight: 0.8},
		},
	}
	event := EventProfile{
		Performers: []EventPerformer{{Display: "Phoebe Bridgers", Normalized: "phoebe bridgers"}},
		Genres:     []string{"indie", "rock"},
	}
	cfg := Defaults()
	got := Score(user, event, cfg)

	// artist: 1.0 * 1.0 = 1.0; genre: 0.8 * 0.3 = 0.24; raw 1.24/3.0 ≈ 0.413
	// embed = 0; total = 0.6*0.413 + 0.4*0 ≈ 0.248
	require.InDelta(t, 0.413, got.StringScore, 0.01)
	require.Equal(t, 0.0, got.EmbeddingScore)
	require.InDelta(t, 0.248, got.TotalScore, 0.01)
	require.Equal(t, []string{"Phoebe Bridgers"}, got.MatchedPerformers)
	require.Equal(t, []string{"indie"}, got.MatchedGenres)
}

func TestScore_StringMaxClamp(t *testing.T) {
	user := UserProfile{
		SpotifyArtists: []NormalizedInterest{
			{Value: "A", Normalized: "a", Weight: 1.0},
			{Value: "B", Normalized: "b", Weight: 1.0},
			{Value: "C", Normalized: "c", Weight: 1.0},
			{Value: "D", Normalized: "d", Weight: 1.0},
		},
	}
	event := EventProfile{
		Performers: []EventPerformer{
			{Display: "A", Normalized: "a"},
			{Display: "B", Normalized: "b"},
			{Display: "C", Normalized: "c"},
			{Display: "D", Normalized: "d"},
		},
	}
	cfg := Defaults()
	got := Score(user, event, cfg)
	require.Equal(t, 1.0, got.StringScore)
}

func TestScore_EmbeddingOnly(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	user := UserProfile{Embedding: a}
	event := EventProfile{Embedding: b}
	cfg := Defaults()
	got := Score(user, event, cfg)
	require.Equal(t, 0.0, got.StringScore)
	require.InDelta(t, 1.0, got.EmbeddingScore, 0.001)
	require.InDelta(t, 0.4, got.TotalScore, 0.001)
}

func TestScore_NoMatchAtAll(t *testing.T) {
	user := UserProfile{
		SpotifyArtists: []NormalizedInterest{{Value: "X", Normalized: "x", Weight: 1.0}},
	}
	event := EventProfile{
		Performers: []EventPerformer{{Display: "Y", Normalized: "y"}},
	}
	cfg := Defaults()
	got := Score(user, event, cfg)
	require.Equal(t, 0.0, got.StringScore)
	require.Equal(t, 0.0, got.EmbeddingScore)
	require.Equal(t, 0.0, got.TotalScore)
	require.Empty(t, got.MatchedPerformers)
}

func TestScore_ManualTagMatchesGenre(t *testing.T) {
	user := UserProfile{
		ManualTags: []NormalizedInterest{
			{Value: "jazz", Normalized: "jazz", Weight: 1.0},
		},
	}
	event := EventProfile{Genres: []string{"jazz"}}
	cfg := Defaults()
	got := Score(user, event, cfg)
	require.InDelta(t, 0.1, got.StringScore, 0.01)
	require.Equal(t, []string{"jazz"}, got.MatchedGenres)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	require.InDelta(t, 0.0, cosineSimilarity([]float32{1, 0}, []float32{0, 1}), 0.001)
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	require.InDelta(t, -1.0, cosineSimilarity([]float32{1, 0}, []float32{-1, 0}), 0.001)
}
