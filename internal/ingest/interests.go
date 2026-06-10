package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// InterestHandler applies an InterestMessage to user_interests, replacing
// the user's Spotify-derived rows per message (per-statement, not wrapped in a
// transaction — a mid-replace failure is retried when the SQS message redelivers).
//
// Weight scaling: artists and track artists use rankWeight (rank 1 → 1.0,
// ramping down to 0.6 at rank 50). Genres use rankGenreWeight, which decays
// further toward a 0.1 floor — the genre list is unbounded, so deep-ranked
// genres should stay weak signal.
type InterestHandler struct {
	q   *store.Queries
	emb matcher.Embedder // may be nil (no TEI configured) → embed step skipped
}

func NewInterestHandler(q *store.Queries, emb matcher.Embedder) *InterestHandler {
	return &InterestHandler{q: q, emb: emb}
}

func (h *InterestHandler) Handle(ctx context.Context, body []byte) error {
	var m events.InterestMessage
	if err := json.Unmarshal(body, &m); err != nil {
		log.Printf("ingest: bad interest message: %v", err)
		return nil // delete malformed
	}
	uid, err := uuid.Parse(m.UserID)
	if err != nil {
		log.Printf("ingest: bad user_id %q: %v", m.UserID, err)
		return nil
	}
	pgUID := pgtype.UUID{Bytes: uid, Valid: true}

	// Empty Op is treated as replace-and-embed for backward compatibility.
	if m.Op != events.OpOnlyEmbed {
		if err := h.replaceInterests(ctx, pgUID, m); err != nil {
			return err
		}
	}
	return h.embedUser(ctx, pgUID)
}

// replaceInterests atomically replaces the user's Spotify-derived interest rows.
func (h *InterestHandler) replaceInterests(ctx context.Context, pgUID pgtype.UUID, m events.InterestMessage) error {
	// Replace artists.
	if err := h.q.ReplaceSpotifyArtistInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete artists: %w", err)
	}
	for _, item := range m.SpotifyTopArtists {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_top_artist",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert artist %q: %w", item.Name, err)
		}
	}

	// Replace track artists (the artists behind the user's top tracks — a
	// distinct signal from their top artists).
	if err := h.q.ReplaceSpotifyTrackArtistInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete track artists: %w", err)
	}
	for _, item := range m.SpotifyTopTrackArtists {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_top_track_artist",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert track artist %q: %w", item.Name, err)
		}
	}

	// Replace saved-song artists (the artists behind the user's saved tracks,
	// ranked by how recently each was saved — a distinct signal again).
	if err := h.q.ReplaceSpotifySavedSongArtistInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete saved song artists: %w", err)
	}
	for _, item := range m.SpotifySavedSongArtists {
		w := rankWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_saved_song_artist",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert saved song artist %q: %w", item.Name, err)
		}
	}

	// Replace genres.
	if err := h.q.ReplaceSpotifyGenreInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete genres: %w", err)
	}
	for _, item := range m.SpotifyTopGenres {
		w := rankGenreWeight(item.Rank)
		if err := h.q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
			UserID:          pgUID,
			Kind:            "spotify_top_genre",
			Value:           item.Name,
			NormalizedValue: events.NormalizeString(item.Name),
			Weight:          w,
		}); err != nil {
			return fmt.Errorf("insert genre %q: %w", item.Name, err)
		}
	}

	return nil
}

// embedUser re-embeds the user via the matcher's single-user path. If no
// embedder is configured (no TEI endpoint), embedding is skipped — the daily
// match batch is the backstop.
func (h *InterestHandler) embedUser(ctx context.Context, pgUID pgtype.UUID) error {
	if h.emb == nil {
		return nil
	}
	if err := matcher.NewUserEmbedder(h.q, h.emb).EmbedUser(ctx, pgUID); err != nil {
		return fmt.Errorf("embed user: %w", err)
	}
	return nil
}

// rankWeight maps a 1-based rank to an interest weight: rank 1 → 1.0, ramping
// down linearly to 0.6 at rank 50 (the size of the Spotify top-artist and
// top-track lists), then holding at 0.6 for any lower-ranked items. Used for
// artists and track artists, whose lists are capped at 50.
func rankWeight(rank int) float64 {
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

// rankGenreWeight maps a 1-based rank to a genre weight: rank 1 → 1.0, decaying
// 0.02 per rank down to a 0.1 floor. The genre list is unbounded (derived from
// the frequency of every genre across the user's top artists), so deep-ranked
// genres decay to near-negligible signal rather than holding at a high floor.
func rankGenreWeight(rank int) float64 {
	w := 1.0 - float64(rank-1)*0.02
	if w < 0.1 {
		return 0.1
	}
	return w
}
