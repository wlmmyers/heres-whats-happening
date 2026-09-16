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

// Ticketmaster fills the Discovery API's `info` field with venue and ticketing
// boilerplate -- bag policies, ticket limits, the venue's street address -- and
// ingest maps it straight to events.description. In the dev catalogue 188 of
// 195 events carried one, but only 35 were distinct: 86 shared a single
// sentence about the arena's bag policy. Feeding that to the embedder dragged
// unrelated events toward the same point in vector space, hurting both
// matching and semantic search, so BuildEventText no longer sees it.
//
// The description column itself is untouched -- the event detail page still
// renders it. This test is the guard on the embedding path specifically.
func TestEmbedEvents_OmitsSourceDescription(t *testing.T) {
	const boilerplate = "Please visit our Ballpark Information Guide to make the most of your " +
		"visit to T-Mobile Park. Find the latest information regarding Bag Policy, " +
		"Prohibited Items, Transportation Options and more."

	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID:         city.ID,
		Name:           "T-Mobile Park",
		NormalizedName: "t-mobile park",
	})
	require.NoError(t, err)

	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "tm-boilerplate-1",
		Title:         "Mariners vs Athletics",
		Description:   boilerplate,
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Seattle Mariners", NormalizedName: "seattle mariners",
	}))
	require.NoError(t, q.InsertEventGenre(ctx, store.InsertEventGenreParams{
		EventID: eventID, GenreSlug: "sports",
	}))

	emb := &fakeEmbedder{vec: make([]float32, 384)}
	require.NoError(t, matcher.NewEventEmbedder(q, emb).Run(ctx))

	require.Len(t, emb.calls, 1)
	require.Len(t, emb.calls[0], 1)
	text := emb.calls[0][0]

	// The signal we keep, so a failure below is about the description and not
	// about the embedder having skipped the event entirely.
	require.Contains(t, text, "Mariners vs Athletics")
	require.Contains(t, text, "Seattle Mariners")
	require.Contains(t, text, "sports")

	require.NotContains(t, text, "Bag Policy")
	require.NotContains(t, text, boilerplate)
}
