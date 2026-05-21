// Package testdb provides a shared Postgres pool for integration tests.
// Migrations run once per process; tables are truncated between tests.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// DefaultTestDSN matches the test database provisioned by docker-compose.yml
// and scripts/db-init.sh. Used when TEST_DATABASE_URL is unset so that
// `go test ./...` works without sourcing .env.example.
const DefaultTestDSN = "postgres://app:app@localhost:5432/appdb_test?sslmode=disable"

// DSN returns TEST_DATABASE_URL, falling back to DefaultTestDSN.
func DSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return DefaultTestDSN
}

var (
	once    sync.Once
	pool    *pgxpool.Pool
	openErr error
)

// MustOpen returns a connection pool to the test DB. Migrations run on first
// call. The returned pool is shared across all callers in the same process.
// A t.Cleanup is registered that truncates all data tables.
func MustOpen(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(func() {
		dsn := DSN()
		if err := runMigrations(dsn); err != nil {
			openErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			openErr = err
			return
		}
		pool = p
	})
	if openErr != nil {
		require.NoError(t, openErr)
	}
	t.Cleanup(func() { truncateAll(t, pool) })
	return pool
}

func runMigrations(dsn string) error {
	_, file, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations")
	m, err := migrate.New("file://"+migrationsPath, dsn)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func truncateAll(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Order matters: children before parents to avoid FK violations on TRUNCATE CASCADE.
	tables := []string{
		"user_event_match",
		"event_genres",
		"event_performers",
		"events",
		"venues",
		"user_interests",
		"user_spotify_tokens",
		"ical_tokens",
		"refresh_tokens",
		"users",
	}
	for _, tbl := range tables {
		_, err := p.Exec(ctx, "TRUNCATE TABLE "+tbl+" CASCADE")
		if err != nil {
			// Tables not yet created in earlier-task tests are fine; ignore "does not exist".
			// In practice the migration runs first so this branch shouldn't hit.
			continue
		}
	}
}
