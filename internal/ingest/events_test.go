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
	mod.Performers = []string{"Phoebe Bridgers"}      // dropped MUNA
	mod.Genres = []string{"folk"}                     // changed genre
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
