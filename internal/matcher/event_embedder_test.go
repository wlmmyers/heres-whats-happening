package matcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/matcher"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type fakeEmbedder struct {
	calls [][]string
	vec   []float32
}

func (f *fakeEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	f.calls = append(f.calls, inputs)
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = f.vec
	}
	return out, nil
}

func TestEmbedEvents_EmbedsUnembeddedUpcoming(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	city, _ := q.GetDefaultCity(ctx)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID:         city.ID,
		Name:           "The Bowl",
		NormalizedName: "the bowl",
	})
	require.NoError(t, err)

	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "tm-embed-1",
		Title:         "Phoebe Bridgers",
		Description:   "Indie rock concert",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	require.NoError(t, q.InsertEventGenre(ctx, store.InsertEventGenreParams{
		EventID: eventID, GenreSlug: "indie",
	}))

	fakeVec := make([]float32, 384)
	for i := range fakeVec {
		fakeVec[i] = 0.1
	}
	emb := &fakeEmbedder{vec: fakeVec}
	step := matcher.NewEventEmbedder(q, emb)
	require.NoError(t, step.Run(ctx))

	require.Len(t, emb.calls, 1)
	require.Contains(t, emb.calls[0][0], "Phoebe Bridgers")
	require.Contains(t, emb.calls[0][0], "indie")

	ev, err := q.GetEventByID(ctx, eventID)
	require.NoError(t, err)
	require.NotNil(t, ev.Embedding)
	stored := ev.Embedding.Slice()
	require.Len(t, stored, 384)
	require.InDelta(t, 0.1, stored[0], 0.001)

	var _ pgvector.Vector // suppress unused-import warning if needed
}
