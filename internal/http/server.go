package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type Server struct {
	Addr          string
	DB            *pgxpool.Pool
	Queries       *store.Queries
	JWTSigner     *auth.JWTSigner
	RefreshTTL    time.Duration
	DefaultCityID string
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	// Public
	r.Get("/healthz", handlers.Healthz())
	r.Get("/readyz", handlers.Readyz(s.DB))

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
		r.Get("/me/interests", handlers.ListInterests(s.Queries))
		r.Post("/me/interests", handlers.CreateInterest(s.Queries))
		r.Delete("/me/interests/{id}", handlers.DeleteInterest(s.Queries))
	})

	return r
}

func (s *Server) Run(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
