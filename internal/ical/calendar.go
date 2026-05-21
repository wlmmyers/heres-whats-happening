// Package ical formats event lists as RFC 5545 VCALENDAR text.
// No I/O — pure string manipulation.
package ical

import (
	"strings"
	"time"
)

// Event is the minimal shape needed to emit a VEVENT block.
type Event struct {
	UID         string    // Stable across feed refreshes; format: "event-<id>@example.com"
	Title       string
	StartsAt    time.Time
	EndsAt      time.Time // zero value → DTEND omitted
	VenueName   string
	VenueAddr   string
	URL         string
	Description string
}

// FormatCalendar returns an RFC 5545 VCALENDAR document.
// `now` is used for DTSTAMP. `calName` is the calendar display name.
func FormatCalendar(calName string, now time.Time, events []Event) string {
	var b strings.Builder
	writeLine := func(s string) {
		b.WriteString(s)
		b.WriteString("\r\n")
	}
	writeLine("BEGIN:VCALENDAR")
	writeLine("VERSION:2.0")
	writeLine("PRODID:-//Here's What's Happening//Calendar//EN")
	writeLine("METHOD:PUBLISH")
	writeLine("X-PUBLISHED-TTL:PT1H")
	writeLine("NAME:" + escape(calName))
	writeLine("X-WR-CALNAME:" + escape(calName))

	stamp := now.UTC().Format("20060102T150405Z")

	for _, e := range events {
		writeLine("BEGIN:VEVENT")
		writeLine("UID:" + e.UID)
		writeLine("DTSTAMP:" + stamp)
		writeLine("DTSTART:" + e.StartsAt.UTC().Format("20060102T150405Z"))
		if !e.EndsAt.IsZero() {
			writeLine("DTEND:" + e.EndsAt.UTC().Format("20060102T150405Z"))
		}
		if e.Title != "" {
			writeLine("SUMMARY:" + escape(e.Title))
		}
		loc := buildLocation(e.VenueName, e.VenueAddr)
		if loc != "" {
			writeLine("LOCATION:" + escape(loc))
		}
		if e.URL != "" {
			writeLine("URL:" + e.URL) // URLs are not escaped per RFC 5545 §3.3.13
		}
		if e.Description != "" {
			writeLine("DESCRIPTION:" + escape(e.Description))
		}
		writeLine("END:VEVENT")
	}
	writeLine("END:VCALENDAR")
	return b.String()
}

func buildLocation(name, addr string) string {
	switch {
	case name == "" && addr == "":
		return ""
	case name == "":
		return addr
	case addr == "":
		return name
	default:
		return name + ", " + addr
	}
}

// escape applies RFC 5545 text-value escaping. Order matters: backslash first
// so we don't double-escape backslashes we add for commas/semicolons.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
