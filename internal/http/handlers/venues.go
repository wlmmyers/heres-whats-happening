package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type venue struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Address            string   `json:"address,omitempty"`
	Lat                *float64 `json:"lat,omitempty"`
	Lng                *float64 `json:"lng,omitempty"`
	WebsiteURL         string   `json:"website_url,omitempty"`
	UpcomingEventCount int32    `json:"upcoming_event_count"`
	NextEventAt        string   `json:"next_event_at,omitempty"`
}

type venuesResponse struct {
	Venues []venue `json:"venues"`
}

// ListCityVenues returns every venue in the given city with upcoming events,
// busiest first. Unpaginated: a city has hundreds of venues at most, not the
// thousands of events the calendar pages through. Like GetCityCalendar, the
// response is identical for every caller. The optional segment narrows both
// the list and the counts by the same rule as the calendar's filter.
func ListCityVenues(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}

		segment, ok := parseSegment(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.ListCityVenues(ctx, store.ListCityVenuesParams{
			CityID:  pgtype.UUID{Bytes: cityUUID, Valid: true},
			Segment: segment,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load venues", err)
			return
		}

		out := venuesResponse{Venues: make([]venue, 0, len(rows))}
		for _, row := range rows {
			v := venue{
				ID:                 uuidString(row.ID),
				Name:               row.Name,
				Address:            textPtrToString(row.Address),
				Lat:                row.Lat,
				Lng:                row.Lng,
				WebsiteURL:         textPtrToString(row.WebsiteUrl),
				UpcomingEventCount: row.UpcomingEventCount,
			}
			if row.NextEventAt.Valid {
				v.NextEventAt = row.NextEventAt.Time.UTC().Format(time.RFC3339)
			}
			out.Venues = append(out.Venues, v)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
