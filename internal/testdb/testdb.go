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
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			openErr = errSkip("TEST_DATABASE_URL not set")
			return
		}
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
		if _, ok := openErr.(errSkip); ok {
			t.Skip(openErr.Error())
		}
		require.NoError(t, openErr)
	}
	t.Cleanup(func() { truncateAll(t, pool) })
	return pool
}

type errSkip string

func (e errSkip) Error() string { return string(e) }

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
	// Truncate tables that hold test data. `cities` is seeded by the migration
	// and is not truncated. Add tables here as migrations introduce them.
	tables := []string{
		"user_interests",
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
