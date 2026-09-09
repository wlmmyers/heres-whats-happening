package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// dateLayout is the wire format for a manually added show's day. Date-only on
// purpose: the row carries no time, so anything with a clock in it — an
// RFC3339 timestamp included — is rejected rather than silently truncated.
const dateLayout = "2006-01-02"

// manualEventNameLimit caps the two free-text fields. Generous for a band and
// a venue, small enough that the column is not a place to park a document.
const manualEventNameLimit = 200

type manualEventOut struct {
	ID        string `json:"id"`
	Date      string `json:"date"`
	EventName string `json:"event_name"`
	VenueName string `json:"venue_name"`
	CreatedAt string `json:"created_at"`
}

type listManualAddedGoingEventsResponse struct {
	Events []manualEventOut `json:"events"`
}

type manualEventRequest struct {
	Date      string `json:"date"`
	EventName string `json:"event_name"`
	VenueName string `json:"venue_name"`
}

// parsed is the validated form of a manual event request: a real calendar day
// and two non-empty names.
type manualEventParsed struct {
	date      pgtype.Date
	eventName string
	venueName string
}

// decodeManualEvent reads and validates the body shared by POST and PUT. It
// writes the error response itself and reports whether the caller may proceed.
func decodeManualEvent(w http.ResponseWriter, r *http.Request) (manualEventParsed, bool) {
	var req manualEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return manualEventParsed{}, false
	}
	// time.Parse with a date-only layout rejects both a bad day ("2026-13-45")
	// and anything carrying a time, which is what keeps the column honest.
	day, err := time.Parse(dateLayout, strings.TrimSpace(req.Date))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_date", "date must be a calendar day in YYYY-MM-DD form")
		return manualEventParsed{}, false
	}
	name := strings.TrimSpace(req.EventName)
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "empty_event_name", "event_name must not be empty")
		return manualEventParsed{}, false
	}
	venue := strings.TrimSpace(req.VenueName)
	if venue == "" {
		httperr.Write(w, http.StatusBadRequest, "empty_venue_name", "venue_name must not be empty")
		return manualEventParsed{}, false
	}
	if len(name) > manualEventNameLimit || len(venue) > manualEventNameLimit {
		httperr.Write(w, http.StatusBadRequest, "value_too_long", "event_name and venue_name must be 200 characters or fewer")
		return manualEventParsed{}, false
	}
	return manualEventParsed{
		date:      pgtype.Date{Time: day, Valid: true},
		eventName: name,
		venueName: venue,
	}, true
}

// manualEventIDParam pulls the {id} path param, answering 400 when it is not a
// uuid so a typo never reaches the query as a scan error.
func manualEventIDParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_id", "id is not a valid uuid")
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: id, Valid: true}, true
}

// formatManualDate renders the stored DATE back as the day the user typed.
// Format, never a UTC conversion: pgtype.Date carries midnight in the local
// zone, so shifting it would move the show a day in either direction.
func formatManualDate(d pgtype.Date) string {
	return d.Time.Format(dateLayout)
}

// ListManualAddedGoingEvents returns the shows the user typed in by hand.
//
// These rows are deliberately absent from every other event endpoint: they
// reference no events row, carry no venue or match, and exist only so the
// calendar sidebar can list a gig the scrapers never saw. The client merges
// them with the real going list at render time.
func ListManualAddedGoingEvents(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		rows, err := q.ListManualAddedGoingEventsByUser(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not list manual events", err)
			return
		}
		// Non-nil so an empty list marshals to [] rather than null.
		out := make([]manualEventOut, 0, len(rows))
		for _, row := range rows {
			out = append(out, manualEventOut{
				ID:        uuidString(row.ID),
				Date:      formatManualDate(row.ShowDate),
				EventName: row.EventName,
				VenueName: row.VenueName,
				CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, listManualAddedGoingEventsResponse{Events: out})
	}
}

// CreateManualAddedGoingEvent records one hand-entered show for the user.
func CreateManualAddedGoingEvent(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		parsed, ok := decodeManualEvent(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		row, err := q.CreateManualAddedGoingEvent(ctx, store.CreateManualAddedGoingEventParams{
			UserID:    pgtype.UUID{Bytes: uid, Valid: true},
			ShowDate:  parsed.date,
			EventName: parsed.eventName,
			VenueName: parsed.venueName,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not create manual event", err)
			return
		}
		writeJSON(w, http.StatusCreated, manualEventOut{
			ID:        uuidString(row.ID),
			Date:      formatManualDate(row.ShowDate),
			EventName: row.EventName,
			VenueName: row.VenueName,
			CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
	}
}

// UpdateManualAddedGoingEvent replaces one of the user's own manual events.
// No UI reaches this yet; it completes the CRUD set the client hooks expose.
func UpdateManualAddedGoingEvent(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		id, ok := manualEventIDParam(w, r)
		if !ok {
			return
		}
		parsed, ok := decodeManualEvent(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		row, err := q.UpdateManualAddedGoingEventForUser(ctx, store.UpdateManualAddedGoingEventForUserParams{
			ID:        id,
			UserID:    pgtype.UUID{Bytes: uid, Valid: true},
			ShowDate:  parsed.date,
			EventName: parsed.eventName,
			VenueName: parsed.venueName,
		})
		if err != nil {
			// The query filters on user_id, so an unknown id and someone
			// else's id are indistinguishable here — both answer 404, which is
			// what keeps the endpoint from confirming a stranger's id exists.
			if errors.Is(err, pgx.ErrNoRows) {
				httperr.Write(w, http.StatusNotFound, "not_found", "no such manual event")
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not update manual event", err)
			return
		}
		writeJSON(w, http.StatusOK, manualEventOut{
			ID:        uuidString(row.ID),
			Date:      formatManualDate(row.ShowDate),
			EventName: row.EventName,
			VenueName: row.VenueName,
			CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
	}
}

// DeleteManualAddedGoingEvent removes one of the user's own manual events.
// Idempotent, like the manual-interests delete: an unknown id is a no-op.
func DeleteManualAddedGoingEvent(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		id, ok := manualEventIDParam(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.DeleteManualAddedGoingEventForUser(ctx, store.DeleteManualAddedGoingEventForUserParams{
			ID:     id,
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
		}); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not delete manual event", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
