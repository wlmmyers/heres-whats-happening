package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/joho/godotenv"

	"github.com/google/uuid"
	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/db"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/queue"
	"github.com/wmyers/heres-whats-happening/internal/scraper"
	spotifyscrape "github.com/wmyers/heres-whats-happening/internal/scraper/spotify"
	"github.com/wmyers/heres-whats-happening/internal/scraper/ticketmaster"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
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
	case "scrape":
		if err := scrape(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "scrape: %v\n", err)
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
  serve                       run the HTTP API server
  scrape events --source=NAME run a one-shot event scraper
  scrape spotify              scrape all connected users' Spotify data
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

	// Build SQS client and consumer if EVENTS_QUEUE_URL is set.
	var consumer *ingest.Consumer
	var qClient *queue.Client
	if cfg.EventsQueueURL != "" {
		qClient, err = queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
		if err != nil {
			return fmt.Errorf("queue client: %w", err)
		}
		h := ingest.NewEventHandler(q, city.ID)
		consumer = ingest.NewConsumer(qClient, cfg.EventsQueueURL, h, cfg.IngestWorkers, "events")
	}

	spClient := spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret, cfg.SpotifyRedirectURI, "")
	var cipher *crypto.Cipher
	if len(cfg.SpotifyTokenEncKey) > 0 {
		cipher, err = crypto.NewCipher(cfg.SpotifyTokenEncKey)
		if err != nil {
			return fmt.Errorf("crypto: %w", err)
		}
	}

	s := &hs.Server{
		Addr:              cfg.HTTPAddr,
		DB:                pool,
		Queries:           q,
		JWTSigner:         auth.NewJWTSigner(cfg.JWTSigningKey, cfg.JWTAccessTTL),
		RefreshTTL:        cfg.RefreshTTL,
		DefaultCityID:     cityIDString(city.ID),
		IngestConsumer:    consumer,
		SpotifyClient:     spClient,
		SpotifyCipher:     cipher,
		OAuthHMACKey:      []byte(cfg.JWTSigningKey),
		InterestsQueueURL: cfg.InterestsQueueURL,
		QueuePublisher:    qClient,
	}
	fmt.Printf("listening on %s (ingest workers=%d)\n", cfg.HTTPAddr, cfg.IngestWorkers)
	return s.Run(ctx)
}

func cityIDString(u pgtype.UUID) string {
	return uuid.UUID(u.Bytes).String()
}

func scrape(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`expected "app scrape events|spotify [...]"`)
	}
	switch args[0] {
	case "events":
		return scrapeEvents(args[1:])
	case "spotify":
		return scrapeSpotify(args[1:])
	default:
		return fmt.Errorf("unknown scrape target: %s", args[0])
	}
}

func scrapeEvents(args []string) error {
	fs := flag.NewFlagSet("scrape events", flag.ExitOnError)
	source := fs.String("source", "", "adapter name (e.g., ticketmaster)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("--source is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	qClient, err := queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}

	switch *source {
	case "ticketmaster":
		return runTicketmasterScrape(ctx, cfg, qClient)
	default:
		return fmt.Errorf("unknown source: %s", *source)
	}
}

func scrapeSpotify(args []string) error {
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

	qClient, err := queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}
	spClient := spotify.New(cfg.SpotifyClientID, cfg.SpotifyClientSecret, cfg.SpotifyRedirectURI, "")
	cipher, err := crypto.NewCipher(cfg.SpotifyTokenEncKey)
	if err != nil {
		return fmt.Errorf("crypto: %w", err)
	}

	adapter := spotifyscrape.NewAdapter(q, cipher, spClient, qClient, cfg.InterestsQueueURL)
	errs := adapter.ScrapeAll(ctx)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "scrape spotify error: %v\n", e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d users failed", len(errs))
	}
	return nil
}

func runTicketmasterScrape(ctx context.Context, cfg *config.Config, q *queue.Client) error {
	if cfg.TicketmasterAPIKey == "" {
		return fmt.Errorf("TICKETMASTER_API_KEY is required")
	}
	if cfg.TicketmasterCity == "" {
		return fmt.Errorf("TICKETMASTER_CITY is required")
	}
	a := ticketmaster.New("", cfg.TicketmasterAPIKey, cfg.TicketmasterCity)
	r := scraper.NewRunner(a, q, cfg.EventsQueueURL)
	fmt.Printf("scraping %s for city=%s ...\n", a.Name(), cfg.TicketmasterCity)
	return r.Run(ctx)
}
