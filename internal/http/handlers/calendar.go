package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type calendarEvent struct {
	ID             string        `json:"id"`
	Title          string        `json:"title"`
	Description    string        `json:"description,omitempty"`
	StartsAt       string        `json:"starts_at"`
	EndsAt         string        `json:"ends_at,omitempty"`
	ImageURL       string        `json:"image_url,omitempty"`
	URL            string        `json:"url,omitempty"`
	Venue          calendarVenue `json:"venue"`
	Score          float64       `json:"score"`
	MatchedBecause calendarMatch `json:"matched_because"`
}

type calendarVenue struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

type calendarMatch struct {
	Performers []string `json:"performers"`
	Genres     []string `json:"genres"`
}

type calendarResponse struct {
	Events []calendarEvent `json:"events"`
}

// GetMyCalendar returns the authenticated user's matched events whose
// starts_at falls in [from, to). Dates are YYYY-MM-DD.
func GetMyCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		fromStr := r.URL.Query().Get("from")
		toStr := r.URL.Query().Get("to")
		if fromStr == "" || toStr == "" {
			httperr.Write(w, http.StatusBadRequest, "missing_range", "from and to query params are required (YYYY-MM-DD)")
			return
		}
		from, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_from", "from must be YYYY-MM-DD")
			return
		}
		to, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_to", "to must be YYYY-MM-DD")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.GetUserCalendarInRange(ctx, store.GetUserCalendarInRangeParams{
			UserID:     pgtype.UUID{Bytes: uid, Valid: true},
			StartsAt:   pgtype.Timestamptz{Time: from, Valid: true},
			StartsAt_2: pgtype.Timestamptz{Time: to, Valid: true},
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load calendar", err)
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
		for _, row := range rows {
			bd := parseBreakdown(row.ScoreBreakdown)
			ev := calendarEvent{
				ID:          uuidString(row.EventID),
				Title:       row.Title,
				Description: row.Description,
				Score:       row.Score,
				StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				MatchedBecause: bd,
			}
			if row.EndsAt.Valid {
				ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
			}
			ev.ImageURL = textPtrToString(row.ImageUrl)
			ev.URL = textPtrToString(row.Url)
			out.Events = append(out.Events, ev)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// parseBreakdown unmarshals a user_event_match.score_breakdown JSON blob
// into the matched_because shape. Empty input → empty (non-nil) slices.
func parseBreakdown(raw []byte) calendarMatch {
	bd := calendarMatch{Performers: []string{}, Genres: []string{}}
	if len(raw) == 0 {
		return bd
	}
	var in struct {
		Performers []string `json:"matched_performers"`
		Genres     []string `json:"matched_genres"`
	}
	_ = json.Unmarshal(raw, &in)
	if in.Performers != nil {
		bd.Performers = in.Performers
	}
	if in.Genres != nil {
		bd.Genres = in.Genres
	}
	return bd
}

func textPtrToString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func uuidString(u pgtype.UUID) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i := 0; i < 16; i++ {
		b := u.Bytes[i]
		out[j] = hex[b>>4]
		out[j+1] = hex[b&0x0F]
		j += 2
		switch i {
		case 3, 5, 7, 9:
			out[j] = '-'
			j++
		}
	}
	return string(out)
}

// GetEventByIDForUser returns one event with the user's match info (or
// score=0 + empty matched_because if the user doesn't have a match for it).
func GetEventByIDForUser(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		idStr := chi.URLParam(r, "id")
		eventUUID, err := uuid.Parse(idStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_id", "id is not a valid uuid")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetMatchedEventForUser(ctx, store.GetMatchedEventForUserParams{
			ID:     pgtype.UUID{Bytes: eventUUID, Valid: true},
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
		})
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "not_found", "event not found")
			return
		}

		bd := parseBreakdown(row.ScoreBreakdown)
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
			MatchedBecause: bd,
		}
		if row.EndsAt.Valid {
			ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
		}
		ev.ImageURL = textPtrToString(row.ImageUrl)
		ev.URL = textPtrToString(row.Url)
		writeJSON(w, http.StatusOK, ev)
	}
}
