package ical

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatCalendar_OneEvent(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	events := []Event{
		{
			UID:         "event-aaa@example.com",
			Title:       "Phoebe Bridgers Live",
			StartsAt:    time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
			EndsAt:      time.Date(2026, 6, 15, 22, 0, 0, 0, time.UTC),
			VenueName:   "The Bowl",
			VenueAddr:   "100 Main St",
			URL:         "https://example.com/event/aaa",
			Description: "Matched because: Phoebe Bridgers, indie rock",
		},
	}
	got := FormatCalendar("Your Matched Events", now, events)

	require.True(t, strings.HasPrefix(got, "BEGIN:VCALENDAR"))
	require.True(t, strings.HasSuffix(strings.TrimRight(got, "\r\n"), "END:VCALENDAR"))
	require.Contains(t, got, "VERSION:2.0")
	require.Contains(t, got, "METHOD:PUBLISH")
	require.Contains(t, got, "X-PUBLISHED-TTL:PT1H")
	require.Contains(t, got, "NAME:Your Matched Events")
	require.Contains(t, got, "X-WR-CALNAME:Your Matched Events")
	require.Contains(t, got, "BEGIN:VEVENT")
	require.Contains(t, got, "END:VEVENT")
	require.Contains(t, got, "UID:event-aaa@example.com")
	require.Contains(t, got, "DTSTAMP:20260520T120000Z")
	require.Contains(t, got, "DTSTART:20260615T200000Z")
	require.Contains(t, got, "DTEND:20260615T220000Z")
	require.Contains(t, got, "SUMMARY:Phoebe Bridgers Live")
	require.Contains(t, got, `LOCATION:The Bowl\, 100 Main St`)
	require.Contains(t, got, "URL:https://example.com/event/aaa")
	require.Contains(t, got, `DESCRIPTION:Matched because: Phoebe Bridgers\, indie rock`)
}

func TestFormatCalendar_NoEndsAt_OmitsDTEND(t *testing.T) {
	events := []Event{
		{
			UID:      "x@example.com",
			Title:    "Open-ended",
			StartsAt: time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
		},
	}
	got := FormatCalendar("c", time.Now(), events)
	require.Contains(t, got, "DTSTART:20260615T200000Z")
	require.NotContains(t, got, "DTEND")
}

func TestFormatCalendar_Empty(t *testing.T) {
	got := FormatCalendar("c", time.Now(), nil)
	require.True(t, strings.HasPrefix(got, "BEGIN:VCALENDAR"))
	require.NotContains(t, got, "BEGIN:VEVENT")
}

func TestEscape_HandlesSpecialChars(t *testing.T) {
	require.Equal(t, `a\,b`, escape("a,b"))
	require.Equal(t, `a\;b`, escape("a;b"))
	require.Equal(t, `a\\b`, escape(`a\b`))
	require.Equal(t, `a\nb`, escape("a\nb"))
}

func TestUseCRLF(t *testing.T) {
	got := FormatCalendar("c", time.Now(), nil)
	require.Contains(t, got, "\r\n", "iCal lines must be CRLF-separated per RFC 5545")
}
