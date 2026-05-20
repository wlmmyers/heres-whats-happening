package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	JWTSigningKey string
	JWTAccessTTL  time.Duration
	RefreshTTL    time.Duration
	LogLevel      string
}

func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
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

	return &Config{
		DatabaseURL:   dbURL,
		HTTPAddr:      addr,
		JWTSigningKey: signingKey,
		JWTAccessTTL:  accessTTL,
		RefreshTTL:    refreshTTL,
		LogLevel:      logLevel,
	}, nil
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
