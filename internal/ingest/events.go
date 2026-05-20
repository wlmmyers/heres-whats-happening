// Package ingest bridges the events-queue (SQS) into the database.
// EventHandler is the per-message logic; Consumer is the loop.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// EventHandler applies a single events.Message to the database.
type EventHandler struct {
	q      *store.Queries
	cityID pgtype.UUID
}

func NewEventHandler(q *store.Queries, cityID pgtype.UUID) *EventHandler {
	return &EventHandler{q: q, cityID: cityID}
}

// Handle decodes an SQS message body as an events.Message and applies it.
func (h *EventHandler) Handle(ctx context.Context, body []byte) error {
	var m events.Message
	if err := json.Unmarshal(body, &m); err != nil {
		// Malformed message — log and return nil so consumer deletes it.
		log.Printf("ingest: bad event message: %v", err)
		return nil
	}
	return h.handleMessage(ctx, m)
}

func (h *EventHandler) handleMessage(ctx context.Context, m events.Message) error {
	src, err := h.q.GetEventSourceByName(ctx, m.SourceID)
	if err != nil {
		return fmt.Errorf("lookup source %q: %w", m.SourceID, err)
	}

	// Upsert venue
	venueID, err := h.q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID:         h.cityID,
		Name:           m.Venue.Name,
		NormalizedName: events.NormalizeString(m.Venue.Name),
		Address:        optString(m.Venue.Address),
		Lat:            m.Venue.Lat,
		Lng:            m.Venue.Lng,
		WebsiteUrl:     optString(m.Venue.WebsiteURL),
	})
	if err != nil {
		return fmt.Errorf("upsert venue: %w", err)
	}

	// Upsert event
	eventID, err := h.q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: m.SourceEventID,
		Title:         m.Title,
		Description:   m.Description,
		StartsAt:      pgtype.Timestamptz{Time: m.StartsAt, Valid: true},
		EndsAt:        pgTimePtr(m.EndsAt),
		VenueID:       venueID,
		ImageUrl:      optString(m.ImageURL),
		Url:           optString(m.URL),
	})
	if err != nil {
		return fmt.Errorf("upsert event: %w", err)
	}

	// Replace performers
	if err := h.q.DeleteEventPerformersByEvent(ctx, eventID); err != nil {
		return fmt.Errorf("delete performers: %w", err)
	}
	for _, p := range m.Performers {
		if p == "" {
			continue
		}
		if err := h.q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
			EventID:        eventID,
			PerformerName:  p,
			NormalizedName: events.NormalizeString(p),
		}); err != nil {
			return fmt.Errorf("insert performer %q: %w", p, err)
		}
	}

	// Replace genres (drop unknown ones)
	if err := h.q.DeleteEventGenresByEvent(ctx, eventID); err != nil {
		return fmt.Errorf("delete genres: %w", err)
	}
	for _, g := range m.Genres {
		slug := events.NormalizeGenre(g)
		if slug == "" {
			// NormalizeGenre returned "" — not in alias map.
			// Fall back to the original string in case it's already a canonical slug.
			slug = g
		}
		exists, err := h.q.GenreExists(ctx, slug)
		if err != nil {
			return fmt.Errorf("check genre %q: %w", slug, err)
		}
		if !exists {
			continue
		}
		if err := h.q.InsertEventGenre(ctx, store.InsertEventGenreParams{
			EventID:   eventID,
			GenreSlug: slug,
		}); err != nil {
			return fmt.Errorf("insert genre %q: %w", slug, err)
		}
	}

	return nil
}

// optString converts a Go string to a *string suitable for nullable text columns.
// Returns nil for empty strings (treated as absent).
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// pgTimePtr converts a *time.Time to a pgtype.Timestamptz for nullable timestamp columns.
func pgTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
