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
