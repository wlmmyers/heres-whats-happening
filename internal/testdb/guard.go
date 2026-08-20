package testdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// This package's bootstrap is destructive: it runs migrations and then
// TRUNCATE ... CASCADE across every data table between tests. Aimed at the
// wrong server that is not a failing test, it is data loss.
//
// The realistic way that happens has nothing to do with a typo. `make
// bastion-tunnel` forwards prod RDS to localhost:5432 -- the same port the
// docker test database uses -- so with the container down, every DB-backed test
// silently retargets production while the DSN still reads "localhost". Host and
// port are useless as a safety signal precisely because the tunnel forges them.
//
// So the guards below ask two questions neither a tunnel nor a typo can fake:
// what is the database CALLED, and what does the SERVER say it is.

// managedProviderRoles are roles that only a managed Postgres service creates.
// A vanilla server (docker, CI, a developer's laptop) has none of them --
// verified against this project's docker test database, which returns no rows
// for this set.
//
// Presence of any one means the connection reached a cloud-hosted database,
// which is never a legitimate target for a destructive test bootstrap.
var managedProviderRoles = map[string]string{
	"rdsadmin":          "AWS RDS / Aurora",
	"rds_superuser":     "AWS RDS / Aurora",
	"cloudsqlsuperuser": "Google Cloud SQL",
	"alloydbsuperuser":  "Google AlloyDB",
	"azure_pg_admin":    "Azure Database for PostgreSQL",
}

// checkTestDatabaseName rejects a database whose name does not mark it
// disposable. Cheap, and it runs before anything connects -- but it is only the
// first line: the incident that motivated this guard had a correctly-named
// appdb_test in the DSN and still reached prod, which is what
// checkNotManagedDatabase is for.
func checkTestDatabaseName(name string) error {
	if strings.HasSuffix(name, "_test") {
		return nil
	}
	return fmt.Errorf(
		"refusing to run destructive test setup against database %q: the name does not end in _test.\n"+
			"This package truncates every data table. Set TEST_DB_NAME to a disposable database.",
		name)
}

// checkNotManagedDatabase rejects a server that identifies itself as a managed
// cloud database. roles is the subset of managedProviderRoles the server
// actually reports.
func checkNotManagedDatabase(roles []string) error {
	for _, r := range roles {
		if provider, ok := managedProviderRoles[r]; ok {
			return fmt.Errorf(
				"refusing to run destructive test setup: the server reports the %q role, so this is a %s instance, not a local test database.\n"+
					"A localhost DSN reaching a cloud database almost always means an SSM tunnel is forwarding the port "+
					"(`make bastion-tunnel` binds localhost:5432, the same port docker uses) while the local container is down.\n"+
					"Close the tunnel and run `make db-up`.",
				r, provider)
		}
	}
	return nil
}

// assertSafeTarget runs the server-side half of the guard on an open
// connection. Called before migrations and before the advisory lock, so a
// misdirected run stops before it writes anything.
func assertSafeTarget(ctx context.Context, conn *pgx.Conn) error {
	names := make([]string, 0, len(managedProviderRoles))
	for r := range managedProviderRoles {
		names = append(names, r)
	}
	rows, err := conn.Query(ctx, "SELECT rolname FROM pg_roles WHERE rolname = ANY($1)", names)
	if err != nil {
		// pg_roles is world-readable; a failure here means something is wrong
		// enough that proceeding to TRUNCATE would be reckless.
		return fmt.Errorf("test-database guard could not read pg_roles: %w", err)
	}
	defer rows.Close()

	var found []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return fmt.Errorf("test-database guard: scan pg_roles: %w", err)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("test-database guard: read pg_roles: %w", err)
	}
	return checkNotManagedDatabase(found)
}
