// Package matcher implements the nightly match-job: embed events and users,
// score (user, event) pairs, upsert matches above threshold, archive stale
// events.
package matcher

import (
	"github.com/jackc/pgx/v5/pgtype"
)

// Config holds the tunable weights/factors read from the match_config table.
type Config struct {
	WString        float64
	WEmbedding     float64
	ScoreThreshold float64
	ArtistFactor   float64
	GenreFactor    float64
	StringMax      float64
}

// Defaults returns the v1 defaults (matches the seed rows in migration 0009).
// Used as a fallback when match_config can't be loaded or a key is missing.
func Defaults() Config {
	return Config{
		WString:        0.6,
		WEmbedding:     0.4,
		ScoreThreshold: 0.3,
		ArtistFactor:   1.0,
		GenreFactor:    0.3,
		StringMax:      3.0,
	}
}

// NormalizedInterest is one user_interests row reduced to the fields the
// matcher needs.
type NormalizedInterest struct {
	Value      string  // raw display value (artist name, genre slug, manual tag)
	Normalized string  // normalized form used for matching
	Weight     float64
}

// UserProfile is a user's matchable interest profile.
type UserProfile struct {
	UserID         pgtype.UUID
	Embedding      []float32 // 384-dim; may be nil if not yet embedded
	ScoreThreshold *float64  // per-user threshold; nil = use Config.ScoreThreshold
	SpotifyArtists []NormalizedInterest
	SpotifyGenres  []NormalizedInterest
	ManualTags     []NormalizedInterest
}

// EventPerformer pairs the display name with its normalized form.
type EventPerformer struct {
	Display    string
	Normalized string
}

// EventProfile is a single event's matchable profile.
type EventProfile struct {
	EventID    pgtype.UUID
	Embedding  []float32
	Performers []EventPerformer
	Genres     []string // slugs
}

// MatchScore is the output of Score() — what gets written to user_event_match.
type MatchScore struct {
	StringScore       float64
	EmbeddingScore    float64
	TotalScore        float64
	MatchedPerformers []string // display names of matched performers
	MatchedGenres     []string // genre slugs that matched
}
