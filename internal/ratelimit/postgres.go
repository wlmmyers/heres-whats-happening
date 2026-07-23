package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Postgres is a durable sliding-window Limiter backed by the rate_limit_events
// table. Unlike Memory it survives restarts and stays correct across tasks,
// which is what the signup limit needs.
//
// Allow and Record are deliberately separate: the signup middleware checks
// before running the handler and records only when the handler created an
// account.
type Postgres struct {
	q      *store.Queries
	bucket string
	limit  int
	window time.Duration
	now    func() time.Time
}

// NewPostgres returns a limiter permitting limit recorded events per window,
// per key, within the named bucket.
func NewPostgres(q *store.Queries, bucket string, limit int, window time.Duration) *Postgres {
	return NewPostgresWithClock(q, bucket, limit, window, time.Now)
}

// NewPostgresWithClock is NewPostgres with an injectable clock, for tests.
func NewPostgresWithClock(q *store.Queries, bucket string, limit int, window time.Duration, now func() time.Time) *Postgres {
	return &Postgres{q: q, bucket: bucket, limit: limit, window: window, now: now}
}

// Allow reports whether key is under its limit. On a query error it returns the
// error alongside Allowed=true: the caller should log it and let the request
// through rather than fail the endpoint closed.
func (p *Postgres) Allow(ctx context.Context, key string) (Decision, error) {
	now := p.now()
	since := pgtype.Timestamptz{Time: now.Add(-p.window), Valid: true}

	n, err := p.q.CountRateLimitEvents(ctx, store.CountRateLimitEventsParams{
		Bucket: p.bucket,
		Key:    key,
		Since:  since,
	})
	if err != nil {
		return Decision{Allowed: true}, fmt.Errorf("count rate limit events: %w", err)
	}
	if n < int64(p.limit) {
		return Decision{Allowed: true}, nil
	}

	// Over the limit. Budget frees up when the oldest event in the window ages
	// out, which is a far more useful Retry-After than the whole window.
	retryAfter := p.window
	oldest, err := p.q.OldestRateLimitEvent(ctx, store.OldestRateLimitEventParams{
		Bucket: p.bucket,
		Key:    key,
		Since:  since,
	})
	if err != nil {
		// The decision doesn't change: the caller is already over the limit, so
		// Allowed stays false and RetryAfter keeps its safe full-window default.
		// The error still needs to surface so a genuine database fault here
		// isn't silently swallowed.
		return Decision{Allowed: false, RetryAfter: retryAfter}, fmt.Errorf("oldest rate limit event: %w", err)
	}
	if oldest.Valid {
		if d := oldest.Time.Add(p.window).Sub(now); d > 0 && d < retryAfter {
			retryAfter = d
		}
	}
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return Decision{Allowed: false, RetryAfter: retryAfter}, nil
}

// Record writes one event for key, timestamped from this limiter's clock so
// that Allow and Record always agree on what "now" means.
func (p *Postgres) Record(ctx context.Context, key string) error {
	if err := p.q.InsertRateLimitEvent(ctx, store.InsertRateLimitEventParams{
		Bucket:    p.bucket,
		Key:       key,
		CreatedAt: pgtype.Timestamptz{Time: p.now(), Valid: true},
	}); err != nil {
		return fmt.Errorf("insert rate limit event: %w", err)
	}
	return nil
}

var _ Limiter = (*Postgres)(nil)
