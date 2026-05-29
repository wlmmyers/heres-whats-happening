package migrate_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	migratepkg "github.com/wmyers/heres-whats-happening/internal/migrate"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// TestUpAppliesEmbeddedMigrationsToFreshDB exercises the real production
// bootstrap: a brand-new, empty database migrated entirely from the embedded
// SQL (no filesystem access, as in the distroless image).
func TestUpAppliesEmbeddedMigrationsToFreshDB(t *testing.T) {
	const freshDB = "migrate_embed_test"

	adminDSN := testdb.DSN()
	freshDSN := withDatabase(t, adminDSN, freshDB)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN)
	require.NoError(t, err)
	defer admin.Close(ctx)

	// Guarantee a clean starting point.
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+freshDB+" WITH (FORCE)")
	_, err = admin.Exec(ctx, "CREATE DATABASE "+freshDB)
	require.NoError(t, err)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), adminDSN)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+freshDB+" WITH (FORCE)")
	})

	// Behavior under test: apply every embedded migration from scratch...
	require.NoError(t, migratepkg.Up(freshDSN))
	// ...and be safe to run again when already at the latest version.
	require.NoError(t, migratepkg.Up(freshDSN))

	pool, err := pgxpool.New(ctx, freshDSN)
	require.NoError(t, err)
	defer pool.Close()

	// A table from the final migration proves the full embedded set ran.
	var exists bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ical_tokens')`,
	).Scan(&exists))
	require.True(t, exists, "ical_tokens table should exist after embedded migrations run")
}

// withDatabase returns dsn with its database path swapped to name.
func withDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}
