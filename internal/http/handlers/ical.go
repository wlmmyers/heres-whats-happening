package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type icalTokenResponse struct {
	URL string `json:"url"`
}

// CreateIcalToken generates a fresh 32-byte token, stores its sha256 hash in
// ical_tokens (UPSERT — rotates if a row already exists), and returns the
// subscription URL exactly once.
func CreateIcalToken(q *store.Queries, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		raw, err := auth.GenerateRefresh()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "token_failed", "could not generate token")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
			UserID:    pgtype.UUID{Bytes: uid, Valid: true},
			TokenHash: auth.HashRefresh(raw),
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist token")
			return
		}
		writeJSON(w, http.StatusCreated, icalTokenResponse{
			URL: baseURL + "/ical/" + raw + ".ics",
		})
	}
}
