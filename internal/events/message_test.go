package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMessage_JSONRoundTrip(t *testing.T) {
	m := Message{
		SourceID:      "ticketmaster",
		SourceEventID: "tm-123",
		Title:         "Phoebe Bridgers Live",
		Description:   "Indie rock concert",
		StartsAt:      time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
		EndsAt:        nil,
		Venue: Venue{
			Name:    "The Bowl",
			Address: "100 Main St",
			Lat:     ptr(40.7),
			Lng:     ptr(-74.0),
		},
		Performers: []string{"Phoebe Bridgers", "MUNA"},
		Genres:     []string{"indie", "rock"},
		ImageURL:   "https://example.com/p.jpg",
		URL:        "https://example.com/event/123",
	}

	data, err := json.Marshal(m)
	require.NoError(t, err)

	var out Message
	require.NoError(t, json.Unmarshal(data, &out))
	require.Equal(t, m.Title, out.Title)
	require.Equal(t, m.StartsAt.Unix(), out.StartsAt.Unix())
	require.Equal(t, m.Performers, out.Performers)
	require.Equal(t, m.Genres, out.Genres)
	require.NotNil(t, out.Venue.Lat)
	require.InDelta(t, 40.7, *out.Venue.Lat, 0.0001)
}

func TestMessage_OmitEmptyFields(t *testing.T) {
	m := Message{
		SourceID:      "x",
		SourceEventID: "y",
		Title:         "t",
		StartsAt:      time.Now(),
		Venue:         Venue{Name: "v"},
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"ends_at"`)
	require.NotContains(t, string(data), `"image_url"`)
}

func ptr[T any](v T) *T { return &v }
