# Calendar Cursor Pagination Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `from`/`to` date window on `GET /me/calendar` and `GET /calendar/{cityId}` with forward-only cursor pagination at a fixed page size of 20.

**Architecture:** A composite keyset cursor over `(starts_at, id)` encoded as one opaque base64url token. Two new sqlc queries drop the date-range bounds and add a NULL-guarded keyset predicate, so an absent cursor yields the first page. Each handler asks the DB for 21 rows to detect "more exist" without a second count query.

**Tech Stack:** Go, chi v5, pgx/v5, sqlc v1.31.1, testify, Postgres.

**Spec:** `docs/superpowers/specs/2026-07-30-calendar-cursor-pagination-design.md`

## Global Constraints

- Page size is a fixed server-side constant of **20**. There is no `limit` query param.
- The cursor is **opaque**. Only `internal/http/handlers/cursor.go` may construct or parse it.
- `from` and `to` are removed. Unrecognized query params are **ignored**, never a 400 — a client still sending `from`/`to` must receive a normal first page.
- A malformed cursor is always **400 `bad_cursor`**, never a 500.
- **Do not modify anything under `web/`.** The frontend is being changed separately by the repo owner.
- **Do not modify `internal/http/handlers/ical.go` or the `GetUserCalendarInRange` query.** The iCal feed keeps its own unpaginated ranged query.
- No new migrations. No new indexes.
- Every task ends with a passing `make test` and a commit.

## Environment setup

The handler and store tests need a live local Postgres:

```bash
make db-up          # start the container
make migrate-test   # apply migrations to appdb_test
```

If `make migrate-test` reports `no migration found for version N`, another worktree has applied a newer migration to the shared `appdb_test`. Run `make db-reset` then `make migrate-test`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/http/handlers/cursor.go` (create) | Encode/decode the opaque keyset cursor. Pure functions, no DB, no HTTP. |
| `internal/http/handlers/cursor_test.go` (create) | Unit tests for the codec. No DB. |
| `sql/queries/calendar.sql` (modify) | Add `GetUserCalendarPage` + `GetCityCalendarPage`; later delete `GetCityCalendarInRange`. |
| `internal/store/calendar.sql.go` (generated) | `sqlc generate` output — never hand-edit. |
| `internal/store/city_calendar_test.go` (modify) | Store-level tests, retargeted to the paged city query. |
| `internal/http/handlers/calendar.go` (modify) | Both handlers; the `parseCursor` helper; `parseDateRange` deleted at the end. |
| `internal/http/handlers/calendar_test.go` (modify) | Handler-level pagination tests. |

Task order matters: the codec has no dependencies, the queries depend on nothing in Go, and each handler depends on both. `parseDateRange` can only be deleted once *both* handlers stop calling it, which is why that deletion lands in Task 4.

---

### Task 1: Opaque cursor codec

**Files:**
- Create: `internal/http/handlers/cursor.go`
- Test: `internal/http/handlers/cursor_test.go`

**Interfaces:**
- Consumes: `uuidString(pgtype.UUID) string`, already defined at `internal/http/handlers/calendar.go:205`.
- Produces:
  - `encodeCursor(startsAt time.Time, eventID pgtype.UUID) string`
  - `decodeCursor(s string) (time.Time, uuid.UUID, error)`
  - `errBadCursor` — the single error value every failure mode returns.

  Tasks 3 and 4 call both functions. The test file is `package handlers` (internal), not `handlers_test`, because these are unexported.

- [ ] **Step 1: Write the failing tests**

Create `internal/http/handlers/cursor_test.go`:

```go
package handlers

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	want := time.Date(2026, 8, 1, 19, 30, 0, 0, time.UTC)

	tok := encodeCursor(want, pgtype.UUID{Bytes: id, Valid: true})
	gotTime, gotID, err := decodeCursor(tok)

	require.NoError(t, err)
	require.True(t, want.Equal(gotTime))
	require.Equal(t, id, gotID)
}

// starts_at carries sub-second precision in Postgres. A cursor that truncated
// it would re-emit or skip the row sitting exactly on a page boundary.
func TestCursorRoundTripPreservesSubSecond(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	want := time.Date(2026, 8, 1, 19, 30, 0, 123456000, time.UTC)

	gotTime, _, err := decodeCursor(encodeCursor(want, pgtype.UUID{Bytes: id, Valid: true}))

	require.NoError(t, err)
	require.True(t, want.Equal(gotTime), "want %s got %s", want, gotTime)
}

// A non-UTC input must come back as the same instant.
func TestCursorNormalizesToUTC(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	loc := time.FixedZone("PDT", -7*60*60)
	want := time.Date(2026, 8, 1, 12, 30, 0, 0, loc)

	gotTime, _, err := decodeCursor(encodeCursor(want, pgtype.UUID{Bytes: id, Valid: true}))

	require.NoError(t, err)
	require.True(t, want.Equal(gotTime))
}

func TestCursorRejectsBadInput(t *testing.T) {
	valid := encodeCursor(time.Now(), pgtype.UUID{Bytes: uuid.New(), Valid: true})

	cases := map[string]string{
		"empty":             "",
		"not base64":        "!!!not-base64!!!",
		"base64 of garbage": base64.RawURLEncoding.EncodeToString([]byte("garbage")),
		"no separator":      base64.RawURLEncoding.EncodeToString([]byte("2026-08-01T19:30:00Z")),
		"bad timestamp":     base64.RawURLEncoding.EncodeToString([]byte("nope|" + uuid.New().String())),
		"bad uuid":          base64.RawURLEncoding.EncodeToString([]byte("2026-08-01T19:30:00Z|nope")),
		"truncated token":   valid[:len(valid)-4],
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodeCursor(in)
			require.ErrorIs(t, err, errBadCursor)
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/http/handlers/ -run TestCursor -count=1
```

Expected: FAIL — `undefined: encodeCursor`, `undefined: decodeCursor`, `undefined: errBadCursor`.

- [ ] **Step 3: Write the implementation**

Create `internal/http/handlers/cursor.go`:

```go
package handlers

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// errBadCursor is what every decodeCursor failure returns. Callers only ever
// need to distinguish "usable cursor" from "not one", so the specific reason a
// token was rejected is deliberately not surfaced — telling a client which byte
// of an opaque token offended us invites them to start constructing their own.
var errBadCursor = errors.New("malformed cursor")

// encodeCursor packs a keyset position into one opaque token.
//
// Both halves are required: starts_at is not unique (events routinely share a
// start instant), so a cursor carrying only the timestamp would skip or repeat
// rows sitting on a page boundary. The pair (starts_at, id) is unique because id
// is the events primary key.
//
// The encoding is an implementation detail. Nothing outside this file may parse
// it, which is what leaves us free to change it without breaking clients.
func encodeCursor(startsAt time.Time, eventID pgtype.UUID) string {
	raw := startsAt.UTC().Format(time.RFC3339Nano) + "|" + uuidString(eventID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reverses encodeCursor. Every failure is client error, never
// server error — see the 400 in parseCursor.
func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	tsPart, idPart, found := strings.Cut(string(b), "|")
	if !found {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	startsAt, err := time.Parse(time.RFC3339Nano, tsPart)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	eventID, err := uuid.Parse(idPart)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	return startsAt, eventID, nil
}
```

Note `base64.RawURLEncoding` (unpadded, URL-safe) — padded standard base64 would put `=` and `+` in a query string.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/http/handlers/ -run TestCursor -v -count=1
```

Expected: PASS, all subtests included.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/cursor.go internal/http/handlers/cursor_test.go
git commit -m "Add the opaque keyset cursor codec"
```

---

### Task 2: Paged SQL queries

**Files:**
- Modify: `sql/queries/calendar.sql`
- Regenerate: `internal/store/calendar.sql.go`, `internal/store/models.go`
- Test: `internal/store/city_calendar_test.go` (rewrite in place)

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `q.GetUserCalendarPage(ctx, store.GetUserCalendarPageParams{...})` and `q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{...})`, both `:many`. Tasks 3 and 4 call them. The row structs carry the same columns as the existing `GetUserCalendarInRangeRow` / `GetCityCalendarInRangeRow`.

**Do not** touch `GetUserCalendarInRange` — `internal/http/handlers/ical.go:108` depends on it. **Do not** delete `GetCityCalendarInRange` yet; the city handler still calls it until Task 4.

- [ ] **Step 1: Add the two queries**

Append to `sql/queries/calendar.sql`:

```sql
-- name: GetUserCalendarPage :many
-- One page of the user's matched events. Ordered by (starts_at, id) so the
-- keyset cursor is stable when several events start at the same instant —
-- ordering on starts_at alone would drop or repeat rows at page boundaries.
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    v.name            AS venue_name,
    v.address         AS venue_address,
    m.score,
    m.score_breakdown
FROM user_event_match m
JOIN events e ON e.id = m.event_id
JOIN venues v ON v.id = e.venue_id
WHERE m.user_id = sqlc.arg(user_id)
  AND e.archived_at IS NULL
  -- Still showable: a date-only event runs until its local day is out, a timed
  -- one until it ends. With the from/to window gone this is also the feed's
  -- lower bound — there is no upper bound.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  AND NOT EXISTS (
      SELECT 1 FROM user_event_not_interested ni
      WHERE ni.user_id = m.user_id AND ni.event_id = e.id
  )
  -- A NULL cursor means "first page": the guard short-circuits and every row
  -- qualifies. Otherwise this is a strict row-comparison keyset seek.
  AND (
      sqlc.narg(cursor_starts_at)::timestamptz IS NULL
      OR (e.starts_at, e.id) > (sqlc.narg(cursor_starts_at)::timestamptz,
                                sqlc.narg(cursor_event_id)::uuid)
  )
ORDER BY e.starts_at ASC, e.id ASC
LIMIT sqlc.arg(page_limit);

-- name: GetCityCalendarPage :many
-- One page of every showable event in the city, with no match filtering and --
-- deliberately -- no not-interested filtering: this endpoint returns an
-- identical response for every caller. Same ordering and cursor rules as
-- GetUserCalendarPage.
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    v.name            AS venue_name,
    v.address         AS venue_address
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = sqlc.arg(city_id)
  AND e.archived_at IS NULL
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
  AND (
      sqlc.narg(cursor_starts_at)::timestamptz IS NULL
      OR (e.starts_at, e.id) > (sqlc.narg(cursor_starts_at)::timestamptz,
                                sqlc.narg(cursor_event_id)::uuid)
  )
ORDER BY e.starts_at ASC, e.id ASC
LIMIT sqlc.arg(page_limit);
```

- [ ] **Step 2: Generate the Go bindings**

```bash
sqlc generate
git diff --stat internal/store/
```

`sqlc generate` rewrites every file in `internal/store/*.sql.go` plus `models.go`. Expect changes only in `calendar.sql.go`; if `models.go` also changed, commit it too rather than reverting it.

**Then open `internal/store/calendar.sql.go` and confirm the generated param struct field names and types.** They should be:

```go
type GetUserCalendarPageParams struct {
	UserID         pgtype.UUID        `json:"user_id"`
	CursorStartsAt pgtype.Timestamptz `json:"cursor_starts_at"`
	CursorEventID  pgtype.UUID        `json:"cursor_event_id"`
	PageLimit      int32              `json:"page_limit"`
}
```

If sqlc named or typed anything differently (in particular `PageLimit` may come out as `int32` or `int64`), use the **generated** names and types in Tasks 3 and 4 rather than the ones written in this plan, and note the difference in the commit message.

- [ ] **Step 3: Rewrite the store tests against the paged query**

Replace the three `TestGetCityCalendarInRange_*` functions in `internal/store/city_calendar_test.go` with the following. Keep the `otherCity` and `seedCityEvent` helpers at the top of the file exactly as they are.

Note the behavior change in the second test: the old version seeded a `Far Show` at +90 days and asserted the range window excluded it. **There is no upper bound any more, so `Far Show` is now expected in the results**, ordered after `Soon Show`.

```go
func TestGetCityCalendarPage_ReturnsUnmatchedEventsAndExcludesOtherCities(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	away := otherCity(t, pool)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "city-1", "Home Show", time.Now().Add(48*time.Hour))
	seedCityEvent(t, q, ctx, away, "Away Hall", "city-2", "Away Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 21,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Home Show", rows[0].Title)
	require.Equal(t, "The Bowl", rows[0].VenueName)
}

// Past events stay out. Far-future ones no longer do: dropping from/to removed
// the upper bound, so the feed runs forward indefinitely.
func TestGetCityCalendarPage_ExcludesPastEventsButNotFarFutureOnes(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "past-1", "Past Show", time.Now().Add(-72*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "far-1", "Far Show", time.Now().Add(90*24*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "soon-1", "Soon Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 21,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "Soon Show", rows[0].Title)
	require.Equal(t, "Far Show", rows[1].Title)
}

func TestGetCityCalendarPage_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	live := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "live-1", "Live Show", time.Now().Add(48*time.Hour))
	gone := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "gone-1", "Archived Show", time.Now().Add(48*time.Hour))
	_, err = pool.Exec(ctx, `UPDATE events SET archived_at = NOW() WHERE id = $1`, gone)
	require.NoError(t, err)

	rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 21,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Live Show", rows[0].Title)
	require.Equal(t, live, rows[0].EventID)
}

// LIMIT caps the page, and the cursor resumes exactly after the last row of the
// previous one.
func TestGetCityCalendarPage_LimitAndCursorSeek(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	base := time.Now().Add(24 * time.Hour)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "p-1", "First", base)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "p-2", "Second", base.Add(time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "p-3", "Third", base.Add(2*time.Hour))

	first, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:    home.ID,
		PageLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "First", first[0].Title)
	require.Equal(t, "Second", first[1].Title)

	last := first[len(first)-1]
	second, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
		CityID:         home.ID,
		CursorStartsAt: last.StartsAt,
		CursorEventID:  last.EventID,
		PageLimit:      2,
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "Third", second[0].Title)
}

// The tiebreaker that justifies the composite cursor: three events sharing one
// starts_at must paginate without dropping or repeating any of them.
func TestGetCityCalendarPage_TiedStartsAtPaginateCleanly(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	tied := time.Now().Add(24 * time.Hour)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "tie-1", "Tie A", tied)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "tie-2", "Tie B", tied)
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "tie-3", "Tie C", tied)

	seen := []string{}
	params := store.GetCityCalendarPageParams{CityID: home.ID, PageLimit: 2}
	for {
		rows, err := q.GetCityCalendarPage(ctx, params)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			seen = append(seen, r.Title)
		}
		last := rows[len(rows)-1]
		params.CursorStartsAt = last.StartsAt
		params.CursorEventID = last.EventID
	}

	require.ElementsMatch(t, []string{"Tie A", "Tie B", "Tie C"}, seen)
	require.Len(t, seen, 3, "no event may appear twice")
}
```

An unset `CursorStartsAt` / `CursorEventID` is the zero `pgtype` value with `Valid: false`, which pgx sends as SQL NULL — that is exactly the first-page case the query's `IS NULL` guard handles. No explicit nil is needed.

- [ ] **Step 4: Run the store tests**

```bash
make db-up && make migrate-test
go test ./internal/store/ -run TestGetCityCalendarPage -v -count=1
```

Expected: PASS, all five tests.

If `sqlc generate` produced a compile error about the `(a, b) > (c, d)` row comparison, confirm the `::timestamptz` and `::uuid` casts are present — sqlc needs them to infer the param types.

- [ ] **Step 5: Run the full suite**

```bash
make test
```

Expected: PASS. The handlers still use the old ranged queries at this point and are unaffected.

- [ ] **Step 6: Commit**

```bash
git add sql/queries/calendar.sql internal/store/
git commit -m "Add cursor-paged calendar queries"
```

---

### Task 3: Paginate GET /me/calendar

**Files:**
- Modify: `internal/http/handlers/calendar.go`
- Test: `internal/http/handlers/calendar_test.go`

**Interfaces:**
- Consumes: `encodeCursor` / `decodeCursor` / `errBadCursor` (Task 1); `store.GetUserCalendarPageParams` (Task 2).
- Produces:
  - `const calendarPageSize = 20`
  - `parseCursor(w http.ResponseWriter, r *http.Request) (pgtype.Timestamptz, pgtype.UUID, bool)` — Task 4 reuses this.
  - `calendarResponse` gains a `NextCursor string` field with `json:"next_cursor,omitempty"`.

Leave `parseDateRange` in place — `GetCityCalendar` still calls it until Task 4.

- [ ] **Step 1: Write the failing tests**

In `internal/http/handlers/calendar_test.go`, **delete** `TestGetMyCalendar_DateRangeFiltering` (lines 97-120) and `TestGetMyCalendar_MissingDates_Returns400` (lines 122-137) — both assert behavior this task removes. Then add a seed helper and the new tests:

```go
// seedManyMatchedEvents adds n matched events for userID, one hour apart
// starting 24h out, titled "Event 00".."Event NN" in chronological order.
func seedManyMatchedEvents(t *testing.T, q *store.Queries, ctx context.Context, userID pgtype.UUID, n int) {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Paging Hall", NormalizedName: "paging hall",
	})
	require.NoError(t, err)

	base := time.Now().Add(24 * time.Hour)
	for i := 0; i < n; i++ {
		eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: fmt.Sprintf("page-%03d", i),
			Title:         fmt.Sprintf("Event %02d", i),
			Description:   "seeded",
			StartsAt:      pgtype.Timestamptz{Time: base.Add(time.Duration(i) * time.Hour), Valid: true},
			VenueID:       venueID,
		})
		require.NoError(t, err)
		require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
			UserID:         userID,
			EventID:        eventID,
			Score:          0.5,
			ScoreBreakdown: []byte(`{}`),
			ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}))
	}
}

type pagedCalendarResponse struct {
	Events []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"events"`
	NextCursor string `json:"next_cursor"`
}

// getMyCalendarPage issues one request and decodes it. cursor may be "".
func getMyCalendarPage(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID pgtype.UUID, cursor string) pagedCalendarResponse {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/me/calendar"
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

func TestGetMyCalendar_FirstPageCapsAt20AndReturnsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	resp := getMyCalendarPage(t, q, signer, userID, "")

	require.Len(t, resp.Events, 20)
	require.NotEmpty(t, resp.NextCursor)
}

func TestGetMyCalendar_CursorWalksEveryEventExactlyOnce(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	// 25 seeded here + the 1 from seedCalendarFixture = 26 total.
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getMyCalendarPage(t, q, signer, userID, cursor)
		require.LessOrEqual(t, len(resp.Events), 20)
		for _, e := range resp.Events {
			require.False(t, seenIDs[e.ID], "event %s returned twice", e.ID)
			seenIDs[e.ID] = true
			total++
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	require.Equal(t, 26, total)
}

func TestGetMyCalendar_LastPageOmitsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	// Exactly one event exists, so the first page is also the last.
	resp := getMyCalendarPage(t, q, signer, userID, "")

	require.Len(t, resp.Events, 1)
	require.Empty(t, resp.NextCursor)
}

// A page that is exactly full but has nothing after it must not advertise a
// next page. This is what the fetch-21-return-20 trick buys us.
func TestGetMyCalendar_ExactlyFullPageOmitsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	// 19 + the fixture's 1 = exactly 20.
	seedManyMatchedEvents(t, q, ctx, userID, 19)

	resp := getMyCalendarPage(t, q, signer, userID, "")

	require.Len(t, resp.Events, 20)
	require.Empty(t, resp.NextCursor)
}

func TestGetMyCalendar_BadCursor_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?cursor=!!!not-a-cursor!!!", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_cursor")
}

// The frontend still sends from/to until it is updated separately. Those params
// must be ignored, not rejected.
func TestGetMyCalendar_StaleFromToParamsAreIgnored(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from=2026-01-01&to=2026-01-02", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	// The window would have excluded it; pagination ignores the window.
	require.Len(t, resp.Events, 1)
}
```

Add `"fmt"` to the import block of `calendar_test.go`.

`TestGetMyCalendar_ReturnsMatchedEvents` (line 55) keeps passing unchanged — it sends `from`/`to`, which are now ignored, and still expects the single fixture event.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/http/handlers/ -run TestGetMyCalendar -count=1
```

Expected: FAIL — `resp.NextCursor` is always empty and the bad-cursor case returns 200, because the handler still reads `from`/`to`.

- [ ] **Step 3: Implement the handler changes**

In `internal/http/handlers/calendar.go`, add the page-size constant and extend the response struct:

```go
// calendarPageSize is the maximum number of events in one calendar response.
// Fixed server-side: there is no client-supplied limit.
const calendarPageSize = 20

type calendarResponse struct {
	Events     []calendarEvent `json:"events"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
```

Add `parseCursor` next to `parseDateRange`:

```go
// parseCursor reads the optional cursor query param into keyset params. Absent
// cursor yields two invalid pgtypes, which pgx sends as NULL — the query reads
// that as "first page". On bad input it writes the error response and returns
// ok=false.
func parseCursor(w http.ResponseWriter, r *http.Request) (startsAt pgtype.Timestamptz, eventID pgtype.UUID, ok bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, true
	}
	ts, id, err := decodeCursor(raw)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_cursor", "cursor is not valid")
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{Time: ts, Valid: true}, pgtype.UUID{Bytes: id, Valid: true}, true
}
```

Then rewrite `GetMyCalendar`. Replace the `parseDateRange` call and the `GetUserCalendarInRange` call; update the doc comment; leave the row-mapping loop as it is apart from the trim:

```go
// GetMyCalendar returns one page of the authenticated user's matched events,
// ordered by start time, beginning at the optional cursor. At most
// calendarPageSize events come back; next_cursor is present only when more
// exist.
func GetMyCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		cursorStartsAt, cursorEventID, ok := parseCursor(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// One more than the page size: if it comes back, there is a next page.
		rows, err := q.GetUserCalendarPage(ctx, store.GetUserCalendarPageParams{
			UserID:         pgtype.UUID{Bytes: uid, Valid: true},
			CursorStartsAt: cursorStartsAt,
			CursorEventID:  cursorEventID,
			PageLimit:      calendarPageSize + 1,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load calendar", err)
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
		if len(rows) > calendarPageSize {
			rows = rows[:calendarPageSize]
			last := rows[len(rows)-1]
			out.NextCursor = encodeCursor(last.StartsAt.Time, last.EventID)
		}
		for _, row := range rows {
			bd := parseBreakdown(row.ScoreBreakdown)
			ev := calendarEvent{
				ID:          uuidString(row.EventID),
				Title:       row.Title,
				Description: row.Description,
				Score:       row.Score,
				StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				MatchedBecause: bd,
			}
			if row.EndsAt.Valid {
				ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
			}
			ev.ImageURL = textPtrToString(row.ImageUrl)
			ev.URL = textPtrToString(row.Url)
			out.Events = append(out.Events, ev)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
```

The trim happens **before** the mapping loop so the extra row is never converted or emitted. `PageLimit` must match the generated type from Task 2 — cast if sqlc emitted `int32`.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/http/handlers/ -run TestGetMyCalendar -v -count=1
```

Expected: PASS, including `TestGetMyCalendar_ReturnsMatchedEvents`.

- [ ] **Step 5: Run the full suite**

```bash
make test
```

Expected: PASS. `parseDateRange` is still referenced by `GetCityCalendar`, so nothing is unused yet.

- [ ] **Step 6: Commit**

```bash
git add internal/http/handlers/calendar.go internal/http/handlers/calendar_test.go
git commit -m "Paginate GET /me/calendar with a keyset cursor"
```

---

### Task 4: Paginate GET /calendar/{cityId} and remove the dead range code

**Files:**
- Modify: `internal/http/handlers/calendar.go`
- Modify: `sql/queries/calendar.sql`
- Regenerate: `internal/store/calendar.sql.go`
- Test: `internal/http/handlers/calendar_test.go`

**Interfaces:**
- Consumes: `parseCursor`, `calendarPageSize`, `calendarResponse.NextCursor` (Task 3); `store.GetCityCalendarPageParams` (Task 2).
- Produces: nothing new. This task ends the change.

- [ ] **Step 1: Write the failing tests**

In `internal/http/handlers/calendar_test.go`, **delete** `TestGetCityCalendar_MissingDates_Returns400` — find it by name, since Task 3's deletions shifted every line number in this file. Then update the three tests that build a URL with `?from=...&to=...` — `TestGetCityCalendar_ReturnsUnmatchedEvents`, `TestGetCityCalendar_IncludesNotInterestedEvents`, and `TestGetCityCalendar_UnknownCityReturnsEmptyList` — to drop those params:

```go
// in TestGetCityCalendar_ReturnsUnmatchedEvents and
// TestGetCityCalendar_IncludesNotInterestedEvents, replace the url line with:
url := "/calendar/" + uuidFromPgCal(city.ID).String()

// in TestGetCityCalendar_UnknownCityReturnsEmptyList:
url := "/calendar/" + uuid.New().String()
```

`TestGetCityCalendar_BadCityID_Returns400` and `TestGetCityCalendar_NoToken_Returns401` may keep their stale query strings — they exercise failure paths that never reach cursor parsing — but dropping the params from them too is tidier.

Add the pagination tests:

```go
func getCityCalendarPage(t *testing.T, q *store.Queries, signer *auth.JWTSigner, userID, cityID pgtype.UUID, cursor string) pagedCalendarResponse {
	t.Helper()
	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(cityID).String()
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

func TestGetCityCalendar_FirstPageCapsAt20AndReturnsCursor(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	resp := getCityCalendarPage(t, q, signer, userID, city.ID, "")

	require.Len(t, resp.Events, 20)
	require.NotEmpty(t, resp.NextCursor)
}

func TestGetCityCalendar_CursorWalksEveryEventExactlyOnce(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)
	seedManyMatchedEvents(t, q, ctx, userID, 25)

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getCityCalendarPage(t, q, signer, userID, city.ID, cursor)
		require.LessOrEqual(t, len(resp.Events), 20)
		for _, e := range resp.Events {
			require.False(t, seenIDs[e.ID], "event %s returned twice", e.ID)
			seenIDs[e.ID] = true
			total++
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	require.Equal(t, 26, total)
}

// The composite-cursor test at the HTTP layer: events sharing a start instant
// straddle the page boundary and must not be dropped or repeated.
func TestGetCityCalendar_TiedStartsAtStraddlingPageBoundary(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Tie Hall", NormalizedName: "tie hall",
	})
	require.NoError(t, err)

	// 25 events all at the same instant, so the page boundary at 20 falls in
	// the middle of the tie group.
	tied := time.Now().Add(24 * time.Hour)
	for i := 0; i < 25; i++ {
		_, err := q.UpsertEvent(ctx, store.UpsertEventParams{
			SourceID:      src.ID,
			SourceEventID: fmt.Sprintf("tie-%03d", i),
			Title:         fmt.Sprintf("Tied %02d", i),
			Description:   "seeded",
			StartsAt:      pgtype.Timestamptz{Time: tied, Valid: true},
			VenueID:       venueID,
		})
		require.NoError(t, err)
	}

	seenIDs := map[string]bool{}
	total := 0
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 10, "pagination did not terminate")
		resp := getCityCalendarPage(t, q, signer, userID, city.ID, cursor)
		for _, e := range resp.Events {
			require.False(t, seenIDs[e.ID], "event %s returned twice across a tie", e.ID)
			seenIDs[e.ID] = true
			total++
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	// 25 tied + the fixture's 1.
	require.Equal(t, 26, total)
}

func TestGetCityCalendar_BadCursor_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?cursor=!!!not-a-cursor!!!"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_cursor")
}

func TestGetCityCalendar_StaleFromToParamsAreIgnored(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?from=2026-01-01&to=2026-01-02"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp pagedCalendarResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/http/handlers/ -run TestGetCityCalendar -count=1
```

Expected: FAIL — the three de-parameterized tests now get 400 `missing_range`, and the new tests get no cursor back.

- [ ] **Step 3: Rewrite the city handler**

In `internal/http/handlers/calendar.go`, replace the `parseDateRange` call and the `GetCityCalendarInRange` call in `GetCityCalendar`. Keep the existing doc comment's explanation of why this endpoint is caller-independent; only the range sentence changes:

```go
// GetCityCalendar returns one page of every showable event in the given city —
// no match filtering, so events the caller has no user_event_match row for are
// included. This is what the calendar page falls back to when the user has no
// interests to match against, so the response is deliberately identical for
// every caller: no not-interested filtering, and score/matched_because are
// always the empty values. Paginated exactly like GetMyCalendar.
func GetCityCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}
		cursorStartsAt, cursorEventID, ok := parseCursor(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.GetCityCalendarPage(ctx, store.GetCityCalendarPageParams{
			CityID:         pgtype.UUID{Bytes: cityUUID, Valid: true},
			CursorStartsAt: cursorStartsAt,
			CursorEventID:  cursorEventID,
			PageLimit:      calendarPageSize + 1,
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load city calendar", err)
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
		if len(rows) > calendarPageSize {
			rows = rows[:calendarPageSize]
			last := rows[len(rows)-1]
			out.NextCursor = encodeCursor(last.StartsAt.Time, last.EventID)
		}
		for _, row := range rows {
			ev := calendarEvent{
				ID:          uuidString(row.EventID),
				Title:       row.Title,
				Description: row.Description,
				StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				// No match exists for these events; parseBreakdown(nil) gives the
				// empty non-nil slices the FE expects.
				MatchedBecause: parseBreakdown(nil),
			}
			if row.EndsAt.Valid {
				ev.EndsAt = row.EndsAt.Time.UTC().Format(time.RFC3339)
			}
			ev.ImageURL = textPtrToString(row.ImageUrl)
			ev.URL = textPtrToString(row.Url)
			out.Events = append(out.Events, ev)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
```

- [ ] **Step 4: Delete the now-dead range code**

Delete `parseDateRange` from `internal/http/handlers/calendar.go` — the function and its doc comment, found by name rather than line number since Task 3 shifted the file. Nothing calls it now. Remove the `"time"` import only if nothing else uses it — the `time.Second` timeouts and `time.RFC3339` formatting still do, so it stays.

Delete the whole `-- name: GetCityCalendarInRange :many` block from `sql/queries/calendar.sql`. Its only caller was the handler you just rewrote. **Leave `GetUserCalendarInRange` alone** — `ical.go` still uses it.

Regenerate:

```bash
sqlc generate
```

- [ ] **Step 5: Verify nothing references the deleted symbols**

```bash
grep -rn "parseDateRange\|missing_range\|GetCityCalendarInRange" --include="*.go" .
```

Expected: no output. If `GetCityCalendarInRange` still appears in `internal/store/calendar.sql.go`, `sqlc generate` did not pick up the deletion — confirm the block was removed from the `.sql` file and rerun.

- [ ] **Step 6: Run the full suite**

```bash
go build ./... && make test
```

Expected: PASS, including the untouched `TestGetUserCalendarInRange*` and iCal tests.

- [ ] **Step 7: Verify the endpoints by hand**

```bash
make run   # in a second terminal
```

The listen address comes from `cfg.HTTPAddr`, not a fixed port — `make run` prints `listening on <addr>` at startup (`cmd/app/main.go:199`). Put that address in `$BASE` (e.g. `BASE=http://localhost:8080`) and a valid access token in `$TOK`:

```bash
# First page: expect 20 events and a next_cursor.
curl -s -H "Authorization: Bearer $TOK" "$BASE/me/calendar" | jq '{n: (.events|length), next: .next_cursor}'

# Follow it.
CUR=$(curl -s -H "Authorization: Bearer $TOK" "$BASE/me/calendar" | jq -r .next_cursor)
curl -s -H "Authorization: Bearer $TOK" "$BASE/me/calendar?cursor=$CUR" | jq '{n: (.events|length), next: .next_cursor}'

# Malformed cursor: expect 400 bad_cursor.
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$BASE/me/calendar?cursor=bogus"

# Stale params: expect 200.
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOK" "$BASE/me/calendar?from=2026-01-01&to=2026-01-02"
```

Skip this step if there is not enough seeded local data to produce more than 20 events; the automated tests cover the same ground.

- [ ] **Step 8: Commit**

```bash
git add internal/http/handlers/calendar.go internal/http/handlers/calendar_test.go sql/queries/calendar.sql internal/store/
git commit -m "Paginate GET /calendar/{cityId} and drop the from/to range code"
```

---

## Done criteria

- Both endpoints accept an optional `cursor` and return at most 20 events.
- `next_cursor` is present when more results exist and absent on the last page.
- `from`/`to` are gone from the handlers and from `sql/queries/calendar.sql`'s city query; sending them is a 200, not a 400.
- A malformed cursor is a 400 `bad_cursor`.
- `internal/http/handlers/ical.go` and `GetUserCalendarInRange` are byte-for-byte unchanged.
- Nothing under `web/` is modified — confirm with `git diff --stat f159e4bb..HEAD -- web/` returning empty.
- `make test` passes.

## Follow-ups explicitly out of scope

The spec's "Performance follow-ups" section lists four optimizations (horizon bound, `event_over_at` expression index, denormalizing `city_id`, denormalizing `starts_at`). **None are part of this plan.** They are gated on measuring real matches-per-user and events-per-city numbers first.
