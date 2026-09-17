# Event search — design

## Problem

There is no way to find a specific event. The calendar pages list events in
starts_at order — the matched feed (`/me/calendar`) and the whole-city feed
(`/calendar/{cityId}`) — and both are browsed, not queried. A user who knows an
artist is playing has to page until they see it.

This adds a search dialog on the calendar page, backed by a new typeahead
endpoint.

## Goals

- Find an upcoming event by title, performer, or venue, as the user types.
- Tolerate ordinary input error: wrong case, missing accents, a one-character
  typo, words in the wrong order.
- No new infrastructure. Postgres only.
- Leave a path to semantic recall without committing to it in v1.

## Non-goals

- **ElasticSearch / OpenSearch.** Rejected on measurement, not on principle —
  see "Why not ElasticSearch" below.
- **Pagination.** Typeahead returns a fixed top 10. A keyset cursor over a
  similarity rank is not stably orderable, and no one pages a dropdown.
- **Searching past or archived events.** The corpus is exactly what the
  calendar already considers showable.
- **Searching artist bios, setlists, or genres as text.** Genres already reach
  the embedding via `BuildEventText`; that is the semantic leg's business, not
  the lexical one's.

## Measurements this design rests on

Benchmarked against 10k synthetic events (~8.5k live after archiving) on the
local `pgvector/pgvector:pg16` container. That is a Mac, not a `db.t4g.small`
burstable — assume 5–10x worse in production, which is still two orders of
magnitude inside a typeahead budget.

| | |
|---|---|
| Trigram typeahead, 3+ char query | **2.8 – 8.6 ms** |
| Same query, 1 char (index unusable) | **43 ms** |
| 10 concurrent clients | **1073 tps, 9.3 ms avg** |
| All three trigram indexes | **~1.2 MB**, built in 0.68 s |
| Brute-force pgvector over live rows | **4.45 ms** |
| TEI embed, short query (amd64 emulation) | median **27.7 ms**, max **191 ms** |

Correctness probes that shaped the design:

- `ILIKE '%q%'` fails word-order swaps *and* typos. It is not a viable
  implementation of this feature.
- `tsvector` FTS handles word order and stemming but has **zero** typo
  tolerance.
- Trigram similarity handles both, but only below the default threshold: at
  `0.3` a bare short typo (`"orchrd"`) returns nothing; `0.2` recovers it.
- `pg_trgm` is already case-insensitive (`similarity('megan moroney','MEGAN
  MORONEY')` = 1.0) but **not** accent-insensitive:
  `similarity('eden munoz','Edén Muñoz')` = **0.294**, under even the lowered
  threshold. `Edén Muñoz: Como En Los Viejos Tiempos Tour` is in the catalogue
  now and would be unreachable without accent keys.

## Why not ElasticSearch

At <10k events the measured query cost is 3–9 ms with ~1.2 MB of index. A
managed OpenSearch node is ~$26/mo single-node (3x that with real HA) against
an app whose database is a `db.t4g.small`, and it would attach a search cluster
to an API running at `desired_count = 1`.

The disqualifying cost is not money, it is the second copy. `event_over_at(
starts_at, ends_at, time_tbd)` encodes genuinely subtle rules about date-only
events; search must apply the same predicate. Reimplementing it in ES query DSL
invites drift, and two-phasing back through Postgres surrenders the speed the
cluster was bought for. A nightly reindex also makes every ingestion bug
present as a search bug.

Revisit at ~500k+ events, or when faceted cross-city aggregation is wanted, or
when search QPS starts competing with the rest of the API. The query lives
behind one `store` method so that swap stays local.

## Data model

### Migration `0029_event_search`

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE, not IMMUTABLE, so it cannot appear in an index
-- expression. The two-argument form names the dictionary explicitly, which
-- makes the result deterministic and the wrapper safe to mark IMMUTABLE --
-- the documented Postgres workaround.
--
-- What this buys: "eden munoz" finds "Edén Muñoz". Untreated, pg_trgm scores
-- that pair at 0.294 -- under the default 0.3 threshold AND under the lowered
-- one this design uses -- so the event is simply unreachable without the
-- accent keys.
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

Down migration drops the three indexes and the function; the extensions stay
(dropping `vector`-adjacent extensions on a shared database is not worth the
blast radius, and both are idempotent to re-create).

### Deliberately not reusing `normalized_name`

`event_performers.normalized_name` and `venues.normalized_name` already exist
and are already folded, but by Go's `events.NormalizeString` (NFD → strip
combining marks → lowercase). That agrees with `unaccent` on Latin accents and
diverges elsewhere — `unaccent` maps `ß`→`ss` and `Ø`→`O`, NFD leaves both
alone. Using them would make the performer leg match on a subtly different rule
than the title leg. One transform, defined once, applied to all three legs on
both sides of every comparison.

### Similarity threshold

The `%` operator reads `pg_trgm.similarity_threshold`, default `0.3`. Set to
`0.2` once per pooled connection via `cfg.AfterConnect` in `internal/db/db.go`,
alongside the existing `BeforeConnect` rotation hook. It is a placeholder GUC,
so `SET` succeeds even before the extension module loads on that backend.

**Unvalidated:** `0.2` was tuned against a synthetic corpus with only ~20
distinct titles, so its false-positive cost on the real catalogue is unknown.
Ship it as a named constant carrying that caveat and re-check against
production data once the endpoint is live.

## SQL / sqlc

New file `sql/queries/search.sql`:

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

`sqlc generate` also regenerates `internal/store/models.go`; commit it
alongside `search.sql.go`.

### `SearchEventsSemantic` (flag-gated leg)

```sql
-- name: SearchEventsSemantic :many
-- The semantic leg, used only when SEARCH_SEMANTIC_ENABLED is on. Brute-force
-- cosine over the live rows: 4.45ms at 10k events, so no ivfflat/hnsw index is
-- warranted yet -- and adding one would trade exact results for approximate
-- ones to save time we are not short of.
--
-- Returns ids in rank order and nothing else. The handler fuses this list with
-- SearchEvents by RANK, never by score, so the distances deliberately do not
-- leave SQL -- see the Fusion note for why a cosine cutoff cannot work.
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

**Candidate width.** RRF over two top-10 lists is thin — an event ranked 11th
lexically and 1st semantically could not surface at all. Both legs therefore
fetch `candidate_limit = 50` and fusion truncates to 10. When the flag is off
`SearchEvents` is called with `result_limit = 10` directly and no fusion runs.

**Check the generated param type before building on it.** `sqlc.yaml` overrides
`db_type: vector` to `any` + pointer, but the committed
`UpdateEventEmbeddingParams.Embedding` is `*pgvector.Vector` (importing
`github.com/pgvector/pgvector-go`). Those disagree, so the committed code and
the config are out of step somewhere. Run `sqlc generate` and see what it
actually emits before writing the handler against an assumed type — and if it
rewrites the existing `*pgvector.Vector` fields, that is a pre-existing drift to
resolve separately, not part of this change.

## API endpoint + handler

**`GET /search/{cityId}/events?q=...`**, in the authenticated + confirmed
group.

City comes from the path, mirroring `/calendar/{cityId}` — the client already
holds it, and it saves a `users` lookup per keystroke. The `/search/{cityId}/`
prefix leaves room for artist or venue search later. `/events/search` was
rejected: `/events/{id}` already exists, and while chi resolves static before
param, it is a trap for whoever adds `/events/{id}/...` later.

New handler file `internal/http/handlers/search.go`.

### Validation

| Input | Behavior |
|---|---|
| `q` missing or empty after trim | `400 bad_query` |
| `q` shorter than 3 runes after trim | `400 query_too_short` |
| `q` longer than 100 runes | truncated to 100, not rejected |
| `cityId` not a UUID | `400 bad_city_id` |

The 3-rune floor is the measured cliff, not a style choice: below 3 characters
the trigram index cannot be used and the query degrades to a full scan with
per-row `similarity()` — 43 ms at 10k rows, growing linearly. Counted in runes
so a 3-character CJK or accented query is not rejected for being 6–9 bytes.

Truncation rather than rejection at the top end because pasting a long string
is a plausible user action and the first 100 characters carry the signal.

### Response

```json
{ "results": [
    { "id": "...", "title": "Megan Moroney: The Cloud 9 Tour",
      "starts_at": "2026-10-02T03:00:00Z", "image_url": "...",
      "segment": "music", "venue": { "name": "WaMu Theater" } }
] }
```

Fixed `LIMIT 10`, server-side, no client-supplied limit — same posture as
`calendarPageSize`.

`rank` is deliberately absent. It is an internal ordering signal, and
publishing it invites clients to threshold on it — see the fusion note below
for why score thresholds are the wrong instrument. The server owns relevance;
the client renders an ordered list.

### Rate limiting

`searchLimiter := ratelimit.NewMemory(60, time.Minute)` with a new
`EndpointSearch = "search"` constant, added to `TestEndpointConstants` (which
asserts values individually, so this is one line). `NewMemory` sets
`burst = limit`, so this allows a 60-request burst refilling at 1/sec —
forgiving enough for typeahead.

chi composes `With` on top of the group's `Use`, so **a search request spends
both the search budget and the shared `authed` 120/min budget.** Search cannot
be isolated without moving it out of the group, and `server.go` argues
correctly that routes must default into that net so a later addition is not
silently unlimited. That reasoning stands.

The arithmetic therefore has to work inside 120/min: at a 400 ms debounce with
a 3-character floor, a query like "megan moroney" costs 2–3 requests, and
twenty searches in a busy minute is ~50 — leaving room for calendar paging. If
the `authed` alarm fires in production, raise the authed budget; do not move
search outside the net.

`terraform/prod/observability.tf` says the app "defines eleven endpoint
values." There are already fourteen, so that comment is stale before this work
touches it — correct it while adding `search`. No alarm for `search` initially:
it is a read with no downstream cost.

## Semantic leg (built, flag-gated, off by default)

Config flag `SEARCH_SEMANTIC_ENABLED` via `internal/config`, default off. When
on, the handler embeds `q` through the existing TEI client, runs a second
pgvector query, and fuses the two rank lists before truncating to 10.

### Why it is gated rather than always on

Lexical and semantic retrieve different things, and blending them per-keystroke
makes the common case worse. Measured on the real 195-event dev catalogue:

```
QUERY: "baseball"
  lexical:   (no rows)                         <- zero events contain the string
  semantic:  0.653 Seattle Mariners vs. Cincinnati Reds
             0.645 Seattle Mariners vs. San Francisco Giants
             0.558 Seattle Kraken vs. New York Rangers    <- hockey

QUERY: "Megan Moroney"
  lexical:   Megan Moroney: The Cloud 9 Tour   <- one row, exactly right
  semantic:  0.768 Megan Moroney: The Cloud 9 Tour
             0.543 JOJI: SOLARIS
             0.531 Noah Kahan: The Great Divide Tour
```

Semantic search buys real recall no trigram tuning can reach, but it **cannot
say "no match"** — a nearest-neighbour scan always returns k rows. For the
dominant typeahead behaviour (user types a prefix of a name they already know),
that is 1 useful row and 7 pieces of furniture.

There is also no usable score cutoff: `0.561` is a correct hit for "indie folk"
(Noah Kahan) while `0.556` is noise for "megan mor" (JOJI). The relevant and
irrelevant bands overlap *across queries*, because cosine similarity is only
meaningful relative to other results for the same query.

### Fusion

Reciprocal Rank Fusion, discarding the raw scores and using only ordering:

```
score(event) = Σ 1 / (k + rank_in_leg)        k = 60
```

Scale-free, needs no tuning, and sidesteps the incomparable-score problem
entirely. Implemented as a pure function over two rank lists — no database, no
TEI — so it is unit-testable directly.

### Failure handling

A TEI error is logged and swallowed; the handler returns the lexical results
alone. Search degrades rather than breaking, which is what makes the flag safe
to flip against a service running on Fargate Spot.

### Deployment note

Adding `SEARCH_SEMANTIC_ENABLED` to terraform's `api_env_vars` **will not reach
a running task** — `container_definitions` is under `ignore_changes`. Flipping
it in production means `scripts/taskdef-edit.sh --set-env` against all four
task families, not just `hwh-api`.

## Frontend

### `CalendarPage.tsx` — entry point

`c.pageHeader` is already `display: flex; justify-content: space-between` with
the `<h1>` as its only live child, so a button as the second child lands at the
right end of the row with no new layout CSS:

```tsx
<div className={c.pageHeader}>
  <h1 className={c.pageTitle}>
    {isShowingAllCityCalendar ? `What's happening in Seattle` : `Your Seattle calendar`}
  </h1>
  <button type="button" className={c.buttonSecondary} onClick={() => setSearchOpen(true)}>
    Search
  </button>
</div>
<SearchDialog open={searchOpen} onClose={() => setSearchOpen(false)} cityId={user?.city_id} />
```

Reuses `c.buttonSecondary`, as `AddManualEventDialog` does. Styling is
intentionally minimal and expected to be replaced.

### `CalendarPage.tsx` — the `c`-key collision (bug fix, in scope)

`CalendarPage.tsx:55` registers a bare-`c` shortcut on `window` via `keypress`
with no focus guard, and no dialog in the tree calls `stopPropagation` on key
events. A `keypress` inside an `<input>` bubbles to `window`, so **on `master`
today, typing "Concert at the Crocodile" into the Add Manual Event dialog
toggles the all-city calendar three times.**

This is pre-existing, but a search box is where users will actually hit it —
"comedy", "crocodile", "electric". Two-line fix in the existing handler:

```tsx
const listener = (e: KeyboardEvent) => {
  // Bare-letter shortcut: must not fire while the user is typing into a field,
  // or every 'c' in a search term toggles the calendar behind the dialog.
  const t = e.target as HTMLElement | null;
  if (searchOpen || t?.isContentEditable ||
      t?.tagName === 'INPUT' || t?.tagName === 'TEXTAREA') return;
  if (e.key === 'c') { /* unchanged */ }
};
```

### `web/src/hooks/useDebouncedValue.ts` (new, generic)

```ts
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}
```

400 ms at the call site, chosen to fit the rate-limit arithmetic above.

### `web/src/api/search.ts` (new)

`searchEvents(cityId, q)` through `apiFetch`, mirroring `calendar.ts`.

### `web/src/hooks/useEventSearch.ts` (new)

```ts
useQuery({
  queryKey: ['event-search', cityId, debouncedQuery],
  queryFn: () => searchEvents(cityId!, debouncedQuery),
  // The 3-rune floor the API enforces, mirrored client-side so a short query
  // is never sent at all rather than round-tripping for a 400.
  enabled: !!cityId && [...debouncedQuery.trim()].length >= 3,
  placeholderData: keepPreviousData,
  staleTime: 30_000,
});
```

Not keyed on `user.id`: results are city-scoped and identical for every user in
that city, matching `useCityCalendar` rather than the user-scoped convention in
`userScopedQueries.test.tsx`.

`keepPreviousData` matters — without it the list blanks on every new query and
the dropdown strobes as the user types. `[...str]` counts runes, matching the
server's rule rather than UTF-16 code units.

### `web/src/components/SearchDialog.tsx` + `.css.ts` (new)

Follows `AddManualEventDialog`'s structure: `open` prop returning `null` when
closed so the inner component's state is discarded on close; portal into
`DIALOG_ROOT_ID`; Escape and backdrop-click to close; `role="dialog"` labelled
by a `useId`.

Results carry combobox semantics rather than a plain list — `role="combobox"`
on the input with `aria-expanded` / `aria-controls` / `aria-activedescendant`,
`role="listbox"` on the results, `role="option"` on each row. Arrow keys move
the active row; Enter navigates via `useNavigate` to `/events/:id` and closes
the dialog.

Four render states: idle (under 3 characters), loading, empty ("No events
match"), results.

## Testing

TDD throughout, per repo convention.

**Go integration (real DB):**

- `"eden munoz"` returns `Edén Muñoz: Como En Los Viejos Tiempos Tour`. This is
  the regression test for the entire `immutable_unaccent` design — without the
  wrapper it scores 0.294 and returns nothing.
- A one-character typo still matches at threshold 0.2.
- Weighting: an event *titled* with a venue's name outranks events merely *at*
  that venue.
- Archived events, and events past `event_over_at`, are excluded.
- City scoping: an event in another city never appears.
- `q` of 2 runes → 400; empty → 400; 150 runes → 200 on the truncated prefix.
- Results capped at 10, and the order is total (identical requests do not
  reorder).

**Go unit (no DB, no TEI):**

- RRF fusion over fixture rank lists.
- TEI returning an error yields the lexical results, not a 500.

**Vitest:**

- The Search button opens the dialog.
- **Typing "c" into the search box does not toggle the all-city calendar** —
  pins the bug fix above.
- A burst of keystrokes produces exactly one request.
- Arrow-key selection and Enter navigate to the event.
- Escape closes and discards state.

The pre-commit hook runs the full Go suite, eslint, `tsc`, prettier and vitest
(~35s) and needs `make db-up queue-up` running. Typecheck the web package with
`tsc -b` — plain `tsc --noEmit` hits the solution-style root config, compiles
nothing, and passes falsely.

## Summary of changes

| Area | Change |
|---|---|
| `sql/migrations/` | `0029_event_search.{up,down}.sql` — 2 extensions, `immutable_unaccent`, 3 GIN indexes |
| `sql/queries/` | `search.sql` — `SearchEvents` (+ `SearchEventsSemantic`, flag-gated) |
| `internal/store/` | regenerated `search.sql.go` + `models.go` |
| `internal/db/db.go` | `AfterConnect` setting `pg_trgm.similarity_threshold = 0.2` |
| `internal/config/` | `SEARCH_SEMANTIC_ENABLED` |
| `internal/http/middleware/ratelimit.go` | `EndpointSearch` (+ test line) |
| `internal/http/handlers/search.go` | new handler, validation, RRF fusion |
| `internal/http/server.go` | route + `searchLimiter` |
| `terraform/prod/observability.tf` | correct the stale "eleven endpoints" comment |
| `web/src/api/search.ts` | new |
| `web/src/hooks/` | `useDebouncedValue.ts`, `useEventSearch.ts` |
| `web/src/components/SearchDialog.tsx` + `.css.ts` | new |
| `web/src/pages/CalendarPage.tsx` | Search button, dialog mount, `c`-shortcut guard |
