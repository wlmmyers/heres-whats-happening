// Package events defines the canonical wire types used between scrapers, the
// events-queue (SQS), and the ingest consumer. No I/O lives here — just types
// and value helpers.
package events

import "time"

// Message is the canonical event record placed on the events-queue by scrapers
// and read by the ingest consumer.
type Message struct {
	SourceID      string     `json:"source_id"`
	SourceEventID string     `json:"source_event_id"`
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	StartsAt      time.Time  `json:"starts_at"`
	EndsAt        *time.Time `json:"ends_at,omitempty"`
	// TimeTBD marks an event whose source gave a date but no start time.
	// StartsAt is then local midnight on that date — a placeholder for sorting
	// and display, not a real start time. Consumers must not treat such an
	// event as over just because that midnight has passed.
	TimeTBD bool  `json:"time_tbd,omitempty"`
	Venue   Venue `json:"venue"`
	// Segment is the source's top-level category slug ("music", "sports",
	// "arts-theatre"). Empty means the source does not classify its events —
	// the email path never sets it — and is stored as SQL NULL. See
	// events/segments.go for why this vocabulary is open while the API's
	// filter vocabulary is closed.
	Segment    string   `json:"segment,omitempty"`
	Performers []string `json:"performers,omitempty"`
	Genres     []string `json:"genres,omitempty"`
	ImageURL   string   `json:"image_url,omitempty"`
	URL        string   `json:"url,omitempty"`
}

// Venue is denormalized inline on the Message. The ingest consumer is
// responsible for upserting it into the venues table and resolving venue_id.
type Venue struct {
	Name       string   `json:"name"`
	Address    string   `json:"address,omitempty"`
	Lat        *float64 `json:"lat,omitempty"`
	Lng        *float64 `json:"lng,omitempty"`
	WebsiteURL string   `json:"website_url,omitempty"`
}
