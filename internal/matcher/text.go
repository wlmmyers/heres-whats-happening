package matcher

import "strings"

// EventText is the input to BuildEventText.
//
// Deliberately no Description. Ticketmaster is the only source that fills
// events.description, and it fills it from the Discovery API's `info` field,
// which carries venue and ticketing boilerplate rather than anything about the
// event: bag policies, per-card ticket limits, the venue's street address,
// arbitration clauses. In the dev catalogue 188 of 195 events had one, drawn
// from just 35 distinct strings -- 86 of them sharing a single sentence about
// an arena's bag policy.
//
// Measured on those 86 plus two near-duplicates: embedding the description
// raised mean pairwise cosine across the group from 0.615 to 0.666, i.e. it
// made a K-pop tour, a Kraken game and a rock show look more alike, purely
// because they share a venue. Note it does NOT change exact-tie structure --
// recurring shows share a description as well as a title, so the tie-break in
// SearchEventsSemantic is unaffected either way.
//
// The column still exists and the event detail page still renders it; it is
// only the embedding input that drops it.
type EventText struct {
	Title      string
	Performers []string
	Genres     []string
}

// BuildEventText composes an event's embedding-input string.
// Format: "<title> — <performers, joined>. <genres, joined>"
// Empty sections are omitted.
func BuildEventText(in EventText) string {
	var parts []string
	if in.Title != "" {
		parts = append(parts, in.Title)
	}
	if len(in.Performers) > 0 {
		if len(parts) > 0 {
			parts[len(parts)-1] += " — " + strings.Join(in.Performers, ", ")
		} else {
			parts = append(parts, strings.Join(in.Performers, ", "))
		}
	}
	if len(in.Genres) > 0 {
		parts = append(parts, strings.Join(in.Genres, ", "))
	}
	return strings.Join(parts, ". ")
}

// UserText is the input to BuildUserText.
type UserText struct {
	TopArtists []string
	TopGenres  []string
	ManualTags []string
}

// BuildUserText composes a user's embedding-input string.
// Each section is included only when non-empty. Sections are joined with ". ".
func BuildUserText(in UserText) string {
	var sections []string
	if len(in.TopArtists) > 0 {
		sections = append(sections, "Top artists: "+strings.Join(in.TopArtists, ", "))
	}
	if len(in.TopGenres) > 0 {
		sections = append(sections, "Top genres: "+strings.Join(in.TopGenres, ", "))
	}
	if len(in.ManualTags) > 0 {
		sections = append(sections, "Interests: "+strings.Join(in.ManualTags, ", "))
	}
	return strings.Join(sections, ". ")
}
