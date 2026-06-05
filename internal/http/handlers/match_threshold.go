package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

const (
	minMatchThreshold = 0.20
	maxMatchThreshold = 0.60
)

type updateMatchThresholdRequest struct {
	Threshold float64 `json:"threshold"`
}

// UpdateMatchThreshold persists the caller's per-user score threshold and kicks
// off an in-process, single-user re-score (no re-embedding). It responds 202
// immediately; the recompute runs in a background goroutine whose errors are
// only logged (the client has already received its response).
func UpdateMatchThreshold(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		var req updateMatchThresholdRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		if req.Threshold < minMatchThreshold || req.Threshold > maxMatchThreshold {
			httperr.Write(w, http.StatusBadRequest, "invalid_threshold",
				"threshold must be between 0.20 and 0.60")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		th := req.Threshold
		pgUID := pgtype.UUID{Bytes: uid, Valid: true}
		if err := q.UpdateUserScoreThreshold(ctx, store.UpdateUserScoreThresholdParams{
			ID:             pgUID,
			ScoreThreshold: &th,
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not update threshold")
			return
		}

		// Recompute this user's matches in the background. Use a fresh context —
		// the request context is cancelled once we respond below.
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := matcher.RescoreUser(bg, q, pgUID); err != nil {
				log.Printf("rescore user %s after threshold change: %v", uid, err)
			}
		}()

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "recomputing"})
	}
}
