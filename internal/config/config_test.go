package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad_AllFieldsParsed(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("JWT_ACCESS_TTL", "10m")
	t.Setenv("REFRESH_TTL", "100h")
	t.Setenv("LOG_LEVEL", "info")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://x", cfg.DatabaseURL)
	require.Equal(t, ":9999", cfg.HTTPAddr)
	require.Equal(t, "k", cfg.JWTSigningKey)
	require.Equal(t, 10*time.Minute, cfg.JWTAccessTTL)
	require.Equal(t, 100*time.Hour, cfg.RefreshTTL)
	require.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SIGNING_KEY", "k")
	_, err := Load()
	require.Error(t, err)
}
