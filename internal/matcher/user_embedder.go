package matcher

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// UserEmbedder embeds users whose interest_embedding is stale or missing.
type UserEmbedder struct {
	q   *store.Queries
	emb Embedder
}

func NewUserEmbedder(q *store.Queries, emb Embedder) *UserEmbedder {
	return &UserEmbedder{q: q, emb: emb}
}

func (u *UserEmbedder) Run(ctx context.Context) error {
	userIDs, err := u.q.SelectUsersNeedingEmbedding(ctx)
	if err != nil {
		return fmt.Errorf("select users: %w", err)
	}
	if len(userIDs) == 0 {
		return nil
	}

	interests, err := u.q.ListUserInterestsBatch(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("list interests: %w", err)
	}

	type bucket struct {
		artists      []string
		trackArtists []string
		genres       []string
		tags         []string
	}
	byUser := make(map[pgtype.UUID]*bucket, len(userIDs))
	for _, id := range userIDs {
		byUser[id] = &bucket{}
	}
	for _, ui := range interests {
		b := byUser[ui.UserID]
		switch ui.Kind {
		case "spotify_top_artist":
			b.artists = append(b.artists, ui.Value)
		case "spotify_top_track_artist":
			b.trackArtists = append(b.trackArtists, ui.Value)
		case "spotify_top_genre":
			b.genres = append(b.genres, ui.Value)
		case "manual_tag":
			b.tags = append(b.tags, ui.Value)
		}
	}

	texts := make([]string, len(userIDs))
	for i, id := range userIDs {
		b := byUser[id]
		// Fold track artists into the artist list, deduped by normalized name
		// (top artists take precedence) — matching how match_step merges them.
		artists := foldDeduped(b.artists, b.trackArtists, events.NormalizeString)
		texts[i] = BuildUserText(UserText{
			TopArtists: artists,
			TopGenres:  b.genres,
			ManualTags: b.tags,
		})
	}

	// Skip empty-text users — but still mark them so we don't keep selecting them.
	// Strategy: filter, embed the non-empty texts, then update only those rows.
	// Users with no interests get nothing written here; next run will revisit if
	// they add interests.
	nonEmptyIdx := make([]int, 0, len(texts))
	nonEmptyTexts := make([]string, 0, len(texts))
	for i, t := range texts {
		if t != "" {
			nonEmptyIdx = append(nonEmptyIdx, i)
			nonEmptyTexts = append(nonEmptyTexts, t)
		}
	}

	var vectors [][]float32
	if len(nonEmptyTexts) > 0 {
		vectors, err = u.emb.Embed(ctx, nonEmptyTexts)
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		if len(vectors) != len(nonEmptyTexts) {
			return fmt.Errorf("embedder returned %d vectors for %d users", len(vectors), len(nonEmptyTexts))
		}
	}

	for j, idx := range nonEmptyIdx {
		v := pgvector.NewVector(vectors[j])
		if err := u.q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
			ID:                userIDs[idx],
			InterestEmbedding: &v,
		}); err != nil {
			return fmt.Errorf("update user: %w", err)
		}
	}
	return nil
}
