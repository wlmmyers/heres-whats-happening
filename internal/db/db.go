package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PasswordProvider returns the current database password. A prod implementation
// fetches it fresh from Secrets Manager so a rotated credential is picked up on
// the next connection, without restarting the task.
type PasswordProvider func(context.Context) (string, error)

// NewPool builds a pool using the password baked into the DSN. Suitable for
// local dev and tests where credentials don't rotate.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return NewPoolWithPassword(ctx, dsn, nil)
}

// NewPoolWithPassword builds a pool that, when provider is non-nil, overrides
// the DSN password with the provider's current value before every new
// connection. pgx recycles connections on its own clock (max lifetime / idle
// time), so as old connections turn over the pool transparently reconnects with
// the rotated password — no restart, no DLQ backlog.
func NewPoolWithPassword(ctx context.Context, dsn string, provider PasswordProvider) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	// Floor the pool so a quiet period does not leave the next caller paying a
	// cold connect. The two knobs do different jobs, and both are needed.
	//
	// MinIdleConns floors IDLE connections and is what actually guarantees one
	// is ready to hand out; MinConns floors TOTAL, so on its own its single
	// connection is checked out under any load and there is still nothing free.
	//
	// MinConns then earns its place in checkConnsHealth (pgxpool/pool.go:542),
	// which destroys a connection idle past MaxConnIdleTime only while
	// totalConns > minConns. At 0 the sweep destroys our last connection and
	// checkMinConns recreates it 500ms later to satisfy MinIdleConns — a
	// handshake plus a Secrets Manager GetSecretValue burned to arrive back
	// where we started. At 1 the sweep leaves it alone.
	//
	// Note it does NOT protect against lifetime expiry: that branch (:536) is
	// deliberately `totalConns >= minConns`, so an expired connection is
	// destroyed regardless. MaxConnLifetimeJitter below is what handles that.
	cfg.MinConns = 1
	cfg.MinIdleConns = 1
	// Rotate connections on a bounded clock: picks up DNS changes after an RDS
	// failover, rebalances across replicas, and bounds server-side memory growth
	// on long-lived backends. The jitter is not optional here — the pool is built
	// in one go at task start, so without it every connection expires in the same
	// second, and BeforeConnect makes each replacement a Secrets Manager round
	// trip *inside* the acquire path. 5m spreads that herd.
	cfg.MaxConnLifetime = 45 * time.Minute
	cfg.MaxConnLifetimeJitter = 5 * time.Minute
	// Bound the liveness ping. pgxpool pings any connection idle for more than a
	// second before handing it out (its default ShouldPing), and at zero that
	// ping inherits the CALLER's context — so a single black-holed socket, which
	// is what a pooled connection becomes after an RDS failover, spends a whole
	// request budget proving itself dead before Acquire tries the next one.
	//
	// 1s is ~1000x a healthy intra-VPC round trip, so it will not evict a live
	// connection, while still letting Acquire's retry loop (destroy the failed
	// connection, try another, up to MaxConns+1 times) clear several stale
	// connections inside one 5s handler budget.
	cfg.PingTimeout = time.Second
	if provider != nil {
		cfg.BeforeConnect = func(ctx context.Context, cc *pgx.ConnConfig) error {
			pw, err := provider(ctx)
			if err != nil {
				return fmt.Errorf("fetch db password: %w", err)
			}
			cc.Password = pw
			return nil
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
