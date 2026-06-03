package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// MatchStep loads active users + upcoming events, runs Score() for each pair,
// and upserts rows above the configured threshold into user_event_match.
type MatchStep struct {
	q   *store.Queries
	cfg Config
}

func NewMatchStep(q *store.Queries, cfg Config) *MatchStep {
	return &MatchStep{q: q, cfg: cfg}
}

func (m *MatchStep) Run(ctx context.Context) error {
	users, err := m.loadUsers(ctx)
	if err != nil {
		return fmt.Errorf("load users: %w", err)
	}
	events, err := m.loadEvents(ctx)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	// One timestamp for the whole run: every upsert stamps computed_at = runAt.
	// (The stale-prune below deletes anything not stamped this run.)
	runAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	for _, user := range users {
		for _, event := range events {
			score := Score(user, event, m.cfg)
			if score.TotalScore <= m.cfg.ScoreThreshold {
				continue
			}
			bd, err := json.Marshal(map[string]any{
				"string_score":       score.StringScore,
				"embedding_score":    score.EmbeddingScore,
				"matched_performers": score.MatchedPerformers,
				"matched_genres":     score.MatchedGenres,
			})
			if err != nil {
				return fmt.Errorf("marshal breakdown: %w", err)
			}
			if err := m.q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
				UserID:         user.UserID,
				EventID:        event.EventID,
				Score:          score.TotalScore,
				ScoreBreakdown: bd,
				ComputedAt:     runAt,
			}); err != nil {
				return fmt.Errorf("upsert match: %w", err)
			}
		}
	}

	if err := m.q.DeleteObsoleteMatches(ctx); err != nil {
		return fmt.Errorf("delete obsolete: %w", err)
	}
	return nil
}

func (m *MatchStep) loadUsers(ctx context.Context) ([]UserProfile, error) {
	rows, err := m.q.ListActiveUsersForMatching(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	userIDs := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		userIDs[i] = r.ID
	}
	interests, err := m.q.ListUserInterestsBatch(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("list interests: %w", err)
	}

	profiles := make(map[pgtype.UUID]*UserProfile, len(rows))
	for _, r := range rows {
		p := &UserProfile{UserID: r.ID}
		if r.InterestEmbedding != nil {
			p.Embedding = r.InterestEmbedding.Slice()
		}
		profiles[r.ID] = p
	}
	// Track artists are folded into SpotifyArtists, but only after top
	// artists are all collected, so a name in both lists keeps its top-artist
	// (higher) weight instead of being double-counted.
	trackArtistsByUser := make(map[pgtype.UUID][]NormalizedInterest)
	for _, ui := range interests {
		ni := NormalizedInterest{
			Value:      ui.Value,
			Normalized: ui.NormalizedValue,
			Weight:     ui.Weight,
		}
		p := profiles[ui.UserID]
		switch ui.Kind {
		case "spotify_top_artist":
			p.SpotifyArtists = append(p.SpotifyArtists, ni)
		case "spotify_top_track_artist":
			trackArtistsByUser[ui.UserID] = append(trackArtistsByUser[ui.UserID], ni)
		case "spotify_top_genre":
			p.SpotifyGenres = append(p.SpotifyGenres, ni)
		case "manual_tag":
			p.ManualTags = append(p.ManualTags, ni)
		}
	}
	for id, tracks := range trackArtistsByUser {
		p := profiles[id]
		p.SpotifyArtists = foldDeduped(p.SpotifyArtists, tracks,
			func(ni NormalizedInterest) string { return ni.Normalized })
	}

	out := make([]UserProfile, 0, len(profiles))
	for _, r := range rows {
		out = append(out, *profiles[r.ID])
	}
	return out, nil
}

func (m *MatchStep) loadEvents(ctx context.Context) ([]EventProfile, error) {
	rows, err := m.q.ListUpcomingEventsForMatching(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	eventIDs := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		eventIDs[i] = r.ID
	}
	performers, err := m.q.ListEventPerformersBatch(ctx, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("list performers: %w", err)
	}
	genres, err := m.q.ListEventGenresBatch(ctx, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("list genres: %w", err)
	}

	profiles := make(map[pgtype.UUID]*EventProfile, len(rows))
	for _, r := range rows {
		p := &EventProfile{EventID: r.ID}
		if r.Embedding != nil {
			p.Embedding = r.Embedding.Slice()
		}
		profiles[r.ID] = p
	}
	for _, p := range performers {
		profiles[p.EventID].Performers = append(profiles[p.EventID].Performers, EventPerformer{
			Display:    p.PerformerName,
			Normalized: p.NormalizedName,
		})
	}
	for _, g := range genres {
		profiles[g.EventID].Genres = append(profiles[g.EventID].Genres, g.GenreSlug)
	}

	out := make([]EventProfile, 0, len(profiles))
	for _, r := range rows {
		out = append(out, *profiles[r.ID])
	}
	return out, nil
}
