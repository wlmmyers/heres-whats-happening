package ingest_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func enrichedSample() events.EnrichedMessage {
	m := sampleMessage()
	m.SourceEventID = "tm-enriched"
	m.ImageURL = "" // no source image, so the artist image is the fallback
	return events.EnrichedMessage{
		Message: m,
		Enrichment: &events.Enrichment{
			Artist: &events.ArtistInfo{
				Performer:      "Phoebe Bridgers",
				DisplayName:    "Phoebe Bridgers",
				MBID:           "cc0b7089-c08d-4c10-b6b0-873582c17fd6",
				Disambiguation: "US singer-songwriter",
				Type:           "Person",
				Country:        "US",
				BeginYear:      "1994",
				Status:         "ok",
			},
			Image: &events.ImageInfo{
				Status: "ok",
				URL:    "https://upload.wikimedia.org/pb.jpg",
				Width:  640,
				Height: 427,
				File:   "PB.jpg",
				Source: "p18",
				Credit: &events.ImageCredit{LicenseShortName: "CC BY-SA 4.0", AttributionRequired: true},
			},
			Bio: &events.BioInfo{
				Status:  "ok",
				BioMD:   "Phoebe Bridgers is an American singer-songwriter.",
				Sources: []events.BioSource{{Kind: "wikipedia", Title: "Phoebe Bridgers", RevisionID: 999}},
				Model:   "anthropic/claude-sonnet-4-5",
			},
			Tour: &events.TourInfo{
				Status:        "ok",
				TourName:      "Reunion Tour",
				Songs:         []events.SetlistSong{{Name: "Motion Sickness"}, {Name: "Smoke Signals", Encore: 1}},
				ObservedDate:  "2026-07-14",
				ObservedVenue: "The Greek",
				ObservedCity:  "Berkeley",
				SetlistURL:    "https://www.setlist.fm/setlist/x.html",
				Blurb:         "Currently out on the Reunion Tour.",
				BlurbModel:    "anthropic/claude-sonnet-4-5",
			},
		},
	}
}

func TestHandle_PersistsEnrichment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	body, _ := json.Marshal(enrichedSample())
	require.NoError(t, h.Handle(ctx, body))

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-enriched",
	})
	require.NoError(t, err)

	rows, err := q.GetArtistEnrichmentBatch(ctx, []pgtype.UUID{ev.HeadlineArtistID})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	r := rows[0]
	require.Equal(t, "Phoebe Bridgers", r.DisplayName)
	require.Equal(t, "ok", r.ArtistStatus)
	require.Equal(t, "https://upload.wikimedia.org/pb.jpg", *r.ImageUrl)
	require.Contains(t, *r.BioMd, "singer-songwriter")
	require.Equal(t, "Reunion Tour", *r.TourName)
	require.Equal(t, "Currently out on the Reunion Tour.", *r.Blurb)
}

// A plain, un-enriched message must behave exactly as it does today. This is
// the cutover safety net and what keeps old DLQ messages replayable.
func TestHandle_PlainMessageStillWorks(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, h.Handle(ctx, body))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-aaa",
	})
	require.NoError(t, err)
	require.False(t, ev.HeadlineArtistID.Valid, "no enrichment means no artist link")
}

// The COALESCE guard: a later message whose enrichment failed must not blank
// a link an earlier successful enrichment established.
func TestHandle_FailedReenrichment_DoesNotBlankArtistLink(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	body, _ := json.Marshal(enrichedSample())
	require.NoError(t, h.Handle(ctx, body))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	before, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-enriched",
	})
	require.NoError(t, err)
	require.True(t, before.HeadlineArtistID.Valid)

	// Re-scrape whose prelude failed: same event, no enrichment at all.
	bare := enrichedSample()
	bare.Enrichment = nil
	bareBody, _ := json.Marshal(bare)
	require.NoError(t, h.Handle(ctx, bareBody))

	after, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-enriched",
	})
	require.NoError(t, err)
	require.Equal(t, before.HeadlineArtistID, after.HeadlineArtistID)
}

// An unresolvable performer still gets an artists row, so the attempt is
// recorded rather than retried forever.
func TestHandle_NotFoundArtist_StillCreatesRow(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	m := enrichedSample()
	m.SourceEventID = "tm-unknown"
	m.Enrichment = &events.Enrichment{
		Artist: &events.ArtistInfo{
			Performer:   "Some Local Opener",
			DisplayName: "Some Local Opener",
			Status:      "not_found",
		},
	}
	body, _ := json.Marshal(m)
	require.NoError(t, h.Handle(ctx, body))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-unknown",
	})
	require.NoError(t, err)
	require.True(t, ev.HeadlineArtistID.Valid)

	rows, err := q.GetArtistEnrichmentBatch(ctx, []pgtype.UUID{ev.HeadlineArtistID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "not_found", rows[0].ArtistStatus)
	require.Nil(t, rows[0].Mbid)
	require.Nil(t, rows[0].ImageStatus, "no image section means no image row")
}
