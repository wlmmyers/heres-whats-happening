package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/dsn"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	JWTSigningKey string
	JWTAccessTTL  time.Duration
	RefreshTTL    time.Duration
	LogLevel      string

	// Plan 2 additions
	AWSRegion          string
	DBSecretARN        string
	SQSEndpoint        string
	EventsQueueURL     string
	IngestWorkers      int
	TicketmasterAPIKey string
	TicketmasterCity   string

	// Plan 3 additions
	SpotifyClientID     string
	SpotifyClientSecret string
	SpotifyRedirectURI  string
	SpotifyTokenEncKey  []byte
	InterestsQueueURL   string

	// Plan 4 additions
	TEIEndpoint string

	// Plan 5 additions
	IcalBaseURL string

	// Plan 6 additions
	CORSAllowedOrigins []string

	// Plan 7 additions
	TrustProxy bool

	// Plan 8 additions
	EmailSender      string
	EmailFromAddress string
	AppBaseURL       string
	APIBaseURL       string

	// Poster proxy additions
	PosterFunctionURL string
	PostersBucket     string
}

func Load() (*Config, error) {
	dbComponents, err := dsn.FromEnv("DB_")
	if err != nil {
		return nil, err
	}
	dbURL := dbComponents.DSN()
	signingKey := os.Getenv("JWT_SIGNING_KEY")
	if signingKey == "" {
		return nil, errors.New("JWT_SIGNING_KEY is required")
	}

	accessTTL, err := parseDuration("JWT_ACCESS_TTL", "15m")
	if err != nil {
		return nil, err
	}
	refreshTTL, err := parseDuration("REFRESH_TTL", "720h")
	if err != nil {
		return nil, err
	}

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	workers := 4
	if v := os.Getenv("INGEST_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid INGEST_WORKERS=%q", v)
		}
		workers = n
	}

	var encKey []byte
	if v := os.Getenv("SPOTIFY_TOKEN_ENC_KEY"); v != "" {
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("SPOTIFY_TOKEN_ENC_KEY: %w", err)
		}
		encKey = decoded
	}

	var corsOrigins []string
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				corsOrigins = append(corsOrigins, o)
			}
		}
	}

	trustProxy := false
	if v := os.Getenv("TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid TRUST_PROXY=%q: %w", v, err)
		}
		trustProxy = b
	}

	emailSender := os.Getenv("EMAIL_SENDER")
	emailFrom := os.Getenv("EMAIL_FROM_ADDRESS")
	appBaseURL := os.Getenv("APP_BASE_URL")
	apiBaseURL := os.Getenv("API_BASE_URL")

	// Email confirmation is unconditional, so its configuration is required —
	// there is no mode left that makes these optional.
	//
	// Fail fast rather than warn (the TRUST_PROXY precedent does not fit): a
	// misconfigured rate limiter degrades availability and self-heals, but a
	// half-configured mailer strands every new signup with no signal and no way
	// in. A crashlooping task fails the ECS rolling deploy and leaves the
	// previous version serving, which is the louder and safer failure.
	var missing []string
	for _, v := range []struct {
		name, val string
	}{
		{"EMAIL_SENDER", emailSender},
		{"EMAIL_FROM_ADDRESS", emailFrom},
		{"APP_BASE_URL", appBaseURL},
		{"API_BASE_URL", apiBaseURL},
	} {
		if v.val == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("email confirmation requires %s", strings.Join(missing, ", "))
	}
	if emailSender != "ses" && emailSender != "log" {
		return nil, fmt.Errorf("invalid EMAIL_SENDER=%q (want ses or log)", emailSender)
	}

	cfg := &Config{
		DatabaseURL:         dbURL,
		HTTPAddr:            addr,
		JWTSigningKey:       signingKey,
		JWTAccessTTL:        accessTTL,
		RefreshTTL:          refreshTTL,
		LogLevel:            logLevel,
		AWSRegion:           os.Getenv("AWS_REGION"),
		DBSecretARN:         os.Getenv("DB_SECRET_ARN"),
		SQSEndpoint:         os.Getenv("SQS_ENDPOINT"),
		EventsQueueURL:      os.Getenv("EVENTS_QUEUE_URL"),
		IngestWorkers:       workers,
		TicketmasterAPIKey:  os.Getenv("TICKETMASTER_API_KEY"),
		TicketmasterCity:    os.Getenv("TICKETMASTER_CITY"),
		SpotifyClientID:     os.Getenv("SPOTIFY_CLIENT_ID"),
		SpotifyClientSecret: os.Getenv("SPOTIFY_CLIENT_SECRET"),
		SpotifyRedirectURI:  os.Getenv("SPOTIFY_REDIRECT_URI"),
		SpotifyTokenEncKey:  encKey,
		InterestsQueueURL:   os.Getenv("INTERESTS_QUEUE_URL"),
		TEIEndpoint:         os.Getenv("TEI_ENDPOINT"),
		IcalBaseURL:         os.Getenv("ICAL_BASE_URL"),
		CORSAllowedOrigins:  corsOrigins,
		TrustProxy:          trustProxy,

		EmailSender:      emailSender,
		EmailFromAddress: emailFrom,
		AppBaseURL:       appBaseURL,
		APIBaseURL:       apiBaseURL,

		PosterFunctionURL: os.Getenv("POSTER_FUNCTION_URL"),
		PostersBucket:     os.Getenv("POSTERS_BUCKET"),
	}
	return cfg, nil
}

func parseDuration(envKey, fallback string) (time.Duration, error) {
	v := os.Getenv(envKey)
	if v == "" {
		v = fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: %w", envKey, v, err)
	}
	return d, nil
}
