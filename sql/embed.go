// Package sql embeds the database migration files so they can be applied from
// the production binary without filesystem access (the distroless runtime image
// ships only the Go binary).
package sql

import "embed"

// Migrations holds every file under migrations/. Consumed via the golang-migrate
// iofs source driver.
//
//go:embed migrations/*.sql
var Migrations embed.FS
