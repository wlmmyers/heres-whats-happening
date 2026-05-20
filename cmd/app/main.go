package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/joho/godotenv"

	"github.com/google/uuid"
	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/db"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

func main() {
	_ = godotenv.Load() // ignore error if no .env
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: app <subcommand>

subcommands:
  serve   run the HTTP API server
`)
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	q := store.New(pool)
	city, err := q.GetDefaultCity(ctx)
	if err != nil {
		return fmt.Errorf("load default city: %w", err)
	}

	s := &hs.Server{
		Addr:          cfg.HTTPAddr,
		DB:            pool,
		Queries:       q,
		JWTSigner:     auth.NewJWTSigner(cfg.JWTSigningKey, cfg.JWTAccessTTL),
		RefreshTTL:    cfg.RefreshTTL,
		DefaultCityID: cityIDString(city.ID),
	}
	fmt.Printf("listening on %s\n", cfg.HTTPAddr)
	return s.Run(ctx)
}

func cityIDString(u pgtype.UUID) string {
	return uuid.UUID(u.Bytes).String()
}
