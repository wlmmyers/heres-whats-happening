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

type eventGoingRequest struct {
	EventID string `json:"event_id"`
}

// AddGoing records that the authenticated user is going to an event.
// Idempotent (ON CONFLICT DO NOTHING).
func AddGoing(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		var req eventGoingRequest
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
		if err := q.AddGoing(ctx, store.AddGoingParams{
			UserID:  pgtype.UUID{Bytes: uid, Valid: true},
			EventID: pgtype.UUID{Bytes: eventID, Valid: true},
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" { // FK violation
				httperr.Write(w, http.StatusBadRequest, "unknown_event", "event does not exist")
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not save going", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ResetGoing clears the authenticated user's entire going list.
func ResetGoing(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		eventID, err := uuid.Parse(r.URL.Query().Get("event_id"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_event", "event_id is not a valid uuid")
			return
		}
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		params := store.ClearGoingParams{
			UserID:  pgtype.UUID{Bytes: uid, Valid: true},
			EventID: pgtype.UUID{Bytes: eventID, Valid: true},
		}
		if err := q.ClearGoing(ctx, params); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not reset going", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func ListGoing(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		rows, err := q.ListGoing(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not list going", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "json_error", "could not encode response", err)
			return
		}
	}
}

// ListGoingEvents returns the user's going list as full calendar rows, in the
// same shape the calendar endpoints emit. ListGoing above returns bare ids —
// enough for EventCard to colour its toggle, but not enough to render a list —
// and the calendar's own pages can't stand in for this: they are paginated and
// upcoming-only, so a going event the user hasn't scrolled to, or has already
// attended, would simply be missing.
func ListGoingEvents(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.ListGoingEvents(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not list going events", err)
			return
		}

		// Non-nil so an empty going list marshals to [] rather than null.
		out := make([]calendarEvent, 0, len(rows))
		var artistIDs []pgtype.UUID
		for _, row := range rows {
			// NULL for an event marked going off the all-city calendar, which
			// has no user_event_match row to score it.
			var score float64
			if row.Score != nil {
				score = *row.Score
			}
			ev := calendarEvent{
				ID:          uuidString(row.EventID),
				Title:       row.Title,
				Description: row.Description,
				StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
				Score:       score,
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				MatchedBecause: parseBreakdown(row.ScoreBreakdown),
				artistID:       row.HeadlineArtistID,
			}
			if row.EndsAt.Valid {
				ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
			}
			ev.ImageURL = textPtrToString(row.ImageUrl)
			ev.URL = textPtrToString(row.Url)
			ev.Segment = textPtrToString(row.Segment)
			out = append(out, ev)
			if row.HeadlineArtistID.Valid {
				artistIDs = append(artistIDs, row.HeadlineArtistID)
			}
		}
		attachArtists(ctx, q, out, artistIDs)
		writeJSON(w, http.StatusOK, out)
	}
}
