package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInterestMessage_JSONRoundTrip(t *testing.T) {
	m := InterestMessage{
		UserID: "11111111-1111-1111-1111-111111111111",
		SpotifyTopArtists: []SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
			{Name: "MUNA", Rank: 2},
		},
		SpotifyTopGenres: []SpotifyTopItem{
			{Name: "indie rock", Rank: 1},
		},
		FetchedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)

	var out InterestMessage
	require.NoError(t, json.Unmarshal(data, &out))
	require.Equal(t, m.UserID, out.UserID)
	require.Equal(t, m.SpotifyTopArtists, out.SpotifyTopArtists)
	require.Equal(t, m.SpotifyTopGenres, out.SpotifyTopGenres)
}

func TestInterestMessage_OmitsEmptyArrays(t *testing.T) {
	m := InterestMessage{UserID: "u", FetchedAt: time.Now()}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"spotify_top_artists"`)
	require.NotContains(t, string(data), `"spotify_top_genres"`)
}
