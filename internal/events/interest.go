package events

import "time"

// InterestMessage carries one user's snapshot of Spotify-derived interests
// from the spotify-scraper to the ingest consumer. Manual tags do not flow
// through this message; they're written directly by the API.
type InterestMessage struct {
	UserID string `json:"user_id"`
	// Op selects how the consumer processes this message. Empty is treated as
	// OpReplaceInterestsAndEmbed for backward compatibility with messages in
	// flight at deploy time.
	Op                     string           `json:"op,omitempty"`
	SpotifyTopArtists      []SpotifyTopItem `json:"spotify_top_artists,omitempty"`
	SpotifyTopTrackArtists []SpotifyTopItem `json:"spotify_top_track_artists,omitempty"`
	// SpotifySavedSongArtists are the artists behind the user's saved tracks
	// ("/me/tracks"), ranked by how recently each was saved (added_at), deduped
	// by name, and capped at 200.
	SpotifySavedSongArtists []SpotifyTopItem `json:"spotify_saved_song_artists,omitempty"`
	SpotifyTopGenres        []SpotifyTopItem `json:"spotify_top_genres,omitempty"`
	FetchedAt               time.Time        `json:"fetched_at"`
}

// InterestMessage.Op values.
const (
	// OpReplaceInterestsAndEmbed replaces the user's Spotify-derived interest
	// rows from this message, then re-embeds the user. Published by the Spotify
	// scraper. Empty Op is treated identically.
	OpReplaceInterestsAndEmbed = "replace_interests_and_embed"
	// OpOnlyEmbed skips all row replacement and only re-embeds the user.
	// Published by the manual-interest API handlers, which write rows themselves.
	OpOnlyEmbed = "only_embed"
)

// SpotifyTopItem represents a ranked Spotify entity (artist name or genre tag)
// where Rank starts at 1 for the most-listened.
type SpotifyTopItem struct {
	Name string `json:"name"`
	Rank int    `json:"rank"`
}

// RankWeight maps a 1-based rank to an interest weight: rank 1 -> 1.0, ramping
// down linearly to 0.6 at rank 50 (the size of the Spotify top-artist and
// top-track lists), then holding at 0.6 for lower ranks. Shared by the ingest
// weighting of artists/track-artists and the scraper's genre aggregation.
func RankWeight(rank int) float64 {
	const (
		maxRank   = 50
		minWeight = 0.6
	)
	if rank <= 1 {
		return 1.0
	}
	w := 1.0 - float64(rank-1)*(1.0-minWeight)/(maxRank-1)
	if w < minWeight {
		return minWeight
	}
	return w
}
