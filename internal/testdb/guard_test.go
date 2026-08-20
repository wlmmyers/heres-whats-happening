package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/stretchr/testify/require"
)

func TestCheckTestDatabaseName_AcceptsTestSuffix(t *testing.T) {
	require.NoError(t, checkTestDatabaseName("appdb_test"))
	require.NoError(t, checkTestDatabaseName("hwh_test"))
}

// The bootstrap truncates every data table, so pointing it at a database that
// is not disposable is destructive. A name without the suffix is the cheapest
// signal that someone aimed it somewhere real.
func TestCheckTestDatabaseName_RejectsNonTestName(t *testing.T) {
	for _, name := range []string{"appdb", "postgres", "appdb_prod", "test_appdb"} {
		err := checkTestDatabaseName(name)
		require.Error(t, err, "%q must be rejected", name)
		require.Contains(t, err.Error(), name)
	}
}

// A vanilla Postgres has none of these roles; every managed provider creates
// its own. Verified locally: the docker test DB returns no rows for this set.
func TestCheckNotManagedDatabase_AllowsVanillaPostgres(t *testing.T) {
	require.NoError(t, checkNotManagedDatabase(nil))
	require.NoError(t, checkNotManagedDatabase([]string{}))
	require.NoError(t, checkNotManagedDatabase([]string{"app", "postgres", "pg_monitor"}))
}

// The incident this guards against: no local Postgres, and an SSM tunnel
// forwarding localhost:5432 to prod RDS. Host and port look entirely local, so
// only something the SERVER reports can tell the difference.
func TestCheckNotManagedDatabase_RejectsManagedProviders(t *testing.T) {
	for _, role := range []string{
		"rdsadmin", "rds_superuser", // AWS RDS / Aurora
		"cloudsqlsuperuser", // Google Cloud SQL
		"alloydbsuperuser",  // Google AlloyDB
		"azure_pg_admin",    // Azure Database for PostgreSQL
	} {
		err := checkNotManagedDatabase([]string{"app", role, "postgres"})
		require.Error(t, err, "%q must be rejected", role)
		require.Contains(t, err.Error(), role)
	}
}

// The message has to name the tunnel, because that is the only way this
// happens in practice and the symptom otherwise looks like a DB outage.
func TestCheckNotManagedDatabase_ErrorExplainsTheTunnel(t *testing.T) {
	err := checkNotManagedDatabase([]string{"rdsadmin"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tunnel")
}

// The pure checks above prove the decision logic; this proves the QUERY. It is
// the half that was never exercised against a real managed instance, so it is
// verified the other way round: create the tell-tale role on the local test
// server and confirm the guard sees it.
func TestAssertSafeTarget_AgainstRealServer(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, DSN())
	require.NoError(t, err)
	// Deferred LIFO: the role drop must run while the connection is still open.
	// t.Cleanup would NOT do -- it runs after every defer, so the DROP would hit
	// a closed connection, and a leftover rdsadmin role makes the guard reject
	// this database on every subsequent run.
	defer conn.Close(ctx)

	// Clean server: the guard must let the real test database through.
	require.NoError(t, assertSafeTarget(ctx, conn))

	if _, err := conn.Exec(ctx, "CREATE ROLE rdsadmin NOLOGIN"); err != nil {
		t.Skipf("cannot create roles as this user, skipping live guard check: %v", err)
	}
	defer func() {
		_, dropErr := conn.Exec(context.Background(), "DROP ROLE IF EXISTS rdsadmin")
		require.NoError(t, dropErr, "leftover rdsadmin role would block every later test run")
	}()

	err = assertSafeTarget(ctx, conn)
	require.Error(t, err, "guard must reject a server reporting rdsadmin")
	require.Contains(t, err.Error(), "AWS RDS")
	require.Contains(t, err.Error(), "tunnel")
}
