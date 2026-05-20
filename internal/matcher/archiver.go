package matcher

import (
	"context"
	"fmt"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Archiver marks events as archived if their last_seen_at is older than 7 days.
type Archiver struct {
	q *store.Queries
}

func NewArchiver(q *store.Queries) *Archiver {
	return &Archiver{q: q}
}

func (a *Archiver) Run(ctx context.Context) error {
	if err := a.q.ArchiveStaleEvents(ctx); err != nil {
		return fmt.Errorf("archive stale events: %w", err)
	}
	return nil
}
