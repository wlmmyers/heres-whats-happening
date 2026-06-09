package matcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// MatchStep loads users + upcoming events, runs Score() for each pair, and
// upserts rows above each user's threshold into user_event_match. When userID
// is set, it processes only that one user (still scoring against all events).
type MatchStep struct {
	q      *store.Queries
	cfg    Config
	userID *pgtype.UUID // nil = all active users
}

func NewMatchStep(q *store.Queries, cfg Config) *MatchStep {
	return &MatchStep{q: q, cfg: cfg}
}

// NewMatchStepForUser scopes the step to a single user.
func NewMatchStepForUser(q *store.Queries, cfg Config, userID pgtype.UUID) *MatchStep {
	return &MatchStep{q: q, cfg: cfg, userID: &userID}
}

type matchUserRow struct {
	ID                pgtype.UUID
	InterestEmbedding *pgvector.Vector
	ScoreThreshold    *float64
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

	// One timestamp for the whole run: every upsert stamps computed_at = runAt,
	// and the stale-prune below deletes anything not stamped this run. Using a
	// single value for both write and prune removes any clock-skew ambiguity.
	runAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	userIDs := make([]pgtype.UUID, len(users))
	for i, u := range users {
		userIDs[i] = u.UserID
	}

	for _, user := range users {
		for _, event := range events {
			score := Score(user, event, m.cfg)
			threshold := m.cfg.ScoreThreshold
			if user.ScoreThreshold != nil {
				threshold = *user.ScoreThreshold
			}
			if score.TotalScore <= threshold {
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

	// Prune matches for the recomputed users that were not re-stamped this run
	// (pairs that dropped to/below threshold). Scoped to processed users so a
	// zero-user run is a no-op.
	if len(userIDs) > 0 {
		if err := m.q.DeleteStaleMatchesForUsers(ctx, store.DeleteStaleMatchesForUsersParams{
			UserIds: userIDs,
			Cutoff:  runAt,
		}); err != nil {
			return fmt.Errorf("delete stale matches: %w", err)
		}
	}

	if err := m.q.DeleteObsoleteMatches(ctx); err != nil {
		return fmt.Errorf("delete obsolete: %w", err)
	}
	return nil
}

func (m *MatchStep) loadUsers(ctx context.Context) ([]UserProfile, error) {
	var rows []matchUserRow
	if m.userID != nil {
		r, err := m.q.GetUserForMatching(ctx, *m.userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		rows = append(rows, matchUserRow{r.ID, r.InterestEmbedding, r.ScoreThreshold})
	} else {
		rs, err := m.q.ListActiveUsersForMatching(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			rows = append(rows, matchUserRow{r.ID, r.InterestEmbedding, r.ScoreThreshold})
		}
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
		p := &UserProfile{UserID: r.ID, ScoreThreshold: r.ScoreThreshold}
		if r.InterestEmbedding != nil {
			p.Embedding = r.InterestEmbedding.Slice()
		}
		profiles[r.ID] = p
	}
	// Track artists then saved-song artists are folded into SpotifyArtists, but
	// only after top artists are all collected, so a name appearing in multiple
	// lists keeps its highest-precedence (top > track > saved) weight instead of
	// being double-counted.
	trackArtistsByUser := make(map[pgtype.UUID][]NormalizedInterest)
	savedArtistsByUser := make(map[pgtype.UUID][]NormalizedInterest)
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
		case "spotify_saved_song_artist":
			savedArtistsByUser[ui.UserID] = append(savedArtistsByUser[ui.UserID], ni)
		case "spotify_top_genre":
			p.SpotifyGenres = append(p.SpotifyGenres, ni)
		case "manual_tag":
			p.ManualTags = append(p.ManualTags, ni)
		}
	}
	normKey := func(ni NormalizedInterest) string { return ni.Normalized }
	for id, tracks := range trackArtistsByUser {
		p := profiles[id]
		p.SpotifyArtists = foldDeduped(p.SpotifyArtists, tracks, normKey)
	}
	for id, saved := range savedArtistsByUser {
		p := profiles[id]
		p.SpotifyArtists = foldDeduped(p.SpotifyArtists, saved, normKey)
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
