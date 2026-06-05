package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// LoadConfig reads match_config rows and overlays them on the given defaults.
// Missing or unparseable keys keep the default value.
func LoadConfig(ctx context.Context, q *store.Queries, def Config) (Config, error) {
	cfg := def
	rows, err := q.ListMatchConfig(ctx)
	if err != nil {
		return cfg, err
	}
	for _, r := range rows {
		var raw json.Number
		if err := json.Unmarshal(r.Value, &raw); err != nil {
			continue
		}
		f, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			continue
		}
		switch r.Key {
		case "w_string":
			cfg.WString = f
		case "w_embedding":
			cfg.WEmbedding = f
		case "score_threshold":
			cfg.ScoreThreshold = f
		case "artist_factor":
			cfg.ArtistFactor = f
		case "genre_factor":
			cfg.GenreFactor = f
		case "string_max":
			cfg.StringMax = f
		}
	}
	return cfg, nil
}

// RescoreUser recomputes matches for a single user using current embeddings.
// It does NOT re-embed (interests/events are assumed current), so it needs no
// TEI client — suitable for calling in-process from the API after a threshold
// change. The global config defaults are overlaid with match_config, then the
// user's own score_threshold (if set) is applied inside MatchStep.
func RescoreUser(ctx context.Context, q *store.Queries, userID pgtype.UUID) error {
	cfg, err := LoadConfig(ctx, q, Defaults())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := NewMatchStepForUser(q, cfg, userID).Run(ctx); err != nil {
		return fmt.Errorf("match step: %w", err)
	}
	return nil
}
