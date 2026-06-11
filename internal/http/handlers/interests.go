package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type interestOut struct {
	ID              string  `json:"id"`
	Value           string  `json:"value"`
	NormalizedValue string  `json:"normalized_value"`
	Weight          float64 `json:"weight"`
	CreatedAt       string  `json:"created_at"`
}

type listInterestsResponse struct {
	Interests []interestOut `json:"interests"`
}

func ListInterests(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		rows, err := q.ListManualInterestsByUser(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not list interests", err)
			return
		}
		out := make([]interestOut, 0, len(rows))
		for _, row := range rows {
			out = append(out, interestOut{
				ID:              uuid.UUID(row.ID.Bytes).String(),
				Value:           row.Value,
				NormalizedValue: row.NormalizedValue,
				Weight:          row.Weight,
				CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, listInterestsResponse{Interests: out})
	}
}

type createInterestRequest struct {
	Value string `json:"value"`
}

// publishEmbed asks the interest consumer to re-embed the user. Best-effort:
// a nil publisher / empty queue URL (local dev without SQS) is a no-op, and a
// send failure is logged, not returned — the daily match batch is the backstop.
func publishEmbed(ctx context.Context, pub CallbackPublisher, queueURL string, uid uuid.UUID) {
	if pub == nil || queueURL == "" {
		return
	}
	body, err := json.Marshal(events.InterestMessage{
		UserID: uid.String(),
		Op:     events.OpOnlyEmbed,
	})
	if err != nil {
		log.Printf("interests: marshal embed message: %v", err)
		return
	}
	if err := pub.Send(ctx, queueURL, body); err != nil {
		log.Printf("interests: publish embed message: %v", err)
	}
}

func CreateInterest(q *store.Queries, pub CallbackPublisher, queueURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		var req createInterestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		req.Value = strings.TrimSpace(req.Value)
		if req.Value == "" {
			httperr.Write(w, http.StatusBadRequest, "empty_value", "value must not be empty")
			return
		}
		normalized := strings.ToLower(req.Value)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		row, err := q.CreateManualInterest(ctx, store.CreateManualInterestParams{
			UserID:          pgtype.UUID{Bytes: uid, Valid: true},
			Value:           req.Value,
			NormalizedValue: normalized,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				httperr.Write(w, http.StatusConflict, "duplicate_interest", "this interest already exists")
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not create interest", err)
			return
		}
		writeJSON(w, http.StatusCreated, interestOut{
			ID:              uuid.UUID(row.ID.Bytes).String(),
			Value:           row.Value,
			NormalizedValue: row.NormalizedValue,
			Weight:          row.Weight,
			CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
		publishEmbed(ctx, pub, queueURL, uid)
	}
}

func DeleteInterest(q *store.Queries, pub CallbackPublisher, queueURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_id", "id is not a valid uuid")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.DeleteInterestByIDForUser(ctx, store.DeleteInterestByIDForUserParams{
			ID:     pgtype.UUID{Bytes: id, Valid: true},
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
		}); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not delete interest", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		publishEmbed(ctx, pub, queueURL, uid)
	}
}
