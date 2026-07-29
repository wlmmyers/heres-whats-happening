# City Calendar Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a user has neither connected Spotify nor added a manual interest, show them every event in their city instead of an empty matched calendar.

**Architecture:** A new authenticated endpoint `GET /calendar/{cityId}?from&to` reads events joined to venues by `venues.city_id`, with no `user_event_match` join — that absence is the feature. `/me` gains `city_id` so the SPA already knows which city to ask for. `CalendarPage` runs two gate queries (Spotify status, manual interests) and swaps in the city list when both come back empty.

**Tech Stack:** Go 1.x + chi v5 + pgx/v5 + sqlc v1.31.1 (`internal/`, `sql/`); React 19 + @tanstack/react-query 5 + react-router-dom 7 + vanilla-extract, tested with vitest + @testing-library/react 16 (`web/`).

Design doc: `docs/superpowers/specs/2026-07-29-city-calendar-fallback-design.md`

## Global Constraints

- Go tests need the local Postgres running (`make db-up`) and use `testdb.MustOpen(t)`, which holds a process-wide advisory lock — always run Go tests with `-p 1` (that is what `make test` does).
- `testdb` truncates every table between tests **except** `cities`, `event_sources`, and `schema_migrations`. Rows inserted into `cities` by a test survive the whole run, so any city insert must be idempotent (`ON CONFLICT (slug) DO UPDATE`).
- `sqlc generate` rewrites `internal/store/models.go` as well as the per-file `*.sql.go`. Commit every file it touches.
- The repo has a pre-commit hook running gofmt, go vet, go test, eslint, tsc, prettier, and vitest. Every `git commit` in this plan runs all of it; do not use `--no-verify`.
- Run web commands from `web/` with `pnpm` (not npm): `pnpm test`, `pnpm lint`, `pnpm build`.
- New JSON fields use `snake_case` to match every existing response.
- User-facing copy, exactly: title `Everything happening in Seattle`; banner `Showing everything in Seattle. Connect your Spotify or add some interests to get a calendar matched to your taste.`; empty city list `Nothing on the calendar in Seattle right now.`

---

### Task 1: `GetCityCalendarInRange` store query

**Files:**
- Modify: `sql/queries/calendar.sql` (append)
- Create: `internal/store/city_calendar_test.go`
- Generated (by `sqlc generate`): `internal/store/calendar.sql.go`, `internal/store/models.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `(*store.Queries).GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams) ([]store.GetCityCalendarInRangeRow, error)`.
  `GetCityCalendarInRangeParams{CityID pgtype.UUID; StartsAt pgtype.Timestamptz; StartsAt_2 pgtype.Timestamptz}` — `StartsAt` is the inclusive lower bound, `StartsAt_2` the exclusive upper bound (sqlc names repeated columns this way; `GetUserCalendarInRangeParams` already looks like this).
  `GetCityCalendarInRangeRow{EventID pgtype.UUID; Title string; Description string; StartsAt, EndsAt pgtype.Timestamptz; ImageUrl, Url *string; VenueName string; VenueAddress *string}`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/city_calendar_test.go`:

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

// otherCity returns a second city, distinct from the default one. cities is not
// truncated between tests, so this must be idempotent across the whole run.
func otherCity(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO cities (slug, name, timezone)
		VALUES ('test-other-city', 'Other City', 'America/Los_Angeles')
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`).Scan(&id)
	require.NoError(t, err)
	return id
}

// seedCityEvent inserts a venue in cityID and one event at it, with no
// user_event_match row of any kind.
func seedCityEvent(t *testing.T, q *store.Queries, ctx context.Context, cityID pgtype.UUID, venueName, sourceEventID, title string, startsAt time.Time) pgtype.UUID {
	t.Helper()
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: cityID, Name: venueName, NormalizedName: venueName,
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: sourceEventID,
		Title:         title,
		Description:   "seeded",
		StartsAt:      pgtype.Timestamptz{Time: startsAt, Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	return eventID
}

func TestGetCityCalendarInRange_ReturnsUnmatchedEventsAndExcludesOtherCities(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	away := otherCity(t, pool)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "city-1", "Home Show", time.Now().Add(48*time.Hour))
	seedCityEvent(t, q, ctx, away, "Away Hall", "city-2", "Away Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
		CityID:     home.ID,
		StartsAt:   pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		StartsAt_2: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Home Show", rows[0].Title)
	require.Equal(t, "The Bowl", rows[0].VenueName)
}

func TestGetCityCalendarInRange_ExcludesPastAndOutOfRangeEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "past-1", "Past Show", time.Now().Add(-72*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "far-1", "Far Show", time.Now().Add(90*24*time.Hour))
	seedCityEvent(t, q, ctx, home.ID, "The Bowl", "soon-1", "Soon Show", time.Now().Add(48*time.Hour))

	rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
		CityID:     home.ID,
		StartsAt:   pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		StartsAt_2: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Soon Show", rows[0].Title)
}

func TestGetCityCalendarInRange_ExcludesArchivedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	home, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	live := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "live-1", "Live Show", time.Now().Add(48*time.Hour))
	gone := seedCityEvent(t, q, ctx, home.ID, "The Bowl", "gone-1", "Archived Show", time.Now().Add(48*time.Hour))
	_, err = pool.Exec(ctx, `UPDATE events SET archived_at = NOW() WHERE id = $1`, gone)
	require.NoError(t, err)

	rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
		CityID:     home.ID,
		StartsAt:   pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		StartsAt_2: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Live Show", rows[0].Title)
	require.Equal(t, live, rows[0].EventID)
}
```

Add `"github.com/jackc/pgx/v5/pgxpool"` to the imports for `otherCity`'s parameter type.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestGetCityCalendarInRange -count=1`
Expected: FAIL — compile error, `q.GetCityCalendarInRange undefined (type *store.Queries has no field or method GetCityCalendarInRange)`.

- [ ] **Step 3: Add the query and generate**

Append to `sql/queries/calendar.sql`:

```sql
-- name: GetCityCalendarInRange :many
-- Every showable event in the city, with no match filtering — this is what the
-- calendar falls back to for a user who has no interests to match against.
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
WHERE v.city_id = $1
  AND e.archived_at IS NULL
  AND e.starts_at >= $2
  AND e.starts_at <  $3
  -- Same showable rule as GetUserCalendarInRange: a date-only event runs until
  -- its local day is out, a timed one until it ends.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
ORDER BY e.starts_at ASC;
```

Then run: `sqlc generate`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestGetCityCalendarInRange -count=1`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add sql/queries/calendar.sql internal/store/calendar.sql.go internal/store/models.go internal/store/city_calendar_test.go
git commit -m "Add GetCityCalendarInRange query"
```

---

### Task 2: `GetCityCalendar` handler

**Files:**
- Modify: `internal/http/handlers/calendar.go`
- Modify: `internal/http/handlers/calendar_test.go` (append tests)

**Interfaces:**
- Consumes: `(*store.Queries).GetCityCalendarInRange` from Task 1.
- Produces: `handlers.GetCityCalendar(q *store.Queries) http.HandlerFunc`, serving the existing `calendarResponse` shape (`{"events": [...]}`) with `score: 0` and empty `matched_because` on every event. Also an unexported `parseDateRange(w, r) (from, to time.Time, ok bool)` used by both calendar handlers.

- [ ] **Step 1: Write the failing tests**

Append to `internal/http/handlers/calendar_test.go`. `seedCalendarFixture` (already in that file) creates a user in the default city, a venue, an event, **and** a match — these tests reuse it and then assert the city endpoint returns the event regardless of match state.

```go
// cityRouter wires the handler the way server.go does, so {cityId} resolves.
func cityRouter(q *store.Queries, signer *auth.JWTSigner) *chi.Mux {
	r := chi.NewRouter()
	r.With(middleware.RequireAuth(signer)).Get("/calendar/{cityId}", handlers.GetCityCalendar(q))
	return r
}

func TestGetCityCalendar_ReturnsUnmatchedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	// A second event with NO user_event_match row at all.
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Side Room", NormalizedName: "side room",
	})
	_, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "city-unmatched",
		Title:         "Unmatched Show",
		Description:   "nobody matched this",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	from := time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(30 * 24 * time.Hour).UTC().Format("2006-01-02")

	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?from=" + from + "&to=" + to
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []struct {
			Title          string  `json:"title"`
			Score          float64 `json:"score"`
			MatchedBecause struct {
				Performers []string `json:"performers"`
				Genres     []string `json:"genres"`
			} `json:"matched_because"`
		} `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	titles := []string{}
	for _, e := range resp.Events {
		titles = append(titles, e.Title)
	}
	// Both the matched event and the unmatched one.
	require.ElementsMatch(t, []string{"PB Live", "Unmatched Show"}, titles)
	for _, e := range resp.Events {
		require.Zero(t, e.Score, e.Title)
		require.Empty(t, e.MatchedBecause.Performers, e.Title)
		require.Empty(t, e.MatchedBecause.Genres, e.Title)
	}
}

// The endpoint is deliberately city-wide, not caller-specific: a not-interested
// event still appears. Guards against someone "helpfully" adding the filter.
func TestGetCityCalendar_IncludesNotInterestedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	require.NoError(t, q.AddNotInterested(ctx, store.AddNotInterestedParams{
		UserID: userID, EventID: eventID,
	}))

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	from := time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(30 * 24 * time.Hour).UTC().Format("2006-01-02")

	url := "/calendar/" + uuidFromPgCal(city.ID).String() + "?from=" + from + "&to=" + to
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1)
	require.Equal(t, "PB Live", resp.Events[0].Title)
}

func TestGetCityCalendar_UnknownCityReturnsEmptyList(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuid.New().String() + "?from=2026-01-01&to=2026-12-31"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"events":[]}`, rec.Body.String())
}

func TestGetCityCalendar_BadCityID_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	req := httptest.NewRequest(http.MethodGet, "/calendar/not-a-uuid?from=2026-01-01&to=2026-12-31", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad_city_id")
}

func TestGetCityCalendar_MissingDates_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)
	city, _ := q.GetDefaultCity(ctx)

	accessTok, _ := signer.SignAccess(uuidFromPgCal(userID), true)
	url := "/calendar/" + uuidFromPgCal(city.ID).String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing_range")
}

func TestGetCityCalendar_NoToken_Returns401(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/calendar/"+uuid.New().String()+"?from=2026-01-01&to=2026-12-31", nil)
	rec := httptest.NewRecorder()
	cityRouter(q, signer).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/http/handlers/ -run TestGetCityCalendar -count=1 -p 1`
Expected: FAIL — compile error, `undefined: handlers.GetCityCalendar`.

- [ ] **Step 3: Implement the handler**

In `internal/http/handlers/calendar.go`, replace the `from`/`to` parsing block inside `GetMyCalendar` with a call to a shared helper, and add the new handler:

```go
// parseDateRange reads the from/to query params (YYYY-MM-DD). On bad input it
// writes the error response and returns ok=false.
func parseDateRange(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" || toStr == "" {
		httperr.Write(w, http.StatusBadRequest, "missing_range", "from and to query params are required (YYYY-MM-DD)")
		return time.Time{}, time.Time{}, false
	}
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_from", "from must be YYYY-MM-DD")
		return time.Time{}, time.Time{}, false
	}
	to, err = time.Parse("2006-01-02", toStr)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "bad_to", "to must be YYYY-MM-DD")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// GetCityCalendar returns every showable event in the given city whose
// starts_at falls in [from, to) — no match filtering, so events the caller has
// no user_event_match row for are included. This is what the calendar page
// falls back to when the user has no interests to match against, so the
// response is deliberately identical for every caller: no not-interested
// filtering, and score/matched_because are always the empty values.
func GetCityCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cityUUID, err := uuid.Parse(chi.URLParam(r, "cityId"))
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_city_id", "cityId is not a valid uuid")
			return
		}
		from, to, ok := parseDateRange(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.GetCityCalendarInRange(ctx, store.GetCityCalendarInRangeParams{
			CityID:     pgtype.UUID{Bytes: cityUUID, Valid: true},
			StartsAt:   pgtype.Timestamptz{Time: from, Valid: true},
			StartsAt_2: pgtype.Timestamptz{Time: to, Valid: true},
		})
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "db_error", "could not load city calendar", err)
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
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

`GetMyCalendar`'s parsing block becomes:

```go
		from, to, ok := parseDateRange(w, r)
		if !ok {
			return
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/handlers/ -count=1 -p 1`
Expected: PASS — the six new `TestGetCityCalendar_*` tests plus every pre-existing handler test (the `GetMyCalendar` 400 tests cover the refactor).

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/calendar.go internal/http/handlers/calendar_test.go
git commit -m "Add GetCityCalendar handler"
```

---

### Task 3: Route `/calendar/{cityId}`

**Files:**
- Modify: `internal/http/server.go:167` (the reads block in the authed + confirmed group)
- Modify: `internal/http/server_test.go` (the `guardedRoutes` slice)

**Interfaces:**
- Consumes: `handlers.GetCityCalendar` from Task 2.
- Produces: the live route `GET /calendar/{cityId}`, authenticated and confirmation-gated, rate limited only by the group's `authedLimiter` net.

- [ ] **Step 1: Write the failing test**

In `internal/http/server_test.go`, add the route to `guardedRoutes` and update the comment above it. That slice already drives two tests — `TestServer_UnconfirmedIsGatedOffGuardedRoutes` (every route 403 for an unconfirmed user) and the tail of `TestServer_ConfirmLinkEndToEnd` (every route 200 for a freshly confirmed one) — so adding the route here covers both sides of the gate with no new test. Any UUID works: an unknown city is a 200 with an empty list, and what these tests measure is 403-vs-200.

```go
// guardedRoutes are authenticated routes behind the confirmation gate. All
// return 200 for a confirmed user, so the only thing separating 403 from 200 is
// the gate — the calendars carry their date range because the handlers 400
// without one, which would mask the contrast these tests exist to show.
var guardedRoutes = []string{
	"/me/manual-interests",
	"/me/calendar?from=2026-01-01&to=2026-01-31",
	"/calendar/00000000-0000-0000-0000-000000000000?from=2026-01-01&to=2026-01-31",
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/http/ -run 'TestServer_(UnconfirmedIsGatedOffGuardedRoutes|ConfirmLinkEndToEnd)' -count=1 -p 1`
Expected: FAIL — the new route returns 404 (chi has no such route), where the tests want 403 and 200.

- [ ] **Step 3: Add the route**

In `internal/http/server.go`, inside the authenticated + confirmed group's "Reads" block, after the `/me/calendar` line:

```go
		r.Get("/me/calendar", handlers.GetMyCalendar(s.Queries))
		// Every event in a city, unfiltered by match. The calendar page falls
		// back to this for users with no interests yet. Covered by the group's
		// authed net; no dedicated limiter.
		r.Get("/calendar/{cityId}", handlers.GetCityCalendar(s.Queries))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/ -count=1 -p 1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/http/server.go internal/http/server_test.go
git commit -m "Route GET /calendar/{cityId}"
```

---

### Task 4: `city_id` on `/me` and the signup response

**Files:**
- Modify: `internal/http/handlers/auth.go` (the `userOut` struct ~line 33; the signup response ~line 127)
- Modify: `internal/http/handlers/user.go` (`GetMe`'s `writeJSON` call)
- Modify: `internal/http/handlers/user_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `{"city_id": "<uuid>"}` on both `GET /me` and the `POST /auth/signup` user object. Task 5 consumes this as `User.city_id`.

- [ ] **Step 1: Write the failing test**

In `internal/http/handlers/user_test.go`, extend `TestGetMe_ReturnsCurrentUser`'s decode struct and assertions:

```go
	var out struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		CityID string `json:"city_id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "getme@example.com", out.Email)
	require.Equal(t, cityID, out.CityID)
```

`cityID` is already in scope from `defaultCityID(t, q)` at the top of that test, and it is a `string` (`auth_test.go:23` returns `row.ID.String()`) — so it compares directly against the JSON field.

Add a second test for signup:

```go
func TestSignup_ReturnsCityID(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "citysignup@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Sender: &emailpkg.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		User struct {
			CityID string `json:"city_id"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.User.CityID)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/http/handlers/ -run 'TestGetMe_ReturnsCurrentUser|TestSignup_ReturnsCityID' -count=1 -p 1`
Expected: FAIL — `city_id` decodes as `""`.

- [ ] **Step 3: Implement**

`internal/http/handlers/auth.go` — add the field:

```go
type userOut struct {
	ID             string   `json:"id"`
	Email          string   `json:"email"`
	CityID         string   `json:"city_id"`
	Confirmed      bool     `json:"confirmed"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}
```

and populate it in the signup response (`CreateUser` already returns `city_id`):

```go
			User: userOut{
				ID:        row.ID.String(),
				Email:     row.Email,
				CityID:    row.CityID.String(),
				Confirmed: row.Confirmed,
			},
```

`internal/http/handlers/user.go` — populate it in `GetMe`:

```go
		writeJSON(w, http.StatusOK, userOut{
			ID:             uid.String(),
			Email:          row.Email,
			CityID:         row.CityID.String(),
			Confirmed:      row.Confirmed,
			ScoreThreshold: threshold,
		})
```

`row.CityID` is a `pgtype.UUID`; its `String()` gives the canonical dashed form — the same call the signup response already makes for `row.ID`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/... -count=1 -p 1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/auth.go internal/http/handlers/user.go internal/http/handlers/user_test.go
git commit -m "Return city_id from /me and signup"
```

---

### Task 5: FE `getCityCalendar` + `User.city_id`

**Files:**
- Modify: `web/src/api/auth.ts` (the `User` interface)
- Modify: `web/src/api/calendar.ts` (append)
- Modify: `web/src/api/calendar.test.ts` (append)

**Interfaces:**
- Consumes: the `city_id` field from Task 4.
- Produces: `getCityCalendar(cityId: string, from: string, to: string): Promise<CalendarEvent[]>` exported from `web/src/api/calendar.ts`, and `User.city_id: string`. Task 6 consumes `getCityCalendar`; Task 8 consumes `user.city_id`.

- [ ] **Step 1: Write the failing test**

Append to `web/src/api/calendar.test.ts`:

```ts
import { getCityCalendar } from './calendar';

describe('getCityCalendar', () => {
  it('fetches every event for a city', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [{ id: 'c1' }] });
    const events = await getCityCalendar('city-uuid', '2026-01-01', '2026-12-31');
    expect(apiFetch).toHaveBeenCalledWith('/calendar/city-uuid?from=2026-01-01&to=2026-12-31');
    expect(events).toEqual([{ id: 'c1' }]);
  });
});
```

Merge the `import { getCityCalendar }` into the file's existing `import { getCalendar } from './calendar';` line rather than adding a second import statement.

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm test src/api/calendar.test.ts` (from `web/`)
Expected: FAIL — `getCityCalendar is not a function` / no matching export.

- [ ] **Step 3: Implement**

Append to `web/src/api/calendar.ts`:

```ts
// Every event in a city, unfiltered by match score. The calendar falls back to
// this when the user has nothing to match against yet.
export async function getCityCalendar(
  cityId: string,
  from: string,
  to: string,
): Promise<CalendarEvent[]> {
  const params = new URLSearchParams({ from, to });
  const out = await apiFetch<{ events: CalendarEvent[] }>(
    `/calendar/${cityId}?${params.toString()}`,
  );
  return out.events;
}
```

In `web/src/api/auth.ts`, add `city_id` to `User`:

```ts
export interface User {
  id: string;
  email: string;
  city_id: string;
  confirmed: boolean;
  score_threshold?: number;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pnpm test` and `pnpm build` (from `web/`)
Expected: PASS. `pnpm build` runs `tsc -b`, which fails on every test fixture that builds a `User` literal now missing `city_id`. Add `city_id: 'city-1'` to the `user: {...}` literals in these files:

- `web/src/components/Layout.test.tsx`
- `web/src/pages/ConfirmEmailPage.test.tsx`
- `web/src/pages/EventDetailPage.test.tsx`
- `web/src/pages/InterestsPage.test.tsx`
- `web/src/pages/SettingsPage.test.tsx`

`web/src/components/ConfirmModals.test.tsx` also mocks a user-shaped object — fix it too if `tsc` flags it. Let `tsc` be the authority: fix exactly what it reports.

- [ ] **Step 5: Commit**

```bash
git add web/src/api/calendar.ts web/src/api/calendar.test.ts web/src/api/auth.ts
git commit -m "Add getCityCalendar API handler and User.city_id"
```

---

### Task 6: `useCityCalendar` hook

**Files:**
- Create: `web/src/hooks/useCityCalendar.ts`
- Create: `web/src/hooks/useCityCalendar.test.tsx`

**Interfaces:**
- Consumes: `getCityCalendar` from Task 5.
- Produces: `useCityCalendar(cityId: string | undefined, from: string, to: string, enabled: boolean): UseQueryResult<CalendarEvent[]>` — query key `['city-calendar', cityId, from, to]`. Task 8 consumes it.

- [ ] **Step 1: Write the failing test**

Create `web/src/hooks/useCityCalendar.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/calendar', () => ({
  getCityCalendar: vi.fn(),
}));

import { useCityCalendar } from './useCityCalendar';
import { getCityCalendar } from '../api/calendar';

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.resetAllMocks();
});

const cityEvent = {
  id: 'c1',
  title: 'Citywide Show',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'Civic Hall' },
  score: 0,
  matched_because: { performers: [], genres: [] },
};

describe('useCityCalendar', () => {
  it('fetches the city calendar when enabled', async () => {
    vi.mocked(getCityCalendar).mockResolvedValueOnce([cityEvent]);
    const { result } = renderHook(() => useCityCalendar('city-1', '2026-01-01', '2026-04-01', true), {
      wrapper,
    });
    await waitFor(() => expect(result.current.data).toEqual([cityEvent]));
    expect(getCityCalendar).toHaveBeenCalledWith('city-1', '2026-01-01', '2026-04-01');
  });

  it('does not fetch when disabled', async () => {
    renderHook(() => useCityCalendar('city-1', '2026-01-01', '2026-04-01', false), { wrapper });
    await new Promise((r) => setTimeout(r, 20));
    expect(getCityCalendar).not.toHaveBeenCalled();
  });

  it('does not fetch when the city is unknown', async () => {
    renderHook(() => useCityCalendar(undefined, '2026-01-01', '2026-04-01', true), { wrapper });
    await new Promise((r) => setTimeout(r, 20));
    expect(getCityCalendar).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm test src/hooks/useCityCalendar.test.tsx` (from `web/`)
Expected: FAIL — cannot resolve `./useCityCalendar`.

- [ ] **Step 3: Implement**

Create `web/src/hooks/useCityCalendar.ts`:

```ts
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { getCityCalendar, type CalendarEvent } from '../api/calendar';

// Every event in a city, for the calendar's no-interests fallback. `enabled`
// keeps it idle until the caller knows the fallback applies; the query stays
// idle while the city is unknown.
export function useCityCalendar(
  cityId: string | undefined,
  from: string,
  to: string,
  enabled: boolean,
) {
  return useQuery<CalendarEvent[]>({
    queryKey: ['city-calendar', cityId, from, to],
    queryFn: () => getCityCalendar(cityId!, from, to),
    enabled: enabled && !!cityId,
    placeholderData: keepPreviousData,
  });
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pnpm test src/hooks/useCityCalendar.test.tsx` (from `web/`)
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useCityCalendar.ts web/src/hooks/useCityCalendar.test.tsx
git commit -m "Add useCityCalendar hook"
```

---

### Task 7: Hide the match score when there is no match

**Files:**
- Modify: `web/src/components/EventCard.tsx` (the `titleRow` block)
- Modify: `web/src/pages/EventDetailPage.tsx:49`
- Modify: `web/src/components/EventCard.test.tsx` (append)
- Modify: `web/src/pages/EventDetailPage.test.tsx` (append)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: no API change — both components simply stop rendering the `% match` chip when `event.score <= 0`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/components/EventCard.test.tsx` (inside the existing `describe('EventCard', ...)`):

```tsx
  it('shows the match score for a matched event', () => {
    renderCard();
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
  });

  it('hides the match score for an unmatched city event', () => {
    renderCard(undefined, { score: 0, matched_because: { performers: [], genres: [] } });
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });
```

Append to `web/src/pages/EventDetailPage.test.tsx`, inside its existing `describe('EventDetailPage', ...)`. It uses a `renderAt(path)` helper and mocks `calApi.getEvent`:

```tsx
  it('shows the match score for a matched event', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl' },
      score: 0.82,
      matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
    });
    renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
  });

  it('hides the match score for an unmatched city event', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'c1',
      title: 'Citywide Show',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'Civic Hall' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    renderAt('/events/c1');
    await waitFor(() => expect(screen.getByText('Citywide Show')).toBeInTheDocument());
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `pnpm test src/components/EventCard.test.tsx src/pages/EventDetailPage.test.tsx` (from `web/`)
Expected: FAIL — both "hides" tests find `0% match` in the document.

- [ ] **Step 3: Implement**

In `web/src/components/EventCard.tsx`, gate the chip:

```tsx
        <div className={s.titleRow}>
          <h3 className={s.title}>{event.title}</h3>
          {/* City-wide events carry no match, and "0% match" reads as a bad
              match rather than as no match at all. */}
          {event.score > 0 && (
            <span className={s.score}>{Math.round(event.score * 100)}% match</span>
          )}
        </div>
```

In `web/src/pages/EventDetailPage.tsx`, apply the same gate to line 49:

```tsx
            {data.score > 0 && (
              <div className={s.score}>{Math.round(data.score * 100)}% match</div>
            )}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pnpm test` (from `web/`)
Expected: PASS — all suites, including the pre-existing `82% match` assertions in `CalendarPage.test.tsx`.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/EventCard.tsx web/src/components/EventCard.test.tsx web/src/pages/EventDetailPage.tsx web/src/pages/EventDetailPage.test.tsx
git commit -m "Hide the match chip on unmatched events"
```

---

### Task 8: `CalendarPage` fallback orchestration

**Files:**
- Modify: `web/src/pages/CalendarPage.tsx`
- Modify: `web/src/pages/CalendarPage.css.ts` (append `banner`)
- Modify: `web/src/pages/CalendarPage.test.tsx`

**Interfaces:**
- Consumes: `useCityCalendar` (Task 6), `getCityCalendar` (Task 5), `user.city_id` (Tasks 4–5), the score gate (Task 7).
- Produces: the finished feature. Nothing consumes it.

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/CalendarPage.test.tsx`, first add mocks for the two gate modules — the file currently mocks neither, so without this the gate resolves by network failure rather than deterministically:

```tsx
vi.mock('../api/spotify', () => ({
  getSpotifyStatus: vi.fn(),
  startSpotifyConnect: vi.fn(),
}));

vi.mock('../api/manualInterests', () => ({
  listManualInterests: vi.fn(),
}));

import { getSpotifyStatus } from '../api/spotify';
import { listManualInterests } from '../api/manualInterests';
```

In the existing `beforeEach`, after `vi.resetAllMocks()`, default the gate to "user has interests" so every pre-existing test keeps exercising the matched calendar, and give the mocked user a city:

```tsx
  vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: true });
  vi.mocked(listManualInterests).mockResolvedValue([
    {
      id: 'i1',
      value: 'indie',
      normalized_value: 'indie',
      weight: 1,
      created_at: '2026-01-01T00:00:00Z',
    },
  ]);
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'u@example.com', city_id: 'city-1', confirmed: true },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
```

Then add the fallback tests. `getCityCalendar` needs to be added to the existing `vi.mock('../api/calendar', ...)` factory at the top of the file:

```tsx
vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getCityCalendar: vi.fn(),
  getEvent: vi.fn(),
}));
```

```tsx
describe('CalendarPage city fallback', () => {
  const cityEvent = {
    id: 'c1',
    title: 'Citywide Show',
    starts_at: '2026-06-15T20:00:00Z',
    venue: { name: 'Civic Hall' },
    score: 0,
    matched_because: { performers: [], genres: [] },
  };

  function noInterests() {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: false });
    vi.mocked(listManualInterests).mockResolvedValue([]);
  }

  it('shows every city event when Spotify is disconnected and there are no interests', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);
    vi.mocked(calApi.getCityCalendar).mockResolvedValue([cityEvent]);

    renderPage();

    await waitFor(() => expect(screen.getByText('Citywide Show')).toBeInTheDocument());
    expect(calApi.getCityCalendar).toHaveBeenCalledWith(
      'city-1',
      expect.any(String),
      expect.any(String),
    );
    expect(screen.getByText('Everything happening in Seattle')).toBeInTheDocument();
    expect(screen.getByText(/Showing everything in Seattle/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Not interested' })).not.toBeInTheDocument();
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });

  it('shows the matched calendar when Spotify is connected', async () => {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: true });
    vi.mocked(listManualInterests).mockResolvedValue([]);
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  it('shows the matched calendar when the user has manual interests', async () => {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: false });
    vi.mocked(listManualInterests).mockResolvedValue([
      {
        id: 'i1',
        value: 'indie',
        normalized_value: 'indie',
        weight: 1,
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  // A failed gate query must degrade to today's behavior, not to a stuck
  // spinner and not to a city-wide list.
  it('shows the matched calendar when the status query fails', async () => {
    vi.mocked(getSpotifyStatus).mockRejectedValue(new Error('boom'));
    vi.mocked(listManualInterests).mockResolvedValue([]);
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  it('tells the user when the city has no events at all', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);
    vi.mocked(calApi.getCityCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() =>
      expect(screen.getByText(/nothing on the calendar in seattle/i)).toBeInTheDocument(),
    );
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `pnpm test src/pages/CalendarPage.test.tsx` (from `web/`)
Expected: FAIL — the fallback tests never find "Citywide Show" (the page still renders the matched calendar's empty state).

- [ ] **Step 3: Implement**

`web/src/pages/CalendarPage.css.ts` — append the banner style:

```ts
export const banner = style([
  card,
  {
    padding: '1rem',
    marginTop: '1rem',
    color: color.gray600,
    ...fontSize.sm,
  },
]);
```

`web/src/pages/CalendarPage.tsx` — add the gate queries, the city query, and the fallback rendering:

```tsx
import { listManualInterests } from '../api/manualInterests';
import { useCityCalendar } from '../hooks/useCityCalendar';
```

```tsx
  const spotifyQ = useQuery({
    queryKey: ['spotify-status', user?.id],
    queryFn: getSpotifyStatus,
  });
  const interestsQ = useQuery({
    queryKey: ['manual-interests', user?.id],
    queryFn: listManualInterests,
  });

  // Pending, not `data === undefined`: a failed gate query never gets data, and
  // waiting on data would leave the page spinning forever. Optional chaining
  // then keeps a failed gate on the matched calendar rather than the city list.
  const gatePending = spotifyQ.isPending || interestsQ.isPending;
  const showCity =
    !gatePending && spotifyQ.data?.connected === false && interestsQ.data?.length === 0;

  const cityQ = useCityCalendar(user?.city_id, from, to, showCity);
```

Replace the existing `const { data: spotifyStatus } = useQuery({...})` with the `spotifyQ` above, and update the empty state's `spotifyStatus &&` reference to `spotifyQ.data &&`.

The events to render and the loading/error flags become:

```tsx
  const events = showCity ? (cityQ.data ?? []) : (data ?? []);
  const loading = gatePending || (showCity ? cityQ.isLoading : isLoading);
  const errored = showCity ? cityQ.isError : isError;
```

Title:

```tsx
        <h1 className={c.pageTitle}>
          {showCity ? 'Everything happening in Seattle' : 'Your Seattle calendar'}
        </h1>
```

Banner, rendered directly above the list/empty-state block:

```tsx
      {showCity && (
        <div className={s.banner}>
          Showing everything in Seattle.{' '}
          <a
            href="#"
            onClick={(e) => {
              e.preventDefault();
              connectSpotifyMut.mutate();
            }}
            className={s.inlineLink}
          >
            Connect your Spotify
          </a>{' '}
          or{' '}
          <a href="/interests" className={s.inlineLink}>
            add some interests
          </a>{' '}
          to get a calendar matched to your taste.
        </div>
      )}
```

The render block uses the new flags, with a distinct empty state per mode:

```tsx
      {loading ? (
        <Spinner />
      ) : errored ? (
        <div className={s.errorBox}>Couldn't load your calendar.</div>
      ) : events.length === 0 ? (
        showCity ? (
          <div className={s.emptyState}>Nothing on the calendar in Seattle right now.</div>
        ) : (
          <div className={s.emptyState}>
            No upcoming matches yet. <br />
            <br /> Try{' '}
            {spotifyQ.data && !spotifyQ.data.connected && (
              <>
                <a
                  href="#"
                  onClick={(e) => {
                    e.preventDefault();
                    connectSpotifyMut.mutate();
                  }}
                  className={s.inlineLink}
                >
                  connecting your Spotify
                </a>{' '}
                to supercharge your matches or{' '}
              </>
            )}
            <a href="/interests" className={s.inlineLink}>
              adding some interests
            </a>{' '}
            manually.
          </div>
        )
      ) : (
        <ul className={s.list}>
          {events.map((e) => (
            <li key={e.id} className={s.listItem}>
              <EventCard
                event={e}
                interactive
                onNotInterested={showCity ? undefined : (id) => notInterested.mutate(id)}
              />
            </li>
          ))}
        </ul>
      )}
```

Leave the `/me/calendar` query, the `notInterested` mutation, and the range toggle exactly as they are.

- [ ] **Step 4: Run tests to verify they pass**

Run: `pnpm test` then `pnpm lint` and `pnpm build` (from `web/`)
Expected: PASS — the five new fallback tests plus all pre-existing suites.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/CalendarPage.tsx web/src/pages/CalendarPage.css.ts web/src/pages/CalendarPage.test.tsx
git commit -m "Fall back to the city calendar when the user has no interests"
```

---

### Task 9: Full-stack verification

**Files:** none modified — this task only runs things.

**Interfaces:**
- Consumes: everything above.
- Produces: evidence the feature works end to end.

- [ ] **Step 1: Run the whole Go suite**

Run: `make test`
Expected: exit 0, no FAIL lines.

- [ ] **Step 2: Run the whole web suite plus lint and build**

Run (from `web/`): `pnpm test && pnpm lint && pnpm build`
Expected: all pass.

- [ ] **Step 3: Exercise the endpoint against the local DB**

```bash
make run   # in one shell
# in another, with a confirmed local user's access token in $TOK and city in $CITY:
curl -s -H "Authorization: Bearer $TOK" \
  "http://localhost:8080/me" | jq .city_id
curl -s -H "Authorization: Bearer $TOK" \
  "http://localhost:8080/calendar/$CITY?from=$(date +%F)&to=$(date -v+3m +%F)" \
  | jq '.events | length, .[0]'
```

Expected: `/me` returns a non-null `city_id`; the calendar call returns events with `score: 0` and empty `matched_because`, and a count at least as large as the same user's `/me/calendar`.

- [ ] **Step 4: Confirm nothing regressed for a matched user**

Run: `curl -s -H "Authorization: Bearer $TOK" "http://localhost:8080/me/calendar?from=$(date +%F)&to=$(date -v+3m +%F)" | jq '.events[0].score'`
Expected: a non-zero score — the matched path is untouched.

- [ ] **Step 5: Commit any fixes**

If Steps 1–4 surfaced problems, fix them and commit. If everything passed, there is nothing to commit — say so rather than creating an empty commit.
