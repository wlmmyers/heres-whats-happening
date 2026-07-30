# Calendar Cursor Pagination

**Date:** 2026-07-30
**Status:** Designed, not yet implemented

## Problem

`GET /me/calendar` and `GET /calendar/{cityId}` both require `from` and `to` query
params and return every matching event in that window in one unbounded response.
Two consequences:

- The response size is set by how wide a window the client asks for. Nothing caps
  it, so a client requesting a year gets a year in one payload.
- The client has to decide what window to ask for before it knows what's in it,
  which is the wrong shape for a calendar that scrolls forward indefinitely.

Replace the date window with forward-only cursor pagination at a fixed page size
of 20.

## Scope

| Endpoint | Change |
|---|---|
| `GET /me/calendar` | `from`/`to` removed, optional `cursor` added, max 20 events |
| `GET /calendar/{cityId}` | `from`/`to` removed, optional `cursor` added, max 20 events |
| `GET /ical/{token}` | **No change** — see "iCal is untouched" below |

Frontend changes are explicitly out of scope; the API owner is handling
`web/src/api/calendar.ts` separately.

## Wire contract

```
GET /me/calendar?cursor=<opaque>          cursor optional
GET /calendar/{cityId}?cursor=<opaque>    cursor optional

200 → {"events": [ …≤20… ], "next_cursor": "MjAyNi0w…"}
400 → {"code": "bad_cursor", …}
```

`next_cursor` is omitted (`json:",omitempty"`) when the page just returned is the
last one. Its presence is the "more results exist" signal; there is no separate
`has_more` flag.

Omitting `cursor` returns the first page. `from` and `to` are deleted rather than
deprecated — `parseDateRange` and the `missing_range` error code
(`internal/http/handlers/calendar.go:45-65`) are removed entirely. Unrecognized
query params are ignored, so a client still sending `from`/`to` receives a normal
first page instead of a 400. That is deliberate: it lets the frontend change land
independently of this one.

There is no `limit` param. The page size is a fixed server-side constant.

## Time window

With `from`/`to` gone, a page covers **all upcoming events, unbounded**.

The `starts_at >= $2 AND starts_at < $3` bounds are dropped from both queries. The
existing `event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()` predicate
already serves as the lower bound — it excludes anything genuinely over while
keeping in-progress and date-only events visible — so removing the explicit range
leaves "starts at the present moment, continues forward" with no upper bound.

The tradeoff is recorded under [Performance follow-ups](#performance-follow-ups):
an unbounded feed means the candidate set per user or per city grows without
limit. That is acceptable at current data volumes and is the first thing to
revisit if it isn't.

## Ordering and cursor encoding

`starts_at` is not unique — multiple events routinely start at the same instant.
Paginating on it alone silently drops or duplicates events at page boundaries, so
the sort key becomes the composite `(starts_at ASC, id ASC)` and the cursor
carries both halves.

New `internal/http/handlers/cursor.go`:

```go
func encodeCursor(startsAt time.Time, eventID pgtype.UUID) string
func decodeCursor(s string) (time.Time, uuid.UUID, error)
```

The encoding is `base64url(RFC3339Nano + "|" + uuid)`, unpadded. RFC3339Nano
rather than RFC3339 so that sub-second `starts_at` values round-trip exactly; a
truncated timestamp in the cursor would re-include or skip boundary rows.

The token is opaque by contract. Clients pass back what they were given and parse
nothing, which leaves the encoding free to change later without a client break.
`decodeCursor` returning an error is always a 400 `bad_cursor`, never a 500 — a
malformed cursor is client input, not a server fault.

## SQL

Two new queries in `sql/queries/calendar.sql`, followed by `sqlc generate`:

```sql
-- name: GetUserCalendarPage :many
-- name: GetCityCalendarPage :many
… WHERE (existing filters, minus the starts_at >= / < bounds)
  AND (
    sqlc.narg(cursor_starts_at)::timestamptz IS NULL
    OR (e.starts_at, e.id) > (sqlc.narg(cursor_starts_at)::timestamptz,
                              sqlc.narg(cursor_event_id)::uuid)
  )
ORDER BY e.starts_at ASC, e.id ASC
LIMIT $n
```

The `IS NULL` guard is what makes `cursor` optional: the first page passes
`pgtype.Timestamptz{Valid: false}` and the predicate is satisfied by every row.
sqlc reuses one placeholder per named param, so `cursor_starts_at` appearing twice
produces a single argument.

Each query keeps its own existing filters otherwise. `GetUserCalendarPage` retains
the `NOT EXISTS` exclusion against `user_event_not_interested`;
`GetCityCalendarPage` retains its deliberate *absence* of one, since the city
calendar is specified to return an identical response for every caller
(`internal/http/handlers/calendar.go:120-125`).

### iCal is untouched

`GetUserCalendarInRange` stays exactly as it is. Its only remaining caller is the
iCal feed at `internal/http/handlers/ical.go:108`, which wants one unpaginated
−1day/+60day dump — genuinely different requirements from a scrolling UI. Leaving
it alone means the feed's behavior cannot regress as part of this change.

`GetCityCalendarInRange` has no caller left once `GetCityCalendar` moves to the
paged query, so it and its store tests (`internal/store/city_calendar_test.go`)
are deleted rather than left as dead code. Its test coverage moves to the new
paged query.

## Handlers

```go
const calendarPageSize = 20
```

Both handlers request `calendarPageSize + 1` rows. If 21 come back, the slice is
trimmed to 20 and `next_cursor` is encoded from the last retained row; if 20 or
fewer come back, this is the final page and `next_cursor` stays empty. This is the
standard fetch-one-extra trick — it detects "more exist" without a second
`COUNT(*)` query.

The two handlers keep their own row-mapping loops. They read from different sqlc
row types and differ in how they populate `score`/`matched_because`, so the only
genuinely shared logic is the cursor helpers and the trim step; unifying the loops
behind generics would cost more clarity than the ~4 duplicated lines are worth.

## Testing

Handler tests in `internal/http/handlers/calendar_test.go`, replacing
`TestGetMyCalendar_DateRangeFiltering`, `TestGetMyCalendar_MissingDates_Returns400`,
and `TestGetCityCalendar_MissingDates_Returns400`:

- A first page with 21+ candidates returns exactly 20 events and a `next_cursor`.
- Following `next_cursor` returns the next slice with no overlap and no gaps
  against the first.
- The final page omits `next_cursor`.
- **Events sharing an identical `starts_at` that straddle the page boundary
  paginate correctly.** This is the test that justifies the composite cursor; a
  `starts_at`-only cursor passes every other test here and fails this one.
- A malformed cursor returns 400 `bad_cursor`.
- Stale `from`/`to` params return 200, not 400.

Existing city-calendar behavior tests (unmatched events included, not-interested
events included, unknown city → empty list, bad city id → 400, no token → 401)
keep passing unchanged.

Unit tests for `cursor.go`: round-trip fidelity including sub-second precision,
non-base64 input, well-formed base64 with a garbage payload, and empty string.

## Performance follow-ups

None of the below is part of this change. It is recorded so the tradeoffs are not
re-derived later.

### What the access paths actually cost

For `/me/calendar`, the driving index is `user_event_match (user_id, …)`
(`sql/migrations/0010_user_event_match.up.sql`), which yields all N of the user's
match rows. The sort key `e.starts_at` lives on `events`, reached by join, so the
keyset predicate is evaluated *after* the join. It shrinks the sort input on later
pages but never shrinks the N index entries scanned or the N join lookups.

The practical consequence, which is worth stating plainly because it inverts the
usual intuition: **cost per page is roughly flat with depth, and page 1 is already
the expensive case.** Deep pages are marginally cheaper, not more expensive. There
is no "deep pagination cliff" here; there is a constant per-page full pass over
one user's matches.

For `/calendar/{cityId}` the planner chooses between an `events_starts_at` scan
from the cursor position (walking forward until 21 rows in the target city
accumulate — depth-tolerant, sensitive to how large a fraction of events the city
represents) and a venues-in-city nested loop over `events_venue_id` followed by a
sort. Both are flat with depth.

### Options, cheapest first

**1. Re-add a horizon bound.** Capping at `NOW() + 90 days` shrinks N directly for
both queries, needs no migration, and composes with everything below. Best
value-per-effort of the options here; the only reason it isn't in this design is
that an unbounded forward feed is what was specified.

**2. Expression index on `event_over_at`.** The function is declared `IMMUTABLE`
and takes only `events` columns (`sql/migrations/0021_event_time_tbd.up.sql:15-24`),
so it is indexable:

```sql
CREATE INDEX events_over_at ON events (event_over_at(starts_at, ends_at, time_tbd));
```

This turns the showable filter from a per-row function call into an index range
condition, and it also helps the prune-stale-matches queries. One migration, no
application changes. Note that `NOW()` is `STABLE`, not `IMMUTABLE`, so it cannot
appear in a partial-index *predicate* — but comparing an indexed expression
against `NOW()` at query time is fine. Folding `WHERE archived_at IS NULL` in as a
partial predicate is free and immutable-safe.

**3. Denormalize `city_id` onto `events`,** indexed `(city_id, starts_at, id)`.
`city_id` currently lives on `venues`, which is exactly what prevents the city
query from being a pure index scan. With it, `/calendar/{cityId}` becomes
walk-from-cursor, stop at 21, no sort, at any depth. Costs a backfill and keeping
the column correct at ingest.

**4. Denormalize `starts_at` onto `user_event_match`,** indexed
`(user_id, starts_at, event_id)`. Same trick for the user query; eliminates the
per-page full pass entirely. Most invasive of the four — the match writer must
populate it and event reschedules must propagate. It may piggyback on whatever
already recomputes matches (see `docs/superpowers/plans/2026-06-02-prune-stale-matches.md`),
but that needs confirming rather than assuming.

### Recommendation

Ship the pagination without any of it. The number that decides all four options is
what N actually is — matches-per-user and upcoming-events-per-city. At low
thousands the sort is sub-millisecond and every option above is wasted work.

Get `EXPLAIN (ANALYZE, BUFFERS)` against production-shaped data first. If it does
bite, take options 1 and 2 — they need no denormalization and no app-side sync —
and only reach for 3 or 4 if measurement identifies the sort specifically as the
bottleneck.

Each option is also a new migration, and a new migration strands other worktrees'
shared `appdb_test` until they catch up, so there is a coordination cost on top of
the SQL.

## Non-goals

- No `limit` query param; 20 is fixed server-side.
- No index migrations as part of this change (see above).
- No backward-compatibility shim for `from`/`to` beyond being harmlessly ignored.
- No frontend changes.
