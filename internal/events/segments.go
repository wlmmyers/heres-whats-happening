package events

import "strings"

// Segment is a source's top-level category for an event — the axis that
// separates a concert from a ball game, which no genre tag reliably does
// (Ticketmaster classifies both "Sports / Miscellaneous" and "Music / Other"
// into genres that normalize to the same slug).
//
// Two rules govern this vocabulary, and they deliberately differ:
//
//   - NormalizeSegment is OPEN. An unrecognized segment is slugified and kept,
//     because the label is the only record that a new source category exists.
//   - ValidSegment is CLOSED. It gates the API's filter parameter, where a
//     typo must fail loudly instead of quietly matching nothing.
const (
	SegmentMusic         = "music"
	SegmentSports        = "sports"
	SegmentArtsTheatre   = "arts-theatre"
	SegmentMiscellaneous = "miscellaneous"
	SegmentUndefined     = "undefined"
	SegmentFilm          = "film"
)

// filterableSegments is the closed set the API accepts as a ?segment= value.
var filterableSegments = map[string]struct{}{
	SegmentMusic:         {},
	SegmentSports:        {},
	SegmentArtsTheatre:   {},
	SegmentMiscellaneous: {},
	SegmentUndefined:     {},
	SegmentFilm:          {},
}

// NormalizeSegment slugifies a source's segment name ("Arts & Theatre" ->
// "arts-theatre"). Unknown names pass through slugified rather than being
// dropped. Returns "" for input with no alphanumeric content, which callers
// store as SQL NULL — "unclassified", not a segment named "-".
func NormalizeSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	dash := false
	for _, r := range NormalizeString(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			// Collapse any run of separators into a single dash, and never
			// emit one until a following alphanumeric proves it is internal.
			dash = true
		}
	}
	return b.String()
}

// ValidSegment reports whether s is a segment the API will filter on. Compared
// as a canonical slug: callers normalize before asking.
func ValidSegment(s string) bool {
	_, ok := filterableSegments[s]
	return ok
}
