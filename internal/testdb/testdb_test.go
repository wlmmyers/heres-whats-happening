package testdb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// With no TEST_DB_* set, DSN() must equal the value docker-compose + CI rely on.
func TestDSN_DefaultWhenUnset(t *testing.T) {
	for _, k := range []string{"TEST_DB_USER", "TEST_DB_PASSWORD", "TEST_DB_HOST", "TEST_DB_PORT", "TEST_DB_NAME", "TEST_DB_SSLMODE"} {
		t.Setenv(k, "")
	}
	require.Equal(t, "postgres://app:app@localhost:5432/appdb_test?sslmode=disable", DSN())
}

func TestDSN_OverlaysEnv(t *testing.T) {
	t.Setenv("TEST_DB_HOST", "db.internal")
	t.Setenv("TEST_DB_PASSWORD", "s3cr3t")
	got := DSN()
	require.Contains(t, got, "app:s3cr3t@db.internal:5432/appdb_test")
}
