package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// backdateLastSeen forces an event's last_seen_at into the past so the
// 7-day stale sweep would normally archive it.
func backdateLastSeen(t *testing.T, pool *pgxpool.Pool, sourceEventID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx,
		`UPDATE events SET last_seen_at = NOW() - INTERVAL '10 days' WHERE source_event_id = $1`,
		sourceEventID)
	require.NoError(t, err)
}

func TestArchiveStaleEvents_ExemptsEmailSource(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)
	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()

	// A ticketmaster event (non-exempt) and an email event (exempt), both stale.
	tm := sampleMessage()
	tm.SourceEventID = "tm-stale"
	tmBody, _ := json.Marshal(tm)
	require.NoError(t, h.Handle(ctx, tmBody))

	em := sampleMessage()
	em.SourceID = "email_newsletter"
	em.SourceEventID = "email-stale"
	emBody, _ := json.Marshal(em)
	require.NoError(t, h.Handle(ctx, emBody))

	backdateLastSeen(t, pool, "tm-stale")
	backdateLastSeen(t, pool, "email-stale")

	require.NoError(t, q.ArchiveStaleEvents(ctx))

	tmSrc, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	tmEv, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: tmSrc.ID, SourceEventID: "tm-stale"})
	require.NoError(t, err)
	require.True(t, tmEv.ArchivedAt.Valid, "ticketmaster event should be archived")

	emSrc, err := q.GetEventSourceByName(ctx, "email_newsletter")
	require.NoError(t, err)
	emEv, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: emSrc.ID, SourceEventID: "email-stale"})
	require.NoError(t, err)
	require.False(t, emEv.ArchivedAt.Valid, "email event must NOT be archived (exempt)")
}
