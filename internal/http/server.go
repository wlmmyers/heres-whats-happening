package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type Server struct {
	Addr          string
	DB            *pgxpool.Pool
	Queries       *store.Queries
	JWTSigner     *auth.JWTSigner
	RefreshTTL    time.Duration
	DefaultCityID string

	// Optional. If non-nil, Run also starts the ingest consumer.
	IngestConsumer   *ingest.Consumer // events queue
	InterestConsumer *ingest.Consumer // interests queue

	SpotifyClient     *spotify.Client
	SpotifyCipher     *crypto.Cipher
	OAuthHMACKey      []byte
	InterestsQueueURL string
	QueuePublisher    handlers.CallbackPublisher // *queue.Client satisfies this

	// Plan 5 addition
	IcalBaseURL string

	// Plan 6 addition — list of Origin values to allow CORS for. If empty, CORS is disabled.
	CORSAllowedOrigins []string
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	if len(s.CORSAllowedOrigins) > 0 {
		r.Use(middleware.CORS(s.CORSAllowedOrigins))
	}
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	// Public
	r.Get("/healthz", handlers.Healthz())
	r.Get("/readyz", handlers.Readyz(s.DB))
	// Public iCal feed — token in URL is the credential.
	r.Get("/ical/{token}", handlers.GetIcalFeed(s.Queries))

	// Auth (public)
	r.Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
	r.Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
	r.Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
	r.Post("/auth/logout", handlers.Logout(s.Queries))

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		r.Get("/me", handlers.GetMe(s.Queries))
		r.Delete("/me", handlers.DeleteMe(s.Queries))
		r.Patch("/me/match-threshold", handlers.UpdateMatchThreshold(s.Queries))
		r.Get("/me/interests", handlers.ListInterests(s.Queries))
		r.Post("/me/interests", handlers.CreateInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
		r.Delete("/me/interests/{id}", handlers.DeleteInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
		r.Get("/integrations/spotify/connect", handlers.SpotifyConnect(s.SpotifyClient, s.OAuthHMACKey))
		r.Get("/integrations/spotify/status", handlers.SpotifyStatus(s.Queries))
		r.Post("/integrations/spotify/exchange", handlers.SpotifyExchange(
			s.Queries, s.SpotifyClient, s.SpotifyCipher, s.OAuthHMACKey,
			s.QueuePublisher, s.InterestsQueueURL))
		r.Delete("/integrations/spotify", handlers.SpotifyDisconnect(s.Queries))
		r.Get("/me/calendar", handlers.GetMyCalendar(s.Queries))
		r.Get("/events/{id}", handlers.GetEventByIDForUser(s.Queries))
		r.Post("/me/ical-token", handlers.CreateIcalToken(s.Queries, s.IcalBaseURL))
		r.Delete("/me/ical-token", handlers.DeleteIcalToken(s.Queries))
	})

	return r
}

func (s *Server) Run(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 3)
	go func() { errCh <- httpSrv.ListenAndServe() }()

	if s.IngestConsumer != nil {
		go func() { errCh <- s.IngestConsumer.Run(ctx) }()
	}
	if s.InterestConsumer != nil {
		go func() { errCh <- s.InterestConsumer.Run(ctx) }()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
