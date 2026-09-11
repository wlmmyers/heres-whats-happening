package testdb

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// With no TEST_DB_* set, DSN() must equal the value docker-compose + CI rely on.
// Parsed rather than a whole-string comparison: dsn.Components.DSN() also sets
// an "options" query parameter (pg_trgm.similarity_threshold), so the exact
// query string ordering isn't part of this test's contract.
func TestDSN_DefaultWhenUnset(t *testing.T) {
	for _, k := range []string{"TEST_DB_USER", "TEST_DB_PASSWORD", "TEST_DB_HOST", "TEST_DB_PORT", "TEST_DB_NAME", "TEST_DB_SSLMODE"} {
		t.Setenv(k, "")
	}
	u, err := url.Parse(DSN())
	require.NoError(t, err)
	require.Equal(t, "postgres", u.Scheme)
	require.Equal(t, "localhost:5432", u.Host)
	require.Equal(t, "/appdb_test", u.Path)
	require.Equal(t, "disable", u.Query().Get("sslmode"))
	require.Equal(t, "-c pg_trgm.similarity_threshold=0.2", u.Query().Get("options"))
}

func TestDSN_OverlaysEnv(t *testing.T) {
	t.Setenv("TEST_DB_HOST", "db.internal")
	t.Setenv("TEST_DB_PASSWORD", "s3cr3t")
	got := DSN()
	require.Contains(t, got, "app:s3cr3t@db.internal:5432/appdb_test")
}
