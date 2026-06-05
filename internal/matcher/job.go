package matcher

import (
	"context"
	"fmt"

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
	cfg, err := LoadConfig(ctx, j.q, j.cfg)
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
