package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

const (
	// Fixed server-side, like calendarPageSize: there is no client-supplied
	// limit. Typeahead does not paginate.
	searchResultLimit = 10

	// The measured cliff, not a style choice: below 3 runes the trigram index
	// cannot be used and the query degrades to a full scan with per-row
	// similarity() -- 43ms at 10k rows, growing linearly.
	searchMinRunes = 3

	// Truncate rather than reject: pasting a long string is a plausible user
	// action and the first 100 runes carry the signal.
	searchMaxRunes = 100
)

type searchVenue struct {
	Name string `json:"name"`
}

// searchResult is deliberately leaner than calendarEvent: a row navigates to
// /events/:id, which fetches the full event itself.
//
// No rank field. It is an internal ordering signal, and publishing it invites
// clients to threshold on it -- see the design's Fusion note for why no score
// cutoff can work. The server owns relevance; the client renders an order.
type searchResult struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	StartsAt string      `json:"starts_at"`
	ImageURL string      `json:"image_url,omitempty"`
	Segment  string      `json:"segment,omitempty"`
	Venue    searchVenue `json:"venue"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

// parseSearchQuery reads and validates q. On bad input it writes the error
// response and returns ok=false.
func parseSearchQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("q"))
	if raw == "" {
		httperr.Write(w, http.StatusBadRequest, "bad_query", "q is required")
		return "", false
	}
	runes := []rune(raw)
	if len(runes) < searchMinRunes {
		httperr.Write(w, http.StatusBadRequest, "query_too_short",
			"q must be at least 3 characters")
		return "", false
	}
	if len(runes) > searchMaxRunes {
		raw = string(runes[:searchMaxRunes])
	}
	return raw, true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SearchEvents returns the top matching upcoming events in a city.
func SearchEvents(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}
		query, ok := parseSearchQuery(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
			Query:       query,
			CityID:      pgtype.UUID{Bytes: cityUUID, Valid: true},
			ResultLimit: searchResultLimit,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error",
				"could not search events", err)
			return
		}

		// Non-nil so an empty result encodes as [] rather than null.
		out := searchResponse{Results: make([]searchResult, 0, len(rows))}
		for _, row := range rows {
			out.Results = append(out.Results, searchResult{
				ID:       uuid.UUID(row.ID.Bytes).String(),
				Title:    row.Title,
				StartsAt: row.StartsAt.Time.UTC().Format(time.RFC3339),
				ImageURL: deref(row.ImageUrl),
				Segment:  deref(row.Segment),
				Venue:    searchVenue{Name: row.VenueName},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
