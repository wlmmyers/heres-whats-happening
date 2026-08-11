package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad_AllFieldsParsed(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("JWT_ACCESS_TTL", "10m")
	t.Setenv("REFRESH_TTL", "100h")
	t.Setenv("LOG_LEVEL", "info")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://app:pw@localhost:5432/appdb?sslmode=disable", cfg.DatabaseURL)
	require.Equal(t, ":9999", cfg.HTTPAddr)
	require.Equal(t, "k", cfg.JWTSigningKey)
	require.Equal(t, 10*time.Minute, cfg.JWTAccessTTL)
	require.Equal(t, 100*time.Hour, cfg.RefreshTTL)
	require.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	t.Setenv("JWT_SIGNING_KEY", "k")
	_, err := Load()
	require.Error(t, err)
}

func TestLoad_QueueAndScraperFields(t *testing.T) {
	setRequiredDB(t)
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
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 4, cfg.IngestWorkers) // default
}

func TestLoad_SpotifyAndCryptoFields(t *testing.T) {
	setRequiredDB(t)
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
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("SPOTIFY_TOKEN_ENC_KEY", "not-valid-base64!@#")
	_, err := Load()
	require.Error(t, err)
}

func TestLoad_TEIEndpoint(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("TEI_ENDPOINT", "http://localhost:8081")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8081", cfg.TEIEndpoint)
}

func TestLoad_IcalBaseURL(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("ICAL_BASE_URL", "http://localhost:8080")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080", cfg.IcalBaseURL)
}

func TestLoad_PosterFields(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("POSTER_FUNCTION_URL", "https://poster.example.com/lambda-url")
	t.Setenv("POSTERS_BUCKET", "posters-bucket")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "https://poster.example.com/lambda-url", cfg.PosterFunctionURL)
	require.Equal(t, "posters-bucket", cfg.PostersBucket)
}

func TestLoad_CORSAllowedOrigins(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://example.com, https://staging.example.com")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.com", "https://staging.example.com"}, cfg.CORSAllowedOrigins)
}

func TestLoad_DBSecretARN(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("DB_SECRET_ARN", "arn:aws:secretsmanager:us-east-1:1:secret:rds!db-x")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "arn:aws:secretsmanager:us-east-1:1:secret:rds!db-x", cfg.DBSecretARN)
}

func TestLoad_TrustProxy(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")

	t.Setenv("TRUST_PROXY", "")
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.TrustProxy, "must default to false so local dev never trusts XFF")

	t.Setenv("TRUST_PROXY", "true")
	cfg, err = Load()
	require.NoError(t, err)
	require.True(t, cfg.TrustProxy)

	t.Setenv("TRUST_PROXY", "banana")
	_, err = Load()
	require.Error(t, err)
}

// setRequiredDB sets the DB_* component vars plus the mail vars every Load()
// call now needs. Email confirmation is unconditional, so a Load() without the
// mail vars is an error in every test, not just the confirmation ones.
func setRequiredDB(t *testing.T) {
	t.Helper()
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "pw")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "appdb")
	t.Setenv("DB_SSLMODE", "disable")
	t.Setenv("EMAIL_SENDER", "log")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	t.Setenv("API_BASE_URL", "http://localhost:8080")
}

// setMinimalEnv sets everything Load() needs except the mail vars, which it
// clears so a stray value in the environment cannot mask a validation bug.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "test-key-test-key-test-key-32xx")
	t.Setenv("EMAIL_SENDER", "")
	t.Setenv("EMAIL_FROM_ADDRESS", "")
	t.Setenv("APP_BASE_URL", "")
	t.Setenv("API_BASE_URL", "")
}

// A half-configured mailer strands every new signup with no way in, so Load
// refuses to start rather than warn.
func TestLoad_RequiresEveryMailVar(t *testing.T) {
	setMinimalEnv(t)
	_, err := Load()
	require.Error(t, err)
	for _, want := range []string{"EMAIL_SENDER", "EMAIL_FROM_ADDRESS", "APP_BASE_URL", "API_BASE_URL"} {
		require.Contains(t, err.Error(), want)
	}
}

func TestLoad_NamesOnlyTheMissingMailVars(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_SENDER", "log")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	_, err := Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "API_BASE_URL")
	require.NotContains(t, err.Error(), "EMAIL_SENDER")
}

func TestLoad_RejectsUnknownEmailSender(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_SENDER", "sendgrid")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	t.Setenv("API_BASE_URL", "http://localhost:8080")
	_, err := Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "EMAIL_SENDER")
}

func TestLoad_FullyConfiguredMailSucceeds(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_SENDER", "log")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	t.Setenv("API_BASE_URL", "http://localhost:8080")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "log", cfg.EmailSender)
	require.Equal(t, "dev@localhost", cfg.EmailFromAddress)
	require.Equal(t, "http://localhost:5173", cfg.AppBaseURL)
	require.Equal(t, "http://localhost:8080", cfg.APIBaseURL)
}
