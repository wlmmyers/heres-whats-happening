// Package migrate applies the embedded SQL migrations to a Postgres database.
// It exists so the production binary can bootstrap its own schema (e.g. via a
// one-off ECS task) without shipping the migration files on disk.
package migrate

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	sqlfs "github.com/wmyers/heres-whats-happening/sql"
)

// Up applies all embedded migrations to the database at dsn. It is idempotent:
// running against an already-current database returns nil.
func Up(dsn string) error {
	src, err := iofs.New(sqlfs.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
