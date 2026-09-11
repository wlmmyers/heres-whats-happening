# Event Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a typeahead search dialog to the calendar page, backed by a new Postgres trigram endpoint, with a flag-gated pgvector semantic leg.

**Architecture:** Three GIN trigram indexes (event title, performer name, venue name) queried as a UNION and ranked by weighted `max()` similarity, filtered to the same live+upcoming rows the calendar already shows. A second pgvector leg is built but disabled by default; when enabled, the two legs are fused by Reciprocal Rank Fusion on *rank*, never on score. No new infrastructure.

**Tech Stack:** Go 1.24, chi v5, pgx/v5, sqlc, Postgres 16 (`pg_trgm`, `unaccent`, `pgvector`), React 19, TanStack Query v5, vanilla-extract, Vitest.

**Spec:** `docs/superpowers/specs/2026-09-10-event-search-design.md`

## Global Constraints

- **Corpus is live + upcoming only.** Every query filters `archived_at IS NULL AND event_over_at(starts_at, ends_at, time_tbd) > NOW()`. Never search archived or past events.
- **Similarity threshold is `0.2`**, not the Postgres default of `0.3`, delivered as an `options` parameter on `dsn.Components.DSN()` so the app pool, migrations and the test pool all inherit it. At `0.3` a bare short typo returns nothing. This value is **unvalidated against production data** — it was tuned on a synthetic corpus of ~20 distinct titles. Ship it as a named constant carrying that caveat.
- **One accent-folding transform, applied to both sides of every comparison.** `immutable_unaccent(...)` only. Never mix in `events.NormalizeString` or the existing `normalized_name` columns — they disagree on `ß`→`ss` and `Ø`→`O`.
- **`pg_trgm` is already case-insensitive.** Never add `lower()`.
- **Minimum query length is 3 runes** (counted as runes, not bytes or UTF-16 units). Below 3 the trigram index cannot be used and the query degrades to a full scan: 43ms at 10k rows, growing linearly.
- **Maximum query length is 100 runes**, truncated rather than rejected.
- **Result limit is 10**, fixed server-side. Candidate limit when fusing is 50.
- **`rank` never appears in an API response.** It is an internal ordering signal.
- **RRF fuses on rank, never on score.** Cosine similarity bands overlap across queries — `0.561` is a correct hit for one query while `0.556` is noise for another. No score cutoff can work.
- **A TEI failure must never fail the request.** Log it and return the lexical results alone.
- Prerequisites for every Go test step: `make db-up queue-up` must be running. `testdb.MustOpen` migrates the test DB automatically. If you work in a git worktree, note that all worktrees share one local `appdb_test` — a newer migration here will strand `master`'s tests with "no migration found for version N".
- Full suite: `make test` (`go test -p 1 ./... -count=1`). Web typecheck is `tsc -b` — plain `tsc --noEmit` hits the solution-style root config, compiles nothing, and passes falsely.
- The pre-commit hook runs gofmt, `go vet`, the Go suite, eslint, `tsc`, prettier and vitest (~35s+). Do not bypass it with `--no-verify`.

---

### Task 1: Migration and similarity threshold

Creates the extensions, the `immutable_unaccent` wrapper, and the three trigram indexes; sets the session threshold on every pooled connection.

**Files:**
- Create: `sql/migrations/0029_event_search.up.sql`
- Create: `sql/migrations/0029_event_search.down.sql`
- Modify: `internal/dsn/dsn.go`
- Modify: `internal/dsn/dsn_test.go`
- Test: `internal/db/search_index_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: SQL function `immutable_unaccent(text) RETURNS text`; indexes `events_title_trgm`, `event_performers_name_trgm`, `venues_name_trgm`; every connection built from `dsn.Components.DSN()` has `pg_trgm.similarity_threshold = 0.2`.

- [ ] **Step 1: Write the failing test**

Create `internal/db/search_index_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// The whole accent design rests on this. Untreated, pg_trgm scores
// ('eden munoz', 'Edén Muñoz') at 0.294 -- under the default 0.3 threshold AND
// under the 0.2 this app uses -- so the event is unreachable without accent
// keys. immutable_unaccent folds both sides to 1.0.
func TestImmutableUnaccent_FoldsAccentsForTrigramMatching(t *testing.T) {
	pool := testdb.MustOpen(t)
	ctx := context.Background()

	var raw float32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT similarity('eden munoz', 'Edén Muñoz')`).Scan(&raw))
	require.Less(t, raw, float32(0.3), "precondition: raw accented compare is below threshold")

	var folded float32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT similarity(immutable_unaccent('eden munoz'), immutable_unaccent('Edén Muñoz'))`).
		Scan(&folded))
	require.Equal(t, float32(1), folded)
}

// unaccent() is STABLE and cannot appear in an index expression; the wrapper
// must be IMMUTABLE or every index in the migration fails to create.
func TestImmutableUnaccent_IsImmutable(t *testing.T) {
	pool := testdb.MustOpen(t)
	var volatility string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT provolatile FROM pg_proc WHERE proname = 'immutable_unaccent'`).Scan(&volatility))
	require.Equal(t, "i", volatility, "provolatile must be 'i' (immutable)")
}

func TestSearchIndexesExist(t *testing.T) {
	pool := testdb.MustOpen(t)
	for _, name := range []string{
		"events_title_trgm", "event_performers_name_trgm", "venues_name_trgm",
	} {
		var exists bool
		require.NoError(t, pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)`, name).Scan(&exists))
		require.True(t, exists, "index %s missing", name)
	}
}

// The pool sets this on every connection; 0.3 would silently lose typo
// tolerance for short queries.
func TestPoolSetsSimilarityThreshold(t *testing.T) {
	pool := testdb.MustOpen(t)
	var threshold float64
	require.NoError(t, pool.QueryRow(context.Background(),
		`SHOW pg_trgm.similarity_threshold`).Scan(&threshold))
	require.InDelta(t, 0.2, threshold, 0.0001)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/db -run 'TestImmutableUnaccent|TestSearchIndexes|TestPoolSets' -count=1 -v`

Expected: FAIL — `function immutable_unaccent(unknown) does not exist`.

- [ ] **Step 3: Write the migration**

Create `sql/migrations/0029_event_search.up.sql`:

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE, not IMMUTABLE, so it cannot appear in an index
-- expression. The two-argument form names the dictionary explicitly, which
-- makes the result deterministic and the wrapper safe to mark IMMUTABLE --
-- the documented Postgres workaround.
--
-- What this buys: "eden munoz" finds "Edén Muñoz". Untreated, pg_trgm scores
-- that pair at 0.294 -- under the default 0.3 threshold AND under the 0.2 this
-- app uses -- so the event is simply unreachable without the accent keys.
--
-- The caveat inherent to the workaround: if the unaccent dictionary is ever
-- changed, every index below must be REINDEXed. It ships with Postgres and we
-- do not modify it.
CREATE FUNCTION immutable_unaccent(text) RETURNS text
    AS $$ SELECT public.unaccent('public.unaccent', $1) $$
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE;

-- One index per search leg. No lower() -- pg_trgm is already case-insensitive,
-- so the accent fold is the only transform needed. Plain CREATE INDEX, not
-- CONCURRENTLY: all three build in 0.68s at 10k rows, and CONCURRENTLY cannot
-- run inside the migration transaction anyway.
CREATE INDEX events_title_trgm ON events
    USING gin (immutable_unaccent(title) gin_trgm_ops);
CREATE INDEX event_performers_name_trgm ON event_performers
    USING gin (immutable_unaccent(performer_name) gin_trgm_ops);
CREATE INDEX venues_name_trgm ON venues
    USING gin (immutable_unaccent(name) gin_trgm_ops);
```

Create `sql/migrations/0029_event_search.down.sql`:

```sql
DROP INDEX IF EXISTS venues_name_trgm;
DROP INDEX IF EXISTS event_performers_name_trgm;
DROP INDEX IF EXISTS events_title_trgm;
DROP FUNCTION IF EXISTS immutable_unaccent(text);
-- Extensions deliberately left in place: they are idempotent to re-create and
-- dropping them on a shared database is a wider blast radius than this
-- migration owns.
```

- [ ] **Step 4: Set the threshold on every connection**

This goes in the DSN builder, **not** in `internal/db`. `testdb.MustOpen` builds
its pool with a bare `pgxpool.New` and never calls `db.NewPool`, so an
`AfterConnect` hook there would never reach the integration tests in Tasks 2, 3
and 5 — they would silently run at the 0.3 default and the typo test would fail.
`internal/testdb` also cannot import `internal/db`, because `internal/db/db_test.go`
imports testdb and the reverse direction is an import cycle. `Components.DSN()`
is the one point all three consumers share: `cmd/app/main.go:350` (migrate),
`internal/config/config.go:79` (app pool), `internal/testdb/testdb.go:34` (test pool).

In `internal/dsn/dsn.go`, inside `Components.DSN()`, add the parameter to the
query string that currently carries only `sslmode`:

```go
	// pg_trgm's % operator reads this GUC; the default is 0.3, at which a bare
	// short typo ("orchrd" for "Orchard") matches nothing. 0.2 recovers it.
	//
	// UNVALIDATED against production data: tuned on a synthetic corpus with
	// ~20 distinct titles, so its false-positive cost on the real catalogue is
	// unknown. Re-check once search has live traffic.
	//
	// Set here rather than in a pool hook because this is the only construction
	// point every consumer shares -- app pool, migrations, and the test pool,
	// which builds itself with a bare pgxpool.New. A dotted name is accepted as
	// a placeholder GUC even on a database where pg_trgm is not yet installed,
	// and the extension adopts the value when its module loads.
	q := url.Values{}
	if c.SSLMode != "" {
		q.Set("sslmode", c.SSLMode)
	}
	q.Set("options", "-c pg_trgm.similarity_threshold=0.2")
	u.RawQuery = q.Encode()
```

Restructure the surrounding code as needed so the options parameter is set
whether or not `SSLMode` is empty — today the whole `RawQuery` assignment sits
behind an `if c.SSLMode != ""` guard.

Then update `internal/dsn/dsn_test.go`. Three assertions compare the whole DSN
string (`require.Equal(t, "postgres://app:pw@localhost:5432/appdb", c.DSN())` at
roughly lines 34, 39 and 52) and will now fail. Rewrite each to parse the URL and
assert on its parts, so the test stops being coupled to parameter ordering:

```go
	u, err := url.Parse(c.DSN())
	require.NoError(t, err)
	require.Equal(t, "postgres", u.Scheme)
	require.Equal(t, "localhost:5432", u.Host)
	require.Equal(t, "/appdb", u.Path)
	require.Equal(t, "-c pg_trgm.similarity_threshold=0.2", u.Query().Get("options"))
```

Adjust host and path per each existing case (one omits the port). Keep the
existing sslmode assertion in the case that covers it.

- [ ] **Step 5: Run the migration against the dev database**

Run: `make migrate`

Expected: applies `0029_event_search` with no error. (`testdb.MustOpen` migrates the test database on its own, so no separate `make migrate-test` is needed for the test run below.)

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test -p 1 ./internal/db -run 'TestImmutableUnaccent|TestSearchIndexes|TestPoolSets' -count=1 -v`

Expected: PASS, all four.

- [ ] **Step 7: Commit**

```bash
git add sql/migrations/0029_event_search.up.sql sql/migrations/0029_event_search.down.sql \
        internal/dsn/dsn.go internal/dsn/dsn_test.go internal/db/search_index_test.go
git commit -m "Add trigram search indexes and accent-folding wrapper

unaccent() is STABLE and cannot be indexed, so immutable_unaccent wraps the
two-argument form. Without it 'eden munoz' scores 0.294 against 'Edén Muñoz'
and the event is unreachable without accent keys.

Threshold lowered to 0.2 on every connection built from dsn.Components.DSN():
at the 0.3 default a bare short typo matches nothing. It goes in the DSN rather
than a pool hook because the test pool builds itself with a bare pgxpool.New and
internal/testdb cannot import internal/db without a cycle."
```

---

### Task 2: `SearchEvents` query

The lexical query and its generated Go, with the correctness properties pinned by integration tests.

**Files:**
- Create: `sql/queries/search.sql`
- Create: `internal/store/search_test.go`
- Regenerate: `internal/store/search.sql.go`, `internal/store/models.go`

**Interfaces:**
- Consumes: `immutable_unaccent` and the three indexes from Task 1.
- Produces: `store.SearchEventsParams{Query string, CityID pgtype.UUID, ResultLimit int32}` and `[]store.SearchEventsRow` with fields `ID pgtype.UUID`, `Title string`, `StartsAt pgtype.Timestamptz`, `ImageUrl *string`, `Segment *string`, `VenueName string`, `Rank float64`. **Verify the exact generated field names and types after running `sqlc generate` and adjust downstream tasks to match** — sqlc renders `image_url` as `ImageUrl`, not `ImageURL`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/search_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
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

// Threshold 0.2, not the 0.3 default -- at 0.3 this returns nothing.
func TestSearchEvents_ToleratesOneCharacterTypo(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	require.Contains(t, titles(search(t, q, ctx, cityID, "midnite orchard")), "Midnight Orchard")
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

func TestSearchEvents_RespectsResultLimit(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
		Query: "midnight orchard", CityID: cityID, ResultLimit: 1,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// Order must be total, or identical requests reorder and the dropdown flickers
// between keystrokes.
func TestSearchEvents_OrderIsStableAcrossIdenticalCalls(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchFixture(t, q, ctx)

	first := titles(search(t, q, ctx, cityID, "showbox"))
	for i := 0; i < 5; i++ {
		require.Equal(t, first, titles(search(t, q, ctx, cityID, "showbox")))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/store -run TestSearchEvents -count=1 -v`

Expected: FAIL to compile — `q.SearchEvents undefined`.

- [ ] **Step 3: Write the query**

Create `sql/queries/search.sql`:

```sql
-- name: SearchEvents :many
-- Three legs unioned, then max()-ed per event: an event whose title AND
-- performer both match scores as its best leg, not the sum, so a title hit is
-- never outranked by an event matching weakly on two fields.
WITH candidates AS (
        SELECT e.id,
               similarity(immutable_unaccent(e.title),
                          immutable_unaccent(sqlc.arg(query)::text)) AS score
        FROM events e
        WHERE immutable_unaccent(e.title) % immutable_unaccent(sqlc.arg(query)::text)
    UNION ALL
        -- 0.9: a performer match is nearly as good as a title match, and often
        -- IS the title match seen from the other side.
        SELECT p.event_id,
               similarity(immutable_unaccent(p.performer_name),
                          immutable_unaccent(sqlc.arg(query)::text)) * 0.9
        FROM event_performers p
        WHERE immutable_unaccent(p.performer_name) % immutable_unaccent(sqlc.arg(query)::text)
    UNION ALL
        -- 0.6: "Showbox" should surface shows at the Showbox, but must never
        -- outrank an artist of that name. Every event at a matched venue gets
        -- this score, so without the discount one venue hit floods the page.
        SELECT e.id,
               similarity(immutable_unaccent(v.name),
                          immutable_unaccent(sqlc.arg(query)::text)) * 0.6
        FROM venues v
        JOIN events e ON e.venue_id = v.id
        WHERE immutable_unaccent(v.name) % immutable_unaccent(sqlc.arg(query)::text)
),
best AS (SELECT id, max(score) AS rank FROM candidates GROUP BY id)
SELECT e.id, e.title, e.starts_at, e.image_url, e.segment,
       v.name AS venue_name, b.rank
FROM best b
JOIN events e ON e.id = b.id
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  -- Same showable predicate as GetCityCalendarPage. Applied AFTER candidate
  -- generation on purpose: the trigram indexes do the selective work, and this
  -- filters the handful that survive.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
-- e.id last so the order is total: two events tied on rank and starts_at would
-- otherwise come back in arbitrary order and flicker between keystrokes.
ORDER BY b.rank DESC, e.starts_at ASC, e.id ASC
LIMIT sqlc.arg(result_limit);
```

- [ ] **Step 4: Generate the Go code**

Run: `sqlc generate`

Then inspect `internal/store/search.sql.go` and note the exact `SearchEventsRow` field names and types — later tasks depend on them.

Also run `git diff --stat internal/store/` and check whether `models.go` changed beyond the expected additions. **If `sqlc generate` rewrites existing `*pgvector.Vector` fields to `*any`, stop and report it.** `sqlc.yaml` overrides `db_type: vector` to `any` + pointer while the committed code has `*pgvector.Vector`; that drift predates this work and must not be silently absorbed into this commit.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -p 1 ./internal/store -run TestSearchEvents -count=1 -v`

Expected: PASS, all nine.

- [ ] **Step 6: Commit**

```bash
git add sql/queries/search.sql internal/store/search.sql.go internal/store/models.go \
        internal/store/search_test.go
git commit -m "Add SearchEvents trigram query

Three legs (title, performer, venue) unioned and max()-ed per event, weighted
1.0/0.9/0.6 so a venue match never outranks a title match. Filtered to the same
live+upcoming rows GetCityCalendarPage shows, ordered totally so identical
requests cannot reorder."
```

---

### Task 3: Search endpoint (lexical only)

Route, handler, validation, response DTO, rate limiter, and the stale terraform comment.

**Files:**
- Create: `internal/http/handlers/search.go`
- Create: `internal/http/handlers/search_test.go`
- Modify: `internal/http/middleware/ratelimit.go`
- Modify: `internal/http/middleware/ratelimit_test.go`
- Modify: `internal/http/server.go`
- Modify: `terraform/prod/observability.tf`

**Interfaces:**
- Consumes: `store.SearchEventsParams` / `store.SearchEventsRow` from Task 2.
- Produces: `handlers.SearchEvents(q *store.Queries) http.HandlerFunc`; route `GET /search/{cityId}/events?q=`; `middleware.EndpointSearch = "search"`; JSON `{"results":[{"id","title","starts_at","image_url","segment","venue":{"name"}}]}`.

- [ ] **Step 1: Write the failing test**

Create `internal/http/handlers/search_test.go`:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type searchBody struct {
	Results []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		StartsAt string `json:"starts_at"`
		ImageURL string `json:"image_url"`
		Segment  string `json:"segment"`
		Venue    struct {
			Name string `json:"name"`
		} `json:"venue"`
	} `json:"results"`
}

func searchRouter(q *store.Queries) http.Handler {
	r := chi.NewRouter()
	r.Get("/search/{cityId}/events", handlers.SearchEvents(q))
	return r
}

func seedSearchHandlerFixture(t *testing.T, q *store.Queries, ctx context.Context) string {
	t.Helper()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "The Bowl", NormalizedName: "the bowl",
	})
	require.NoError(t, err)
	_, err = q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "search-handler-1",
		Title:         "Midnight Orchard",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	return uuidFromPgCal(city.ID).String()
}

func doSearch(t *testing.T, h http.Handler, cityID, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/search/"+cityID+"/events?q="+rawQuery, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSearchEvents_ReturnsMatchingEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "midnight")
	require.Equal(t, http.StatusOK, rec.Code)

	var body searchBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 1)
	require.Equal(t, "Midnight Orchard", body.Results[0].Title)
	require.Equal(t, "The Bowl", body.Results[0].Venue.Name)
}

// rank is an internal ordering signal. Publishing it invites clients to
// threshold on it, which is exactly the mistake the cosine bands rule out.
func TestSearchEvents_DoesNotExposeRank(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "midnight")
	require.NotContains(t, rec.Body.String(), "rank")
}

func TestSearchEvents_RejectsShortAndEmptyQueries(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)
	h := searchRouter(q)

	for _, tc := range []struct{ name, query, code string }{
		{"empty", "", "bad_query"},
		{"whitespace only", "%20%20", "bad_query"},
		{"two runes", "mi", "query_too_short"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doSearch(t, h, cityID, tc.query)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), tc.code)
		})
	}
}

// Runes, not bytes: three CJK characters are 9 bytes and must be accepted.
func TestSearchEvents_CountsRunesNotBytes(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "%E6%9D%B1%E4%BA%AC%E9%83%BD")
	require.Equal(t, http.StatusOK, rec.Code)
}

// A long paste is truncated, not rejected -- the first 100 runes carry the
// signal and an error dialog is the worse failure.
func TestSearchEvents_TruncatesLongQueryInsteadOfRejecting(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	long := "midnight"
	for len(long) < 200 {
		long += "x"
	}
	rec := doSearch(t, searchRouter(q), cityID, long)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSearchEvents_RejectsBadCityID(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), "not-a-uuid", "midnight")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_city_id")
}

func TestSearchEvents_ReturnsEmptyArrayNotNull(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	cityID := seedSearchHandlerFixture(t, q, ctx)

	rec := doSearch(t, searchRouter(q), cityID, "zzzqqqxxx")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"results":[]`)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/http/handlers -run TestSearchEvents -count=1 -v`

Expected: FAIL to compile — `undefined: handlers.SearchEvents`.

- [ ] **Step 3: Write the handler**

Create `internal/http/handlers/search.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

const (
	// Fixed server-side, like calendarPageSize: there is no client-supplied
	// limit. Typeahead does not paginate.
	searchResultLimit = 10

	// The measured cliff, not a style choice: below 3 runes the trigram index
	// cannot be used and the query degrades to a full scan with per-row
	// similarity() -- 43ms at 10k rows, growing linearly.
	searchMinRunes = 3

	// Truncate rather than reject: pasting a long string is a plausible user
	// action and the first 100 runes carry the signal.
	searchMaxRunes = 100
)

type searchVenue struct {
	Name string `json:"name"`
}

// searchResult is deliberately leaner than calendarEvent: a row navigates to
// /events/:id, which fetches the full event itself.
//
// No rank field. It is an internal ordering signal, and publishing it invites
// clients to threshold on it -- see the design's Fusion note for why no score
// cutoff can work. The server owns relevance; the client renders an order.
type searchResult struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	StartsAt string      `json:"starts_at"`
	ImageURL string      `json:"image_url,omitempty"`
	Segment  string      `json:"segment,omitempty"`
	Venue    searchVenue `json:"venue"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

// parseSearchQuery reads and validates q. On bad input it writes the error
// response and returns ok=false.
func parseSearchQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("q"))
	if raw == "" {
		httperr.Write(w, http.StatusBadRequest, "bad_query", "q is required")
		return "", false
	}
	runes := []rune(raw)
	if len(runes) < searchMinRunes {
		httperr.Write(w, http.StatusBadRequest, "query_too_short",
			"q must be at least 3 characters")
		return "", false
	}
	if len(runes) > searchMaxRunes {
		raw = string(runes[:searchMaxRunes])
	}
	return raw, true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SearchEvents returns the top matching upcoming events in a city.
func SearchEvents(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}
		query, ok := parseSearchQuery(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.SearchEvents(ctx, store.SearchEventsParams{
			Query:       query,
			CityID:      pgtype.UUID{Bytes: cityUUID, Valid: true},
			ResultLimit: searchResultLimit,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error",
				"could not search events", err)
			return
		}

		// Non-nil so an empty result encodes as [] rather than null.
		out := searchResponse{Results: make([]searchResult, 0, len(rows))}
		for _, row := range rows {
			out.Results = append(out.Results, searchResult{
				ID:       uuid.UUID(row.ID.Bytes).String(),
				Title:    row.Title,
				StartsAt: row.StartsAt.Time.UTC().Format(time.RFC3339),
				ImageURL: deref(row.ImageUrl),
				Segment:  deref(row.Segment),
				Venue:    searchVenue{Name: row.VenueName},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -p 1 ./internal/http/handlers -run TestSearchEvents -count=1 -v`

Expected: PASS, all seven.

- [ ] **Step 5: Add the endpoint constant and its test line**

In `internal/http/middleware/ratelimit.go`, alongside the other authenticated endpoint constants:

```go
	EndpointSearch = "search"
```

In `internal/http/middleware/ratelimit_test.go`, inside `TestEndpointConstants`:

```go
	require.Equal(t, "search", middleware.EndpointSearch)
```

Run: `go test -p 1 ./internal/http/middleware -run TestEndpointConstants -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Wire the route**

In `internal/http/server.go`, with the other limiter declarations:

```go
	// Typeahead: one request per keystroke burst, so a budget of its own. Note
	// this STACKS with the authed net below (chi composes With on top of Use),
	// so a search also spends the shared 120/min authed budget. That is
	// deliberate -- routes must default into the net so one added later is not
	// silently unlimited. If the authed alarm starts firing, raise the authed
	// budget rather than moving search out of the group.
	searchLimiter := ratelimit.NewMemory(60, time.Minute)
```

Inside the authenticated + confirmed group, with the other reads:

```go
		r.With(middleware.RateLimitByUser(searchLimiter, middleware.EndpointSearch)).
			Get("/search/{cityId}/events", handlers.SearchEvents(s.Queries))
```

- [ ] **Step 7: Correct the stale terraform comment**

In `terraform/prod/observability.tf`, the header comment says the app "defines eleven endpoint values". It defined fourteen before this change and fifteen after. Change that phrase to "fifteen endpoint values".

Do **not** add a `search` alarm: it is a read with no downstream cost.

- [ ] **Step 8: Run the full Go suite**

Run: `make test`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/http/handlers/search.go internal/http/handlers/search_test.go \
        internal/http/middleware/ratelimit.go internal/http/middleware/ratelimit_test.go \
        internal/http/server.go terraform/prod/observability.tf
git commit -m "Add GET /search/{cityId}/events

Lean result rows -- a hit navigates to /events/:id, which fetches the rest.
rank is deliberately not on the wire. 3-rune floor mirrors the trigram index
cliff; long queries truncate rather than 400.

Dedicated 60/min limiter that stacks with the shared authed net, as every route
in that group must. Also corrects the endpoint-count comment in
observability.tf, stale since well before this change."
```

---

### Task 4: Reciprocal Rank Fusion

A pure function, testable with no database and no TEI.

**Files:**
- Create: `internal/http/handlers/fusion.go`
- Create: `internal/http/handlers/fusion_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `fuseRRF(lexical, semantic []uuid.UUID, limit int) []uuid.UUID`.

- [ ] **Step 1: Write the failing test**

Create `internal/http/handlers/fusion_test.go`:

```go
package handlers

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

// RRF rewards consistent placement across both legs over a single strong hit --
// but only when the disagreement is wide. With k=60 the curve is nearly flat
// across the top ranks: #1-and-#3 scores 1/61+1/63 = 0.0322665, which actually
// EDGES OUT #2-and-#2 at 1/62+1/62 = 0.0322581. So a one-rank gap demonstrates
// nothing and this fixture uses a real one.
func TestFuseRRF_ConsistentPlacementBeatsSingleLegSpike(t *testing.T) {
	u := ids(32)
	agreed, spiky := u[0], u[1]
	filler := u[2:]

	// agreed: #2 in both legs. spiky: #1 lexically, #30 semantically.
	lexical := []uuid.UUID{spiky, agreed}
	semantic := []uuid.UUID{filler[0], agreed}
	semantic = append(semantic, filler[1:28]...) // pads so spiky lands at #30
	semantic = append(semantic, spiky)

	got := fuseRRF(lexical, semantic, 10)
	require.Equal(t, agreed, got[0],
		"agreed 1/62+1/62 = 0.032258 must beat spiky 1/61+1/90 = 0.027505")
}

func TestFuseRRF_IncludesResultsPresentInOnlyOneLeg(t *testing.T) {
	u := ids(2)
	got := fuseRRF([]uuid.UUID{u[0]}, []uuid.UUID{u[1]}, 10)
	require.ElementsMatch(t, []uuid.UUID{u[0], u[1]}, got)
}

func TestFuseRRF_TruncatesToLimit(t *testing.T) {
	u := ids(20)
	require.Len(t, fuseRRF(u, u, 10), 10)
}

func TestFuseRRF_EmptySemanticLegPreservesLexicalOrder(t *testing.T) {
	u := ids(3)
	require.Equal(t, u, fuseRRF(u, nil, 10))
}

func TestFuseRRF_EmptyLexicalLegPreservesSemanticOrder(t *testing.T) {
	u := ids(3)
	require.Equal(t, u, fuseRRF(nil, u, 10))
}

func TestFuseRRF_BothLegsEmptyReturnsEmpty(t *testing.T) {
	require.Empty(t, fuseRRF(nil, nil, 10))
}

// Ties must not reorder between identical calls, or the dropdown flickers.
func TestFuseRRF_IsDeterministic(t *testing.T) {
	u := ids(6)
	lex, sem := u[:4], u[2:]
	first := fuseRRF(lex, sem, 10)
	for i := 0; i < 10; i++ {
		require.Equal(t, first, fuseRRF(lex, sem, 10))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/http/handlers -run TestFuseRRF -count=1 -v`

Expected: FAIL to compile — `undefined: fuseRRF`.

- [ ] **Step 3: Write the implementation**

Create `internal/http/handlers/fusion.go`:

```go
package handlers

import (
	"sort"

	"github.com/google/uuid"
)

// rrfK damps the contribution of top ranks so a single leg cannot dominate.
// 60 is the value from the original RRF paper and needs no tuning.
const rrfK = 60.0

// fuseRRF merges two ranked id lists by Reciprocal Rank Fusion:
//
//	score(id) = Σ 1 / (k + rank_in_leg)
//
// It uses ONLY the ordering of each leg, never the underlying scores. That is
// the whole point: trigram similarity and cosine distance are not on comparable
// scales, and the relevant/irrelevant cosine bands overlap across queries --
// 0.561 is a correct hit for one query while 0.556 is noise for another. No
// score cutoff can separate them, so the scores are discarded here.
//
// Ties break on first appearance in the lexical leg, then the semantic leg, so
// the result is deterministic across identical calls.
func fuseRRF(lexical, semantic []uuid.UUID, limit int) []uuid.UUID {
	type entry struct {
		id    uuid.UUID
		score float64
		order int
	}

	byID := make(map[uuid.UUID]*entry)
	next := 0

	add := func(ids []uuid.UUID) {
		for rank, id := range ids {
			e, ok := byID[id]
			if !ok {
				e = &entry{id: id, order: next}
				next++
				byID[id] = e
			}
			e.score += 1.0 / (rrfK + float64(rank+1))
		}
	}
	add(lexical)
	add(semantic)

	out := make([]*entry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].order < out[j].order
	})

	if limit > len(out) {
		limit = len(out)
	}
	ids := make([]uuid.UUID, 0, limit)
	for _, e := range out[:limit] {
		ids = append(ids, e.id)
	}
	return ids
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -p 1 ./internal/http/handlers -run TestFuseRRF -count=1 -v`

Expected: PASS, all seven.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/fusion.go internal/http/handlers/fusion_test.go
git commit -m "Add Reciprocal Rank Fusion for hybrid search

Fuses on rank only, never on score. Trigram similarity and cosine distance are
not comparable scales, and the relevant/irrelevant cosine bands overlap across
queries, so no score cutoff can separate them."
```

---

### Task 5: Semantic leg, flag-gated and off by default

**Files:**
- Modify: `sql/queries/search.sql`
- Modify: `internal/config/config.go`
- Modify: `internal/http/handlers/search.go`
- Modify: `internal/http/server.go`
- Modify: `.env.example`
- Create: `internal/http/handlers/search_semantic_test.go`
- Regenerate: `internal/store/search.sql.go`

**Interfaces:**
- Consumes: `fuseRRF` from Task 4; `store.SearchEventsParams` from Task 2.
- Produces: `store.SearchEventsSemanticParams{CityID pgtype.UUID, QueryEmbedding <vector type sqlc emits>, CandidateLimit int32}`; `config.Config.SearchSemanticEnabled bool`; `handlers.SearchDeps{Queries *store.Queries, Embedder SearchEmbedder, SemanticEnabled bool}`; `handlers.SearchEvents(deps SearchDeps) http.HandlerFunc` — **note this changes Task 3's signature from `*store.Queries` to `SearchDeps`; update `server.go` and `search_test.go` accordingly.**

- [ ] **Step 1: Write the failing test**

Create `internal/http/handlers/search_semantic_test.go`:

```go
package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
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
```

Also update the existing `searchRouter` helper in `internal/http/handlers/search_test.go` to the new signature:

```go
func searchRouter(q *store.Queries) http.Handler {
	r := chi.NewRouter()
	r.Get("/search/{cityId}/events", handlers.SearchEvents(handlers.SearchDeps{Queries: q}))
	return r
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -p 1 ./internal/http/handlers -run 'TestSearch_Flag' -count=1 -v`

Expected: FAIL to compile — `undefined: handlers.SearchDeps`.

- [ ] **Step 3: Add the semantic query**

Append to `sql/queries/search.sql`:

```sql
-- name: SearchEventsSemantic :many
-- The semantic leg, used only when SEARCH_SEMANTIC_ENABLED is on. Brute-force
-- cosine over the live rows: 4.45ms at 10k events, so no ivfflat/hnsw index is
-- warranted yet -- and adding one would trade exact results for approximate
-- ones to save time we are not short of.
--
-- Returns ids in rank order and nothing else. The handler fuses this list with
-- SearchEvents by RANK, never by score, so the distances deliberately do not
-- leave SQL.
SELECT e.id
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  AND e.embedding IS NOT NULL
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
ORDER BY e.embedding <=> sqlc.arg(query_embedding)
LIMIT sqlc.arg(candidate_limit);
```

Run: `sqlc generate`, then note the generated type of `QueryEmbedding`.

- [ ] **Step 4: Add the config flag**

In `internal/config/config.go`, add to the `Config` struct near `TEIEndpoint`:

```go
	// SearchSemanticEnabled turns on the pgvector leg of event search. Off by
	// default: it puts TEI in the request path, and TEI runs on Fargate Spot.
	SearchSemanticEnabled bool
```

With the other bool parsing (mirroring `trustProxy`):

```go
	searchSemanticEnabled := false
	if v := os.Getenv("SEARCH_SEMANTIC_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SEARCH_SEMANTIC_ENABLED=%q: %w", v, err)
		}
		searchSemanticEnabled = b
	}
```

And in the `cfg := &Config{...}` literal:

```go
		SearchSemanticEnabled: searchSemanticEnabled,
```

In `.env.example`, near `TEI_ENDPOINT`:

```
# Turns on the pgvector leg of event search. Off by default: it puts TEI in the
# request path. Flipping this in production also needs
# scripts/taskdef-edit.sh --set-env on ALL FOUR task families -- terraform's
# api_env_vars does not reach a running task (ignore_changes on
# container_definitions).
SEARCH_SEMANTIC_ENABLED=false
```

- [ ] **Step 5: Rework the handler to take `SearchDeps`**

In `internal/http/handlers/search.go`, add the candidate constant next to the others:

```go
	// RRF over two top-10 lists is thin -- an event ranked 11th lexically and
	// 1st semantically could not surface at all. Both legs fetch wider and
	// fusion truncates to searchResultLimit.
	searchCandidateLimit = 50
```

Add the dependency struct and embedder interface:

```go
// SearchEmbedder is the subset of the TEI client search needs. Narrow on
// purpose: it makes the handler testable with a stub and nothing else.
type SearchEmbedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type SearchDeps struct {
	Queries  *store.Queries
	Embedder SearchEmbedder
	// SemanticEnabled gates the pgvector leg. When false the Embedder is never
	// called and TEI is not in the request path at all.
	SemanticEnabled bool
}
```

Change the signature and the query call so the limit widens only when fusing:

```go
func SearchEvents(deps SearchDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ... cityUUID and query parsing unchanged ...

		limit := int32(searchResultLimit)
		if deps.SemanticEnabled {
			limit = searchCandidateLimit
		}

		rows, err := deps.Queries.SearchEvents(ctx, store.SearchEventsParams{
			Query:       query,
			CityID:      pgtype.UUID{Bytes: cityUUID, Valid: true},
			ResultLimit: limit,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error",
				"could not search events", err)
			return
		}

		rows = applySemanticLeg(ctx, deps, r, pgtype.UUID{Bytes: cityUUID, Valid: true}, query, rows)

		// ... response building unchanged, but iterate at most
		// searchResultLimit rows ...
	}
}
```

Add the semantic leg itself:

```go
// applySemanticLeg reorders rows by fusing the lexical ranking with a pgvector
// ranking of the same query. Every failure path returns rows unchanged: a TEI
// outage or a bad embedding degrades search to lexical-only rather than
// failing the request, which is what makes the flag safe to flip against a
// service running on Fargate Spot.
func applySemanticLeg(
	ctx context.Context,
	deps SearchDeps,
	r *http.Request,
	cityID pgtype.UUID,
	query string,
	rows []store.SearchEventsRow,
) []store.SearchEventsRow {
	if !deps.SemanticEnabled || deps.Embedder == nil {
		return truncateRows(rows, searchResultLimit)
	}

	vecs, err := deps.Embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		log.Printf("search: semantic leg unavailable, serving lexical only: err=%v vectors=%d",
			err, len(vecs))
		return truncateRows(rows, searchResultLimit)
	}

	semanticIDs, err := deps.Queries.SearchEventsSemantic(ctx, store.SearchEventsSemanticParams{
		CityID:         cityID,
		QueryEmbedding: pgvector.NewVector(vecs[0]),
		CandidateLimit: searchCandidateLimit,
	})
	if err != nil {
		log.Printf("search: semantic query failed, serving lexical only: %v", err)
		return truncateRows(rows, searchResultLimit)
	}

	byID := make(map[uuid.UUID]store.SearchEventsRow, len(rows))
	lexical := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		id := uuid.UUID(row.ID.Bytes)
		byID[id] = row
		lexical = append(lexical, id)
	}
	semantic := make([]uuid.UUID, 0, len(semanticIDs))
	for _, pgID := range semanticIDs {
		semantic = append(semantic, uuid.UUID(pgID.Bytes))
	}

	fused := fuseRRF(lexical, semantic, searchResultLimit)
	out := make([]store.SearchEventsRow, 0, len(fused))
	for _, id := range fused {
		// Semantic-only hits have no lexical row to render; the lexical leg is
		// the source of display data, so they are dropped rather than
		// re-queried. Widening candidates to 50 keeps this rare.
		if row, ok := byID[id]; ok {
			out = append(out, row)
		}
	}
	return out
}

func truncateRows(rows []store.SearchEventsRow, limit int) []store.SearchEventsRow {
	if len(rows) > limit {
		return rows[:limit]
	}
	return rows
}
```

Add `"log"`, `"github.com/pgvector/pgvector-go"` to the imports. **If `sqlc generate` emitted a different type for `QueryEmbedding` in Step 3, use that instead of `pgvector.NewVector(...)`.**

- [ ] **Step 6: Wire the flag through the server**

In `internal/http/server.go`, add to the `Server` struct near `IcalBaseURL`:

```go
	SearchEmbedder        handlers.SearchEmbedder
	SearchSemanticEnabled bool
```

Change the route registration to:

```go
		r.With(middleware.RateLimitByUser(searchLimiter, middleware.EndpointSearch)).
			Get("/search/{cityId}/events", handlers.SearchEvents(handlers.SearchDeps{
				Queries:         s.Queries,
				Embedder:        s.SearchEmbedder,
				SemanticEnabled: s.SearchSemanticEnabled,
			}))
```

Then wire both fields where the `Server` is constructed in `cmd/app`, passing the existing TEI client and `cfg.SearchSemanticEnabled`.

- [ ] **Step 7: Run the tests**

Run: `go test -p 1 ./internal/http/handlers ./internal/config -count=1`

Expected: PASS, including the four new flag tests and Task 3's seven.

- [ ] **Step 8: Run the full suite**

Run: `make test`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add sql/queries/search.sql internal/store/search.sql.go internal/store/models.go \
        internal/config/config.go internal/http/handlers/search.go \
        internal/http/handlers/search_test.go internal/http/handlers/search_semantic_test.go \
        internal/http/server.go cmd/app .env.example
git commit -m "Add flag-gated pgvector leg to event search

Off by default: it puts TEI in the request path and TEI runs on Fargate Spot.
Every failure path degrades to lexical-only rather than failing the request,
which is what makes the flag safe to flip.

Both legs fetch 50 candidates when fusing -- RRF over two top-10 lists is thin
enough that an event ranked 11th in one leg could not surface at all."
```

---

### Task 6: Frontend data layer

**Files:**
- Create: `web/src/api/search.ts`
- Create: `web/src/hooks/useDebouncedValue.ts`
- Create: `web/src/hooks/useDebouncedValue.test.ts`
- Create: `web/src/hooks/useEventSearch.ts`
- Create: `web/src/hooks/useEventSearch.test.tsx`

**Interfaces:**
- Consumes: the endpoint contract from Tasks 3 and 5.
- Produces: `searchEvents(cityId: string, q: string): Promise<SearchResponse>`; `SearchResult`, `SearchResponse` types; `useDebouncedValue<T>(value: T, delayMs: number): T`; `useEventSearch(cityId: string | undefined, query: string)`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/hooks/useDebouncedValue.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useDebouncedValue } from './useDebouncedValue';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('useDebouncedValue', () => {
  it('returns the initial value immediately', () => {
    const { result } = renderHook(() => useDebouncedValue('a', 400));
    expect(result.current).toBe('a');
  });

  it('does not update before the delay elapses', () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 400), {
      initialProps: { v: 'a' },
    });
    rerender({ v: 'b' });
    act(() => {
      vi.advanceTimersByTime(399);
    });
    expect(result.current).toBe('a');
  });

  it('updates once the delay elapses', () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 400), {
      initialProps: { v: 'a' },
    });
    rerender({ v: 'b' });
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(result.current).toBe('b');
  });

  // The property that keeps a typed word to one request rather than one per key.
  it('collapses a burst of changes into the final value', () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 400), {
      initialProps: { v: 'm' },
    });
    for (const v of ['mi', 'mid', 'midn', 'midni']) {
      rerender({ v });
      act(() => {
        vi.advanceTimersByTime(100);
      });
    }
    expect(result.current).toBe('m');
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(result.current).toBe('midni');
  });
});
```

Create `web/src/hooks/useEventSearch.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

import { searchEvents } from '../api/search';
import { useEventSearch } from './useEventSearch';

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.resetAllMocks();
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});

describe('useEventSearch', () => {
  it('stays idle while the city is unknown', () => {
    renderHook(() => useEventSearch(undefined, 'midnight'), { wrapper });
    expect(searchEvents).not.toHaveBeenCalled();
  });

  // Mirrors the server's 3-rune floor so a short query never round-trips for
  // a 400.
  it('stays idle below three characters', () => {
    renderHook(() => useEventSearch('city-1', 'mi'), { wrapper });
    expect(searchEvents).not.toHaveBeenCalled();
  });

  it('counts runes, not UTF-16 code units', () => {
    vi.mocked(searchEvents).mockResolvedValue({ results: [] });
    renderHook(() => useEventSearch('city-1', '東京都'), { wrapper });
    expect(searchEvents).toHaveBeenCalledWith('city-1', '東京都');
  });

  it('queries once the floor is met', async () => {
    vi.mocked(searchEvents).mockResolvedValue({
      results: [
        {
          id: 'e1',
          title: 'Midnight Orchard',
          starts_at: '2026-10-02T03:00:00Z',
          venue: { name: 'The Bowl' },
        },
      ],
    });
    const { result } = renderHook(() => useEventSearch('city-1', 'midnight'), { wrapper });
    await waitFor(() => expect(result.current.data?.results).toHaveLength(1));
    expect(searchEvents).toHaveBeenCalledWith('city-1', 'midnight');
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && pnpm vitest run src/hooks/useDebouncedValue.test.ts src/hooks/useEventSearch.test.tsx`

Expected: FAIL — cannot resolve `./useDebouncedValue` and `./useEventSearch`.

- [ ] **Step 3: Write the API client**

Create `web/src/api/search.ts`:

```ts
import { apiFetch } from './client';

export interface SearchResultVenue {
  name: string;
}

export interface SearchResult {
  id: string;
  title: string;
  starts_at: string;
  image_url?: string;
  segment?: string;
  venue: SearchResultVenue;
}

export interface SearchResponse {
  results: SearchResult[];
}

export async function searchEvents(cityId: string, q: string): Promise<SearchResponse> {
  return apiFetch<SearchResponse>(`/search/${cityId}/events?q=${encodeURIComponent(q)}`);
}
```

Check `web/src/api/calendar.ts` for the exact `apiFetch` call shape and match it.

- [ ] **Step 4: Write the hooks**

Create `web/src/hooks/useDebouncedValue.ts`:

```ts
import { useEffect, useState } from 'react';

/**
 * Delays propagating `value` until it has been stable for `delayMs`. Used to
 * collapse a burst of keystrokes into one request: the search endpoint shares
 * the 120/min authed rate-limit budget with calendar paging, so one request
 * per keystroke would starve it.
 */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}
```

Create `web/src/hooks/useEventSearch.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { searchEvents, type SearchResponse } from '../api/search';

// Mirrors the server's floor. Spread-counted so it is runes, not UTF-16 code
// units: three CJK characters are a valid query.
export const SEARCH_MIN_LENGTH = 3;

/**
 * Typeahead search over a city's upcoming events.
 *
 * Not keyed on user id, unlike the user-scoped hooks: results are city-scoped
 * and identical for every user in that city, so this follows useCityCalendar.
 */
export function useEventSearch(cityId: string | undefined, query: string) {
  const trimmed = query.trim();
  return useQuery<SearchResponse>({
    queryKey: ['event-search', cityId, trimmed],
    queryFn: () => searchEvents(cityId!, trimmed),
    enabled: !!cityId && [...trimmed].length >= SEARCH_MIN_LENGTH,
    // Without this the list blanks on every new query and the dropdown strobes
    // as the user types.
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && pnpm vitest run src/hooks/useDebouncedValue.test.ts src/hooks/useEventSearch.test.tsx`

Expected: PASS, all eight.

- [ ] **Step 6: Typecheck**

Run: `cd web && pnpm exec tsc -b`

Expected: no output. (`tsc --noEmit` would pass falsely — it hits the solution-style root config and compiles nothing.)

- [ ] **Step 7: Commit**

```bash
git add web/src/api/search.ts web/src/hooks/useDebouncedValue.ts \
        web/src/hooks/useDebouncedValue.test.ts web/src/hooks/useEventSearch.ts \
        web/src/hooks/useEventSearch.test.tsx
git commit -m "Add event search API client and hooks

400ms debounce collapses a typed word into one request; search shares the
120/min authed budget with calendar paging. The 3-character floor mirrors the
server's and is counted in runes."
```

---

### Task 7: `SearchDialog` component

**Files:**
- Create: `web/src/components/SearchDialog.tsx`
- Create: `web/src/components/SearchDialog.css.ts`
- Create: `web/src/components/SearchDialog.test.tsx`

**Interfaces:**
- Consumes: `useEventSearch`, `useDebouncedValue`, `SEARCH_MIN_LENGTH` from Task 6; `DIALOG_ROOT_ID` from `./Layout`.
- Produces: `default export SearchDialog({ open, onClose, cityId }: { open: boolean; onClose: () => void; cityId?: string })`.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/SearchDialog.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

import { searchEvents } from '../api/search';
import SearchDialog from './SearchDialog';

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SearchDialog open onClose={onClose} cityId="city-1" />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

const field = () => screen.getByRole('combobox');

const oneResult = {
  results: [
    {
      id: 'e1',
      title: 'Midnight Orchard',
      starts_at: '2026-10-02T03:00:00Z',
      venue: { name: 'The Bowl' },
    },
  ],
};

beforeEach(() => {
  vi.resetAllMocks();
  navigate.mockReset();
});

describe('SearchDialog', () => {
  it('renders nothing when closed', () => {
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SearchDialog open={false} onClose={vi.fn()} cityId="city-1" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('prompts rather than querying below the minimum length', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'mi');
    expect(searchEvents).not.toHaveBeenCalled();
    expect(screen.getByText(/keep typing/i)).toBeInTheDocument();
  });

  it('renders results as options', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toHaveTextContent('Midnight Orchard'));
    expect(screen.getByRole('option')).toHaveTextContent('The Bowl');
  });

  it('shows an empty state when nothing matches', async () => {
    vi.mocked(searchEvents).mockResolvedValue({ results: [] });
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'zzzqqq');
    await waitFor(() => expect(screen.getByText(/no events match/i)).toBeInTheDocument());
  });

  it('navigates to the event on Enter and closes', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toBeInTheDocument());
    await user.keyboard('{ArrowDown}{Enter}');
    expect(navigate).toHaveBeenCalledWith('/events/e1');
    expect(onClose).toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && pnpm vitest run src/components/SearchDialog.test.tsx`

Expected: FAIL — cannot resolve `./SearchDialog`.

- [ ] **Step 3: Write the styles**

Create `web/src/components/SearchDialog.css.ts`:

```ts
import { style } from '@vanilla-extract/css';

// NOTE: never write the token `global.css.ts` inside a .css.ts file, even in a
// comment -- it breaks the vanilla-extract compile with a misleading "Styles
// were unable to be assigned to a file" error.

export const input = style({
  width: '100%',
  padding: '0.75rem',
  fontSize: '1rem',
  boxSizing: 'border-box',
});

export const list = style({
  listStyle: 'none',
  margin: '0.5rem 0 0',
  padding: 0,
  maxHeight: '20rem',
  overflowY: 'auto',
});

export const option = style({
  padding: '0.5rem 0.75rem',
  cursor: 'pointer',
});

export const optionActive = style({
  background: 'rgba(0, 0, 0, 0.06)',
});

export const meta = style({
  opacity: 0.7,
  fontSize: '0.85rem',
});

export const status = style({
  padding: '0.75rem',
  opacity: 0.7,
});
```

- [ ] **Step 4: Write the component**

Create `web/src/components/SearchDialog.tsx`:

```tsx
import { useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import clsx from 'clsx';
import { DIALOG_ROOT_ID } from './Layout';
import { useDebouncedValue } from '../hooks/useDebouncedValue';
import { useEventSearch, SEARCH_MIN_LENGTH } from '../hooks/useEventSearch';
import * as c from '../styles/common.css';
import * as s from './SearchDialog.css';

interface Props {
  open: boolean;
  onClose: () => void;
  cityId?: string;
}

// Collapses a typed word into one request. See useDebouncedValue for why.
const DEBOUNCE_MS = 400;

/**
 * Typeahead search over the city's upcoming events, opened from the Search
 * button in the calendar page header.
 *
 * The body lives in a child that only exists while the dialog is open, so a
 * cancelled search is discarded by unmounting rather than by clearing state on
 * the way in. It renders through a portal into Layout's dialog root for the
 * same reason AddManualEventDialog does: the calendar's animated, transformed
 * ancestors would otherwise clip the backdrop.
 */
export default function SearchDialog({ open, onClose, cityId }: Props) {
  if (!open) return null;
  return <SearchDialogBody onClose={onClose} cityId={cityId} />;
}

function SearchDialogBody({ onClose, cityId }: Omit<Props, 'open'>) {
  const titleId = useId();
  const listId = useId();
  const navigate = useNavigate();
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(-1);
  const debounced = useDebouncedValue(query, DEBOUNCE_MS);
  const { data, isFetching } = useEventSearch(cityId, debounced);
  const [host] = useState(() => document.getElementById(DIALOG_ROOT_ID) ?? document.body);

  const results = data?.results ?? [];
  const tooShort = [...debounced.trim()].length < SEARCH_MIN_LENGTH;

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  // A stale active row would point at a different event once results change.
  useEffect(() => setActiveIndex(-1), [debounced]);

  const openResult = (index: number) => {
    const hit = results[index];
    if (!hit) return;
    navigate(`/events/${hit.id}`);
    onClose();
  };

  const onFieldKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActiveIndex((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      openResult(activeIndex >= 0 ? activeIndex : 0);
    }
  };

  return createPortal(
    <div className={c.backdrop} onClick={onClose} data-testid="search-backdrop">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={c.dialog}
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id={titleId}>Search events</h2>
        <input
          autoFocus
          type="text"
          role="combobox"
          aria-expanded={results.length > 0}
          aria-controls={listId}
          aria-activedescendant={activeIndex >= 0 ? `${listId}-${activeIndex}` : undefined}
          aria-label="Search events"
          className={s.input}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onFieldKeyDown}
          placeholder="Artist, event, or venue"
        />

        {tooShort ? (
          <p className={s.status}>Keep typing — at least {SEARCH_MIN_LENGTH} characters.</p>
        ) : isFetching && results.length === 0 ? (
          <p className={s.status}>Searching…</p>
        ) : results.length === 0 ? (
          <p className={s.status}>No events match “{debounced.trim()}”.</p>
        ) : (
          <ul id={listId} role="listbox" aria-label="Search results" className={s.list}>
            {results.map((hit, i) => (
              <li
                key={hit.id}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === activeIndex}
                className={clsx(s.option, i === activeIndex && s.optionActive)}
                onMouseEnter={() => setActiveIndex(i)}
                onClick={() => openResult(i)}
              >
                <div>{hit.title}</div>
                <div className={s.meta}>{hit.venue.name}</div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>,
    host,
  );
}
```

Confirm `c.backdrop` and `c.dialog` exist in `web/src/styles/common.css.ts` — `AddManualEventDialog` uses both. If either is named differently, match that file.

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd web && pnpm vitest run src/components/SearchDialog.test.tsx`

Expected: PASS, all six.

- [ ] **Step 6: Typecheck and lint**

Run: `cd web && pnpm exec tsc -b && pnpm exec eslint src/components/SearchDialog.tsx`

Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/SearchDialog.tsx web/src/components/SearchDialog.css.ts \
        web/src/components/SearchDialog.test.tsx
git commit -m "Add SearchDialog

Combobox/listbox semantics with arrow-key selection; Enter navigates to the
event detail page. Follows AddManualEventDialog: portal into the Layout dialog
root, body unmounted when closed so a cancelled search is discarded."
```

---

### Task 8: Calendar page wiring and the `c`-key guard

**Files:**
- Modify: `web/src/pages/CalendarPage.tsx`
- Modify: `web/src/pages/CalendarPage.test.tsx`

**Interfaces:**
- Consumes: `SearchDialog` from Task 7.
- Produces: nothing downstream.

- [ ] **Step 1: Write the failing test**

First add a mock for the search API alongside the other `vi.mock` calls at the
top of `web/src/pages/CalendarPage.test.tsx` — without it `SearchDialog` reaches
a real `fetch`:

```tsx
vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));
```

Then add these two cases. The file's render helper is `renderPage()` (it takes
an optional `QueryClient`); `beforeEach` already calls `vi.resetAllMocks()`, so
give `searchEvents` a resolved value inside each test that types a full query.

```tsx
it('opens the search dialog from the Search button', async () => {
  const user = userEvent.setup();
  renderPage();
  await user.click(screen.getByRole('button', { name: /search/i }));
  expect(screen.getByRole('dialog', { name: /search events/i })).toBeInTheDocument();
});

// Regression: CalendarPage registers a bare-'c' window shortcut. Without a
// focus guard, every 'c' typed into any field on the page toggles the all-city
// calendar -- so searching for "comedy" or "crocodile" silently swaps the
// calendar behind the dialog.
it('does not toggle the all-city calendar when a c is typed into the search box', async () => {
  vi.mocked(searchEvents).mockResolvedValue({ results: [] });
  const user = userEvent.setup();
  renderPage();
  const headingBefore = screen.getByRole('heading', { level: 1 }).textContent;

  await user.click(screen.getByRole('button', { name: /search/i }));
  await user.type(screen.getByRole('combobox'), 'crocodile');

  expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(headingBefore!);
});
```

The second test needs `searchEvents` imported alongside the file's other mocked
imports:

```tsx
import { searchEvents } from '../api/search';
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && pnpm vitest run src/pages/CalendarPage.test.tsx`

Expected: FAIL — no Search button; and the `c` test fails once the button exists because the heading flips.

- [ ] **Step 3: Add the button, the dialog, and the guard**

In `web/src/pages/CalendarPage.tsx`:

Add the import and state:

```tsx
import { useEffect, useState } from 'react';
import SearchDialog from '../components/SearchDialog';
```

```tsx
  const [searchOpen, setSearchOpen] = useState(false);
```

Replace the existing keyboard effect with the guarded version:

```tsx
  useEffect(() => {
    const listener = (e: KeyboardEvent) => {
      // Bare-letter shortcut: it must not fire while the user is typing into a
      // field, or every 'c' in a search term toggles the calendar behind the
      // dialog. Pre-existing bug -- the Add Manual Event dialog has it too --
      // but the search box is where users actually hit it.
      const t = e.target as HTMLElement | null;
      if (
        searchOpen ||
        t?.isContentEditable ||
        t?.tagName === 'INPUT' ||
        t?.tagName === 'TEXTAREA'
      ) {
        return;
      }
      if (e.key === 'c') {
        toggledAllCityActions.setValue(toggledAllCity === 'true' ? 'false' : 'true');
      }
    };
    window.addEventListener('keypress', listener);
    return () => {
      window.removeEventListener('keypress', listener);
    };
  }, [toggledAllCity, toggledAllCityActions, searchOpen]);
```

Add the button inside the existing `c.pageHeader` div, immediately after the `<h1>`:

```tsx
          <button
            type="button"
            className={c.buttonSecondary}
            onClick={() => setSearchOpen(true)}
          >
            Search
          </button>
```

And mount the dialog just inside the outermost `<div>` of the return, after the banner block:

```tsx
        <SearchDialog
          open={searchOpen}
          onClose={() => setSearchOpen(false)}
          cityId={user?.city_id}
        />
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && pnpm vitest run src/pages/CalendarPage.test.tsx`

Expected: PASS, including both new tests.

- [ ] **Step 5: Run the whole frontend suite**

Run: `cd web && pnpm vitest run && pnpm exec tsc -b && pnpm exec eslint src && pnpm exec prettier --check src`

Expected: all pass.

- [ ] **Step 6: Run the whole backend suite**

Run: `make test`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/CalendarPage.tsx web/src/pages/CalendarPage.test.tsx
git commit -m "Add Search button to the calendar page

Also guards the bare-'c' window shortcut against firing while a field has
focus. That bug predates this change -- typing into the Add Manual Event dialog
already toggles the calendar -- but a search box is where users meet it."
```

---

## Post-implementation

Not tasks; flag these to the user when the plan is done.

- **Validate the `0.2` similarity threshold against production data.** It was tuned on a synthetic corpus of ~20 distinct titles. Sample real queries and check the false-positive rate before assuming it is right.
- **The semantic flag is off.** Turning it on in production needs `scripts/taskdef-edit.sh --set-env` on all four task families — terraform's `api_env_vars` does not reach a running task.
- **Ticketmaster descriptions are venue boilerplate** ("Please visit our Ballpark Information Guide…") and 500 characters of them go into every event embedding via `BuildEventText`. That dilutes the vectors for matching as well as for search. Worth its own investigation.
