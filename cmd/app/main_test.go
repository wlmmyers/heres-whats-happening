package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// runMigrate must fail fast with a clear, named-variable error when the DB_*
// components are absent — never a confusing url.Parse error or a hang trying to
// dial a half-formed DSN.
func TestRunMigrate_MissingDBEnv(t *testing.T) {
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	err := runMigrate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB_USER")
}
