package matcher

import "strings"

const descriptionCharCap = 500

// EventText is the input to BuildEventText.
type EventText struct {
	Title       string
	Performers  []string
	Genres      []string
	Description string
}

// BuildEventText composes an event's embedding-input string.
// Format: "<title> — <performers, joined>. <genres, joined>. <description (truncated)>"
// Empty sections are omitted. Description is hard-capped at 500 chars.
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
	if in.Description != "" {
		d := in.Description
		if len(d) > descriptionCharCap {
			d = d[:descriptionCharCap]
		}
		parts = append(parts, d)
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
