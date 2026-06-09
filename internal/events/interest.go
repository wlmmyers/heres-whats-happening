package events

import "time"

// InterestMessage carries one user's snapshot of Spotify-derived interests
// from the spotify-scraper to the ingest consumer. Manual tags do not flow
// through this message; they're written directly by the API.
type InterestMessage struct {
	UserID                 string           `json:"user_id"`
	SpotifyTopArtists      []SpotifyTopItem `json:"spotify_top_artists,omitempty"`
	SpotifyTopTrackArtists []SpotifyTopItem `json:"spotify_top_track_artists,omitempty"`
	// SpotifySavedSongArtists are the artists behind the user's saved tracks
	// ("/me/tracks"), ranked by how recently each was saved (added_at), deduped
	// by name, and capped at 200.
	SpotifySavedSongArtists []SpotifyTopItem `json:"spotify_saved_song_artists,omitempty"`
	SpotifyTopGenres        []SpotifyTopItem `json:"spotify_top_genres,omitempty"`
	FetchedAt              time.Time        `json:"fetched_at"`
}

// SpotifyTopItem represents a ranked Spotify entity (artist name or genre tag)
// where Rank starts at 1 for the most-listened.
type SpotifyTopItem struct {
	Name string `json:"name"`
	Rank int    `json:"rank"`
}
