package http

import (
	"context"
	"log"
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

	// Rate limiters for the public auth surface. All in-process: state resets on
	// restart, which is acceptable while the API runs a single task.
	signupLimiter := ratelimit.NewMemory(3, time.Hour)
	loginLimiter := ratelimit.NewMemory(10, time.Minute)
	refreshLimiter := ratelimit.NewMemory(30, time.Minute)
	logoutLimiter := ratelimit.NewMemory(30, time.Minute)
	icalFeedLimiter := ratelimit.NewMemory(60, time.Minute)
	readyzLimiter := ratelimit.NewMemory(30, time.Minute)

	// Authenticated, keyed on user ID. The net covers every route in the group,
	// including ones added later; the rest stack on top of it.
	authedLimiter := ratelimit.NewMemory(120, time.Minute)
	authedWriteLimiter := ratelimit.NewMemory(30, time.Minute)
	manualInterestsLimiter := ratelimit.NewMemory(60, time.Hour)
	spotifyExchangeLimiter := ratelimit.NewMemory(10, time.Hour)
	icalTokenLimiter := ratelimit.NewMemory(10, time.Hour)

	// Public
	//
	// /healthz is deliberately NOT rate limited: it is the ALB health check
	// target (terraform/prod/alb.tf), so limiting it would let a flood fail the
	// health check and cycle healthy tasks. It does no work beyond writing a
	// static body, so there is nothing to protect.
	r.Get("/healthz", handlers.Healthz())
	r.With(middleware.RateLimit(readyzLimiter, middleware.EndpointReadyz)).
		Get("/readyz", handlers.Readyz(s.DB))
	// Public iCal feed — token in URL is the credential. The 32-byte token is
	// not guessable; the limit caps DB lookups and calendar renders from any one
	// source. 60/min is ~1 req/sec, far above real calendar-client polling.
	r.With(middleware.RateLimit(icalFeedLimiter, middleware.EndpointIcalFeed)).
		Get("/ical/{token}", handlers.GetIcalFeed(s.Queries))

	// Auth (public)
	r.With(middleware.RateLimitOnSuccess(signupLimiter, middleware.EndpointSignup)).
		Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
	r.With(middleware.RateLimit(loginLimiter, middleware.EndpointLogin)).
		Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
	r.With(middleware.RateLimit(refreshLimiter, middleware.EndpointRefresh)).
		Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
	r.With(middleware.RateLimit(logoutLimiter, middleware.EndpointLogout)).
		Post("/auth/logout", handlers.Logout(s.Queries))

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		// Safety net across the whole group. Installed with Use, not With, so a
		// route added later is covered by default rather than silently unlimited.
		// This line must stay above every nested r.Group below: chi copies the
		// middleware stack by value at Group()/With() time, so a group inserted
		// above it would snapshot an incomplete stack and its routes would be
		// silently unlimited.
		r.Use(middleware.RateLimitByUser(authedLimiter, middleware.EndpointAuthed))

		// Reads — covered by the net alone.
		r.Get("/me", handlers.GetMe(s.Queries))
		r.Get("/me/manual-interests", handlers.ListManualInterests(s.Queries))
		r.Get("/me/spotify-interests", handlers.SpotifyInterests(s.Queries))
		r.Get("/integrations/spotify/connect", handlers.SpotifyConnect(s.SpotifyClient, s.OAuthHMACKey))
		r.Get("/integrations/spotify/status", handlers.SpotifyStatus(s.Queries))
		r.Get("/me/calendar", handlers.GetMyCalendar(s.Queries))
		r.Get("/events/{id}", handlers.GetEventByIDForUser(s.Queries))

		// Writes. A nested group states the limiter once; chi composes it with
		// the outer net, so these routes pass through both.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite))
			r.Delete("/me", handlers.DeleteMe(s.Queries))
			r.Patch("/me/match-threshold", handlers.UpdateMatchThreshold(s.Queries))
			r.Post("/me/not-interested", handlers.AddNotInterested(s.Queries))
			r.Delete("/me/not-interested", handlers.ResetNotInterested(s.Queries))
			r.Delete("/integrations/spotify", handlers.SpotifyDisconnect(s.Queries))
			r.Delete("/me/ical-token", handlers.DeleteIcalToken(s.Queries))
		})

		// Both publish to the interests queue, so both cost downstream compute.
		// One shared budget, so exhausting adds never blocks deletes.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimitByUser(manualInterestsLimiter, middleware.EndpointManualInterests))
			r.Post("/me/manual-interests", handlers.CreateManualInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
			r.Delete("/me/manual-interests/{id}", handlers.DeleteManualInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
		})

		// Spends Spotify API quota that is shared across all users, so one
		// abusive account can break the integration for everyone.
		r.With(middleware.RateLimitByUser(spotifyExchangeLimiter, middleware.EndpointSpotifyExchange)).
			Post("/integrations/spotify/exchange", handlers.SpotifyExchange(
				s.Queries, s.SpotifyClient, s.SpotifyCipher, s.OAuthHMACKey,
				s.QueuePublisher, s.InterestsQueueURL))

		// Mints a fresh token on every call.
		r.With(middleware.RateLimitByUser(icalTokenLimiter, middleware.EndpointIcalToken)).
			Post("/me/ical-token", handlers.CreateIcalToken(s.Queries, s.IcalBaseURL))
	})

	return r
}

func (s *Server) Run(ctx context.Context) error {
	if w := s.trustProxyWarning(); w != "" {
		log.Print(w)
	}

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

// trustProxyWarning returns a startup warning when rate limiting is active but
// the client IP is NOT being taken from the proxy's X-Forwarded-For header. In
// that mode every request keys on r.RemoteAddr; behind a proxy or load balancer
// that is a single shared address, so all callers collapse into one rate-limit
// bucket and the limits apply site-wide. It returns "" when TrustProxy is set.
//
// This is deliberately loud: the env var that enables trust (TRUST_PROXY) reaches
// the running ECS task only via a manual taskdef-edit.sh step, so a deploy that
// forgets it would silently throttle every user. The warning is a false alarm in
// local development (direct connections, where RemoteAddr is the real client) —
// there it just states the keying mode.
func (s *Server) trustProxyWarning() string {
	if s.TrustProxy {
		return ""
	}
	return "WARNING: TRUST_PROXY is not set — rate limiting will key on RemoteAddr. " +
		"This is correct for direct connections but WRONG behind a proxy/ALB, where all " +
		"clients share one address and the rate limits apply site-wide. Set TRUST_PROXY=true " +
		"when running behind the load balancer."
}
