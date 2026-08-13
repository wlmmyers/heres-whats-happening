package events

import "time"

// EnrichedMessage is the canonical record on the events-enriched-queue: a
// strict superset of Message, so a plain Message still decodes during cutover
// and stale DLQ messages stay replayable.
type EnrichedMessage struct {
	Message
	Enrichment *Enrichment `json:"enrichment,omitempty"`
}

// Enrichment carries whatever the Lambda produced for this event's headline
// performer. Every section is independent: one workflow failing leaves the
// others populated.
type Enrichment struct {
	// Artist is present whenever the resolution prelude RAN, including when it
	// found nothing (Status "not_found", empty MBID) — that is how an
	// unresolvable performer still gets an artists row recording the attempt.
	// Nil only when the prelude itself failed or never ran, in which case the
	// other three sections are nil too.
	Artist      *ArtistInfo `json:"artist,omitempty"`
	Image       *ImageInfo  `json:"image,omitempty"`
	Bio         *BioInfo    `json:"bio,omitempty"`
	Tour        *TourInfo   `json:"tour,omitempty"`
	AttemptedAt time.Time   `json:"attempted_at"`
}

// ArtistInfo carries the RAW performer string, never a normalized key: the
// consumer applies NormalizeString itself, keeping the one normalization that
// reaches the database in a single language.
type ArtistInfo struct {
	Performer      string `json:"performer"`
	DisplayName    string `json:"display_name"`
	MBID           string `json:"mbid,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
	Type           string `json:"type,omitempty"`
	Country        string `json:"country,omitempty"`
	BeginYear      string `json:"begin_year,omitempty"`
	Status         string `json:"status"` // ok | not_found
}

type ImageInfo struct {
	Status string       `json:"status"` // ok | none | error
	URL    string       `json:"url,omitempty"`
	Width  int          `json:"width,omitempty"`
	Height int          `json:"height,omitempty"`
	File   string       `json:"file,omitempty"`
	Source string       `json:"source,omitempty"` // p18 | category
	Credit *ImageCredit `json:"credit,omitempty"`
	Reason string       `json:"reason,omitempty"`
}

// ImageCredit is snake_case on the wire even though the Lambda's internal
// representation is camelCase — the Lambda maps explicitly at the boundary.
// Commons files are predominantly CC-BY/CC-BY-SA, so this travels with every
// image whether or not anything renders it yet.
type ImageCredit struct {
	File                string `json:"file,omitempty"`
	DescriptionURL      string `json:"description_url,omitempty"`
	Artist              string `json:"artist,omitempty"`
	Credit              string `json:"credit,omitempty"`
	License             string `json:"license,omitempty"`
	LicenseShortName    string `json:"license_short_name,omitempty"`
	LicenseURL          string `json:"license_url,omitempty"`
	UsageTerms          string `json:"usage_terms,omitempty"`
	AttributionRequired bool   `json:"attribution_required"`
}

type BioInfo struct {
	Status  string      `json:"status"` // ok | none | error
	BioMD   string      `json:"bio_md,omitempty"`
	Sources []BioSource `json:"sources,omitempty"`
	Model   string      `json:"model,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

type BioSource struct {
	Kind       string `json:"kind"` // wikipedia | musicbrainz
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	RevisionID int64  `json:"revision_id,omitempty"`
	MBID       string `json:"mbid,omitempty"`
}

type TourInfo struct {
	Status   string        `json:"status"` // ok | none | error
	TourName string        `json:"tour_name,omitempty"`
	Songs    []SetlistSong `json:"songs,omitempty"`
	// ObservedDate is YYYY-MM-DD, a plain calendar date with no zone: the
	// column is DATE and setlist.fm reports dd-MM-yyyy with no time at all.
	ObservedDate  string `json:"observed_date,omitempty"`
	ObservedVenue string `json:"observed_venue,omitempty"`
	ObservedCity  string `json:"observed_city,omitempty"`
	SetlistURL    string `json:"setlist_url,omitempty"`
	Blurb         string `json:"blurb,omitempty"`
	BlurbModel    string `json:"blurb_model,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type SetlistSong struct {
	Name    string `json:"name"`
	Encore  int    `json:"encore,omitempty"`
	CoverOf string `json:"cover_of,omitempty"`
	Tape    bool   `json:"tape,omitempty"`
	Info    string `json:"info,omitempty"`
}
