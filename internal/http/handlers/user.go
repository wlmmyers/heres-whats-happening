package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

func GetMe(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		// Convert google uuid.UUID to pgtype.UUID for the sqlc-generated query.
		row, err := q.GetUserByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "no_user", "user not found")
			return
		}
		writeJSON(w, http.StatusOK, userOut{
			ID:    uid.String(),
			Email: row.Email,
		})
	}
}

func DeleteMe(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.SoftDeleteUser(ctx, pgtype.UUID{Bytes: uid, Valid: true}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete user")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
