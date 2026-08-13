package events_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

// A plain Message must still decode into EnrichedMessage — that is what makes
// the cutover safe and old DLQ messages replayable.
func TestEnrichedMessage_DecodesPlainMessage(t *testing.T) {
	raw := []byte(`{
		"source_id": "ticketmaster",
		"source_event_id": "tm-aaa",
		"title": "Phoebe Bridgers",
		"starts_at": "2026-06-15T20:00:00Z",
		"venue": {"name": "The Bowl"}
	}`)

	var m events.EnrichedMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&m))

	require.Equal(t, "Phoebe Bridgers", m.Title)
	require.Nil(t, m.Enrichment)
}

func TestEnrichedMessage_RoundTripsEnrichment(t *testing.T) {
	raw := []byte(`{
		"source_id": "ticketmaster",
		"source_event_id": "tm-bbb",
		"title": "La Luz",
		"starts_at": "2026-09-02T20:00:00Z",
		"venue": {"name": "The Chapel"},
		"enrichment": {
			"attempted_at": "2026-08-12T04:11:22Z",
			"artist": {
				"performer": "La Luz",
				"display_name": "La Luz",
				"mbid": "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a",
				"disambiguation": "US rock band",
				"status": "ok"
			},
			"image": {
				"status": "ok",
				"url": "https://upload.wikimedia.org/x.jpg",
				"width": 640,
				"height": 427,
				"file": "La_Luz.jpg",
				"source": "p18",
				"credit": {"license_short_name": "CC BY-SA 4.0", "attribution_required": true}
			},
			"bio": {
				"status": "ok",
				"bio_md": "La Luz formed in Seattle in 2012.",
				"sources": [{"kind": "wikipedia", "title": "La Luz (band)", "revision_id": 12345}]
			},
			"tour": {
				"status": "ok",
				"tour_name": "News of the Universe Tour",
				"songs": [{"name": "Sure As Spring"}, {"name": "Strange World", "encore": 1}],
				"observed_date": "2026-07-14"
			}
		}
	}`)

	var m events.EnrichedMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&m))

	require.NotNil(t, m.Enrichment)
	require.Equal(t, "ok", m.Enrichment.Artist.Status)
	require.Equal(t, "La Luz", m.Enrichment.Artist.Performer)
	require.Equal(t, 640, m.Enrichment.Image.Width)
	require.True(t, m.Enrichment.Image.Credit.AttributionRequired)
	require.Len(t, m.Enrichment.Bio.Sources, 1)
	require.EqualValues(t, 12345, m.Enrichment.Bio.Sources[0].RevisionID)
	require.Len(t, m.Enrichment.Tour.Songs, 2)
	require.Equal(t, 1, m.Enrichment.Tour.Songs[1].Encore)
	require.Equal(t, "2026-07-14", m.Enrichment.Tour.ObservedDate)

	// Re-marshalling must not resurrect absent fields as empty strings.
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(out), `"blurb"`)
}

func TestEnrichedContractFixtures_Unmarshal(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "enriched-message-contract")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	ran := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)

			var m events.EnrichedMessage
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields() // catches a TS field the Go struct lacks
			require.NoError(t, dec.Decode(&m))

			require.NotEmpty(t, m.SourceID)
			require.NotEmpty(t, m.Title)
			require.NotNil(t, m.Enrichment)
			require.NotNil(t, m.Enrichment.Artist)
		})
	}
	require.Positive(t, ran, "no .json fixtures found in %s", dir)
}
