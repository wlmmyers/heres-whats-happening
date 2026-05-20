package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Job runs the four steps in order: embed events, embed users, match, archive.
type Job struct {
	q   *store.Queries
	emb Embedder
	cfg Config
}

// NewJob builds a Job. The cfg is used as a fallback; Job.Run re-reads
// match_config from the DB before scoring so SQL-tuned values take effect
// without restarting the binary.
func NewJob(q *store.Queries, emb Embedder, cfg Config) *Job {
	return &Job{q: q, emb: emb, cfg: cfg}
}

func (j *Job) Run(ctx context.Context) error {
	cfg, err := j.loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := NewEventEmbedder(j.q, j.emb).Run(ctx); err != nil {
		return fmt.Errorf("event embedder: %w", err)
	}
	if err := NewUserEmbedder(j.q, j.emb).Run(ctx); err != nil {
		return fmt.Errorf("user embedder: %w", err)
	}
	if err := NewMatchStep(j.q, cfg).Run(ctx); err != nil {
		return fmt.Errorf("match step: %w", err)
	}
	if err := NewArchiver(j.q).Run(ctx); err != nil {
		return fmt.Errorf("archiver: %w", err)
	}
	return nil
}

// loadConfig reads match_config rows and overlays them on j.cfg defaults.
// Missing keys keep the default value.
func (j *Job) loadConfig(ctx context.Context) (Config, error) {
	cfg := j.cfg
	rows, err := j.q.ListMatchConfig(ctx)
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
