package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// InterestHandler applies an InterestMessage to user_interests, replacing
// the user's Spotify-derived rows atomically per message.
//
// Weight scaling: rank 1 → 1.0; rank N → max(0.1, 1.0 - (N-1)*0.02). This
// gives top-1 full weight and ramps down gently so rank-50 still contributes.
type InterestHandler struct {
	q *store.Queries
}

func NewInterestHandler(q *store.Queries) *InterestHandler {
	return &InterestHandler{q: q}
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

	// Replace genres.
	if err := h.q.ReplaceSpotifyGenreInterests(ctx, pgUID); err != nil {
		return fmt.Errorf("delete genres: %w", err)
	}
	for _, item := range m.SpotifyTopGenres {
		w := rankWeight(item.Rank)
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

func rankWeight(rank int) float64 {
	w := 1.0 - float64(rank-1)*0.02
	if w < 0.1 {
		return 0.1
	}
	return w
}
