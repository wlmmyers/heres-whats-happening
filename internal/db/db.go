package db

import (
	"context"
	"fmt"

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
