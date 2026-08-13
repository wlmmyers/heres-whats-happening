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

// validStatus guards the CHECK constraints on the enrichment tables. A section
// with an unrecognized status is skipped with a log rather than returned as an
// error: a contract violation in one section must not stop the event itself
// from landing, and returning an error here would retry the whole message
// three times before DLQing an event that is otherwise perfectly good.
func validStatus(s string) bool {
	return s == "ok" || s == "none" || s == "error"
}

// applyEnrichment upserts the artist row and whichever enrichment sections the
// message carries, returning the artist id for the caller to put on the event.
//
// This runs BEFORE the event upsert because events.headline_artist_id has a
// foreign key to artists.id. No transaction wraps it: every write is an
// idempotent upsert and a handler error leaves the message on the queue, so a
// partial failure self-heals on redelivery. The worst case is an orphan artists
// row that the next delivery repairs.
func (h *EventHandler) applyEnrichment(ctx context.Context, e *events.Enrichment) (pgtype.UUID, error) {
	if e == nil || e.Artist == nil {
		return pgtype.UUID{}, nil
	}
	a := e.Artist

	if a.Status != "ok" && a.Status != "not_found" {
		log.Printf("ingest: unknown artist status %q for %q, skipping enrichment", a.Status, a.Performer)
		return pgtype.UUID{}, nil
	}
	// display_name is NOT NULL; fall back to the raw performer if the Lambda
	// resolved nothing to name it with.
	display := a.DisplayName
	if display == "" {
		display = a.Performer
	}

	artistID, err := h.q.UpsertArtist(ctx, store.UpsertArtistParams{
		NameKey:        events.NormalizeString(a.Performer),
		DisplayName:    display,
		Mbid:           optString(a.MBID),
		Disambiguation: optString(a.Disambiguation),
		ArtistType:     optString(a.Type),
		Country:        optString(a.Country),
		BeginYear:      optString(a.BeginYear),
		Status:         a.Status,
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("upsert artist %q: %w", a.Performer, err)
	}

	if img := e.Image; img != nil && validStatus(img.Status) {
		// Marshal only when present: a nil *ImageCredit must store SQL NULL, not
		// the four bytes "null".
		var credit []byte
		if img.Credit != nil {
			credit, err = json.Marshal(img.Credit)
			if err != nil {
				return pgtype.UUID{}, fmt.Errorf("marshal image credit: %w", err)
			}
		}
		if err := h.q.UpsertArtistImage(ctx, store.UpsertArtistImageParams{
			ArtistID: artistID,
			Status:   img.Status,
			Url:      optString(img.URL),
			Width:    optInt32(img.Width),
			Height:   optInt32(img.Height),
			File:     optString(img.File),
			Source:   optString(img.Source),
			Credit:   credit,
			Reason:   optString(img.Reason),
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("upsert artist image: %w", err)
		}
	}

	if bio := e.Bio; bio != nil && validStatus(bio.Status) {
		sources, err := json.Marshal(bio.Sources)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("marshal bio sources: %w", err)
		}
		if bio.Sources == nil {
			sources = []byte("[]") // the column is NOT NULL DEFAULT '[]'
		}
		if err := h.q.UpsertArtistBio(ctx, store.UpsertArtistBioParams{
			ArtistID: artistID,
			Status:   bio.Status,
			BioMd:    optString(bio.BioMD),
			Sources:  sources,
			Model:    optString(bio.Model),
			Reason:   optString(bio.Reason),
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("upsert artist bio: %w", err)
		}
	}

	if tour := e.Tour; tour != nil && validStatus(tour.Status) {
		songs, err := json.Marshal(tour.Songs)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("marshal setlist songs: %w", err)
		}
		if tour.Songs == nil {
			songs = []byte("[]")
		}
		observed, err := parseObservedDate(tour.ObservedDate)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("parse observed_date %q: %w", tour.ObservedDate, err)
		}
		if err := h.q.UpsertArtistTourSnapshot(ctx, store.UpsertArtistTourSnapshotParams{
			ArtistID:      artistID,
			Status:        tour.Status,
			TourName:      optString(tour.TourName),
			Songs:         songs,
			ObservedDate:  observed,
			ObservedVenue: optString(tour.ObservedVenue),
			ObservedCity:  optString(tour.ObservedCity),
			SetlistUrl:    optString(tour.SetlistURL),
			Blurb:         optString(tour.Blurb),
			BlurbModel:    optString(tour.BlurbModel),
			Reason:        optString(tour.Reason),
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("upsert artist tour snapshot: %w", err)
		}
	}

	return artistID, nil
}

// parseObservedDate reads the wire's YYYY-MM-DD calendar date. Empty is valid
// and means absent — setlist.fm reports a date with no time and no zone, so
// this deliberately never touches time.Local.
func parseObservedDate(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

// optInt32 converts a Go int to a *int32 for nullable integer columns.
// Zero is treated as absent — no real image is 0px wide.
func optInt32(v int) *int32 {
	if v == 0 {
		return nil
	}
	n := int32(v)
	return &n
}
