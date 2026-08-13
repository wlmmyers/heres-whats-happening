package handlers

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// calendarArtist is the enrichment block hung off a calendar event. Every
// section is a pointer so a failed or never-attempted workflow is ABSENT from
// the JSON rather than present-and-empty.
type calendarArtist struct {
	Name           string       `json:"name"`
	Disambiguation string       `json:"disambiguation,omitempty"`
	MBID           string       `json:"mbid,omitempty"`
	Image          *artistImage `json:"image,omitempty"`
	Bio            *artistBio   `json:"bio,omitempty"`
	Tour           *artistTour  `json:"tour,omitempty"`
}

type artistImage struct {
	URL    string          `json:"url"`
	Width  int             `json:"width,omitempty"`
	Height int             `json:"height,omitempty"`
	Credit json.RawMessage `json:"credit,omitempty"`
}

type artistBio struct {
	Text    string          `json:"text"`
	Sources json.RawMessage `json:"sources,omitempty"`
}

type artistTour struct {
	Name       string          `json:"name,omitempty"`
	Blurb      string          `json:"blurb,omitempty"`
	SetlistURL string          `json:"setlist_url,omitempty"`
	Songs      json.RawMessage `json:"songs,omitempty"`
	Observed   *tourObserved   `json:"observed,omitempty"`
}

type tourObserved struct {
	Date  string `json:"date,omitempty"`
	Venue string `json:"venue,omitempty"`
	City  string `json:"city,omitempty"`
}

// buildArtist maps one batch row into the API shape. A section appears only
// when its status is "ok" — "none" and "error" both mean there is nothing
// worth showing, and the distinction between them is an operational concern,
// not a client one.
func buildArtist(row store.GetArtistEnrichmentBatchRow) calendarArtist {
	a := calendarArtist{
		Name:           row.DisplayName,
		Disambiguation: strVal(row.Disambiguation),
		MBID:           strVal(row.Mbid),
	}

	if strVal(row.ImageStatus) == "ok" && row.ImageUrl != nil {
		a.Image = &artistImage{
			URL:    *row.ImageUrl,
			Width:  int32Val(row.ImageWidth),
			Height: int32Val(row.ImageHeight),
			Credit: json.RawMessage(row.ImageCredit),
		}
	}

	if strVal(row.BioStatus) == "ok" && row.BioMd != nil {
		a.Bio = &artistBio{
			Text:    *row.BioMd,
			Sources: json.RawMessage(row.BioSources),
		}
	}

	if strVal(row.TourStatus) == "ok" {
		t := &artistTour{
			Name:       strVal(row.TourName),
			Blurb:      strVal(row.Blurb),
			SetlistURL: strVal(row.SetlistUrl),
			Songs:      json.RawMessage(row.Songs),
		}
		if row.ObservedDate.Valid || row.ObservedVenue != nil || row.ObservedCity != nil {
			t.Observed = &tourObserved{
				Date:  dateVal(row.ObservedDate),
				Venue: strVal(row.ObservedVenue),
				City:  strVal(row.ObservedCity),
			}
		}
		a.Tour = t
	}

	return a
}

func int32Val(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

func dateVal(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}
