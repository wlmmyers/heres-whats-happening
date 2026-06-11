package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type notInterestedRequest struct {
	EventID string `json:"event_id"`
}

// AddNotInterested records that the authenticated user is not interested in an
// event, hiding it from their calendar. Idempotent (ON CONFLICT DO NOTHING).
func AddNotInterested(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		var req notInterestedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		eventID, err := uuid.Parse(req.EventID)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_event", "event_id is not a valid uuid")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.AddNotInterested(ctx, store.AddNotInterestedParams{
			UserID:  pgtype.UUID{Bytes: uid, Valid: true},
			EventID: pgtype.UUID{Bytes: eventID, Valid: true},
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" { // FK violation
				httperr.Write(w, http.StatusBadRequest, "unknown_event", "event does not exist")
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not save not-interested", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ResetNotInterested clears the authenticated user's entire not-interested list.
func ResetNotInterested(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.ClearNotInterested(ctx, pgtype.UUID{Bytes: uid, Valid: true}); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not reset not-interested", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
