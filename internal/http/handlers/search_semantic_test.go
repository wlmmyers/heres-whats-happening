package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type stubEmbedder struct {
	calls int
	err   error
	vec   []float32
}

func (s *stubEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = s.vec
	}
	return out, nil
}

func semanticRouter(deps handlers.SearchDeps) http.Handler {
	r := chi.NewRouter()
	r.Get("/search/{cityId}/events", handlers.SearchEvents(deps))
	return r
}

func zeroVec() []float32 { return make([]float32, 384) }

// Flag off is the default: TEI must not be touched at all.
func TestSearch_FlagOff_DoesNotCallEmbedder(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{vec: zeroVec()}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: false,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, emb.calls)
}

func TestSearch_FlagOn_CallsEmbedder(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{vec: zeroVec()}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, emb.calls)
}

// The property that makes the flag safe to flip against a Spot service.
func TestSearch_FlagOn_TEIFailureDegradesToLexical(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{err: errors.New("connection refused")}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code, "a TEI outage must never fail the request")
	require.Contains(t, rec.Body.String(), "Midnight Orchard")
}

// An embedder that returns no vectors is a contract violation, not an outage --
// it must degrade the same way rather than panic on an empty slice.
func TestSearch_FlagOn_EmptyEmbeddingDegradesToLexical(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	emb := &stubEmbedder{vec: nil}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), cityID, "midnight")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Midnight Orchard")
}

// --- Fusion seam -----------------------------------------------------------
//
// The tests above prove the flag is wired and that every failure degrades to
// lexical. None of them proves the two legs actually FUSE: the fixture event
// they share has no embedding, so the semantic query returns zero ids and
// fusion is a no-op over an empty list. This section builds a catalogue where
// the two legs disagree on purpose.

// probe384 is the vector the stub embedder returns for every query: a unit
// vector along axis 0 of the column's declared 384-dim space.
func probe384() []float32 {
	v := make([]float32, 384)
	v[0] = 1
	return v
}

// blend384 returns [k, 1, 0, ...]. Its cosine distance from probe384 is
// 1 - k/sqrt(k²+1), which decreases strictly as k grows, so the k values below
// name an exact distance ladder rather than a measured approximation:
//
//	k=100 -> 0.000050    k=10 -> 0.004963    k=3 -> 0.051317    k=1 -> 0.292893
//
// Verified directly against pgvector with `'[1,0,0]'::vector <=> ...`.
func blend384(k float32) []float32 {
	v := make([]float32, 384)
	v[0] = k
	v[1] = 1
	return v
}

type fusionFixture struct {
	cityID string
	// Titles, in the order each leg alone would return them.
	lexicalOrder  []string
	semanticOrder []string
}

// seedFusionFixture builds four events whose lexical and semantic orders
// CONTRADICT each other, so the fused order can equal neither leg alone.
//
// The lexical ladder is built from the query's three weighted legs rather than
// from fuzzy similarity, so it is exact: the query string is one event's whole
// title (similarity 1.0 x weight 1.0), another event's whole performer name
// (1.0 x 0.9) and a third event's whole venue name (1.0 x 0.6). Every other
// pairing in this fixture measures at most 0.045 -- far under the 0.2
// threshold -- so no unintended row joins the ladder.
//
// The semantic ladder is the deliberate inversion of it, plus one event that
// the lexical leg cannot see at all: "Baseball Night" is the spec's own
// motivating example, an event no trigram query for "Neon Constellation" will
// ever return.
func seedFusionFixture(t *testing.T, q *store.Queries, ctx context.Context) fusionFixture {
	t.Helper()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)

	mkVenue := func(name string) pgtype.UUID {
		id, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
			CityID: city.ID, Name: name, NormalizedName: strings.ToLower(name),
		})
		require.NoError(t, err)
		return id
	}
	// One venue named exactly like the query, so events held there pick up the
	// 0.6 venue leg; everything else sits at a venue that matches nothing.
	alpha := mkVenue("Venue Alpha")
	namedVenue := mkVenue("Neon Constellation")

	future := time.Now().Add(48 * time.Hour)
	mkEvent := func(sourceEventID, title string, venue pgtype.UUID, embedding []float32) pgtype.UUID {
		id, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID: src.ID, SourceEventID: sourceEventID, Title: title,
			StartsAt: pgtype.Timestamptz{Time: future, Valid: true}, VenueID: venue,
		})
		require.NoError(t, err)
		vec := pgvector.NewVector(embedding)
		require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
			ID: id, Embedding: &vec,
		}))
		return id
	}

	// Lexical 1 (title, 1.0) / semantic 4 (k=1, the farthest).
	mkEvent("fuse-title", "Neon Constellation", alpha, blend384(1))
	// Lexical 2 (performer, 0.9) / semantic 3 (k=3).
	performerEvent := mkEvent("fuse-performer", "Warehouse District Sessions", alpha, blend384(3))
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: performerEvent, PerformerName: "Neon Constellation",
		NormalizedName: "neon constellation",
	}))
	// Lexical 3 (venue, 0.6) / semantic 1 (k=100, the nearest).
	mkEvent("fuse-venue", "Harbor Lights Revue", namedVenue, blend384(100))
	// Lexically invisible / semantic 2 (k=10). The "baseball" case.
	mkEvent("fuse-semantic-only", "Baseball Night", alpha, blend384(10))

	return fusionFixture{
		cityID:        uuidFromPgCal(city.ID).String(),
		lexicalOrder:  []string{"Neon Constellation", "Warehouse District Sessions", "Harbor Lights Revue"},
		semanticOrder: []string{"Harbor Lights Revue", "Baseball Night", "Warehouse District Sessions", "Neon Constellation"},
	}
}

func resultTitles(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body searchBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	out := make([]string, 0, len(body.Results))
	for _, r := range body.Results {
		out = append(out, r.Title)
	}
	return out
}

// The seam test. With k = 60 and the ladders seeded above:
//
//	Harbor Lights Revue          lex 3, sem 1 -> 1/63 + 1/61 = 0.0322664
//	Neon Constellation           lex 1, sem 4 -> 1/61 + 1/64 = 0.0320184
//	Warehouse District Sessions  lex 2, sem 3 -> 1/62 + 1/63 = 0.0320020
//	Baseball Night               ----   sem 2 ->        1/62 = 0.0161290
//
// so the fused order is neither leg's. Three separate things break this test:
// fusing on score instead of rank, ignoring one leg entirely, and -- the
// finding it was written for -- rendering fused ids out of the LEXICAL rows
// only, which silently drops "Baseball Night".
func TestSearch_FlagOn_FusesBothLegs(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	fx := seedFusionFixture(t, q, ctx)

	emb := &stubEmbedder{vec: probe384()}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), fx.cityID, "Neon%20Constellation")
	require.Equal(t, http.StatusOK, rec.Code)

	got := resultTitles(t, rec)
	require.Equal(t, []string{
		"Harbor Lights Revue",
		"Neon Constellation",
		"Warehouse District Sessions",
		"Baseball Night",
	}, got)
	require.NotEqual(t, fx.lexicalOrder, got, "the fused order must not be the lexical order")
	require.NotEqual(t, fx.semanticOrder, got, "the fused order must not be the semantic order")
}

// The spec's motivating example, isolated: the shape of "baseball" against a
// catalogue whose Mariners events never contain that string -- zero lexical
// rows, and every hit reachable through the semantic leg alone. Before that leg
// carried display columns this returned {"results":[]}, identical to the flag
// being off, so the entire recall benefit the leg exists for was unreachable.
//
// The query is nonsense rather than the literal word "baseball" because the
// fixture's semantic-only event is TITLED "Baseball Night" -- that string is a
// trigram hit on itself, which would give the lexical leg a row and stop this
// from being the zero-lexical-rows case. The stub embedder ignores the query
// text entirely, so the string's only job here is to match nothing lexically.
func TestSearch_FlagOn_SemanticOnlyQueryStillReturnsResults(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	fx := seedFusionFixture(t, q, ctx)

	emb := &stubEmbedder{vec: probe384()}
	rec := doSearch(t, semanticRouter(handlers.SearchDeps{
		Queries: q, Embedder: emb, SemanticEnabled: true,
	}), fx.cityID, "zzzqqqxxx")
	require.Equal(t, http.StatusOK, rec.Code)

	// Every row here arrived through the semantic leg alone and is rendered
	// from its own projection. The order is the semantic order, because RRF
	// over one leg is just that leg.
	require.Equal(t, fx.semanticOrder, resultTitles(t, rec))

	// The rendered row must be whole, not an id with empty display fields:
	// that was the other way a semantic-only hit could "surface" uselessly.
	var body searchBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "Harbor Lights Revue", body.Results[0].Title)
	require.Equal(t, "Neon Constellation", body.Results[0].Venue.Name)
	require.NotEmpty(t, body.Results[0].StartsAt)
}
