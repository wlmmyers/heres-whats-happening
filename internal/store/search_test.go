package store_test

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// seedSearchFixture builds a small catalogue with the properties each test
// below probes: an accented title, a venue whose name is also a word, and one
// event that is past so the showable filter has something to exclude.
func seedSearchFixture(t *testing.T, q *store.Queries, ctx context.Context) pgtype.UUID {
	t.Helper()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)

	mkVenue := func(name, norm string) pgtype.UUID {
		id, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
			CityID: city.ID, Name: name, NormalizedName: norm,
		})
		require.NoError(t, err)
		return id
	}
	showbox := mkVenue("The Showbox", "the showbox")
	bowl := mkVenue("The Bowl", "the bowl")

	mkEvent := func(srcID, title string, venue pgtype.UUID, startsAt time.Time) pgtype.UUID {
		id, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: srcID,
			Title:         title,
			StartsAt:      pgtype.Timestamptz{Time: startsAt, Valid: true},
			VenueID:       venue,
		})
		require.NoError(t, err)
		return id
	}
	future := time.Now().Add(48 * time.Hour)
	mkEvent("s-accent", "Edén Muñoz: Como En Los Viejos Tiempos Tour", bowl, future)
	mkEvent("s-orchard", "Midnight Orchard", bowl, future)
	mkEvent("s-at-showbox", "Some Other Band", showbox, future)
	mkEvent("s-titled-showbox", "Showbox Allstars", bowl, future)
	mkEvent("s-past", "Midnight Orchard Farewell", bowl, time.Now().Add(-72*time.Hour))

	// Performer-leg coverage. "Warehouse District Sessions" carries no trace of
	// "Nova Ridge" in its title, so it is reachable ONLY through the 0.9
	// weighted performer leg -- without this row that leg has zero coverage.
	// "Nova Ridge" is ALSO a headliner event's title, so a single query against
	// the same string exercises the title leg (weight 1.0) and the performer
	// leg (weight 0.9) at equal underlying similarity, a direct probe of the
	// weighting between them.
	support := mkEvent("s-support", "Warehouse District Sessions", bowl, future)
	err = q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: support, PerformerName: "Nova Ridge", NormalizedName: "nova ridge",
	})
	require.NoError(t, err)
	// Later than the performer event on purpose. Given the same starts_at,
	// dropping the 0.9 performer weight ties these two on rank AND on
	// starts_at, leaving only e.id to separate them -- so the weighting test
	// below would catch the regression barely half the time. A later start
	// makes "Nova Ridge" LOSE the starts_at tiebreak, so without the weight the
	// wrong row is first every time.
	mkEvent("s-headliner", "Nova Ridge", bowl, future.Add(24*time.Hour))

	return city.ID
}

func search(t *testing.T, q *store.Queries, ctx context.Context, cityID pgtype.UUID, term string) []store.SearchEventsRow {
	t.Helper()
	rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
		Query: term, CityID: cityID, ResultLimit: 10,
	})
	require.NoError(t, err)
	return rows
}

func titles(rows []store.SearchEventsRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

// The regression test for the whole immutable_unaccent design.
func TestSearchEvents_FindsAccentedTitleWithoutAccentKeys(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "eden munoz")),
		"Edén Muñoz: Como En Los Viejos Tiempos Tour")
}

// Threshold 0.2, not the 0.3 default. "orchrd" (one character short of
// "orchard") against "Midnight Orchard" measures similarity 0.2631579 --
// verified directly with `similarity(immutable_unaccent('Midnight Orchard'),
// immutable_unaccent('orchrd'))` against this schema -- strictly between the
// two thresholds, so this probe only succeeds if the 0.2 override is actually
// in effect. ("midnite orchard" measures 0.65: comfortably over BOTH
// thresholds, so it would pass even if the override silently reverted.)
func TestSearchEvents_ToleratesOneCharacterTypo(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "orchrd")), "Midnight Orchard")

	// Pin the negative side too: at the 0.3 default the same probe must NOT
	// match. SET LOCAL, not SET, so the override lives only inside this
	// transaction and can never leak into the shared pool testdb.MustOpen
	// hands to every other test.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "SET LOCAL pg_trgm.similarity_threshold = 0.3")
	require.NoError(t, err)
	rows, err := q.WithTx(tx).SearchEvents(ctx, store.SearchEventsParams{
		Query: "orchrd", CityID: cityID, ResultLimit: 10,
	})
	require.NoError(t, err)
	require.NotContains(t, titles(rows), "Midnight Orchard")
}

// Trigram similarity is word-order independent; plain ILIKE is not.
func TestSearchEvents_IsWordOrderIndependent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "orchard midnight")), "Midnight Orchard")
}

// The 0.6 venue weight exists for exactly this: a venue match must never
// outrank an event whose TITLE carries the term.
func TestSearchEvents_TitleMatchOutranksVenueMatch(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	rows := search(t, q, ctx, cityID, "showbox")
	require.NotEmpty(t, rows)
	require.Equal(t, "Showbox Allstars", rows[0].Title)
	require.Contains(t, titles(rows), "Some Other Band", "venue match still surfaces, just lower")
}

func TestSearchEvents_ExcludesPastEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.NotContains(t, titles(search(t, q, ctx, cityID, "midnight orchard")),
		"Midnight Orchard Farewell")
}

func TestSearchEvents_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	_, err := pool.Exec(ctx,
		`UPDATE events SET archived_at = NOW() WHERE title = 'Midnight Orchard'`)
	require.NoError(t, err)

	require.NotContains(t, titles(search(t, q, ctx, cityID, "midnight orchard")), "Midnight Orchard")
}

func TestSearchEvents_ScopesToCity(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	seedSearchFixture(t, q, ctx)

	otherCity := pgtype.UUID{Bytes: [16]byte{9, 9, 9, 9}, Valid: true}
	require.Empty(t, search(t, q, ctx, otherCity, "midnight orchard"))
}

// Three events now qualify for "midnight orchard": the fixture's own
// "Midnight Orchard" plus two seeded here (measured similarity 0.68 and
// 0.654, both comfortably over 0.2). With only one qualifying row, asserting
// a single result at ResultLimit 1 would pass even with LIMIT removed
// entirely -- this seeds enough candidates that truncation is the only thing
// that can make the assertion pass.
func TestSearchEvents_RespectsResultLimit(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: cityID, Name: "Limit Test Venue", NormalizedName: "limit test venue",
	})
	require.NoError(t, err)
	future := time.Now().Add(48 * time.Hour)
	for _, seed := range []struct{ srcID, title string }{
		{"s-limit-a", "Midnight Orchard Revival"},
		{"s-limit-b", "Orchard Midnight Sessions"},
	} {
		_, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID: src.ID, SourceEventID: seed.srcID, Title: seed.title,
			StartsAt: pgtype.Timestamptz{Time: future, Valid: true}, VenueID: venueID,
		})
		require.NoError(t, err)
	}

	unlimited, err := q.SearchEvents(ctx, store.SearchEventsParams{
		Query: "midnight orchard", CityID: cityID, ResultLimit: 10,
	})
	require.NoError(t, err)
	require.Greater(t, len(unlimited), 1,
		"fixture must have more than one qualifying row for LIMIT to prove anything")

	rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
		Query: "midnight orchard", CityID: cityID, ResultLimit: 1,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// Order must be total, or identical requests reorder and the dropdown
// flickers between keystrokes. Two events here share an identical title
// (so identical rank) AND an identical starts_at, so b.rank DESC, starts_at
// ASC alone cannot order them -- only e.id ASC can, and it must do so the
// same way on every call.
func TestSearchEvents_OrderIsStableAcrossIdenticalCalls(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: cityID, Name: "Tie Venue", NormalizedName: "tie venue",
	})
	require.NoError(t, err)
	tieTime := time.Now().Add(72 * time.Hour)
	tieIDs := make([]pgtype.UUID, 0, 2)
	for _, srcID := range []string{"s-tie-a", "s-tie-b"} {
		id, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID: src.ID, SourceEventID: srcID, Title: "Tiebreak Rally",
			StartsAt: pgtype.Timestamptz{Time: tieTime, Valid: true}, VenueID: venueID,
		})
		require.NoError(t, err)
		tieIDs = append(tieIDs, id)
	}
	a, b := tieIDs[0], tieIDs[1]
	if bytes.Compare(a.Bytes[:], b.Bytes[:]) > 0 {
		a, b = b, a
	}
	wantOrder := []pgtype.UUID{a, b}

	tieOrder := func() []pgtype.UUID {
		rows := search(t, q, ctx, cityID, "tiebreak rally")
		require.Len(t, rows, 2, "both tied events must come back")
		return []pgtype.UUID{rows[0].ID, rows[1].ID}
	}

	first := tieOrder()
	require.Equal(t, wantOrder, first, "rank and starts_at tie -- only ascending id can have ordered these")
	for i := 0; i < 5; i++ {
		require.Equal(t, first, tieOrder())
	}
}

// The 0.9 performer weight exists for exactly this: a performer match must
// never outrank an event whose TITLE carries the same term, but it must still
// surface an event whose title says nothing about the performer at all.
func TestSearchEvents_PerformerMatchOutrankedByTitleMatch(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	rows := search(t, q, ctx, cityID, "Nova Ridge")
	require.NotEmpty(t, rows)
	require.Equal(t, "Nova Ridge", rows[0].Title)
	require.Contains(t, titles(rows), "Warehouse District Sessions",
		"performer match still surfaces, just lower")
}

// --- SearchEventsSemantic ---------------------------------------------------
//
// Kept separate from seedSearchFixture above: that fixture's events carry no
// embeddings, and giving them one here would entangle two independent query
// paths -- trigram similarity and cosine distance -- under one shared
// dataset. Each test below builds its own small, embedding-bearing fixture.

// unitVector384 is a unit vector along one axis of a 384-dim space (the
// column's declared width and TEI's real output width). Two unit vectors
// along the SAME axis have cosine similarity exactly 1 (distance 0); two
// along DIFFERENT axes are exactly orthogonal (similarity 0, distance 1).
// Both are exact dot-product arithmetic, not measured approximations, so the
// distances the ordering test asserts on cannot be a coincidence of
// floating-point rounding.
func unitVector384(axis int) []float32 {
	v := make([]float32, 384)
	v[axis] = 1
	return v
}

// probeVector is the "query embedding" every test below searches with.
// nearVector points along the same axis (cosine distance from probeVector:
// exactly 0). farVector is orthogonal to it (cosine distance: exactly 1).
func probeVector() []float32 { return unitVector384(0) }
func nearVector() []float32  { return unitVector384(0) }
func farVector() []float32   { return unitVector384(1) }

// semanticFixture returns the default city, the ticketmaster source, and a
// dedicated venue for one SearchEventsSemantic test.
func semanticFixture(t *testing.T, q *store.Queries, ctx context.Context, venueName string) (cityID, srcID, venueID pgtype.UUID) {
	t.Helper()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err = q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: venueName, NormalizedName: strings.ToLower(venueName),
	})
	require.NoError(t, err)
	return city.ID, src.ID, venueID
}

// mkSemanticEvent creates an event and, when embedding is non-nil, sets its
// embedding via UpdateEventEmbedding -- the same call the real backfill job
// makes, so these fixtures exercise the same write path production uses. A
// nil embedding leaves the column NULL, which is the default for a freshly
// upserted event (UpsertEvent never touches it).
func mkSemanticEvent(
	t *testing.T, q *store.Queries, ctx context.Context,
	srcID, venueID pgtype.UUID, sourceEventID, title string, startsAt time.Time, embedding []float32,
) pgtype.UUID {
	t.Helper()
	id, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: srcID, SourceEventID: sourceEventID, Title: title,
		StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, VenueID: venueID,
	})
	require.NoError(t, err)
	if embedding != nil {
		vec := pgvector.NewVector(embedding)
		require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
			ID: id, Embedding: &vec,
		}))
	}
	return id
}

func semanticSearchRows(t *testing.T, q *store.Queries, ctx context.Context, cityID pgtype.UUID, probe []float32, limit int32) []store.SearchEventsSemanticRow {
	t.Helper()
	vec := pgvector.NewVector(probe)
	rows, err := q.SearchEventsSemantic(ctx, store.SearchEventsSemanticParams{
		CityID: cityID, QueryEmbedding: &vec, CandidateLimit: limit,
	})
	require.NoError(t, err)
	return rows
}

// Most tests here care only about which ids came back in what order; the
// projection itself is pinned by TestSearchEventsSemantic_ProjectsDisplayColumns.
func semanticSearch(t *testing.T, q *store.Queries, ctx context.Context, cityID pgtype.UUID, probe []float32, limit int32) []pgtype.UUID {
	t.Helper()
	rows := semanticSearchRows(t, q, ctx, cityID, probe, limit)
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// The semantic leg must carry the SAME display columns as SearchEvents, not
// bare ids. RRF fuses orderings, so a fused id can come from this leg alone
// (the design's "baseball" case) and the handler has nothing else to render it
// from -- when this query returned only ids, every such hit was silently
// dropped and the flag was a no-op for exactly the queries it exists for.
// Reducing this back to `SELECT e.id` must fail here rather than in production.
func TestSearchEventsSemantic_ProjectsDisplayColumns(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue H")

	startsAt := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-projection",
		"Projected Signal", startsAt, probeVector())

	rows := semanticSearchRows(t, q, ctx, cityID, probeVector(), 10)
	require.Len(t, rows, 1)
	require.Equal(t, "Projected Signal", rows[0].Title)
	require.Equal(t, "Semantic Venue H", rows[0].VenueName)
	require.WithinDuration(t, startsAt, rows[0].StartsAt.Time, time.Second)
	require.True(t, rows[0].StartsAt.Valid)
}

func containsID(ids []pgtype.UUID, target pgtype.UUID) bool {
	for _, id := range ids {
		if id.Bytes == target.Bytes {
			return true
		}
	}
	return false
}

// The query works at all: an event with a live embedding comes back for a
// matching probe.
func TestSearchEventsSemantic_ReturnsEventWithEmbedding(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue A")

	future := time.Now().Add(48 * time.Hour)
	eventID := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-has-embedding",
		"Neon Constellation", future, probeVector())

	ids := semanticSearch(t, q, ctx, cityID, probeVector(), 10)
	require.True(t, containsID(ids, eventID), "an event with a live embedding must be returned")
}

// The core ranking property: distances are computed exactly (see
// unitVector384), so an implementation with a wrong join, wrong parameter
// order, or a swapped ORDER BY direction produces the reverse order, not a
// slightly-off one -- there is no floating-point tolerance to hide behind.
func TestSearchEventsSemantic_OrdersByCosineDistance(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue B")

	future := time.Now().Add(48 * time.Hour)
	// far FIRST, deliberately. Seeded near-then-far, insertion order coincides
	// with the asserted order, so the test would still pass with the ORDER BY
	// dropped entirely -- a seq scan returns heap order. Reversing it means
	// only a real ORDER BY can produce the expected answer.
	far := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-far", "Distant Signal", future, farVector())
	near := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-near", "Nearby Signal", future, nearVector())

	ids := semanticSearch(t, q, ctx, cityID, probeVector(), 10)
	require.Len(t, ids, 2)
	require.Equal(t, near, ids[0], "cosine distance 0 (same axis) must rank ahead of distance 1 (orthogonal)")
	require.Equal(t, far, ids[1])
}

// A NULL embedding must never reach the ORDER BY -- if the "embedding IS NOT
// NULL" filter were dropped, this event either sorts arbitrarily (Postgres
// treats a NULL distance as NULLS LAST by default, which could coincidentally
// still exclude it from a small LIMIT) or errors outright depending on the
// bug, so the test seeds a companion WITH an embedding and asserts on the
// null one's absence specifically, not just on result count.
func TestSearchEventsSemantic_ExcludesNullEmbedding(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue C")

	future := time.Now().Add(48 * time.Hour)
	withEmbedding := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-with-embedding",
		"Embedded Echo", future, probeVector())
	withoutEmbedding := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-without-embedding",
		"Unembedded Echo", future, nil)

	ids := semanticSearch(t, q, ctx, cityID, probeVector(), 10)
	require.True(t, containsID(ids, withEmbedding))
	require.False(t, containsID(ids, withoutEmbedding), "a NULL embedding must never be returned")
}

func TestSearchEventsSemantic_ExcludesPastEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue D")

	past := time.Now().Add(-72 * time.Hour)
	pastEvent := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-past",
		"Yesterday's Frequency", past, probeVector())

	ids := semanticSearch(t, q, ctx, cityID, probeVector(), 10)
	require.False(t, containsID(ids, pastEvent))
}

func TestSearchEventsSemantic_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue E")

	future := time.Now().Add(48 * time.Hour)
	archived := mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-archived",
		"Archived Frequency", future, probeVector())
	_, err := pool.Exec(ctx, `UPDATE events SET archived_at = NOW() WHERE id = $1`, archived)
	require.NoError(t, err)

	ids := semanticSearch(t, q, ctx, cityID, probeVector(), 10)
	require.False(t, containsID(ids, archived))
}

func TestSearchEventsSemantic_ScopesToCity(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	_, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue F")

	future := time.Now().Add(48 * time.Hour)
	mkSemanticEvent(t, q, ctx, srcID, venueID, "sem-other-city",
		"Foreign Frequency", future, probeVector())

	otherCity := pgtype.UUID{Bytes: [16]byte{9, 9, 9, 9}, Valid: true}
	require.Empty(t, semanticSearch(t, q, ctx, otherCity, probeVector(), 10))
}

// Order must be total here for the same reason it must be total in the lexical
// leg (see TestSearchEvents_OrderIsStableAcrossIdenticalCalls), and ties are
// not an edge case: recurring events feed BuildEventText identical input, so
// they get byte-identical embeddings. Measured on the 195-event dev catalogue,
// 80 embedded events fall into 26 byte-identical groups -- 41% of the corpus.
//
// Within such a group `ORDER BY e.embedding <=> $2` alone leaves both the
// ordering AND which rows survive LIMIT unpinned, and ingestion re-upserts
// constantly, so the answer can change between keystrokes.
//
// Six tied rows, not two: ids are gen_random_uuid(), so with two rows an
// unordered implementation would coincidentally agree with ascending id half
// the time. Six makes that coincidence 1-in-720.
func TestSearchEventsSemantic_OrderIsStableAcrossIdenticalCalls(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID, srcID, venueID := semanticFixture(t, q, ctx, "Semantic Venue G")

	future := time.Now().Add(48 * time.Hour)
	tied := make([]pgtype.UUID, 0, 6)
	for _, srcEventID := range []string{
		"sem-tie-a", "sem-tie-b", "sem-tie-c", "sem-tie-d", "sem-tie-e", "sem-tie-f",
	} {
		// Identical embedding for every row: the cosine distance cannot
		// separate them, so only the id tiebreak can.
		tied = append(tied, mkSemanticEvent(t, q, ctx, srcID, venueID, srcEventID,
			"Recurring Signal", future, nearVector()))
	}
	want := append([]pgtype.UUID(nil), tied...)
	sort.Slice(want, func(i, j int) bool {
		return bytes.Compare(want[i].Bytes[:], want[j].Bytes[:]) < 0
	})

	first := semanticSearch(t, q, ctx, cityID, probeVector(), 10)
	require.Equal(t, want, first,
		"every row is at the same cosine distance -- only ascending id can have ordered these")
	for i := 0; i < 5; i++ {
		require.Equal(t, first, semanticSearch(t, q, ctx, cityID, probeVector(), 10),
			"identical calls must return an identical order")
	}

	// The half a pure ordering assertion misses: LIMIT cuts the group, so an
	// unpinned order also makes MEMBERSHIP unstable. The first three by id are
	// the only correct answer at CandidateLimit 3.
	require.Equal(t, want[:3], semanticSearch(t, q, ctx, cityID, probeVector(), 3),
		"which tied rows survive LIMIT must be pinned too")
}
