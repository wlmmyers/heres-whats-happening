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
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
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

	// Plan 7 addition — when true, derive the client IP from the rightmost
	// X-Forwarded-For entry. Set only when running behind our ALB.
	TrustProxy bool
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(middleware.ClientIPResolver(s.TrustProxy))
	if len(s.CORSAllowedOrigins) > 0 {
		r.Use(middleware.CORS(s.CORSAllowedOrigins))
	}
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	// Rate limiters for the public auth surface. Signup is Postgres-backed so
	// the ceiling survives restarts; login and refresh are in-process, which is
	// accurate enough for limits this loose.
	signupLimiter := ratelimit.NewPostgres(s.Queries, "signup", 3, time.Hour)
	loginLimiter := ratelimit.NewMemory(10, time.Minute)
	refreshLimiter := ratelimit.NewMemory(30, time.Minute)

	// Public
	r.Get("/healthz", handlers.Healthz())
	r.Get("/readyz", handlers.Readyz(s.DB))
	// Public iCal feed — token in URL is the credential.
	r.Get("/ical/{token}", handlers.GetIcalFeed(s.Queries))

	// Auth (public)
	r.With(middleware.RateLimitOnSuccess(signupLimiter, "signup")).
		Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
	r.With(middleware.RateLimit(loginLimiter, "login")).
		Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
	r.With(middleware.RateLimit(refreshLimiter, "refresh")).
		Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
	r.Post("/auth/logout", handlers.Logout(s.Queries))

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		r.Get("/me", handlers.GetMe(s.Queries))
		r.Delete("/me", handlers.DeleteMe(s.Queries))
		r.Patch("/me/match-threshold", handlers.UpdateMatchThreshold(s.Queries))
		r.Get("/me/manual-interests", handlers.ListManualInterests(s.Queries))
		r.Post("/me/manual-interests", handlers.CreateManualInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
		r.Delete("/me/manual-interests/{id}", handlers.DeleteManualInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
		r.Get("/me/spotify-interests", handlers.SpotifyInterests(s.Queries))
		r.Get("/integrations/spotify/connect", handlers.SpotifyConnect(s.SpotifyClient, s.OAuthHMACKey))
		r.Get("/integrations/spotify/status", handlers.SpotifyStatus(s.Queries))
		r.Post("/integrations/spotify/exchange", handlers.SpotifyExchange(
			s.Queries, s.SpotifyClient, s.SpotifyCipher, s.OAuthHMACKey,
			s.QueuePublisher, s.InterestsQueueURL))
		r.Delete("/integrations/spotify", handlers.SpotifyDisconnect(s.Queries))
		r.Get("/me/calendar", handlers.GetMyCalendar(s.Queries))
		r.Post("/me/not-interested", handlers.AddNotInterested(s.Queries))
		r.Delete("/me/not-interested", handlers.ResetNotInterested(s.Queries))
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
