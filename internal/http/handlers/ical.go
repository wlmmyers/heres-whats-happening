package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/ical"
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

// DeleteIcalToken removes the user's iCal subscription token. The previously
// issued URL stops working immediately.
func DeleteIcalToken(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.DeleteIcalTokenByUser(ctx, pgtype.UUID{Bytes: uid, Valid: true}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete token")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetIcalFeed serves an RFC 5545 calendar for the user identified by the
// token in the URL path. No Authorization header — calendar apps don't
// support custom headers on subscriptions, so the token IS the credential.
// Lookback window: next 60 days of matched events.
func GetIcalFeed(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		token = strings.TrimSuffix(token, ".ics")
		if token == "" {
			http.NotFound(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetIcalTokenByHash(ctx, auth.HashRefresh(token))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = q.UpdateIcalTokenLastAccessed(ctx, row.UserID)

		now := time.Now().UTC()
		from := pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true}
		to := pgtype.Timestamptz{Time: now.AddDate(0, 0, 60), Valid: true}

		rows, err := q.GetUserCalendarInRange(ctx, store.GetUserCalendarInRangeParams{
			UserID:     row.UserID,
			StartsAt:   from,
			StartsAt_2: to,
		})
		if err != nil {
			http.Error(w, "could not load events", http.StatusInternalServerError)
			return
		}

		evs := make([]ical.Event, 0, len(rows))
		for _, e := range rows {
			ev := ical.Event{
				UID:       fmt.Sprintf("event-%s@example.com", uuidString(e.EventID)),
				Title:     e.Title,
				StartsAt:  e.StartsAt.Time,
				VenueName: e.VenueName,
				VenueAddr: textPtrToString(e.VenueAddress),
				URL:       textPtrToString(e.Url),
			}
			if e.EndsAt.Valid {
				ev.EndsAt = e.EndsAt.Time
			}
			ev.Description = buildIcalDescription(e.ScoreBreakdown, e.Description)
			evs = append(evs, ev)
		}
		body := ical.FormatCalendar("Your Matched Events", now, evs)

		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("X-Published-Ttl", "PT1H")
		_, _ = w.Write([]byte(body))
	}
}

func buildIcalDescription(breakdown []byte, eventDescription string) string {
	var because string
	if len(breakdown) > 0 {
		var raw struct {
			Performers []string `json:"matched_performers"`
			Genres     []string `json:"matched_genres"`
		}
		_ = json.Unmarshal(breakdown, &raw)
		bits := []string{}
		bits = append(bits, raw.Performers...)
		bits = append(bits, raw.Genres...)
		if len(bits) > 0 {
			because = "Matched because: " + strings.Join(bits, ", ")
		}
	}
	switch {
	case because == "" && eventDescription == "":
		return ""
	case because == "":
		return eventDescription
	case eventDescription == "":
		return because
	default:
		return because + "\n\n" + eventDescription
	}
}
