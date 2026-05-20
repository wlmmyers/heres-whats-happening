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

func TestLoad_QueueAndScraperFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("SQS_ENDPOINT", "http://localhost:9324")
	t.Setenv("EVENTS_QUEUE_URL", "http://localhost:9324/000000000000/events-queue")
	t.Setenv("INGEST_WORKERS", "8")
	t.Setenv("TICKETMASTER_API_KEY", "tm-key")
	t.Setenv("TICKETMASTER_CITY", "Brooklyn")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "us-east-1", cfg.AWSRegion)
	require.Equal(t, "http://localhost:9324", cfg.SQSEndpoint)
	require.Equal(t, "http://localhost:9324/000000000000/events-queue", cfg.EventsQueueURL)
	require.Equal(t, 8, cfg.IngestWorkers)
	require.Equal(t, "tm-key", cfg.TicketmasterAPIKey)
	require.Equal(t, "Brooklyn", cfg.TicketmasterCity)
}

func TestLoad_IngestWorkersDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 4, cfg.IngestWorkers) // default
}

func TestLoad_SpotifyAndCryptoFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("SPOTIFY_CLIENT_ID", "cid")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "secret")
	t.Setenv("SPOTIFY_REDIRECT_URI", "http://localhost:8080/x")
	t.Setenv("SPOTIFY_TOKEN_ENC_KEY", "ZGV2LW9ubHkta2V5LWRldi1vbmx5LWtleS1kZXYtb24=")
	t.Setenv("INTERESTS_QUEUE_URL", "http://localhost:9324/000000000000/interests-queue")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "cid", cfg.SpotifyClientID)
	require.Equal(t, "secret", cfg.SpotifyClientSecret)
	require.Equal(t, "http://localhost:8080/x", cfg.SpotifyRedirectURI)
	require.Len(t, cfg.SpotifyTokenEncKey, 32)
	require.Equal(t, "http://localhost:9324/000000000000/interests-queue", cfg.InterestsQueueURL)
}

func TestLoad_BadEncKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("SPOTIFY_TOKEN_ENC_KEY", "not-valid-base64!@#")
	_, err := Load()
	require.Error(t, err)
}
