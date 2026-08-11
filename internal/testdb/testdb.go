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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/wmyers/heres-whats-happening/internal/dsn"
)

// defaultTestComponents match the test database provisioned by docker-compose.yml
// and scripts/db-init.sh. Used for any TEST_DB_* field left unset so that
// `go test ./...` works without sourcing .env.example.
var defaultTestComponents = dsn.Components{
	User: "app", Password: "app", Host: "localhost", Port: "5432",
	Name: "appdb_test", SSLMode: "disable",
}

// DSN assembles the test DSN from TEST_DB_* env vars, falling back to
// defaultTestComponents for any field left unset.
func DSN() string {
	c := defaultTestComponents
	if v := os.Getenv("TEST_DB_USER"); v != "" {
		c.User = v
	}
	if v := os.Getenv("TEST_DB_PASSWORD"); v != "" {
		c.Password = v
	}
	if v := os.Getenv("TEST_DB_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("TEST_DB_PORT"); v != "" {
		c.Port = v
	}
	if v := os.Getenv("TEST_DB_NAME"); v != "" {
		c.Name = v
	}
	if v := os.Getenv("TEST_DB_SSLMODE"); v != "" {
		c.SSLMode = v
	}
	return c.DSN()
}

// testDBLockKey is an arbitrary fixed key identifying the whole test database.
// Any process using this package contends on this one key.
const testDBLockKey int64 = 7241893054126637

var (
	once    sync.Once
	pool    *pgxpool.Pool
	openErr error
	// lockConn holds the advisory lock for this process's lifetime. It is
	// deliberately never closed or returned to a pool: the lock is
	// session-scoped, so it lives exactly as long as this connection.
	lockConn *pgx.Conn
)

// MustOpen returns a connection pool to the test DB. Migrations run on first
// call. The returned pool is shared across all callers in the same process.
// A t.Cleanup is registered that truncates all data tables.
//
// Every test here truncates every table on cleanup, so tests touching this DB
// must not overlap. `go test ./...` runs each package's test binary as a
// separate process in parallel (-p defaults to GOMAXPROCS), which meant one
// package's cleanup could truncate tables out from under another package's
// in-flight test — producing FK violations and empty result sets in whichever
// package lost the race. To make the DB safe under any -p value, the first
// MustOpen in a process takes a Postgres advisory lock and holds it until the
// process exits, so only one test binary uses the database at a time.
//
// The lock is taken before migrations so concurrent processes cannot race
// there either. It is held per-process rather than per-test on purpose:
// per-test locking would deadlock any test calling MustOpen twice (e.g.
// handlers.TestSignup_ShortPasswordReturns400), since each call would want a
// separate session's lock.
func MustOpen(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(func() {
		dsn := DSN()
		if err := acquireDBLock(dsn); err != nil {
			openErr = err
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
		require.NoError(t, openErr)
	}
	t.Cleanup(func() { truncateAll(t, pool) })
	return pool
}

// acquireDBLock opens a dedicated connection and blocks until it holds the
// test-database advisory lock. The connection is stored in lockConn and never
// closed — the lock releases when this process exits and Postgres drops the
// session, which also covers the case where a test binary panics or is killed.
//
// The timeout is generous because the wait is legitimately as long as another
// package's entire test binary takes; it exists only so a genuine deadlock
// surfaces as a clear error instead of hanging forever.
func acquireDBLock(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", testDBLockKey); err != nil {
		conn.Close(context.Background())
		return err
	}
	lockConn = conn
	return nil
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

// truncateTables lists every table that accumulates rows written by app or
// test code and so must be emptied between tests. It is a manually maintained
// list, not derived from the schema — TestTruncateAllCoversEveryTable in
// truncate_test.go checks it against the live database on every run so an
// omission (this has happened twice) fails loudly instead of leaking rows
// across tests.
//
// Order matters: children before parents to avoid FK violations on TRUNCATE CASCADE.
var truncateTables = []string{
	"user_event_not_interested",
	"user_event_match",
	"event_genres",
	"event_performers",
	"events",
	"venues",
	"user_interests",
	"user_spotify_tokens",
	"ical_tokens",
	"email_confirmations",
	"refresh_tokens",
	"users",
	"artist_genre_cache",
	"poster_jobs",
}

// referenceTables lists public-schema tables deliberately absent from
// truncateTables: golang-migrate's own bookkeeping table, plus tables holding
// fixed reference/seed data inserted by migrations (sql/migrations 0001,
// 0005, 0009) that neither app nor test code ever writes to. Nothing
// accumulates in them, so nothing needs truncating — and truncating cities
// would delete the seeded "v1-city" row that other tests depend on via
// foreign key (see internal/store/users.sql.go).
var referenceTables = map[string]bool{
	"schema_migrations": true, // golang-migrate/v4 database/postgres.DefaultMigrationsTable
	"cities":            true,
	"event_sources":     true,
	"genres":            true,
	"match_config":      true,
}

func truncateAll(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tbl := range truncateTables {
		_, err := p.Exec(ctx, "TRUNCATE TABLE "+tbl+" CASCADE")
		if err != nil {
			// Tables not yet created in earlier-task tests are fine; ignore "does not exist".
			// In practice the migration runs first so this branch shouldn't hit.
			continue
		}
	}
}
