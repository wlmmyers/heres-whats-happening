package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func defaultCityID(t *testing.T, q *store.Queries) pgtype.UUID {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	row, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	return row.ID
}

func sampleMessage() events.Message {
	return events.Message{
		SourceID:      "ticketmaster",
		SourceEventID: "tm-aaa",
		Title:         "Phoebe Bridgers",
		Description:   "Indie rock concert",
		StartsAt:      time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
		Venue: events.Venue{
			Name:    "The Bowl",
			Address: "100 Main St",
		},
		Performers: []string{"Phoebe Bridgers", "MUNA"},
		Genres:     []string{"indie", "rock"},
		ImageURL:   "https://example.com/p.jpg",
		URL:        "https://example.com/event/aaa",
	}
}

func TestHandle_InsertsEventVenuePerformersGenres(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()
	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, h.Handle(ctx, body))

	// Event exists
	srcRow, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: "tm-aaa",
	})
	require.NoError(t, err)
	require.Equal(t, "Phoebe Bridgers", ev.Title)

	// Performers
	performers, err := q.ListEventPerformersByEvent(ctx, ev.ID)
	require.NoError(t, err)
	require.Len(t, performers, 2)

	// Genres
	genres, err := q.ListEventGenresByEvent(ctx, ev.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"indie", "rock"}, genres)
}

func TestHandle_Reupsert_UpdatesLastSeenAndReplacesAssociations(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()

	// First ingest
	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, h.Handle(ctx, body))

	// Modify performers + genres
	mod := sampleMessage()
	mod.Performers = []string{"Phoebe Bridgers"} // dropped MUNA
	mod.Genres = []string{"folk"}                // changed genre
	modBody, _ := json.Marshal(mod)
	require.NoError(t, h.Handle(ctx, modBody))

	srcRow, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, _ := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: "tm-aaa",
	})

	performers, _ := q.ListEventPerformersByEvent(ctx, ev.ID)
	require.Len(t, performers, 1)
	require.Equal(t, "Phoebe Bridgers", performers[0].PerformerName)

	genres, _ := q.ListEventGenresByEvent(ctx, ev.ID)
	require.ElementsMatch(t, []string{"folk"}, genres)
}

func TestHandle_UnknownGenre_SkipsSilently(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()
	m := sampleMessage()
	m.Genres = []string{"rock", "nonexistent-genre"}
	body, _ := json.Marshal(m)
	require.NoError(t, h.Handle(ctx, body))

	srcRow, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, _ := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: m.SourceEventID,
	})
	genres, _ := q.ListEventGenresByEvent(ctx, ev.ID)
	require.ElementsMatch(t, []string{"rock"}, genres)
}

// A source that gives a date but no start time must be recorded as such, so the
// stored local-midnight timestamp is never mistaken for a real start time.
func TestHandle_PersistsTimeTBDFlag(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	m := sampleMessage()
	m.SourceEventID = "tm-tbd"
	m.TimeTBD = true

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()
	body, _ := json.Marshal(m)
	require.NoError(t, h.Handle(ctx, body))

	srcRow, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	var timeTBD bool
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT time_tbd FROM events WHERE source_id = $1 AND source_event_id = $2",
		srcRow.ID, "tm-tbd").Scan(&timeTBD))
	require.True(t, timeTBD)
}

// A body whose enrichment block fails to decode (a type mismatch, as would
// arrive from a Lambda emit that skipped schema validation) must degrade to a
// plain event rather than being dropped — a plain event still ingests
// correctly, which is the whole point of the superset contract.
func TestHandle_BadEnrichmentBlock_DegradesToPlainEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()

	// "encore" is an int on the wire; sending a string breaks the
	// EnrichedMessage decode while every plain-Message field stays valid.
	body := []byte(`{
		"source_id": "ticketmaster",
		"source_event_id": "tm-bad-enrich",
		"title": "Phoebe Bridgers",
		"starts_at": "2026-06-15T20:00:00Z",
		"venue": {"name": "The Bowl"},
		"enrichment": {
			"attempted_at": "2026-08-12T04:11:22Z",
			"tour": {"status": "ok", "songs": [{"name": "S", "encore": "not-a-number"}]}
		}
	}`)
	require.NoError(t, h.Handle(ctx, body))

	srcRow, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: srcRow.ID, SourceEventID: "tm-bad-enrich",
	})
	require.NoError(t, err)
	require.Equal(t, "Phoebe Bridgers", ev.Title)
	require.False(t, ev.HeadlineArtistID.Valid, "the broken enrichment block must not be applied")
}

// A body that fails BOTH the EnrichedMessage and the plain-Message decode
// (not even valid JSON) is genuinely malformed and must be dropped rather
// than looping forever.
func TestHandle_GenuinelyMalformedBody_IsDropped(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()

	require.NoError(t, h.Handle(ctx, []byte(`not json`)))
}

func TestHandle_DefaultsTimeTBDToFalse(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()
	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, h.Handle(ctx, body))

	srcRow, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	var timeTBD bool
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT time_tbd FROM events WHERE source_id = $1 AND source_event_id = $2",
		srcRow.ID, "tm-aaa").Scan(&timeTBD))
	require.False(t, timeTBD)
}

// ---- segment ---------------------------------------------------------------

func eventBySourceKey(t *testing.T, q *store.Queries, sourceEventID string) store.GetEventBySourceKeyRow {
	t.Helper()
	ctx := context.Background()
	srcRow, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: sourceEventID,
	})
	require.NoError(t, err)
	return ev
}

func TestHandle_PersistsSegment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))

	m := sampleMessage()
	m.Segment = "sports"
	body, _ := json.Marshal(m)
	require.NoError(t, h.Handle(context.Background(), body))

	ev := eventBySourceKey(t, q, "tm-aaa")
	require.NotNil(t, ev.Segment)
	require.Equal(t, "sports", *ev.Segment)
}

// The email path never classifies its events, and every row that predates the
// column is NULL too. Both must stay distinguishable from a real segment.
func TestHandle_EmptySegmentStoresNull(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))

	body, _ := json.Marshal(sampleMessage()) // Segment unset
	require.NoError(t, h.Handle(context.Background(), body))

	require.Nil(t, eventBySourceKey(t, q, "tm-aaa").Segment)
}

// scripts/backfill.ts rebuilds wire messages from DB rows that carry no
// segment, so a backfill run republishes every event with the field empty.
// Assigning EXCLUDED directly would blank the column across the whole table.
func TestHandle_ReUpsertWithoutSegmentKeepsStoredSegment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	classified := sampleMessage()
	classified.Segment = "music"
	body, _ := json.Marshal(classified)
	require.NoError(t, h.Handle(ctx, body))

	backfilled := sampleMessage() // same source key, no segment
	body, _ = json.Marshal(backfilled)
	require.NoError(t, h.Handle(ctx, body))

	ev := eventBySourceKey(t, q, "tm-aaa")
	require.NotNil(t, ev.Segment, "a segment-less re-upsert must not blank the column")
	require.Equal(t, "music", *ev.Segment)
}

// A genuine reclassification still lands: COALESCE only guards the NULL case.
func TestHandle_ReUpsertWithNewSegmentOverwrites(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	m := sampleMessage()
	m.Segment = "miscellaneous"
	body, _ := json.Marshal(m)
	require.NoError(t, h.Handle(ctx, body))

	m.Segment = "music"
	body, _ = json.Marshal(m)
	require.NoError(t, h.Handle(ctx, body))

	ev := eventBySourceKey(t, q, "tm-aaa")
	require.NotNil(t, ev.Segment)
	require.Equal(t, "music", *ev.Segment)
}
