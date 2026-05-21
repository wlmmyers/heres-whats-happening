# Plan 5 — Calendar API + iCal Feed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose Plan 4's `user_event_match` data through the API: `GET /me/calendar?from=...&to=...` returns a user's matched upcoming events ordered by date; `GET /events/:id` returns single-event detail with the user's match info if any; `POST/DELETE /me/ical-token` manages a signed-URL iCal subscription token; `GET /ical/:token.ics` serves an RFC 5545 calendar that iOS Calendar / Google Calendar / Fantastical can subscribe to.

**Architecture:** Two new sqlc query files feed the read path: one for `ical_tokens` (create-by-user-with-hash-storage, get-by-hash, delete, update-last-accessed) and one for the calendar join (`user_event_match` × `events` × `venues` with date-range filtering). Token generation reuses `internal/auth`'s `GenerateRefresh` + `HashRefresh` (32-byte base64url + sha256) — same primitive, different cookie/URL story. A new `internal/ical` package owns the RFC 5545 VCALENDAR formatter (pure function: events → text/calendar string). Handlers live in `internal/http/handlers/calendar.go` and `internal/http/handlers/ical.go`. Three new authenticated routes plus one PUBLIC route (the iCal feed; the URL token is the credential — calendar apps don't support custom Authorization headers on subscriptions).

**Tech Stack:** Go 1.24+ · existing `chi`, `sqlc`, `pgx/v5`, integration tests against real Postgres — no new third-party deps. Pure-stdlib RFC 5545 formatter.

---

## File Structure

```
.
├── cmd/app/main.go                                # add ICALBaseURL to Server fields
├── internal/
│   ├── config/config.go                           # add ICAL_BASE_URL env var
│   ├── ical/
│   │   ├── calendar.go                            # FormatCalendar(events) string
│   │   └── calendar_test.go
│   └── http/
│       ├── server.go                              # wire 4 new routes; the iCal one is public
│       └── handlers/
│           ├── calendar.go                        # GetMyCalendar + GetEventByID
│           ├── calendar_test.go
│           ├── ical.go                            # CreateIcalToken + DeleteIcalToken + GetIcalFeed
│           └── ical_test.go
├── sql/
│   ├── migrations/
│   │   └── 0011_ical_tokens.up.sql/.down.sql
│   └── queries/
│       ├── ical_tokens.sql                        # 4 queries
│       └── calendar.sql                           # 2 queries (calendar range + single event)
└── README.md                                      # Plan 5 quickstart
```

**Boundaries:**

- `internal/ical` is a pure VCALENDAR text formatter. No HTTP, no DB.
- `handlers/ical.go` owns token lifecycle + the public feed endpoint.
- `handlers/calendar.go` owns the authenticated JSON read endpoints.
- `internal/auth.GenerateRefresh` + `HashRefresh` are reused for iCal tokens (same crypto primitive — random 32 bytes + sha256-at-rest).

---

## Prerequisites

- Plans 1–4 merged to master. The `user_event_match` table is populated either by running `./app match` after `./app scrape events` + `./app scrape spotify`, or directly via SQL during testing.
- No new infrastructure needed for Plan 5.

---

### Task 1: Migration 0011 — `ical_tokens`

**Files:**
- Create: `sql/migrations/0011_ical_tokens.up.sql`
- Create: `sql/migrations/0011_ical_tokens.down.sql`
- Modify: `internal/testdb/testdb.go` (extend truncate list)

- [ ] **Step 1: Write `0011_ical_tokens.up.sql`**

```sql
CREATE TABLE ical_tokens (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash        BYTEA NOT NULL UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_accessed_at  TIMESTAMPTZ
);
```

One row per user — regenerating a token UPSERTs over the previous row. `token_hash` carries a UNIQUE index for lookup-by-hash on the feed endpoint.

- [ ] **Step 2: Write `0011_ical_tokens.down.sql`**

```sql
DROP TABLE IF EXISTS ical_tokens;
```

- [ ] **Step 3: Run migrations and verify**

```bash
make migrate
make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c "\d ical_tokens"
```

Expected: `11/u ical_tokens` applied to both DBs; table shows `user_id` PK, `token_hash` BYTEA NOT NULL UNIQUE, two timestamps.

- [ ] **Step 4: Update testdb truncate list**

Edit `internal/testdb/testdb.go` and add `"ical_tokens"` to the `tables` slice (before `users` since it FKs `users`):

```go
	tables := []string{
		"user_event_match",
		"event_genres",
		"event_performers",
		"events",
		"venues",
		"user_interests",
		"user_spotify_tokens",
		"ical_tokens",
		"refresh_tokens",
		"users",
	}
```

- [ ] **Step 5: Verify build + tests**

```bash
go build ./...
make test
```

Expected: full suite passes.

- [ ] **Step 6: Commit**

```bash
git add sql/migrations/0011_ical_tokens.up.sql sql/migrations/0011_ical_tokens.down.sql internal/testdb/testdb.go
git commit -m "feat: migration 0011 — ical_tokens"
```

---

### Task 2: sqlc queries — `ical_tokens`

**Files:**
- Create: `sql/queries/ical_tokens.sql`
- Regenerate: `internal/store/*` via `sqlc generate`

- [ ] **Step 1: Write `sql/queries/ical_tokens.sql`**

```sql
-- name: UpsertIcalToken :exec
INSERT INTO ical_tokens (user_id, token_hash)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET
    token_hash       = EXCLUDED.token_hash,
    created_at       = NOW(),
    last_accessed_at = NULL;

-- name: GetIcalTokenByHash :one
SELECT user_id, token_hash, created_at, last_accessed_at
FROM ical_tokens
WHERE token_hash = $1;

-- name: DeleteIcalTokenByUser :exec
DELETE FROM ical_tokens WHERE user_id = $1;

-- name: UpdateIcalTokenLastAccessed :exec
UPDATE ical_tokens SET last_accessed_at = NOW() WHERE user_id = $1;
```

- [ ] **Step 2: Generate + build + test**

```bash
sqlc generate
go build ./...
make test
```

Expected: clean. New file `internal/store/ical_tokens.sql.go` with the four methods.

- [ ] **Step 3: Commit**

```bash
git add sql/queries/ical_tokens.sql internal/store/
git commit -m "feat: sqlc queries for ical_tokens"
```

---

### Task 3: sqlc queries — calendar read path

**Files:**
- Create: `sql/queries/calendar.sql`
- Regenerate

- [ ] **Step 1: Write `sql/queries/calendar.sql`**

```sql
-- name: GetUserCalendarInRange :many
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
WHERE m.user_id = $1
  AND e.archived_at IS NULL
  AND e.starts_at >= $2
  AND e.starts_at <  $3
ORDER BY e.starts_at ASC;

-- name: GetMatchedEventForUser :one
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
FROM events e
JOIN venues v ON v.id = e.venue_id
LEFT JOIN user_event_match m ON m.event_id = e.id AND m.user_id = $2
WHERE e.id = $1
  AND e.archived_at IS NULL;
```

Note: `GetMatchedEventForUser` uses a LEFT JOIN so the caller gets event data even if the user has no match for it; `score` and `score_breakdown` will be NULL in that case.

- [ ] **Step 2: Generate + build + test**

```bash
sqlc generate
go build ./...
make test
```

- [ ] **Step 3: Commit**

```bash
git add sql/queries/calendar.sql internal/store/
git commit -m "feat: sqlc queries for calendar read path"
```

---

### Task 4: Config — `ICAL_BASE_URL`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `.env.example`

- [ ] **Step 1: Append to `.env.example`**

```
# iCal feed: the base URL that gets returned to the user when they create a
# token. In dev: http://localhost:8080. In production: https://api.example.com.
ICAL_BASE_URL=http://localhost:8080
```

- [ ] **Step 2: Append failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoad_IcalBaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("ICAL_BASE_URL", "http://localhost:8080")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080", cfg.IcalBaseURL)
}
```

- [ ] **Step 3: Run test (FAIL expected)**

```bash
go test ./internal/config -v -run IcalBaseURL
```

- [ ] **Step 4: Extend Config struct + Load()**

In `internal/config/config.go`, add to the `Config` struct (after the Plan 4 fields):

```go
	// Plan 5 additions
	IcalBaseURL string
```

In `Load()`, add to the `&Config{...}` literal:

```go
		IcalBaseURL: os.Getenv("ICAL_BASE_URL"),
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/config -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go .env.example
git commit -m "feat(config): ICAL_BASE_URL env var"
```

---

### Task 5: VCALENDAR formatter (`internal/ical`)

**Files:**
- Create: `internal/ical/calendar.go`
- Create: `internal/ical/calendar_test.go`

- [ ] **Step 1: Write failing test**

`internal/ical/calendar_test.go`:

```go
package ical

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatCalendar_OneEvent(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	events := []Event{
		{
			UID:         "event-aaa@example.com",
			Title:       "Phoebe Bridgers Live",
			StartsAt:    time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
			EndsAt:      time.Date(2026, 6, 15, 22, 0, 0, 0, time.UTC),
			VenueName:   "The Bowl",
			VenueAddr:   "100 Main St",
			URL:         "https://example.com/event/aaa",
			Description: "Matched because: Phoebe Bridgers, indie rock",
		},
	}
	got := FormatCalendar("Your Matched Events", now, events)

	require.True(t, strings.HasPrefix(got, "BEGIN:VCALENDAR"))
	require.True(t, strings.HasSuffix(strings.TrimRight(got, "\r\n"), "END:VCALENDAR"))
	require.Contains(t, got, "VERSION:2.0")
	require.Contains(t, got, "METHOD:PUBLISH")
	require.Contains(t, got, "X-PUBLISHED-TTL:PT1H")
	require.Contains(t, got, "NAME:Your Matched Events")
	require.Contains(t, got, "X-WR-CALNAME:Your Matched Events")
	require.Contains(t, got, "BEGIN:VEVENT")
	require.Contains(t, got, "END:VEVENT")
	require.Contains(t, got, "UID:event-aaa@example.com")
	require.Contains(t, got, "DTSTAMP:20260520T120000Z")
	require.Contains(t, got, "DTSTART:20260615T200000Z")
	require.Contains(t, got, "DTEND:20260615T220000Z")
	require.Contains(t, got, "SUMMARY:Phoebe Bridgers Live")
	require.Contains(t, got, "LOCATION:The Bowl\\, 100 Main St")
	require.Contains(t, got, "URL:https://example.com/event/aaa")
	require.Contains(t, got, "DESCRIPTION:Matched because: Phoebe Bridgers\\, indie rock")
}

func TestFormatCalendar_NoEndsAt_OmitsDTEND(t *testing.T) {
	events := []Event{
		{
			UID:      "x@example.com",
			Title:    "Open-ended",
			StartsAt: time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
		},
	}
	got := FormatCalendar("c", time.Now(), events)
	require.Contains(t, got, "DTSTART:20260615T200000Z")
	require.NotContains(t, got, "DTEND")
}

func TestFormatCalendar_Empty(t *testing.T) {
	got := FormatCalendar("c", time.Now(), nil)
	require.True(t, strings.HasPrefix(got, "BEGIN:VCALENDAR"))
	require.NotContains(t, got, "BEGIN:VEVENT")
}

func TestEscape_HandlesSpecialChars(t *testing.T) {
	// Comma, semicolon, backslash, newline must be escaped per RFC 5545.
	require.Equal(t, `a\\,b`, escape("a,b"))
	require.Equal(t, `a\\;b`, escape("a;b"))
	require.Equal(t, `a\\\\b`, escape(`a\b`))
	require.Equal(t, `a\\nb`, escape("a\nb"))
}

func TestUseCRLF(t *testing.T) {
	got := FormatCalendar("c", time.Now(), nil)
	require.Contains(t, got, "\r\n", "iCal lines must be CRLF-separated per RFC 5545")
}
```

- [ ] **Step 2: Run test (FAIL expected)**

```bash
go test ./internal/ical -v
```

Expected: FAIL — `package ical; no Go files`.

- [ ] **Step 3: Implement**

`internal/ical/calendar.go`:

```go
// Package ical formats event lists as RFC 5545 VCALENDAR text.
// No I/O — pure string manipulation.
package ical

import (
	"strings"
	"time"
)

// Event is the minimal shape needed to emit a VEVENT block.
type Event struct {
	UID         string    // Stable across feed refreshes; format: "event-<id>@example.com"
	Title       string
	StartsAt    time.Time
	EndsAt      time.Time // zero value → DTEND omitted
	VenueName   string
	VenueAddr   string
	URL         string
	Description string
}

// FormatCalendar returns an RFC 5545 VCALENDAR document.
// `now` is used for DTSTAMP. `calName` is the calendar display name.
func FormatCalendar(calName string, now time.Time, events []Event) string {
	var b strings.Builder
	writeLine := func(s string) {
		b.WriteString(s)
		b.WriteString("\r\n")
	}
	writeLine("BEGIN:VCALENDAR")
	writeLine("VERSION:2.0")
	writeLine("PRODID:-//Here's What's Happening//Calendar//EN")
	writeLine("METHOD:PUBLISH")
	writeLine("X-PUBLISHED-TTL:PT1H")
	writeLine("NAME:" + escape(calName))
	writeLine("X-WR-CALNAME:" + escape(calName))

	stamp := now.UTC().Format("20060102T150405Z")

	for _, e := range events {
		writeLine("BEGIN:VEVENT")
		writeLine("UID:" + e.UID)
		writeLine("DTSTAMP:" + stamp)
		writeLine("DTSTART:" + e.StartsAt.UTC().Format("20060102T150405Z"))
		if !e.EndsAt.IsZero() {
			writeLine("DTEND:" + e.EndsAt.UTC().Format("20060102T150405Z"))
		}
		if e.Title != "" {
			writeLine("SUMMARY:" + escape(e.Title))
		}
		loc := buildLocation(e.VenueName, e.VenueAddr)
		if loc != "" {
			writeLine("LOCATION:" + escape(loc))
		}
		if e.URL != "" {
			writeLine("URL:" + e.URL) // URLs are not escaped per RFC 5545 §3.3.13
		}
		if e.Description != "" {
			writeLine("DESCRIPTION:" + escape(e.Description))
		}
		writeLine("END:VEVENT")
	}
	writeLine("END:VCALENDAR")
	return b.String()
}

func buildLocation(name, addr string) string {
	switch {
	case name == "" && addr == "":
		return ""
	case name == "":
		return addr
	case addr == "":
		return name
	default:
		return name + ", " + addr
	}
}

// escape applies RFC 5545 text-value escaping. Order matters: backslash first.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\\\`)
	s = strings.ReplaceAll(s, ",", `\\,`)
	s = strings.ReplaceAll(s, ";", `\\;`)
	s = strings.ReplaceAll(s, "\n", `\\n`)
	return s
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ical -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ical/calendar.go internal/ical/calendar_test.go
git commit -m "feat(ical): RFC 5545 VCALENDAR formatter"
```

---

### Task 6: `GET /me/calendar` handler

**Files:**
- Create: `internal/http/handlers/calendar.go`
- Create: `internal/http/handlers/calendar_test.go`

- [ ] **Step 1: Write failing test**

`internal/http/handlers/calendar_test.go`:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func seedCalendarFixture(t *testing.T, q *store.Queries, ctx context.Context) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	city, _ := q.GetDefaultCity(ctx)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "calendar@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, err)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "The Bowl", NormalizedName: "the bowl",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "cal-1",
		Title:         "PB Live",
		Description:   "Indie rock",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userRow.ID,
		EventID:        eventID,
		Score:          0.82,
		ScoreBreakdown: []byte(`{"matched_performers":["Phoebe Bridgers"],"matched_genres":["indie"]}`),
	}))
	return userRow.ID, eventID
}

func TestGetMyCalendar_ReturnsMatchedEvents(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPg(userID))

	from := time.Now().Add(-time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from="+from+"&to="+to, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []struct {
			ID       string  `json:"id"`
			Title    string  `json:"title"`
			Score    float64 `json:"score"`
			Venue    struct {
				Name string `json:"name"`
			} `json:"venue"`
			MatchedBecause struct {
				Performers []string `json:"performers"`
				Genres     []string `json:"genres"`
			} `json:"matched_because"`
		} `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Events, 1)
	require.Equal(t, "PB Live", resp.Events[0].Title)
	require.InDelta(t, 0.82, resp.Events[0].Score, 0.01)
	require.Equal(t, "The Bowl", resp.Events[0].Venue.Name)
	require.Equal(t, []string{"Phoebe Bridgers"}, resp.Events[0].MatchedBecause.Performers)
	require.Equal(t, []string{"indie"}, resp.Events[0].MatchedBecause.Genres)
}

func TestGetMyCalendar_DateRangeFiltering(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPg(userID))

	// Use a date range that excludes the seeded event (which is 48h in the future).
	from := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	to := time.Now().Add(14 * 24 * time.Hour).UTC().Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from="+from+"&to="+to, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(t, resp.Events)
}

func TestGetMyCalendar_MissingDates_Returns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPg(userID))

	req := httptest.NewRequest(http.MethodGet, "/me/calendar", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMyCalendar(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

Add a helper at the bottom of the file (or in a shared file in the package) to convert pgtype.UUID → google/uuid for the JWT signer:

```go
import (
	"github.com/google/uuid"
)

func uuidFromPg(u pgtype.UUID) uuid.UUID { return uuid.UUID(u.Bytes) }
```

If this helper is already defined elsewhere in the `handlers_test` package, reuse it instead.

- [ ] **Step 2: Run test (FAIL expected)**

```bash
go test ./internal/http/handlers -v -run GetMyCalendar
```

Expected: FAIL — `undefined: handlers.GetMyCalendar`.

- [ ] **Step 3: Implement**

`internal/http/handlers/calendar.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type calendarEvent struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Description    string            `json:"description,omitempty"`
	StartsAt       string            `json:"starts_at"`
	EndsAt         string            `json:"ends_at,omitempty"`
	ImageURL       string            `json:"image_url,omitempty"`
	URL            string            `json:"url,omitempty"`
	Venue          calendarVenue     `json:"venue"`
	Score          float64           `json:"score"`
	MatchedBecause calendarMatch     `json:"matched_because"`
}

type calendarVenue struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

type calendarMatch struct {
	Performers []string `json:"performers"`
	Genres     []string `json:"genres"`
}

type calendarResponse struct {
	Events []calendarEvent `json:"events"`
}

// GetMyCalendar returns the authenticated user's matched events whose
// starts_at falls in [from, to). Date params are inclusive-of-from,
// exclusive-of-to, formatted as YYYY-MM-DD.
func GetMyCalendar(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		fromStr := r.URL.Query().Get("from")
		toStr := r.URL.Query().Get("to")
		if fromStr == "" || toStr == "" {
			httperr.Write(w, http.StatusBadRequest, "missing_range", "from and to query params are required (YYYY-MM-DD)")
			return
		}
		from, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_from", "from must be YYYY-MM-DD")
			return
		}
		to, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_to", "to must be YYYY-MM-DD")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := q.GetUserCalendarInRange(ctx, store.GetUserCalendarInRangeParams{
			UserID:    pgtype.UUID{Bytes: uid, Valid: true},
			StartsAt:  pgtype.Timestamptz{Time: from, Valid: true},
			StartsAt_2: pgtype.Timestamptz{Time: to, Valid: true},
		})
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not load calendar")
			return
		}

		out := calendarResponse{Events: make([]calendarEvent, 0, len(rows))}
		for _, row := range rows {
			var bd calendarMatch
			if len(row.ScoreBreakdown) > 0 {
				var raw struct {
					Performers []string `json:"matched_performers"`
					Genres     []string `json:"matched_genres"`
				}
				_ = json.Unmarshal(row.ScoreBreakdown, &raw)
				bd.Performers = raw.Performers
				bd.Genres = raw.Genres
			}
			if bd.Performers == nil {
				bd.Performers = []string{}
			}
			if bd.Genres == nil {
				bd.Genres = []string{}
			}
			ev := calendarEvent{
				ID:       uuidString(row.EventID),
				Title:    row.Title,
				Score:    row.Score,
				StartsAt: row.StartsAt.Time.UTC().Format(time.RFC3339),
				Venue: calendarVenue{
					Name:    row.VenueName,
					Address: textPtrToString(row.VenueAddress),
				},
				MatchedBecause: bd,
			}
			ev.Description = row.Description
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

// textPtrToString unwraps a *string (sqlc's nullable text) to a plain string,
// returning "" for nil.
func textPtrToString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// uuidString stringifies a pgtype.UUID — duplicated from internal/scraper/spotify
// for the same reason (avoids forcing a google/uuid dep on every package).
func uuidString(u pgtype.UUID) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i := 0; i < 16; i++ {
		b := u.Bytes[i]
		out[j] = hex[b>>4]
		out[j+1] = hex[b&0x0F]
		j += 2
		switch i {
		case 3, 5, 7, 9:
			out[j] = '-'
			j++
		}
	}
	return string(out)
}
```

Note: the parameter struct name from sqlc may differ. If sqlc generates `GetUserCalendarInRangeParams{UserID, StartsAt, StartsAt_2}` (the second timestamp param), use the actual field name from the generated code. The `$2` and `$3` params have the same column name in the SQL, so sqlc disambiguates with a suffix — typically `StartsAt_2` or similar. Confirm via the generated struct.

- [ ] **Step 4: Run tests**

```bash
make test
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/calendar.go internal/http/handlers/calendar_test.go
git commit -m "feat(http): GET /me/calendar with date-range filtering"
```

---

### Task 7: `GET /events/:id` handler

**Files:**
- Modify: `internal/http/handlers/calendar.go` — append `GetEventByIDForUser`
- Modify: `internal/http/handlers/calendar_test.go` — append test

- [ ] **Step 1: Append failing test**

Add to `internal/http/handlers/calendar_test.go`:

```go
func TestGetEventByID_MatchedEvent(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	userID, eventID := seedCalendarFixture(t, q, ctx)

	accessTok, _ := signer.SignAccess(uuidFromPg(userID))

	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	url := "/events/" + uuidFromPg(eventID).String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID    string  `json:"id"`
		Title string  `json:"title"`
		Score float64 `json:"score"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "PB Live", resp.Title)
	require.InDelta(t, 0.82, resp.Score, 0.01)
}

func TestGetEventByID_UnmatchedEvent_ScoreIsZero(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "lone@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "Q", NormalizedName: "q",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "unmatched-1",
		Title:         "Unmatched",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})

	accessTok, _ := signer.SignAccess(uuidFromPg(userRow.ID))
	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	req := httptest.NewRequest(http.MethodGet, "/events/"+uuidFromPg(eventID).String(), nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Title string  `json:"title"`
		Score float64 `json:"score"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "Unmatched", resp.Title)
	require.Equal(t, 0.0, resp.Score)
}

func TestGetEventByID_NotFound(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "nf@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	accessTok, _ := signer.SignAccess(uuidFromPg(userRow.ID))

	r := chi.NewRouter()
	mw := middleware.RequireAuth(signer)
	r.With(mw).Get("/events/{id}", handlers.GetEventByIDForUser(q))

	// Random non-existent UUID
	req := httptest.NewRequest(http.MethodGet, "/events/00000000-0000-0000-0000-000000000000", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
```

Make sure imports include `"github.com/go-chi/chi/v5"`.

- [ ] **Step 2: Run test (FAIL expected)**

```bash
go test ./internal/http/handlers -v -run GetEventByID
```

Expected: FAIL — `undefined: handlers.GetEventByIDForUser`.

- [ ] **Step 3: Implement — append to `internal/http/handlers/calendar.go`**

```go
import (
	// add to existing imports:
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetEventByIDForUser returns one event with the user's match info (or
// score=0 + empty matched_because if the user doesn't have a match for it).
func GetEventByIDForUser(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		idStr := chi.URLParam(r, "id")
		eventUUID, err := uuid.Parse(idStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_id", "id is not a valid uuid")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetMatchedEventForUser(ctx, store.GetMatchedEventForUserParams{
			ID:     pgtype.UUID{Bytes: eventUUID, Valid: true},
			UserID: pgtype.UUID{Bytes: uid, Valid: true},
		})
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "not_found", "event not found")
			return
		}

		var bd calendarMatch
		if len(row.ScoreBreakdown) > 0 {
			var raw struct {
				Performers []string `json:"matched_performers"`
				Genres     []string `json:"matched_genres"`
			}
			_ = json.Unmarshal(row.ScoreBreakdown, &raw)
			bd.Performers = raw.Performers
			bd.Genres = raw.Genres
		}
		if bd.Performers == nil {
			bd.Performers = []string{}
		}
		if bd.Genres == nil {
			bd.Genres = []string{}
		}
		var score float64
		if row.Score != nil {
			score = *row.Score
		}

		ev := calendarEvent{
			ID:          uuidString(row.EventID),
			Title:       row.Title,
			Description: row.Description,
			StartsAt:    row.StartsAt.Time.UTC().Format(time.RFC3339),
			Score:       score,
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
		writeJSON(w, http.StatusOK, ev)
	}
}
```

If the LEFT JOIN makes `row.Score` non-nullable (sqlc generated `float64` instead of `*float64`), drop the nil check. Conversely, if `row.ScoreBreakdown` is a different type, adapt accordingly.

- [ ] **Step 4: Run tests**

```bash
make test
```

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/calendar.go internal/http/handlers/calendar_test.go
git commit -m "feat(http): GET /events/:id with optional user match info"
```

---

### Task 8: `POST /me/ical-token` handler

**Files:**
- Create: `internal/http/handlers/ical.go`
- Create: `internal/http/handlers/ical_test.go`

- [ ] **Step 1: Write failing test**

`internal/http/handlers/ical_test.go`:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestCreateIcalToken_ReturnsURL(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "ical@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	accessTok, _ := signer.SignAccess(uuidFromPg(userRow.ID))

	mw := middleware.RequireAuth(signer)
	h := mw(handlers.CreateIcalToken(q, "http://localhost:8080"))

	req := httptest.NewRequest(http.MethodPost, "/me/ical-token", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, strings.HasPrefix(resp.URL, "http://localhost:8080/ical/"))
	require.True(t, strings.HasSuffix(resp.URL, ".ics"))

	// One row in ical_tokens for this user.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM ical_tokens WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestCreateIcalToken_RotatesOnRepeat(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "ical-rotate@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	accessTok, _ := signer.SignAccess(uuidFromPg(userRow.ID))

	mw := middleware.RequireAuth(signer)
	h := mw(handlers.CreateIcalToken(q, "http://localhost:8080"))

	send := func() string {
		req := httptest.NewRequest(http.MethodPost, "/me/ical-token", nil)
		req.Header.Set("Authorization", "Bearer "+accessTok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var resp struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		return resp.URL
	}
	first := send()
	second := send()
	require.NotEqual(t, first, second, "token must rotate on repeat POST")

	// Still only one row (UPSERT semantics).
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM ical_tokens WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 1, n)

	// And it's the row holding the hash of `second`, not `first`.
	parts := strings.Split(strings.TrimSuffix(second, ".ics"), "/ical/")
	require.Len(t, parts, 2)
	secondToken := parts[1]

	// Look up by hash to confirm the new token is the active one.
	row, err := q.GetIcalTokenByHash(ctx, auth.HashRefresh(secondToken))
	require.NoError(t, err)
	require.Equal(t, userRow.ID, row.UserID)

	// suppress unused-import warning for pgtype
	var _ pgtype.UUID
}
```

- [ ] **Step 2: Run test (FAIL expected)**

```bash
go test ./internal/http/handlers -v -run CreateIcalToken
```

- [ ] **Step 3: Implement**

`internal/http/handlers/ical.go`:

```go
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type icalTokenResponse struct {
	URL string `json:"url"`
}

// CreateIcalToken generates a fresh 32-byte token, stores its sha256 hash in
// ical_tokens (UPSERT — rotates if a row already exists), and returns the
// subscription URL exactly once.
func CreateIcalToken(q *store.Queries, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		raw, err := auth.GenerateRefresh()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "token_failed", "could not generate token")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
			UserID:    pgtype.UUID{Bytes: uid, Valid: true},
			TokenHash: auth.HashRefresh(raw),
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist token")
			return
		}
		writeJSON(w, http.StatusCreated, icalTokenResponse{
			URL: baseURL + "/ical/" + raw + ".ics",
		})
	}
}
```

- [ ] **Step 4: Run tests**

```bash
make test
```

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/ical.go internal/http/handlers/ical_test.go
git commit -m "feat(http): POST /me/ical-token (generate + rotate)"
```

---

### Task 9: `DELETE /me/ical-token` handler

**Files:**
- Modify: `internal/http/handlers/ical.go` — append `DeleteIcalToken`
- Modify: `internal/http/handlers/ical_test.go` — append test

- [ ] **Step 1: Append failing test**

```go
func TestDeleteIcalToken_RemovesRow(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx := context.Background()
	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "ical-del@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID:    userRow.ID,
		TokenHash: []byte("hash-bytes"),
	}))

	accessTok, _ := signer.SignAccess(uuidFromPg(userRow.ID))
	mw := middleware.RequireAuth(signer)
	h := mw(handlers.DeleteIcalToken(q))

	req := httptest.NewRequest(http.MethodDelete, "/me/ical-token", nil)
	req.Header.Set("Authorization", "Bearer "+accessTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM ical_tokens WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 0, n)
}
```

- [ ] **Step 2: Run test (FAIL expected)**

```bash
go test ./internal/http/handlers -v -run DeleteIcalToken
```

- [ ] **Step 3: Implement — append to `internal/http/handlers/ical.go`**

```go
// DeleteIcalToken removes the user's iCal subscription token. The previously
// issued URL stops working immediately.
func DeleteIcalToken(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.DeleteIcalTokenByUser(ctx, pgtype.UUID{Bytes: uid, Valid: true}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete token")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
make test
```

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/ical.go internal/http/handlers/ical_test.go
git commit -m "feat(http): DELETE /me/ical-token"
```

---

### Task 10: `GET /ical/:token.ics` (public feed)

**Files:**
- Modify: `internal/http/handlers/ical.go` — append `GetIcalFeed`
- Modify: `internal/http/handlers/ical_test.go` — append tests

- [ ] **Step 1: Append failing tests**

```go
func TestGetIcalFeed_ReturnsRFC5545(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()
	userID, _ := seedCalendarFixture(t, q, ctx)

	// Provision a token for this user
	rawToken := "test-token-not-random-but-fine-for-test"
	require.NoError(t, q.UpsertIcalToken(ctx, store.UpsertIcalTokenParams{
		UserID:    userID,
		TokenHash: auth.HashRefresh(rawToken),
	}))

	r := chi.NewRouter()
	r.Get("/ical/{token}.ics", handlers.GetIcalFeed(q))

	req := httptest.NewRequest(http.MethodGet, "/ical/"+rawToken+".ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/calendar; charset=utf-8", rec.Result().Header.Get("Content-Type"))
	require.Contains(t, rec.Result().Header.Get("Cache-Control"), "max-age=3600")
	require.Equal(t, "PT1H", rec.Result().Header.Get("X-Published-Ttl"))

	body := rec.Body.String()
	require.Contains(t, body, "BEGIN:VCALENDAR")
	require.Contains(t, body, "BEGIN:VEVENT")
	require.Contains(t, body, "SUMMARY:PB Live")
	require.Contains(t, body, "LOCATION:The Bowl\\, 100 Main St")
	require.Contains(t, body, "END:VCALENDAR")
}

func TestGetIcalFeed_UnknownToken_404(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)

	r := chi.NewRouter()
	r.Get("/ical/{token}.ics", handlers.GetIcalFeed(q))

	req := httptest.NewRequest(http.MethodGet, "/ical/nope-not-a-real-token.ics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
```

- [ ] **Step 2: Run test (FAIL expected)**

```bash
go test ./internal/http/handlers -v -run GetIcalFeed
```

- [ ] **Step 3: Implement — append to `internal/http/handlers/ical.go`**

```go
import (
	// add to existing imports:
	"fmt"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/wmyers/heres-whats-happening/internal/ical"
)

// GetIcalFeed serves an RFC 5545 calendar for the user identified by the
// token in the URL path. No Authorization header — calendar apps don't
// support custom headers on subscriptions, so the token IS the credential.
// Lookback window: next 60 days of matched events.
func GetIcalFeed(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		token = strings.TrimSuffix(token, ".ics")
		if token == "" {
			http.NotFound(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetIcalTokenByHash(ctx, auth.HashRefresh(token))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Mark last-accessed; ignore errors (best-effort telemetry).
		_ = q.UpdateIcalTokenLastAccessed(ctx, row.UserID)

		now := time.Now().UTC()
		from := pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true}
		to := pgtype.Timestamptz{Time: now.AddDate(0, 0, 60), Valid: true}

		rows, err := q.GetUserCalendarInRange(ctx, store.GetUserCalendarInRangeParams{
			UserID:     row.UserID,
			StartsAt:   from,
			StartsAt_2: to,
		})
		if err != nil {
			http.Error(w, "could not load events", http.StatusInternalServerError)
			return
		}

		evs := make([]ical.Event, 0, len(rows))
		for _, e := range rows {
			ev := ical.Event{
				UID:       fmt.Sprintf("event-%s@example.com", uuidString(e.EventID)),
				Title:     e.Title,
				StartsAt:  e.StartsAt.Time,
				VenueName: e.VenueName,
				VenueAddr: textPtrToString(e.VenueAddress),
				URL:       textPtrToString(e.Url),
			}
			if e.EndsAt.Valid {
				ev.EndsAt = e.EndsAt.Time
			}
			// Build a "Matched because:" description from the breakdown.
			ev.Description = buildIcalDescription(e.ScoreBreakdown, e.Description)
			evs = append(evs, ev)
		}
		body := ical.FormatCalendar("Your Matched Events", now, evs)

		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("X-Published-Ttl", "PT1H")
		_, _ = w.Write([]byte(body))
	}
}

func buildIcalDescription(breakdown []byte, eventDescription string) string {
	var because string
	if len(breakdown) > 0 {
		var raw struct {
			Performers []string `json:"matched_performers"`
			Genres     []string `json:"matched_genres"`
		}
		_ = json.Unmarshal(breakdown, &raw)
		bits := []string{}
		bits = append(bits, raw.Performers...)
		bits = append(bits, raw.Genres...)
		if len(bits) > 0 {
			because = "Matched because: " + strings.Join(bits, ", ")
		}
	}
	switch {
	case because == "" && eventDescription == "":
		return ""
	case because == "":
		return eventDescription
	case eventDescription == "":
		return because
	default:
		return because + "\n\n" + eventDescription
	}
}
```

- [ ] **Step 4: Run tests**

```bash
make test
```

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/ical.go internal/http/handlers/ical_test.go
git commit -m "feat(http): GET /ical/:token.ics public RFC 5545 feed"
```

---

### Task 11: Wire routes in `server.go`

**Files:**
- Modify: `internal/http/server.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Add `IcalBaseURL` field to `Server` struct**

In `internal/http/server.go`, add to the `Server` struct:

```go
	// Plan 5 addition
	IcalBaseURL string
```

- [ ] **Step 2: Register routes**

In `Router()`:

- Inside the authenticated `r.Group(...)` block (after the existing `/me/interests/...` registrations), append:

```go
		r.Get("/me/calendar", handlers.GetMyCalendar(s.Queries))
		r.Get("/events/{id}", handlers.GetEventByIDForUser(s.Queries))
		r.Post("/me/ical-token", handlers.CreateIcalToken(s.Queries, s.IcalBaseURL))
		r.Delete("/me/ical-token", handlers.DeleteIcalToken(s.Queries))
```

- OUTSIDE the authenticated group (alongside `/healthz` and `/readyz`, before the `r.Post("/auth/...")` block), append:

```go
	// Public iCal feed — token in URL is the credential.
	r.Get("/ical/{token}", handlers.GetIcalFeed(s.Queries))
```

Note: the route pattern is `/ical/{token}` not `/ical/{token}.ics` — chi treats the dot-extension as part of the token param. The handler strips the `.ics` suffix.

- [ ] **Step 3: Update `cmd/app/main.go`**

In `serve()`, add to the `Server{...}` literal:

```go
		IcalBaseURL: cfg.IcalBaseURL,
```

- [ ] **Step 4: Verify build + tests**

```bash
go build ./...
make test
```

Expected: full suite passes.

- [ ] **Step 5: Smoke test**

```bash
make db-up
set -a; source .env.example; set +a
./app serve &
SERVE_PID=$!
sleep 1
# Test the public iCal endpoint
curl -i http://localhost:8080/ical/nope.ics  # expect 404
kill $SERVE_PID
```

Expected: `HTTP/1.1 404 Not Found`.

- [ ] **Step 6: Commit**

```bash
git add internal/http/server.go cmd/app/main.go
git commit -m "feat: wire calendar + iCal routes in HTTP server"
```

---

### Task 12: README — Plan 5 quickstart

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append Plan 5 section at the end of README**

````markdown

## Plan 5 quickstart — calendar API + iCal feed

```bash
# Make sure ICAL_BASE_URL is set in .env (default is http://localhost:8080)
make db-up && make run
```

### Read your matched calendar

```bash
ACCESS=...  # JWT from /auth/login
curl -s -H "Authorization: Bearer $ACCESS" \
  "http://localhost:8080/me/calendar?from=2026-05-20&to=2026-08-01" \
  | python3 -m json.tool | head -40
```

### Subscribe via your calendar app

```bash
# Generate a token — the URL is returned exactly once.
ACCESS=...
curl -s -X POST -H "Authorization: Bearer $ACCESS" \
  http://localhost:8080/me/ical-token
# → {"url":"http://localhost:8080/ical/<token>.ics"}
```

Paste that URL into iOS Calendar → Add Account → Other → Add Subscribed
Calendar, or Google Calendar → Other Calendars → From URL. Your calendar app
will pull the feed roughly hourly (the `X-PUBLISHED-TTL: PT1H` hint).

### Revoke

```bash
curl -s -X DELETE -H "Authorization: Bearer $ACCESS" \
  http://localhost:8080/me/ical-token  # → 204
```

The old URL stops working immediately. Generate a new one via POST.
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: Plan 5 calendar + iCal quickstart"
```

---

## Self-Review

**Spec coverage check (Plan 5 scope only):**

| Spec requirement | Implemented in |
|---|---|
| `ical_tokens(user_id PK, token_hash BYTEA, created_at, last_accessed_at)` | Task 1 |
| Hash token at rest (sha256), raw token returned once | Tasks 2, 8 (reuses `auth.HashRefresh`) |
| `POST /me/ical-token` generates + rotates | Task 8 |
| `DELETE /me/ical-token` revokes immediately | Task 9 |
| `GET /ical/:token.ics` (no auth header, token in URL) | Task 10 |
| Content-Type: `text/calendar; charset=utf-8` | Task 10 |
| Cache-Control + X-Published-Ttl headers | Task 10 |
| `UID:event-<id>@example.com` (stable across feed refreshes) | Task 10 |
| `GET /me/calendar?from=&to=` returns matched events in date range | Task 6 |
| `GET /events/:id` returns event detail (with optional match info) | Task 7 |
| Joins `user_event_match` × `events` × `venues` | Task 3 |
| RFC 5545 escaping of commas/semicolons/backslashes/newlines | Task 5 |

**Deferred to later plans:**

- Frontend that consumes these endpoints (Plan 6).
- Line folding at 75 octets per RFC 5545 §3.1 — most calendar apps tolerate long lines; v1 skips this and adds it if a real client complains.

**Placeholder scan:** no "TBD"/"handle errors"/"add validation" steps; every code-touching step has complete code.

**Type consistency:**

- `calendarEvent` / `calendarVenue` / `calendarMatch` defined in Task 6 and reused (Task 7).
- `ical.Event` defined in Task 5; consumed in Task 10.
- `uuidString(pgtype.UUID) string` defined in Task 6's calendar.go; reused by Task 10.
- `auth.GenerateRefresh()` / `auth.HashRefresh()` from Plan 1 reused for iCal tokens in Tasks 8, 9, 10 (same crypto primitive).
- `GetUserCalendarInRangeParams` (Task 3) has fields `UserID`, `StartsAt`, `StartsAt_2` (the `$2`/`$3` disambiguation suffix sqlc adds when two parameters target the same column name). Confirm the actual generated field name and adjust call sites in Tasks 6 and 10 accordingly.
- `GetMatchedEventForUser` (Task 3) returns nullable score / score_breakdown via the LEFT JOIN. sqlc generates `*float64` for the score; if it generates `pgtype.Float8` or `float64` directly, adapt the nil check in Task 7.

**Plan-internal consistency notes:**

- Plan 1's `uuidFromPg(pgtype.UUID) uuid.UUID` is referenced as a test helper. Plan 1's tests may have it already, named `pgtypeUUIDToString` or similar (it's in `internal/ingest/interests_test.go` per Plan 3 Task 12). For Plan 5, redefine it locally in calendar_test.go or ical_test.go if it isn't already shared.
- The iCal feed handler is the only public route added in this plan. It must NOT be registered inside the authenticated group — Task 11 step 2 is explicit about that placement.
- The 60-day lookback in `GetIcalFeed` is a deliberate v1 choice — most calendar clients honor `X-PUBLISHED-TTL` and refresh hourly, so longer windows aren't necessary. Tuning is a future plan.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-20-plan-05-calendar-ical.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
