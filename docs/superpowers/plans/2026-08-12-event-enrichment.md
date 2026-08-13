# Event Enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich ingested events with a band image, a recent tour setlist, and a generated bio by routing `hwh-events-queue` through the mastra-handler Lambda onto a new `hwh-events-enriched-queue` that the ECS consumer reads instead.

**Architecture:** Enrichment is artist-scoped, not event-scoped — one band playing five dates is one bio, one photo, one setlist. The Lambda gains an SQS trigger, resolves one MusicBrainz artist per event, fans out to three independent workflows, and republishes the event enriched. An S3 decision-and-result cache gates repeat work, because the daily scrape republishes ~200 events unconditionally.

**Tech Stack:** Go 1.x (chi, pgx/v5, sqlc, golang-migrate), TypeScript (Mastra, Zod, Vitest, AWS SDK v3), PostgreSQL, Terraform, SQS, S3.

**Spec:** `docs/superpowers/specs/2026-08-12-event-enrichment-design.md`

## Global Constraints

- **Migration number is `0024`.** Latest existing is `0023_poster_jobs_svg_and_bounds`.
- **`sqlc generate` also rewrites `internal/store/models.go`.** Commit it alongside the `*.sql.go` files, not just the query file.
- **Go tests run `go test -p 1 ./... -count=1`** (`make test`). `-p 1` is required — tests share one local `appdb_test`.
- **All worktrees share one local `appdb_test`.** Running `make migrate-test` here will strand other branches' tests at "no migration found for version N" until they migrate too.
- **`TestTruncateAllCoversEveryTable`** in `internal/testdb/truncate_test.go` fails loudly if a new table is missing from `truncateTables`. Trust it; do not hand-verify.
- **Wire JSON is snake_case throughout**, including nested credit fields. The Lambda's internal `ImageCredit` is camelCase and must be mapped explicitly at the boundary.
- **`web/` type checking uses `tsc -b`**, never `tsc --noEmit`. Not needed for this plan — no frontend changes.
- **Lambda tests:** `pnpm test` (unit, no external services). `LIVE_API_TESTS=1 pnpm vitest run src/mastra/tools/live-apis.test.ts` for opt-in live API checks.
- **The pre-commit hook covers both workspaces.** gofmt, go vet and go test at the repo root; then eslint / tsc / prettier / vitest for `web/` via `in_web()` and again for `lambda/mastra-handler/` via `in_lambda()` (`.githooks/pre-commit:98-111`). A green hook is real evidence for Lambda work. Bypass only with explicit reason.
- **The Lambda is prettier-formatted** (`pnpm run format:check` gates commits). Its style is **single quotes**. Run `pnpm run format` from `lambda/mastra-handler/` before committing rather than hand-matching the style.
- **No frontend changes.** Every new API field is `omitempty`.

## File Structure

**Go — persistence and API**

| File | Responsibility |
| --- | --- |
| `sql/migrations/0024_artist_enrichment.{up,down}.sql` | `artists` + 3 enrichment tables + `events.headline_artist_id` |
| `sql/queries/artists.sql` | Upserts for all four tables + the batched read |
| `sql/queries/events.sql` | `UpsertEvent` gains a COALESCE'd `headline_artist_id` |
| `sql/queries/calendar.sql` | Three page queries gain `e.headline_artist_id` |
| `internal/events/enriched.go` | `EnrichedMessage` + `Enrichment` wire types |
| `internal/ingest/enrichment.go` | Applying an `Enrichment` to the DB |
| `internal/ingest/events.go` | Decode `EnrichedMessage`; order artist-before-event |
| `internal/http/handlers/artist.go` | Assembling the `artist` API object from batch rows |
| `internal/http/handlers/calendar.go` | Wiring `artist` into the three responses |

**TypeScript — the Lambda**

| File | Responsibility |
| --- | --- |
| `src/artist-key.ts` | `artistKey()`, mirroring Go's `NormalizeString` |
| `src/enrichment-schema.ts` | Zod mirrors of the Go wire types + credit mapper |
| `src/enrichment-cache.ts` | `EnrichmentCache` interface, S3 impl, stub impl |
| `src/mastra/tools/setlistfm.tool.ts` | setlist.fm client + setlist selection |
| `src/mastra/agents/bio-author.agent.ts` | Bio generation agent |
| `src/mastra/agents/tour-blurb.agent.ts` | Tour blurb agent |
| `src/mastra/workflows/enrich-bio.ts` | Wikidata → Wikipedia → MB → LLM |
| `src/mastra/workflows/enrich-tour.ts` | setlist.fm → LLM |
| `src/mastra/workflows/enrich-image.ts` | Commons candidates → vision judge loop |
| `src/enrichment.ts` | `pickArtist()` + fan-out orchestration |
| `src/handler.ts` | `isSQSEvent` branch |

**Shared test fixtures**

| File | Responsibility |
| --- | --- |
| `testdata/artist-key-contract/cases.json` | Read by a Go test AND a vitest test |
| `testdata/enriched-message-contract/full.json` | Decoded by the Go enriched contract test AND parsed by the Zod schema. A SIBLING of `event-message-contract/`, whose test decodes into a plain `Message` and would reject it |
| `lambda/mastra-handler/src/__fixtures__/setlistfm-artist-setlists.json` | Recorded real response |

---

# Phase 1 — Persistence and API (no behavior change)

Everything in this phase ships safely on its own: the columns exist, the
consumer tolerates enrichment it never receives, and the API returns fields
that are always absent.

### Task 1: Schema and queries

**Files:**
- Create: `sql/migrations/0024_artist_enrichment.up.sql`
- Create: `sql/migrations/0024_artist_enrichment.down.sql`
- Create: `sql/queries/artists.sql`
- Modify: `sql/queries/events.sql:1-20` (UpsertEvent)
- Modify: `internal/testdb/testdb.go:161-176` (truncateTables)
- Generated: `internal/store/artists.sql.go`, `internal/store/models.go`, `internal/store/events.sql.go`

**Interfaces:**
- Produces: `store.UpsertArtistParams`, `store.UpsertArtistImageParams`, `store.UpsertArtistBioParams`, `store.UpsertArtistTourSnapshotParams`, `store.GetArtistEnrichmentBatchRow`, and `store.UpsertEventParams.HeadlineArtistID pgtype.UUID`.

- [ ] **Step 1: Write the up migration**

```sql
-- sql/migrations/0024_artist_enrichment.up.sql
-- Artist-scoped enrichment. One band playing five dates is one bio, one photo,
-- one setlist — so all of this hangs off the artist, not the event.
--
-- name_key joins the key space event_performers.normalized_name and
-- artist_genre_cache.name_key already use: events.NormalizeString(performer).
-- No link table is needed.
CREATE TABLE artists (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name_key       TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL,
    mbid           TEXT,
    disambiguation TEXT,
    artist_type    TEXT,
    country        TEXT,
    begin_year     TEXT,
    -- 'ok'/'not_found' rather than the enrichment tables' three-state status:
    -- this column describes MusicBrainz resolution and deliberately mirrors
    -- artist_genre_cache.status, which answers the same question.
    status         TEXT NOT NULL CHECK (status IN ('ok','not_found')),
    resolved_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The three-state status is what makes "succeed, fail, or exhaust max attempts"
-- persistable. 'none' means we looked and there is genuinely nothing (retry
-- rarely); 'error' means the attempt broke (retry soon). TTLs live in the
-- Lambda and mirror internal/scraper/spotify/genres.go:17-19.
CREATE TABLE artist_images (
    artist_id   UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    url         TEXT,
    width       INT,
    height      INT,
    file        TEXT,
    source      TEXT,
    credit      JSONB,
    reason      TEXT,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE artist_bios (
    artist_id     UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status        TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    bio_md        TEXT,
    sources       JSONB NOT NULL DEFAULT '[]'::jsonb,
    model         TEXT,
    reason        TEXT,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Setlist and blurb share a table: one workflow produces both and they have
-- identical lifetimes. A NULL blurb with status='ok' means the setlist landed
-- but the blurb call did not.
CREATE TABLE artist_tour_snapshots (
    artist_id      UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status         TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    tour_name      TEXT,
    songs          JSONB NOT NULL DEFAULT '[]'::jsonb,
    observed_date  DATE,
    observed_venue TEXT,
    observed_city  TEXT,
    setlist_url    TEXT,
    blurb          TEXT,
    blurb_model    TEXT,
    reason         TEXT,
    fetched_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Which performer the Lambda actually enriched. event_performers has no
-- ordering column and cannot answer this itself.
ALTER TABLE events ADD COLUMN headline_artist_id UUID REFERENCES artists(id) ON DELETE SET NULL;

-- Postgres does not index a foreign key automatically, and the ON DELETE
-- SET NULL would otherwise seq-scan events.
CREATE INDEX events_headline_artist_id_idx ON events (headline_artist_id);
```

- [ ] **Step 2: Write the down migration**

```sql
-- sql/migrations/0024_artist_enrichment.down.sql
DROP INDEX IF EXISTS events_headline_artist_id_idx;
ALTER TABLE events DROP COLUMN IF EXISTS headline_artist_id;
DROP TABLE IF EXISTS artist_tour_snapshots;
DROP TABLE IF EXISTS artist_bios;
DROP TABLE IF EXISTS artist_images;
DROP TABLE IF EXISTS artists;
```

- [ ] **Step 3: Apply the migration to the test database**

Run: `make migrate-test`
Expected: exits 0. Verify with `psql "$TEST_DATABASE_URL" -c '\d artists'` showing the table.

- [ ] **Step 4: Write the queries**

```sql
-- sql/queries/artists.sql

-- name: UpsertArtist :one
INSERT INTO artists (
    name_key, display_name, mbid, disambiguation, artist_type,
    country, begin_year, status, resolved_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
ON CONFLICT (name_key) DO UPDATE SET
    display_name   = EXCLUDED.display_name,
    mbid           = EXCLUDED.mbid,
    disambiguation = EXCLUDED.disambiguation,
    artist_type    = EXCLUDED.artist_type,
    country        = EXCLUDED.country,
    begin_year     = EXCLUDED.begin_year,
    status         = EXCLUDED.status,
    resolved_at    = NOW(),
    updated_at     = NOW()
RETURNING id;

-- name: UpsertArtistImage :exec
INSERT INTO artist_images (
    artist_id, status, url, width, height, file, source, credit, reason, checked_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (artist_id) DO UPDATE SET
    status     = EXCLUDED.status,
    url        = EXCLUDED.url,
    width      = EXCLUDED.width,
    height     = EXCLUDED.height,
    file       = EXCLUDED.file,
    source     = EXCLUDED.source,
    credit     = EXCLUDED.credit,
    reason     = EXCLUDED.reason,
    checked_at = NOW();

-- name: UpsertArtistBio :exec
INSERT INTO artist_bios (
    artist_id, status, bio_md, sources, model, reason, generated_at
)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (artist_id) DO UPDATE SET
    status       = EXCLUDED.status,
    bio_md       = EXCLUDED.bio_md,
    sources      = EXCLUDED.sources,
    model        = EXCLUDED.model,
    reason       = EXCLUDED.reason,
    generated_at = NOW();

-- name: UpsertArtistTourSnapshot :exec
INSERT INTO artist_tour_snapshots (
    artist_id, status, tour_name, songs, observed_date, observed_venue,
    observed_city, setlist_url, blurb, blurb_model, reason, fetched_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
ON CONFLICT (artist_id) DO UPDATE SET
    status         = EXCLUDED.status,
    tour_name      = EXCLUDED.tour_name,
    songs          = EXCLUDED.songs,
    observed_date  = EXCLUDED.observed_date,
    observed_venue = EXCLUDED.observed_venue,
    observed_city  = EXCLUDED.observed_city,
    setlist_url    = EXCLUDED.setlist_url,
    blurb          = EXCLUDED.blurb,
    blurb_model    = EXCLUDED.blurb_model,
    reason         = EXCLUDED.reason,
    fetched_at     = NOW();

-- name: GetArtistEnrichmentBatch :many
-- One row per artist id, with whatever enrichment exists. Left joins throughout
-- so a resolved artist with no successful enrichment still comes back. Mirrors
-- ListEventPerformersBatch's shape: the calendar handlers fetch a page, then
-- batch-load the page's artists in one round trip.
SELECT
    a.id           AS artist_id,
    a.display_name,
    a.mbid,
    a.disambiguation,
    a.status       AS artist_status,
    i.status       AS image_status,
    i.url          AS image_url,
    i.width        AS image_width,
    i.height       AS image_height,
    i.credit       AS image_credit,
    b.status       AS bio_status,
    b.bio_md,
    b.sources      AS bio_sources,
    t.status       AS tour_status,
    t.tour_name,
    t.songs,
    t.observed_date,
    t.observed_venue,
    t.observed_city,
    t.setlist_url,
    t.blurb
FROM artists a
LEFT JOIN artist_images         i ON i.artist_id = a.id
LEFT JOIN artist_bios           b ON b.artist_id = a.id
LEFT JOIN artist_tour_snapshots t ON t.artist_id = a.id
WHERE a.id = ANY($1::uuid[]);
```

- [ ] **Step 5: Add headline_artist_id to UpsertEvent**

In `sql/queries/events.sql`, replace the `UpsertEvent` block with:

```sql
-- name: UpsertEvent :one
INSERT INTO events (
    source_id, source_event_id, title, description, starts_at, ends_at,
    venue_id, image_url, url, time_tbd, headline_artist_id, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
ON CONFLICT (source_id, source_event_id)
DO UPDATE SET
    title         = EXCLUDED.title,
    description   = EXCLUDED.description,
    starts_at     = EXCLUDED.starts_at,
    ends_at       = EXCLUDED.ends_at,
    venue_id      = EXCLUDED.venue_id,
    image_url     = EXCLUDED.image_url,
    url           = EXCLUDED.url,
    time_tbd      = EXCLUDED.time_tbd,
    -- COALESCE, not EXCLUDED: a re-scrape whose enrichment happened to fail
    -- sends a NULL here, and assigning it would blank a good link.
    headline_artist_id = COALESCE(EXCLUDED.headline_artist_id, events.headline_artist_id),
    last_seen_at  = NOW(),
    archived_at   = NULL,
    updated_at    = NOW()
RETURNING id;
```

- [ ] **Step 6: Add headline_artist_id to the three calendar page queries**

In `sql/queries/calendar.sql`, add `e.headline_artist_id,` immediately after the
`e.url,` line in **each** of `GetUserCalendarPage`, `GetCityCalendarPage`, and
`GetMatchedEventForUser`. Leave `GetUserCalendarInRange` alone — it is the
legacy range query and gains nothing here.

- [ ] **Step 7: Add the new tables to truncateTables**

In `internal/testdb/testdb.go`, the list is ordered children-before-parents.
`events` references `artists`, and the three enrichment tables reference
`artists`, so `artists` must come after all four:

```go
var truncateTables = []string{
	"poster_jobs",
	"user_event_not_interested",
	"user_event_match",
	"event_genres",
	"event_performers",
	"artist_images",
	"artist_bios",
	"artist_tour_snapshots",
	"events",
	"artists",
	"venues",
	"user_interests",
	"user_spotify_tokens",
	"ical_tokens",
	"email_confirmations",
	"refresh_tokens",
	"users",
	"artist_genre_cache",
}
```

- [ ] **Step 8: Regenerate sqlc**

Run: `sqlc generate`
Expected: creates `internal/store/artists.sql.go` and rewrites
`internal/store/models.go` and `internal/store/events.sql.go`.

- [ ] **Step 9: Run the full Go suite**

Run: `make test`
Expected: PASS. `TestTruncateAllCoversEveryTable` is the one that proves Step 7
was complete — if it fails naming a table, add it and re-run.

- [ ] **Step 10: Commit**

```bash
git add sql/migrations/0024_artist_enrichment.up.sql \
        sql/migrations/0024_artist_enrichment.down.sql \
        sql/queries/artists.sql sql/queries/events.sql sql/queries/calendar.sql \
        internal/testdb/testdb.go internal/store/
git commit -m "feat(db): artist-scoped enrichment tables

artists keyed on events.NormalizeString(performer), joining the key space
event_performers.normalized_name already uses. Three 1:1 enrichment tables
each carry their own three-state status and clock so one workflow failing
never blocks another.

events.headline_artist_id records which performer was enriched; its upsert
COALESCEs so a failed re-enrichment cannot blank a good link."
```

---

### Task 2: Wire types

**Files:**
- Create: `internal/events/enriched.go`
- Create: `internal/events/enriched_test.go`
- Create: `testdata/enriched-message-contract/full.json` (a SIBLING directory — see Step 5)

**Interfaces:**
- Consumes: `events.Message` (existing, `internal/events/message.go:10`).
- Produces: `events.EnrichedMessage`, `events.Enrichment`, `events.ArtistInfo`, `events.ImageInfo`, `events.ImageCredit`, `events.BioInfo`, `events.BioSource`, `events.TourInfo`, `events.SetlistSong`. Task 4 consumes all of these; Phase 2 mirrors them in Zod.

- [ ] **Step 1: Write the failing test**

```go
// internal/events/enriched_test.go
package events_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

// A plain Message must still decode into EnrichedMessage — that is what makes
// the cutover safe and old DLQ messages replayable.
func TestEnrichedMessage_DecodesPlainMessage(t *testing.T) {
	raw := []byte(`{
		"source_id": "ticketmaster",
		"source_event_id": "tm-aaa",
		"title": "Phoebe Bridgers",
		"starts_at": "2026-06-15T20:00:00Z",
		"venue": {"name": "The Bowl"}
	}`)

	var m events.EnrichedMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&m))

	require.Equal(t, "Phoebe Bridgers", m.Title)
	require.Nil(t, m.Enrichment)
}

func TestEnrichedMessage_RoundTripsEnrichment(t *testing.T) {
	raw := []byte(`{
		"source_id": "ticketmaster",
		"source_event_id": "tm-bbb",
		"title": "La Luz",
		"starts_at": "2026-09-02T20:00:00Z",
		"venue": {"name": "The Chapel"},
		"enrichment": {
			"attempted_at": "2026-08-12T04:11:22Z",
			"artist": {
				"performer": "La Luz",
				"display_name": "La Luz",
				"mbid": "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a",
				"disambiguation": "US rock band",
				"status": "ok"
			},
			"image": {
				"status": "ok",
				"url": "https://upload.wikimedia.org/x.jpg",
				"width": 640,
				"height": 427,
				"file": "La_Luz.jpg",
				"source": "p18",
				"credit": {"license_short_name": "CC BY-SA 4.0", "attribution_required": true}
			},
			"bio": {
				"status": "ok",
				"bio_md": "La Luz formed in Seattle in 2012.",
				"sources": [{"kind": "wikipedia", "title": "La Luz (band)", "revision_id": 12345}]
			},
			"tour": {
				"status": "ok",
				"tour_name": "News of the Universe Tour",
				"songs": [{"name": "Sure As Spring"}, {"name": "Strange World", "encore": 1}],
				"observed_date": "2026-07-14"
			}
		}
	}`)

	var m events.EnrichedMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&m))

	require.NotNil(t, m.Enrichment)
	require.Equal(t, "ok", m.Enrichment.Artist.Status)
	require.Equal(t, "La Luz", m.Enrichment.Artist.Performer)
	require.Equal(t, 640, m.Enrichment.Image.Width)
	require.True(t, m.Enrichment.Image.Credit.AttributionRequired)
	require.Len(t, m.Enrichment.Bio.Sources, 1)
	require.EqualValues(t, 12345, m.Enrichment.Bio.Sources[0].RevisionID)
	require.Len(t, m.Enrichment.Tour.Songs, 2)
	require.Equal(t, 1, m.Enrichment.Tour.Songs[1].Encore)
	require.Equal(t, "2026-07-14", m.Enrichment.Tour.ObservedDate)

	// Re-marshalling must not resurrect absent fields as empty strings.
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(out), `"blurb"`)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/events/ -run TestEnrichedMessage -v`
Expected: FAIL — `undefined: events.EnrichedMessage`.

- [ ] **Step 3: Write the types**

```go
// internal/events/enriched.go
package events

import "time"

// EnrichedMessage is the canonical record on the events-enriched-queue: a
// strict superset of Message, so a plain Message still decodes during cutover
// and stale DLQ messages stay replayable.
type EnrichedMessage struct {
	Message
	Enrichment *Enrichment `json:"enrichment,omitempty"`
}

// Enrichment carries whatever the Lambda produced for this event's headline
// performer. Every section is independent: one workflow failing leaves the
// others populated.
type Enrichment struct {
	// Artist is present whenever the resolution prelude RAN, including when it
	// found nothing (Status "not_found", empty MBID) — that is how an
	// unresolvable performer still gets an artists row recording the attempt.
	// Nil only when the prelude itself failed or never ran, in which case the
	// other three sections are nil too.
	Artist      *ArtistInfo `json:"artist,omitempty"`
	Image       *ImageInfo  `json:"image,omitempty"`
	Bio         *BioInfo    `json:"bio,omitempty"`
	Tour        *TourInfo   `json:"tour,omitempty"`
	AttemptedAt time.Time   `json:"attempted_at"`
}

// ArtistInfo carries the RAW performer string, never a normalized key: the
// consumer applies NormalizeString itself, keeping the one normalization that
// reaches the database in a single language.
type ArtistInfo struct {
	Performer      string `json:"performer"`
	DisplayName    string `json:"display_name"`
	MBID           string `json:"mbid,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
	Type           string `json:"type,omitempty"`
	Country        string `json:"country,omitempty"`
	BeginYear      string `json:"begin_year,omitempty"`
	Status         string `json:"status"` // ok | not_found
}

type ImageInfo struct {
	Status string       `json:"status"` // ok | none | error
	URL    string       `json:"url,omitempty"`
	Width  int          `json:"width,omitempty"`
	Height int          `json:"height,omitempty"`
	File   string       `json:"file,omitempty"`
	Source string       `json:"source,omitempty"` // p18 | category
	Credit *ImageCredit `json:"credit,omitempty"`
	Reason string       `json:"reason,omitempty"`
}

// ImageCredit is snake_case on the wire even though the Lambda's internal
// representation is camelCase — the Lambda maps explicitly at the boundary.
// Commons files are predominantly CC-BY/CC-BY-SA, so this travels with every
// image whether or not anything renders it yet.
type ImageCredit struct {
	File                string `json:"file,omitempty"`
	DescriptionURL      string `json:"description_url,omitempty"`
	Artist              string `json:"artist,omitempty"`
	Credit              string `json:"credit,omitempty"`
	License             string `json:"license,omitempty"`
	LicenseShortName    string `json:"license_short_name,omitempty"`
	LicenseURL          string `json:"license_url,omitempty"`
	UsageTerms          string `json:"usage_terms,omitempty"`
	AttributionRequired bool   `json:"attribution_required"`
}

type BioInfo struct {
	Status  string      `json:"status"` // ok | none | error
	BioMD   string      `json:"bio_md,omitempty"`
	Sources []BioSource `json:"sources,omitempty"`
	Model   string      `json:"model,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

type BioSource struct {
	Kind       string `json:"kind"` // wikipedia | musicbrainz
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	RevisionID int64  `json:"revision_id,omitempty"`
	MBID       string `json:"mbid,omitempty"`
}

type TourInfo struct {
	Status   string        `json:"status"` // ok | none | error
	TourName string        `json:"tour_name,omitempty"`
	Songs    []SetlistSong `json:"songs,omitempty"`
	// ObservedDate is YYYY-MM-DD, a plain calendar date with no zone: the
	// column is DATE and setlist.fm reports dd-MM-yyyy with no time at all.
	ObservedDate  string `json:"observed_date,omitempty"`
	ObservedVenue string `json:"observed_venue,omitempty"`
	ObservedCity  string `json:"observed_city,omitempty"`
	SetlistURL    string `json:"setlist_url,omitempty"`
	Blurb         string `json:"blurb,omitempty"`
	BlurbModel    string `json:"blurb_model,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type SetlistSong struct {
	Name    string `json:"name"`
	Encore  int    `json:"encore,omitempty"`
	CoverOf string `json:"cover_of,omitempty"`
	Tape    bool   `json:"tape,omitempty"`
	Info    string `json:"info,omitempty"`
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/events/ -run TestEnrichedMessage -v`
Expected: PASS (both cases).

- [ ] **Step 5: Add the enriched contract fixture**

`contract_test.go` decodes every `.json` in that directory into a plain
`events.Message` with `DisallowUnknownFields`, so an enriched fixture placed
there would fail. Put it in a **sibling** directory instead:

```bash
mkdir -p testdata/enriched-message-contract
```

Write `testdata/enriched-message-contract/full.json` containing exactly the
JSON body from the `TestEnrichedMessage_RoundTripsEnrichment` test above.

Then add to `internal/events/enriched_test.go`:

```go
func TestEnrichedContractFixtures_Unmarshal(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "enriched-message-contract")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	ran := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)

			var m events.EnrichedMessage
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields() // catches a TS field the Go struct lacks
			require.NoError(t, dec.Decode(&m))

			require.NotEmpty(t, m.SourceID)
			require.NotEmpty(t, m.Title)
			require.NotNil(t, m.Enrichment)
			require.NotNil(t, m.Enrichment.Artist)
		})
	}
	require.Positive(t, ran, "no .json fixtures found in %s", dir)
}
```

Add `"os"` and `"path/filepath"` to the test file's imports.

- [ ] **Step 6: Run the events suite**

Run: `go test ./internal/events/ -v`
Expected: PASS, including the existing `TestContractFixtures_UnmarshalIntoMessage`.

- [ ] **Step 7: Commit**

```bash
git add internal/events/enriched.go internal/events/enriched_test.go \
        testdata/enriched-message-contract/
git commit -m "feat(events): EnrichedMessage wire contract

A strict superset of Message, so a plain message still decodes during
cutover and old DLQ messages stay replayable. ArtistInfo carries the raw
performer string rather than a normalized key — the consumer applies
NormalizeString itself, keeping that normalization in one language."
```

---

### Task 3: artistKey cross-language contract fixture

The Lambda needs a normalization that matches Go's exactly. This task creates
the shared fixture and pins the Go side; Task 6 pins the TypeScript side
against the same file.

**Files:**
- Create: `testdata/artist-key-contract/cases.json`
- Create: `internal/events/artistkey_contract_test.go`

**Interfaces:**
- Consumes: `events.NormalizeString` (existing, `internal/events/genres.go:64`).
- Produces: `testdata/artist-key-contract/cases.json`, consumed by Task 6's vitest test.

- [ ] **Step 1: Write the fixture**

```json
[
  { "in": "AC/DC",         "out": "ac/dc",       "why": "punctuation is PRESERVED — the key difference from hash.ts normalize()" },
  { "in": "Sunn O)))",     "out": "sunn o)))",   "why": "parentheses survive; the S3 key is hashed so this never reaches a path" },
  { "in": "Sigur Rós",     "out": "sigur ros",   "why": "combining acute is stripped" },
  { "in": "Björk",         "out": "bjork",       "why": "combining diaeresis is stripped" },
  { "in": "MØ",            "out": "mø",          "why": "U+00F8 is a distinct letter, NOT a combining mark — it must survive" },
  { "in": "  The Beatles ", "out": "the beatles", "why": "trimmed, not collapsed internally" },
  { "in": "Godspeed You! Black Emperor", "out": "godspeed you! black emperor", "why": "exclamation and spacing preserved" }
]
```

- [ ] **Step 2: Write the failing test**

```go
// internal/events/artistkey_contract_test.go
package events_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

// The Lambda's artistKey() must produce byte-identical output to
// NormalizeString, because the Lambda keys its S3 skip cache on one and the
// database keys artists.name_key on the other. This fixture is asserted by
// BOTH this test and lambda/mastra-handler/src/artist-key.test.ts; a change to
// either implementation that is not mirrored fails on one side.
//
// Note this is deliberately NOT hash.ts's normalize(), which additionally
// strips punctuation and must not change (it feeds source_event_id).
func TestArtistKeyContract_MatchesNormalizeString(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "artist-key-contract", "cases.json"))
	require.NoError(t, err)

	var cases []struct {
		In  string `json:"in"`
		Out string `json:"out"`
		Why string `json:"why"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases)

	for _, c := range cases {
		t.Run(c.In, func(t *testing.T) {
			require.Equal(t, c.Out, events.NormalizeString(c.In), c.Why)
		})
	}
}
```

- [ ] **Step 3: Run it**

Run: `go test ./internal/events/ -run TestArtistKeyContract -v`
Expected: PASS. If any case fails, the *fixture* is wrong — `NormalizeString`
is the incumbent and defines the contract. Fix the expectation, not the code.

- [ ] **Step 4: Commit**

```bash
git add testdata/artist-key-contract/cases.json internal/events/artistkey_contract_test.go
git commit -m "test(events): pin the artist-key normalization contract

Shared fixture asserted by this Go test and, from Task 6, by the Lambda's
artist-key.test.ts. The Lambda keys its skip cache on the same normalization
the database keys artists.name_key on, so a silent divergence would merge
two artists in one place and split them in the other."
```

---

### Task 4: Consumer persists enrichment

**Files:**
- Create: `internal/ingest/enrichment.go`
- Create: `internal/ingest/enrichment_test.go`
- Modify: `internal/ingest/events.go:29-74` (Handle + handleMessage)

**Interfaces:**
- Consumes: `events.EnrichedMessage` (Task 2), `store.UpsertArtistParams` and siblings (Task 1).
- Produces: no new exported surface — `ingest.NewEventHandler(q, cityID)` keeps its existing signature.

- [ ] **Step 1: Write the failing tests**

```go
// internal/ingest/enrichment_test.go
package ingest_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func enrichedSample() events.EnrichedMessage {
	m := sampleMessage()
	m.SourceEventID = "tm-enriched"
	m.ImageURL = "" // no source image, so the artist image is the fallback
	return events.EnrichedMessage{
		Message: m,
		Enrichment: &events.Enrichment{
			Artist: &events.ArtistInfo{
				Performer:      "Phoebe Bridgers",
				DisplayName:    "Phoebe Bridgers",
				MBID:           "cc0b7089-c08d-4c10-b6b0-873582c17fd6",
				Disambiguation: "US singer-songwriter",
				Type:           "Person",
				Country:        "US",
				BeginYear:      "1994",
				Status:         "ok",
			},
			Image: &events.ImageInfo{
				Status: "ok",
				URL:    "https://upload.wikimedia.org/pb.jpg",
				Width:  640,
				Height: 427,
				File:   "PB.jpg",
				Source: "p18",
				Credit: &events.ImageCredit{LicenseShortName: "CC BY-SA 4.0", AttributionRequired: true},
			},
			Bio: &events.BioInfo{
				Status:  "ok",
				BioMD:   "Phoebe Bridgers is an American singer-songwriter.",
				Sources: []events.BioSource{{Kind: "wikipedia", Title: "Phoebe Bridgers", RevisionID: 999}},
				Model:   "anthropic/claude-sonnet-4-5",
			},
			Tour: &events.TourInfo{
				Status:        "ok",
				TourName:      "Reunion Tour",
				Songs:         []events.SetlistSong{{Name: "Motion Sickness"}, {Name: "Smoke Signals", Encore: 1}},
				ObservedDate:  "2026-07-14",
				ObservedVenue: "The Greek",
				ObservedCity:  "Berkeley",
				SetlistURL:    "https://www.setlist.fm/setlist/x.html",
				Blurb:         "Currently out on the Reunion Tour.",
				BlurbModel:    "anthropic/claude-sonnet-4-5",
			},
		},
	}
}

func TestHandle_PersistsEnrichment(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	body, _ := json.Marshal(enrichedSample())
	require.NoError(t, h.Handle(ctx, body))

	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-enriched",
	})
	require.NoError(t, err)

	rows, err := q.GetArtistEnrichmentBatch(ctx, []pgtype.UUID{ev.HeadlineArtistID})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	r := rows[0]
	require.Equal(t, "Phoebe Bridgers", r.DisplayName)
	require.Equal(t, "ok", r.ArtistStatus)
	require.Equal(t, "https://upload.wikimedia.org/pb.jpg", *r.ImageUrl)
	require.Contains(t, *r.BioMd, "singer-songwriter")
	require.Equal(t, "Reunion Tour", *r.TourName)
	require.Equal(t, "Currently out on the Reunion Tour.", *r.Blurb)
}

// A plain, un-enriched message must behave exactly as it does today. This is
// the cutover safety net and what keeps old DLQ messages replayable.
func TestHandle_PlainMessageStillWorks(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, h.Handle(ctx, body))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-aaa",
	})
	require.NoError(t, err)
	require.False(t, ev.HeadlineArtistID.Valid, "no enrichment means no artist link")
}

// The COALESCE guard: a later message whose enrichment failed must not blank
// a link an earlier successful enrichment established.
func TestHandle_FailedReenrichment_DoesNotBlankArtistLink(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	body, _ := json.Marshal(enrichedSample())
	require.NoError(t, h.Handle(ctx, body))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	before, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-enriched",
	})
	require.NoError(t, err)
	require.True(t, before.HeadlineArtistID.Valid)

	// Re-scrape whose prelude failed: same event, no enrichment at all.
	bare := enrichedSample()
	bare.Enrichment = nil
	bareBody, _ := json.Marshal(bare)
	require.NoError(t, h.Handle(ctx, bareBody))

	after, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-enriched",
	})
	require.NoError(t, err)
	require.Equal(t, before.HeadlineArtistID, after.HeadlineArtistID)
}

// An unresolvable performer still gets an artists row, so the attempt is
// recorded rather than retried forever.
func TestHandle_NotFoundArtist_StillCreatesRow(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	h := ingest.NewEventHandler(q, defaultCityID(t, q))
	ctx := context.Background()

	m := enrichedSample()
	m.SourceEventID = "tm-unknown"
	m.Enrichment = &events.Enrichment{
		Artist: &events.ArtistInfo{
			Performer:   "Some Local Opener",
			DisplayName: "Some Local Opener",
			Status:      "not_found",
		},
	}
	body, _ := json.Marshal(m)
	require.NoError(t, h.Handle(ctx, body))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: src.ID, SourceEventID: "tm-unknown",
	})
	require.NoError(t, err)
	require.True(t, ev.HeadlineArtistID.Valid)

	rows, err := q.GetArtistEnrichmentBatch(ctx, []pgtype.UUID{ev.HeadlineArtistID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "not_found", rows[0].ArtistStatus)
	require.Nil(t, rows[0].Mbid)
	require.Nil(t, rows[0].ImageStatus, "no image section means no image row")
}
```

Add `"github.com/jackc/pgx/v5/pgtype"` to the imports.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/ingest/ -run 'TestHandle_(PersistsEnrichment|PlainMessage|FailedReenrichment|NotFoundArtist)' -v`
Expected: FAIL — `ev.HeadlineArtistID undefined` or `q.GetArtistEnrichmentBatch undefined` if Task 1 was skipped; otherwise the enrichment assertions fail because nothing writes those rows.

- [ ] **Step 3: Write the enrichment applier**

```go
// internal/ingest/enrichment.go
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// validStatus guards the CHECK constraints on the enrichment tables. A section
// with an unrecognized status is skipped with a log rather than returned as an
// error: a contract violation in one section must not stop the event itself
// from landing, and returning an error here would retry the whole message
// three times before DLQing an event that is otherwise perfectly good.
func validStatus(s string) bool {
	return s == "ok" || s == "none" || s == "error"
}

// applyEnrichment upserts the artist row and whichever enrichment sections the
// message carries, returning the artist id for the caller to put on the event.
//
// This runs BEFORE the event upsert because events.headline_artist_id has a
// foreign key to artists.id. No transaction wraps it: every write is an
// idempotent upsert and a handler error leaves the message on the queue, so a
// partial failure self-heals on redelivery. The worst case is an orphan artists
// row that the next delivery repairs.
func (h *EventHandler) applyEnrichment(ctx context.Context, e *events.Enrichment) (pgtype.UUID, error) {
	if e == nil || e.Artist == nil {
		return pgtype.UUID{}, nil
	}
	a := e.Artist

	if a.Status != "ok" && a.Status != "not_found" {
		log.Printf("ingest: unknown artist status %q for %q, skipping enrichment", a.Status, a.Performer)
		return pgtype.UUID{}, nil
	}
	// display_name is NOT NULL; fall back to the raw performer if the Lambda
	// resolved nothing to name it with.
	display := a.DisplayName
	if display == "" {
		display = a.Performer
	}

	artistID, err := h.q.UpsertArtist(ctx, store.UpsertArtistParams{
		NameKey:        events.NormalizeString(a.Performer),
		DisplayName:    display,
		Mbid:           optString(a.MBID),
		Disambiguation: optString(a.Disambiguation),
		ArtistType:     optString(a.Type),
		Country:        optString(a.Country),
		BeginYear:      optString(a.BeginYear),
		Status:         a.Status,
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("upsert artist %q: %w", a.Performer, err)
	}

	if img := e.Image; img != nil && validStatus(img.Status) {
		// Marshal only when present: a nil *ImageCredit must store SQL NULL, not
		// the four bytes "null".
		var credit []byte
		if img.Credit != nil {
			credit, err = json.Marshal(img.Credit)
			if err != nil {
				return pgtype.UUID{}, fmt.Errorf("marshal image credit: %w", err)
			}
		}
		if err := h.q.UpsertArtistImage(ctx, store.UpsertArtistImageParams{
			ArtistID: artistID,
			Status:   img.Status,
			Url:      optString(img.URL),
			Width:    optInt32(img.Width),
			Height:   optInt32(img.Height),
			File:     optString(img.File),
			Source:   optString(img.Source),
			Credit:   credit,
			Reason:   optString(img.Reason),
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("upsert artist image: %w", err)
		}
	}

	if bio := e.Bio; bio != nil && validStatus(bio.Status) {
		sources, err := json.Marshal(bio.Sources)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("marshal bio sources: %w", err)
		}
		if bio.Sources == nil {
			sources = []byte("[]") // the column is NOT NULL DEFAULT '[]'
		}
		if err := h.q.UpsertArtistBio(ctx, store.UpsertArtistBioParams{
			ArtistID: artistID,
			Status:   bio.Status,
			BioMd:    optString(bio.BioMD),
			Sources:  sources,
			Model:    optString(bio.Model),
			Reason:   optString(bio.Reason),
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("upsert artist bio: %w", err)
		}
	}

	if tour := e.Tour; tour != nil && validStatus(tour.Status) {
		songs, err := json.Marshal(tour.Songs)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("marshal setlist songs: %w", err)
		}
		if tour.Songs == nil {
			songs = []byte("[]")
		}
		observed, err := parseObservedDate(tour.ObservedDate)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("parse observed_date %q: %w", tour.ObservedDate, err)
		}
		if err := h.q.UpsertArtistTourSnapshot(ctx, store.UpsertArtistTourSnapshotParams{
			ArtistID:      artistID,
			Status:        tour.Status,
			TourName:      optString(tour.TourName),
			Songs:         songs,
			ObservedDate:  observed,
			ObservedVenue: optString(tour.ObservedVenue),
			ObservedCity:  optString(tour.ObservedCity),
			SetlistUrl:    optString(tour.SetlistURL),
			Blurb:         optString(tour.Blurb),
			BlurbModel:    optString(tour.BlurbModel),
			Reason:        optString(tour.Reason),
		}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("upsert artist tour snapshot: %w", err)
		}
	}

	return artistID, nil
}

// parseObservedDate reads the wire's YYYY-MM-DD calendar date. Empty is valid
// and means absent — setlist.fm reports a date with no time and no zone, so
// this deliberately never touches time.Local.
func parseObservedDate(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

// optInt32 converts a Go int to a *int32 for nullable integer columns.
// Zero is treated as absent — no real image is 0px wide.
func optInt32(v int) *int32 {
	if v == 0 {
		return nil
	}
	n := int32(v)
	return &n
}
```

There is deliberately no generic `jsonOrNil(v any)` helper here. A typed nil
pointer (`(*events.ImageCredit)(nil)`) wrapped in an `any` is **not** `nil`, so
such a helper would marshal it to the four bytes `"null"` instead of storing SQL
NULL — a bug that only shows up as a JSONB column holding a JSON null. Each
call site does its own typed nil check, as the image block above does.

- [ ] **Step 4: Rewire Handle to decode EnrichedMessage**

In `internal/ingest/events.go`, replace `Handle` and the top of `handleMessage`:

```go
// Handle decodes an SQS message body as an events.EnrichedMessage and applies
// it. A plain (un-enriched) message decodes into the same type with a nil
// Enrichment, which is what makes the queue cutover safe.
func (h *EventHandler) Handle(ctx context.Context, body []byte) error {
	var m events.EnrichedMessage
	if err := json.Unmarshal(body, &m); err != nil {
		// Malformed message — log and return nil so consumer deletes it.
		log.Printf("ingest: bad event message: %v", err)
		return nil
	}
	return h.handleMessage(ctx, m)
}

func (h *EventHandler) handleMessage(ctx context.Context, m events.EnrichedMessage) error {
	src, err := h.q.GetEventSourceByName(ctx, m.SourceID)
	if err != nil {
		return fmt.Errorf("lookup source %q: %w", m.SourceID, err)
	}

	// Artist BEFORE event: events.headline_artist_id has an FK to artists.id.
	artistID, err := h.applyEnrichment(ctx, m.Enrichment)
	if err != nil {
		return err
	}

	// Upsert venue
	// ... (rest of the existing body is unchanged)
```

Then add `HeadlineArtistID: artistID,` to the `store.UpsertEventParams` literal
in the same function.

- [ ] **Step 5: Run the ingest suite**

Run: `go test ./internal/ingest/ -v`
Expected: PASS — the four new tests plus every pre-existing one.

- [ ] **Step 6: Commit**

```bash
git add internal/ingest/enrichment.go internal/ingest/enrichment_test.go internal/ingest/events.go
git commit -m "feat(ingest): persist enrichment from EnrichedMessage

Artist upsert runs before the event upsert because headline_artist_id has
an FK to artists.id. No transaction: every write is an idempotent upsert
and a handler error leaves the message queued, so a partial failure
self-heals on redelivery.

An unrecognized section status is skipped with a log rather than erroring
— a contract violation in one section must not DLQ an otherwise good event."
```

---

### Task 5: API surfaces the artist object

**Files:**
- Create: `internal/http/handlers/artist.go`
- Create: `internal/http/handlers/artist_test.go`
- Modify: `internal/http/handlers/calendar.go:18-44` (response types), `:104-124`, `:167-187`, `:266-289` (the three assembly loops)

**Interfaces:**
- Consumes: `store.GetArtistEnrichmentBatch` (Task 1).
- Produces: `calendarEvent.Artist *calendarArtist` in all three endpoint responses.

- [ ] **Step 1: Write the failing test**

```go
// internal/http/handlers/artist_test.go
package handlers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

func TestBuildArtist_MapsEveryPopulatedSection(t *testing.T) {
	ok := "ok"
	url := "https://upload.wikimedia.org/pb.jpg"
	w, hgt := int32(640), int32(427)
	bio := "Phoebe Bridgers is an American singer-songwriter."
	tour := "Reunion Tour"
	blurb := "Currently out on the Reunion Tour."

	row := store.GetArtistEnrichmentBatchRow{
		DisplayName:  "Phoebe Bridgers",
		ArtistStatus: "ok",
		ImageStatus:  &ok,
		ImageUrl:     &url,
		ImageWidth:   &w,
		ImageHeight:  &hgt,
		ImageCredit:  []byte(`{"license_short_name":"CC BY-SA 4.0","attribution_required":true}`),
		BioStatus:    &ok,
		BioMd:        &bio,
		BioSources:   []byte(`[{"kind":"wikipedia","title":"Phoebe Bridgers"}]`),
		TourStatus:   &ok,
		TourName:     &tour,
		Songs:        []byte(`[{"name":"Motion Sickness"}]`),
		Blurb:        &blurb,
	}

	a := buildArtist(row)
	require.Equal(t, "Phoebe Bridgers", a.Name)
	require.NotNil(t, a.Image)
	require.Equal(t, url, a.Image.URL)
	require.Equal(t, 640, a.Image.Width)
	require.NotNil(t, a.Bio)
	require.Contains(t, a.Bio.Text, "singer-songwriter")
	require.NotNil(t, a.Tour)
	require.Equal(t, "Reunion Tour", a.Tour.Name)
	require.Len(t, a.Tour.Songs, 1)
}

// A resolved artist with no successful enrichment must produce an object with
// no image/bio/tour keys at all, not empty ones — the FE distinguishes absent
// from empty.
func TestBuildArtist_OmitsFailedSections(t *testing.T) {
	none := "none"
	row := store.GetArtistEnrichmentBatchRow{
		DisplayName:  "Some Local Opener",
		ArtistStatus: "not_found",
		ImageStatus:  &none,
		BioStatus:    &none,
	}

	a := buildArtist(row)
	require.Nil(t, a.Image)
	require.Nil(t, a.Bio)
	require.Nil(t, a.Tour)

	out, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(out), `"image"`)
	require.NotContains(t, string(out), `"bio"`)
	require.NotContains(t, string(out), `"tour"`)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/http/handlers/ -run TestBuildArtist -v`
Expected: FAIL — `undefined: buildArtist`.

- [ ] **Step 3: Write the assembler**

```go
// internal/http/handlers/artist.go
package handlers

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// calendarArtist is the enrichment block hung off a calendar event. Every
// section is a pointer so a failed or never-attempted workflow is ABSENT from
// the JSON rather than present-and-empty.
type calendarArtist struct {
	Name           string        `json:"name"`
	Disambiguation string        `json:"disambiguation,omitempty"`
	MBID           string        `json:"mbid,omitempty"`
	Image          *artistImage  `json:"image,omitempty"`
	Bio            *artistBio    `json:"bio,omitempty"`
	Tour           *artistTour   `json:"tour,omitempty"`
}

type artistImage struct {
	URL    string          `json:"url"`
	Width  int             `json:"width,omitempty"`
	Height int             `json:"height,omitempty"`
	Credit json.RawMessage `json:"credit,omitempty"`
}

type artistBio struct {
	Text    string          `json:"text"`
	Sources json.RawMessage `json:"sources,omitempty"`
}

type artistTour struct {
	Name       string          `json:"name,omitempty"`
	Blurb      string          `json:"blurb,omitempty"`
	SetlistURL string          `json:"setlist_url,omitempty"`
	Songs      json.RawMessage `json:"songs,omitempty"`
	Observed   *tourObserved   `json:"observed,omitempty"`
}

type tourObserved struct {
	Date  string `json:"date,omitempty"`
	Venue string `json:"venue,omitempty"`
	City  string `json:"city,omitempty"`
}

// buildArtist maps one batch row into the API shape. A section appears only
// when its status is "ok" — "none" and "error" both mean there is nothing
// worth showing, and the distinction between them is an operational concern,
// not a client one.
func buildArtist(row store.GetArtistEnrichmentBatchRow) calendarArtist {
	a := calendarArtist{
		Name:           row.DisplayName,
		Disambiguation: strVal(row.Disambiguation),
		MBID:           strVal(row.Mbid),
	}

	if strVal(row.ImageStatus) == "ok" && row.ImageUrl != nil {
		a.Image = &artistImage{
			URL:    *row.ImageUrl,
			Width:  int32Val(row.ImageWidth),
			Height: int32Val(row.ImageHeight),
			Credit: json.RawMessage(row.ImageCredit),
		}
	}

	if strVal(row.BioStatus) == "ok" && row.BioMd != nil {
		a.Bio = &artistBio{
			Text:    *row.BioMd,
			Sources: json.RawMessage(row.BioSources),
		}
	}

	if strVal(row.TourStatus) == "ok" {
		t := &artistTour{
			Name:       strVal(row.TourName),
			Blurb:      strVal(row.Blurb),
			SetlistURL: strVal(row.SetlistUrl),
			Songs:      json.RawMessage(row.Songs),
		}
		if row.ObservedDate.Valid || row.ObservedVenue != nil || row.ObservedCity != nil {
			t.Observed = &tourObserved{
				Date:  dateVal(row.ObservedDate),
				Venue: strVal(row.ObservedVenue),
				City:  strVal(row.ObservedCity),
			}
		}
		a.Tour = t
	}

	return a
}

func int32Val(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

func dateVal(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}
```

`strVal` already exists at `internal/http/handlers/posters.go:246`; do not
redeclare it.

- [ ] **Step 4: Run the test**

Run: `go test ./internal/http/handlers/ -run TestBuildArtist -v`
Expected: PASS.

- [ ] **Step 5: Add the field and the batch lookup**

In `internal/http/handlers/calendar.go`, add to `calendarEvent`:

```go
	Artist         *calendarArtist `json:"artist,omitempty"`
```

Then add this helper to the same file:

```go
// attachArtists batch-loads enrichment for a page of events and hangs it off
// each one, following the ListEventPerformersBatch pattern: one page query,
// then one round trip for the whole page rather than N.
//
// It also applies the image fallback. The event's own image always wins, so an
// event whose source supplied a photo is untouched; only events with no image
// at all pick up the band photo. That is what lets the existing frontend render
// band images with no change.
func attachArtists(ctx context.Context, q *store.Queries, evs []calendarEvent, artistIDs []pgtype.UUID) {
	if len(artistIDs) == 0 {
		return
	}
	rows, err := q.GetArtistEnrichmentBatch(ctx, artistIDs)
	if err != nil {
		// Enrichment is decoration: a failure here must not fail the calendar.
		log.Printf("calendar: artist enrichment lookup: %v", err)
		return
	}
	byID := make(map[[16]byte]store.GetArtistEnrichmentBatchRow, len(rows))
	for _, r := range rows {
		byID[r.ArtistID.Bytes] = r
	}
	for i := range evs {
		if !evs[i].artistID.Valid {
			continue
		}
		row, ok := byID[evs[i].artistID.Bytes]
		if !ok {
			continue
		}
		a := buildArtist(row)
		evs[i].Artist = &a
		if evs[i].ImageURL == "" && a.Image != nil {
			evs[i].ImageURL = a.Image.URL
		}
	}
}
```

Add an unexported carrier field to `calendarEvent` so the loop can correlate
without widening the JSON:

```go
	// Unexported, so encoding/json ignores it entirely — no tag needed. Carries
	// the row's headline_artist_id from the page query through to attachArtists.
	artistID pgtype.UUID
```

Add `"log"` to the file's imports.

- [ ] **Step 6: Wire the three handlers**

In each of `GetMyCalendar`, `GetCityCalendar` and `GetEventByIDForUser`, set
`artistID: row.HeadlineArtistID` in the `calendarEvent` literal, collect the
valid ids while building `out.Events`, and call `attachArtists` immediately
before `writeJSON`. For `GetEventByIDForUser`, which builds a single `ev`,
wrap it: `evs := []calendarEvent{ev}; attachArtists(ctx, q, evs, ids); ev = evs[0]`.

- [ ] **Step 7: Run the handler suite**

Run: `go test ./internal/http/handlers/ -v`
Expected: PASS. Existing calendar tests must be untouched — every new field is
`omitempty` and absent for unenriched events.

- [ ] **Step 8: Run everything and commit**

Run: `make test`
Expected: PASS.

```bash
git add internal/http/handlers/artist.go internal/http/handlers/artist_test.go \
        internal/http/handlers/calendar.go
git commit -m "feat(api): surface artist enrichment on calendar responses

One batched lookup per page, following ListEventPerformersBatch rather
than widening three queries with four left joins each. A section appears
only when its status is ok, and every field is omitempty so today's
frontend payloads are byte-identical for unenriched events.

image_url falls back to the band photo when the event has none — the
source image always wins, so nothing regresses."
```

---

# Phase 2 — Lambda enrichment

All work below is under `lambda/mastra-handler/`. Paths in this phase are
relative to that directory unless stated otherwise. Run `pnpm test` from there.

### Task 6: artistKey

**Files:**
- Create: `src/artist-key.ts`
- Create: `src/artist-key.test.ts`

**Interfaces:**
- Consumes: `testdata/artist-key-contract/cases.json` (Task 3).
- Produces: `artistKey(performer: string): string`. Used by Task 8's cache and Task 13's orchestrator.

- [ ] **Step 1: Write the failing test**

```ts
// src/artist-key.test.ts
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { artistKey } from "./artist-key.js";

// The same fixture internal/events/artistkey_contract_test.go asserts. The
// Lambda keys its S3 skip cache on artistKey() and the database keys
// artists.name_key on Go's NormalizeString; a silent divergence would merge
// two artists in one place and split them in the other.
const cases: { in: string; out: string; why: string }[] = JSON.parse(
  readFileSync(new URL("../../../testdata/artist-key-contract/cases.json", import.meta.url), "utf8"),
);

describe("artistKey", () => {
  it("has fixture cases to run", () => {
    expect(cases.length).toBeGreaterThan(0);
  });

  for (const c of cases) {
    it(`${JSON.stringify(c.in)} -> ${JSON.stringify(c.out)} (${c.why})`, () => {
      expect(artistKey(c.in)).toBe(c.out);
    });
  }
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/artist-key.test.ts`
Expected: FAIL — cannot resolve `./artist-key.js`.

- [ ] **Step 3: Implement**

```ts
// src/artist-key.ts

/**
 * Mirrors Go's events.NormalizeString (internal/events/genres.go:64) EXACTLY:
 * NFD, drop nonspacing marks, NFC, lowercase, trim. Pinned in both languages by
 * testdata/artist-key-contract/cases.json.
 *
 * This is deliberately NOT hash.ts's normalize(). That one additionally strips
 * punctuation ("AC/DC" -> "acdc" where this gives "ac/dc"), and it cannot be
 * changed to match because it feeds contentHash() -> source_event_id: re-keying
 * that would break dedup for every email-ingested event.
 */
export function artistKey(performer: string): string {
  return performer
    .normalize("NFD")
    .replace(/\p{Mn}/gu, "")
    .normalize("NFC")
    .toLowerCase()
    .trim();
}
```

- [ ] **Step 4: Run the test**

Run: `pnpm vitest run src/artist-key.test.ts`
Expected: PASS, one case per fixture entry.

- [ ] **Step 5: Commit**

```bash
git add lambda/mastra-handler/src/artist-key.ts lambda/mastra-handler/src/artist-key.test.ts
git commit -m "feat(lambda): artistKey mirroring Go NormalizeString

Asserted against the same fixture the Go contract test reads. Separate from
hash.ts normalize(), which strips punctuation and must not change because it
feeds source_event_id."
```

---

### Task 7: Enrichment wire schemas

**Files:**
- Create: `src/enrichment-schema.ts`
- Create: `src/enrichment-schema.test.ts`

**Interfaces:**
- Consumes: `EventMessageSchema` (`src/schema.ts:13`), `ImageCredit` (`src/mastra/tools/band-image.ts:22`).
- Produces: `EnrichedMessageSchema`, `EnrichmentSchema`, `ArtistInfoSchema`, `ImageInfoSchema`, `BioInfoSchema`, `TourInfoSchema`, types `EnrichedMessage`/`Enrichment`/`ArtistInfo`/`ImageInfo`/`BioInfo`/`TourInfo`, and `toWireCredit(c: ImageCredit)`.

- [ ] **Step 1: Write the failing test**

```ts
// src/enrichment-schema.test.ts
import { describe, expect, it } from "vitest";
import type { ImageCredit } from "./mastra/tools/band-image.js";
import { EnrichedMessageSchema, toWireCredit } from "./enrichment-schema.js";

const baseEvent = {
  source_id: "ticketmaster",
  source_event_id: "tm-aaa",
  title: "La Luz",
  starts_at: "2026-09-02T20:00:00Z",
  venue: { name: "The Chapel" },
};

describe("EnrichedMessageSchema", () => {
  it("accepts a plain event with no enrichment", () => {
    const parsed = EnrichedMessageSchema.parse(baseEvent);
    expect(parsed.enrichment).toBeUndefined();
  });

  it("accepts a fully enriched event", () => {
    const parsed = EnrichedMessageSchema.parse({
      ...baseEvent,
      enrichment: {
        attempted_at: "2026-08-12T04:11:22Z",
        artist: { performer: "La Luz", display_name: "La Luz", status: "ok" },
        image: { status: "ok", url: "https://x/y.jpg", width: 640, height: 427 },
        bio: { status: "none", reason: "no article" },
        tour: { status: "ok", tour_name: "T", observed_date: "2026-07-14" },
      },
    });
    expect(parsed.enrichment?.artist?.status).toBe("ok");
    expect(parsed.enrichment?.tour?.observed_date).toBe("2026-07-14");
  });

  it("rejects an unknown status", () => {
    expect(() =>
      EnrichedMessageSchema.parse({
        ...baseEvent,
        enrichment: {
          attempted_at: "2026-08-12T04:11:22Z",
          artist: { performer: "x", display_name: "x", status: "ok" },
          bio: { status: "maybe" },
        },
      }),
    ).toThrow();
  });

  it("rejects an observed_date that is not YYYY-MM-DD", () => {
    expect(() =>
      EnrichedMessageSchema.parse({
        ...baseEvent,
        enrichment: {
          attempted_at: "2026-08-12T04:11:22Z",
          artist: { performer: "x", display_name: "x", status: "ok" },
          tour: { status: "ok", observed_date: "14-07-2026" },
        },
      }),
    ).toThrow();
  });
});

// The Lambda's internal credit is camelCase; the wire is snake_case, matching
// the Go struct tags. Without an explicit mapper the fields silently vanish.
describe("toWireCredit", () => {
  it("renames every camelCase field", () => {
    const internal: ImageCredit = {
      file: "La_Luz.jpg",
      descriptionUrl: "https://commons/desc",
      artist: "Photographer",
      license: "cc-by-sa-4.0",
      licenseShortName: "CC BY-SA 4.0",
      licenseUrl: "https://creativecommons.org/x",
      usageTerms: "terms",
      attributionRequired: true,
    };
    expect(toWireCredit(internal)).toEqual({
      file: "La_Luz.jpg",
      description_url: "https://commons/desc",
      artist: "Photographer",
      license: "cc-by-sa-4.0",
      license_short_name: "CC BY-SA 4.0",
      license_url: "https://creativecommons.org/x",
      usage_terms: "terms",
      attribution_required: true,
    });
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/enrichment-schema.test.ts`
Expected: FAIL — cannot resolve `./enrichment-schema.js`.

- [ ] **Step 3: Implement**

```ts
// src/enrichment-schema.ts
import { z } from "zod";
import type { ImageCredit } from "./mastra/tools/band-image.js";
import { EventMessageSchema } from "./schema.js";

// Wire shape — MUST match Go internal/events/enriched.go JSON tags exactly.
// snake_case throughout, including nested credit fields.

const StatusSchema = z.enum(["ok", "none", "error"]);

export const WireCreditSchema = z.object({
  file: z.string().optional(),
  description_url: z.string().optional(),
  artist: z.string().optional(),
  credit: z.string().optional(),
  license: z.string().optional(),
  license_short_name: z.string().optional(),
  license_url: z.string().optional(),
  usage_terms: z.string().optional(),
  attribution_required: z.boolean().default(false),
}).strict();

export const ArtistInfoSchema = z.object({
  performer: z.string(),          // RAW name; the consumer normalizes it
  display_name: z.string(),
  mbid: z.string().optional(),
  disambiguation: z.string().optional(),
  type: z.string().optional(),
  country: z.string().optional(),
  begin_year: z.string().optional(),
  status: z.enum(["ok", "not_found"]),
}).strict();

export const ImageInfoSchema = z.object({
  status: StatusSchema,
  url: z.string().optional(),
  width: z.number().int().optional(),
  height: z.number().int().optional(),
  file: z.string().optional(),
  source: z.enum(["p18", "category"]).optional(),
  credit: WireCreditSchema.optional(),
  reason: z.string().optional(),
}).strict();

export const BioSourceSchema = z.object({
  kind: z.enum(["wikipedia", "musicbrainz"]),
  title: z.string().optional(),
  url: z.string().optional(),
  revision_id: z.number().int().optional(),
  mbid: z.string().optional(),
}).strict();

export const BioInfoSchema = z.object({
  status: StatusSchema,
  bio_md: z.string().optional(),
  sources: z.array(BioSourceSchema).optional(),
  model: z.string().optional(),
  reason: z.string().optional(),
}).strict();

export const SetlistSongSchema = z.object({
  name: z.string(),
  encore: z.number().int().optional(),
  cover_of: z.string().optional(),
  tape: z.boolean().optional(),
  info: z.string().optional(),
}).strict();

export const TourInfoSchema = z.object({
  status: StatusSchema,
  tour_name: z.string().optional(),
  songs: z.array(SetlistSongSchema).optional(),
  // Plain calendar date. Go parses this with layout "2006-01-02" into a DATE
  // column; anything else errors the consumer.
  observed_date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional(),
  observed_venue: z.string().optional(),
  observed_city: z.string().optional(),
  setlist_url: z.string().optional(),
  blurb: z.string().optional(),
  blurb_model: z.string().optional(),
  reason: z.string().optional(),
}).strict();

export const EnrichmentSchema = z.object({
  artist: ArtistInfoSchema.optional(),
  image: ImageInfoSchema.optional(),
  bio: BioInfoSchema.optional(),
  tour: TourInfoSchema.optional(),
  attempted_at: z.string().datetime({ offset: true }),
}).strict();

export const EnrichedMessageSchema = EventMessageSchema.extend({
  enrichment: EnrichmentSchema.optional(),
});

export type WireCredit = z.infer<typeof WireCreditSchema>;
export type ArtistInfo = z.infer<typeof ArtistInfoSchema>;
export type ImageInfo = z.infer<typeof ImageInfoSchema>;
export type BioInfo = z.infer<typeof BioInfoSchema>;
export type TourInfo = z.infer<typeof TourInfoSchema>;
export type Enrichment = z.infer<typeof EnrichmentSchema>;
export type EnrichedMessage = z.infer<typeof EnrichedMessageSchema>;

/** Map the Lambda's internal camelCase ImageCredit onto the snake_case wire
 * shape. Explicit rather than a generic case converter: a silent rename miss
 * here drops attribution that a CC-BY licence requires. */
export function toWireCredit(c: ImageCredit): WireCredit {
  return {
    file: c.file,
    description_url: c.descriptionUrl,
    artist: c.artist,
    credit: c.credit,
    license: c.license,
    license_short_name: c.licenseShortName,
    license_url: c.licenseUrl,
    usage_terms: c.usageTerms,
    attribution_required: c.attributionRequired,
  };
}
```

Note `EventMessageSchema` is `.strict()`, and Zod's `.extend()` preserves that,
so an unknown top-level key still fails — which is the mirror of the Go side's
`DisallowUnknownFields`.

- [ ] **Step 4: Run the test**

Run: `pnpm vitest run src/enrichment-schema.test.ts`
Expected: PASS (all six cases).

- [ ] **Step 5: Cross-check against the Go fixture**

Add to `src/enrichment-schema.test.ts`:

```ts
it("parses the Go contract fixture", () => {
  const raw = readFileSync(
    new URL("../../../testdata/enriched-message-contract/full.json", import.meta.url),
    "utf8",
  );
  expect(() => EnrichedMessageSchema.parse(JSON.parse(raw))).not.toThrow();
});
```

Add `import { readFileSync } from "node:fs";` at the top.

- [ ] **Step 6: Run and commit**

Run: `pnpm vitest run src/enrichment-schema.test.ts`
Expected: PASS, including the fixture case. If it fails, the Go struct and the
Zod schema have diverged — fix whichever is wrong, not the fixture.

```bash
git add lambda/mastra-handler/src/enrichment-schema.ts lambda/mastra-handler/src/enrichment-schema.test.ts
git commit -m "feat(lambda): Zod mirror of the enriched wire contract

Parses the same fixture the Go contract test decodes, so a divergence
between the struct tags and the schema fails on both sides. toWireCredit
maps camelCase to snake_case explicitly — a silent rename miss would drop
attribution a CC-BY licence requires."
```

---

### Task 8: Enrichment cache

**Files:**
- Create: `src/enrichment-cache.ts`
- Create: `src/enrichment-cache.test.ts`

**Interfaces:**
- Consumes: `artistKey` (Task 6), `ImageInfo`/`BioInfo`/`TourInfo`/`ArtistInfo` (Task 7).
- Produces: `EnrichmentCache` interface with `read(performer): Promise<CacheEntry | null>` and `write(performer, entry): Promise<void>`; classes `S3EnrichmentCache`, `StubEnrichmentCache`; `isFresh(record, now): boolean`; `CACHE_TTL_MS`.

- [ ] **Step 1: Write the failing test**

```ts
// src/enrichment-cache.test.ts
import { describe, expect, it } from "vitest";
import { CACHE_TTL_MS, StubEnrichmentCache, cacheObjectKey, isFresh } from "./enrichment-cache.js";

const NOW = Date.parse("2026-08-12T00:00:00Z");

describe("isFresh", () => {
  it("keeps an ok record for 90 days", () => {
    const at = new Date(NOW - 89 * 24 * 3600_000).toISOString();
    expect(isFresh({ status: "ok", at }, NOW)).toBe(true);
  });

  it("expires an ok record after 90 days", () => {
    const at = new Date(NOW - 91 * 24 * 3600_000).toISOString();
    expect(isFresh({ status: "ok", at }, NOW)).toBe(false);
  });

  it("retries an error record after 6 hours", () => {
    expect(isFresh({ status: "error", at: new Date(NOW - 5 * 3600_000).toISOString() }, NOW)).toBe(true);
    expect(isFresh({ status: "error", at: new Date(NOW - 7 * 3600_000).toISOString() }, NOW)).toBe(false);
  });

  it("retries a none record after 14 days", () => {
    expect(isFresh({ status: "none", at: new Date(NOW - 13 * 24 * 3600_000).toISOString() }, NOW)).toBe(true);
    expect(isFresh({ status: "none", at: new Date(NOW - 15 * 24 * 3600_000).toISOString() }, NOW)).toBe(false);
  });

  it("treats an unparseable timestamp as stale rather than fresh-forever", () => {
    expect(isFresh({ status: "ok", at: "not-a-date" }, NOW)).toBe(false);
  });

  it("has the TTLs the spec fixed", () => {
    expect(CACHE_TTL_MS.ok).toBe(90 * 24 * 3600_000);
    expect(CACHE_TTL_MS.none).toBe(14 * 24 * 3600_000);
    expect(CACHE_TTL_MS.error).toBe(6 * 3600_000);
  });
});

describe("cacheObjectKey", () => {
  it("hashes the artist key so punctuation never reaches the path", () => {
    const key = cacheObjectKey("AC/DC");
    expect(key).toMatch(/^enrichment\/v1\/[0-9a-f]{64}\.json$/);
    expect(key).not.toContain("ac/dc");
  });

  it("is stable and case-insensitive via artistKey", () => {
    expect(cacheObjectKey("La Luz")).toBe(cacheObjectKey("  la luz  "));
  });
});

describe("StubEnrichmentCache", () => {
  it("round-trips an entry", async () => {
    const cache = new StubEnrichmentCache();
    await cache.write("La Luz", {
      artist_key: "la luz",
      performer: "La Luz",
      workflows: { bio: { status: "none", at: new Date(NOW).toISOString() } },
    });
    const got = await cache.read("La Luz");
    expect(got?.workflows.bio?.status).toBe("none");
  });

  it("returns null on a miss", async () => {
    expect(await new StubEnrichmentCache().read("Nobody")).toBeNull();
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/enrichment-cache.test.ts`
Expected: FAIL — cannot resolve `./enrichment-cache.js`.

- [ ] **Step 3: Implement**

```ts
// src/enrichment-cache.ts
import { GetObjectCommand, PutObjectCommand, type S3Client } from "@aws-sdk/client-s3";
import { createHash } from "node:crypto";
import { artistKey } from "./artist-key.js";
import type { ArtistInfo, BioInfo, ImageInfo, TourInfo } from "./enrichment-schema.js";

export type WorkflowName = "image" | "bio" | "tour";
export type CacheStatus = "ok" | "none" | "error";

/** How long an attempt with a given outcome suppresses a retry. Mirrors the
 * shape of internal/scraper/spotify/genres.go:17-19 so the two artist caches
 * behave alike. */
export const CACHE_TTL_MS: Record<CacheStatus, number> = {
  ok: 90 * 24 * 3600_000,
  none: 14 * 24 * 3600_000,
  error: 6 * 3600_000,
};

export interface CacheRecord {
  status: CacheStatus;
  at: string; // ISO 8601
  reason?: string;
  /** The workflow's own result, cached so a skip still yields a COMPLETE
   * message. Without it, "skipped" and "found nothing" become
   * indistinguishable downstream. */
  payload?: ImageInfo | BioInfo | TourInfo;
}

export interface CacheEntry {
  artist_key: string;
  performer: string;
  artist?: ArtistInfo;
  workflows: Partial<Record<WorkflowName, CacheRecord>>;
}

export interface EnrichmentCache {
  read(performer: string): Promise<CacheEntry | null>;
  write(performer: string, entry: CacheEntry): Promise<void>;
}

/** v1/ so a schema change is a prefix bump rather than a migration. The key is
 * hashed because normalized band names are not path-safe — "ac/dc" would create
 * a nested prefix, and "Sunn O)))" and emoji names exist. The readable
 * artist_key lives inside the body so an object stays identifiable. */
export function cacheObjectKey(performer: string): string {
  const digest = createHash("sha256").update(artistKey(performer)).digest("hex");
  return `enrichment/v1/${digest}.json`;
}

export function isFresh(record: CacheRecord, now = Date.now()): boolean {
  const at = Date.parse(record.at);
  if (Number.isNaN(at)) return false; // unparseable -> re-run, never cache forever
  return now - at < CACHE_TTL_MS[record.status];
}

export class S3EnrichmentCache implements EnrichmentCache {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  async read(performer: string): Promise<CacheEntry | null> {
    try {
      const out = await this.s3.send(
        new GetObjectCommand({ Bucket: this.bucket, Key: cacheObjectKey(performer) }),
      );
      return JSON.parse(await out.Body!.transformToString()) as CacheEntry;
    } catch (e) {
      // A genuine miss is NoSuchKey. AccessDenied is a misconfiguration, and
      // swallowing it would silently re-run every workflow on every event
      // forever while looking exactly like "the cache isn't working".
      // See the same warning at terraform/prod/lambda_mastra_handler.tf:57-59.
      if (isNoSuchKey(e)) return null;
      throw e;
    }
  }

  async write(performer: string, entry: CacheEntry): Promise<void> {
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: cacheObjectKey(performer),
        Body: JSON.stringify(entry),
        ContentType: "application/json",
      }),
    );
  }
}

// Classify on error NAME only, exactly as poster-sink.ts does. Do NOT add a
// `status === 404` fallback: NoSuchBucket is also a 404, so a mistyped bucket
// would be swallowed as a cache miss and every workflow would re-run on every
// event forever while presenting as "the cache isn't working" — the same
// failure the AccessDenied rule exists to prevent.
function isNoSuchKey(e: unknown): boolean {
  const name = (e as { name?: string })?.name;
  return name === "NoSuchKey" || name === "NotFound";
}

/** In-memory cache for local dev and unit tests. Lives beside the production
 * implementation for the same reason StubPosterSink does: it is part of the
 * module's contract surface. */
export class StubEnrichmentCache implements EnrichmentCache {
  readonly entries = new Map<string, CacheEntry>();

  async read(performer: string): Promise<CacheEntry | null> {
    return this.entries.get(cacheObjectKey(performer)) ?? null;
  }

  async write(performer: string, entry: CacheEntry): Promise<void> {
    this.entries.set(cacheObjectKey(performer), entry);
  }
}
```

- [ ] **Step 4: Run the test**

Run: `pnpm vitest run src/enrichment-cache.test.ts`
Expected: PASS (all cases).

- [ ] **Step 5: Add the AccessDenied regression test**

```ts
it("throws on AccessDenied rather than reporting a miss", async () => {
  const s3 = {
    send: async () => {
      const e = new Error("Access Denied") as Error & { name: string; $metadata: { httpStatusCode: number } };
      e.name = "AccessDenied";
      e.$metadata = { httpStatusCode: 403 };
      throw e;
    },
  } as unknown as S3Client;

  await expect(new S3EnrichmentCache(s3, "b").read("La Luz")).rejects.toThrow(/Access Denied/);
});

it("reports NoSuchKey as a miss", async () => {
  const s3 = {
    send: async () => {
      const e = new Error("no key") as Error & { name: string };
      e.name = "NoSuchKey";
      throw e;
    },
  } as unknown as S3Client;

  expect(await new S3EnrichmentCache(s3, "b").read("La Luz")).toBeNull();
});
```

Add `import type { S3Client } from "@aws-sdk/client-s3";` and
`S3EnrichmentCache` to the imports.

- [ ] **Step 6: Run and commit**

Run: `pnpm vitest run src/enrichment-cache.test.ts`
Expected: PASS.

```bash
git add lambda/mastra-handler/src/enrichment-cache.ts lambda/mastra-handler/src/enrichment-cache.test.ts
git commit -m "feat(lambda): S3 decision-and-result cache for enrichment

Caches each workflow's payload, not just its verdict, so a skip still
produces a complete message and a missing DB row self-heals on the next
event for that artist.

AccessDenied throws rather than reporting a miss: swallowing it would
re-run every workflow on every event forever while presenting as a cache
that simply isn't working."
```

---

### Task 9: setlist.fm client

**Files:**
- Create: `src/mastra/tools/setlistfm.tool.ts`
- Create: `src/mastra/tools/setlistfm.tool.test.ts`
- Create: `src/__fixtures__/setlistfm-artist-setlists.json`

**Interfaces:**
- Consumes: `StubFetch` (`src/mastra/tools/stub-fetch.ts`).
- Produces: `createSetlistFmClient(opts)`, `type RecentSetlist`, `parseEventDate(s)`, `pickRecentSetlist(setlists, now)`, `MAX_SETLIST_AGE_DAYS`.
- Deliberately **no** module-level singleton or bare `fetchRecentSetlist(mbid)` wrapper, unlike `musicbrainz.tool.ts`. That module can construct its client at import time because it needs no credentials; this one needs an API key that arrives asynchronously from Secrets Manager during the invocation, so a singleton could not be built at import. Task 12's `prodTourDeps(apiKey)` calls `createSetlistFmClient({ apiKey })` directly.

- [ ] **Step 1: Record the fixture**

Create `src/__fixtures__/setlistfm-artist-setlists.json` with a response body
shaped like the real one. **The JSON is XML-derived**: the docs table lists a
`set` field, but real responses nest `sets: { set: [...] }`. This fixture is the
contract — if a live check ever disagrees, re-record it rather than editing the
parser to match a guess.

```json
{
  "type": "setlists",
  "itemsPerPage": 20,
  "page": 1,
  "total": 3,
  "setlist": [
    {
      "id": "aaaaaaa1",
      "versionId": "v1",
      "eventDate": "02-08-2026",
      "lastUpdated": "2026-08-03T10:00:00.000+0000",
      "artist": { "mbid": "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a", "name": "La Luz" },
      "venue": { "id": "ven1", "name": "Empty Room", "city": { "name": "Portland", "state": "OR" } },
      "tour": { "name": "News of the Universe Tour" },
      "sets": { "set": [] },
      "url": "https://www.setlist.fm/setlist/la-luz/2026/empty-room.html"
    },
    {
      "id": "aaaaaaa2",
      "versionId": "v2",
      "eventDate": "14-07-2026",
      "lastUpdated": "2026-07-15T10:00:00.000+0000",
      "artist": { "mbid": "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a", "name": "La Luz" },
      "venue": { "id": "ven2", "name": "The Greek", "city": { "name": "Berkeley", "state": "CA" } },
      "tour": { "name": "News of the Universe Tour" },
      "sets": {
        "set": [
          { "song": [{ "name": "Sure As Spring" }, { "name": "Cicada", "info": "acoustic" }] },
          { "encore": 1, "song": [{ "name": "Strange World", "cover": { "name": "Cover Artist" } }] }
        ]
      },
      "url": "https://www.setlist.fm/setlist/la-luz/2026/the-greek.html"
    },
    {
      "id": "aaaaaaa3",
      "versionId": "v3",
      "eventDate": "23-08-2019",
      "lastUpdated": "2019-08-24T10:00:00.000+0000",
      "artist": { "mbid": "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a", "name": "La Luz" },
      "venue": { "id": "ven3", "name": "Old Hall", "city": { "name": "Seattle", "state": "WA" } },
      "sets": { "set": [{ "song": [{ "name": "Ancient Song" }] }] },
      "url": "https://www.setlist.fm/setlist/la-luz/2019/old-hall.html"
    }
  ]
}
```

The fixture deliberately encodes all three selection hazards: entry 1 is recent
but **songless**, entry 2 is the correct answer, entry 3 has songs but is
**years stale**. It is also listed newest-first so a parser that assumes order
passes — the ordering test below shuffles it.

- [ ] **Step 2: Write the failing test**

```ts
// src/mastra/tools/setlistfm.tool.test.ts
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { stubFetch } from "./stub-fetch.js";
import {
  MAX_SETLIST_AGE_DAYS,
  createSetlistFmClient,
  parseEventDate,
  pickRecentSetlist,
} from "./setlistfm.tool.js";

const fixture = JSON.parse(
  readFileSync(new URL("../../__fixtures__/setlistfm-artist-setlists.json", import.meta.url), "utf8"),
);
const NOW = Date.parse("2026-08-12T00:00:00Z");
const MBID = "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a";

describe("parseEventDate", () => {
  // new Date("08-12-2026") silently parses as August 12th under US
  // interpretation, and new Date("23-08-1964") is Invalid Date. Both produce a
  // plausible-looking wrong answer, so the format is parsed explicitly.
  it("reads dd-MM-yyyy, not the US ordering", () => {
    expect(parseEventDate("08-12-2026")).toBe("2026-12-08");
    expect(parseEventDate("23-08-1964")).toBe("1964-08-23");
  });

  it("returns null for junk rather than a wrong date", () => {
    expect(parseEventDate("2026-08-12")).toBeNull();
    expect(parseEventDate("")).toBeNull();
  });
});

describe("pickRecentSetlist", () => {
  it("skips the songless recent entry and takes the newest one with songs", () => {
    const picked = pickRecentSetlist(fixture.setlist, NOW);
    expect(picked?.setlistId).toBe("aaaaaaa2");
    expect(picked?.observedDate).toBe("2026-07-14");
    expect(picked?.tourName).toBe("News of the Universe Tour");
    expect(picked?.observedVenue).toBe("The Greek");
    expect(picked?.observedCity).toBe("Berkeley");
  });

  it("flattens sets into an ordered song list carrying encore and cover", () => {
    const picked = pickRecentSetlist(fixture.setlist, NOW)!;
    expect(picked.songs).toEqual([
      { name: "Sure As Spring" },
      { name: "Cicada", info: "acoustic" },
      { name: "Strange World", encore: 1, cover_of: "Cover Artist" },
    ]);
  });

  // The endpoint documentation does not state a sort order. It is newest-first
  // in practice, but building on an undocumented ordering produces a bug that
  // appears months later with no code change.
  it("sorts client-side rather than trusting response order", () => {
    const shuffled = [fixture.setlist[2], fixture.setlist[1], fixture.setlist[0]];
    expect(pickRecentSetlist(shuffled, NOW)?.setlistId).toBe("aaaaaaa2");
  });

  it("returns null when everything qualifying is too old", () => {
    const stale = Date.parse("2027-06-01T00:00:00Z");
    expect(pickRecentSetlist(fixture.setlist, stale)).toBeNull();
  });

  it("returns null when every entry is songless", () => {
    expect(pickRecentSetlist([fixture.setlist[0]], NOW)).toBeNull();
  });

  it("bounds staleness at 180 days", () => {
    expect(MAX_SETLIST_AGE_DAYS).toBe(180);
  });
});

describe("createSetlistFmClient", () => {
  it("sends the api key and asks for JSON", async () => {
    const fetchFn = stubFetch([{ match: /artist\/.*\/setlists/, json: fixture }]);
    const client = createSetlistFmClient({
      baseUrl: "https://fake.setlist",
      apiKey: "test-key",
      fetchFn,
      minIntervalMs: 0,
    });

    await client.recentSetlist(MBID);

    expect(fetchFn.calls).toHaveLength(1);
    expect(fetchFn.calls[0].url).toContain("/rest/1.0/artist/" + MBID + "/setlists?p=1");
    expect(fetchFn.calls[0].headers["x-api-key"]).toBe("test-key");
    // The API defaults to XML; without this header the body is unparseable.
    expect(fetchFn.calls[0].headers["accept"]).toBe("application/json");
  });

  it("treats 404 as no setlists rather than an error", async () => {
    const fetchFn = stubFetch([{ match: /setlists/, status: 404, json: {} }]);
    const client = createSetlistFmClient({
      baseUrl: "https://fake.setlist", apiKey: "k", fetchFn, minIntervalMs: 0,
    });
    expect(await client.recentSetlist(MBID)).toBeNull();
  });

  it("throws on 429 so the caller can record an error status", async () => {
    const fetchFn = stubFetch([{ match: /setlists/, status: 429, json: {} }]);
    const client = createSetlistFmClient({
      baseUrl: "https://fake.setlist", apiKey: "k", fetchFn, minIntervalMs: 0,
    });
    await expect(client.recentSetlist(MBID)).rejects.toThrow(/429/);
  });

  it("never paginates", async () => {
    const fetchFn = stubFetch([{ match: /setlists/, json: fixture }]);
    const client = createSetlistFmClient({
      baseUrl: "https://fake.setlist", apiKey: "k", fetchFn, minIntervalMs: 0,
    });
    await client.recentSetlist(MBID);
    expect(fetchFn.calls).toHaveLength(1);
  });
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `pnpm vitest run src/mastra/tools/setlistfm.tool.test.ts`
Expected: FAIL — cannot resolve `./setlistfm.tool.js`.

- [ ] **Step 4: Implement**

```ts
// src/mastra/tools/setlistfm.tool.ts
import type { FetchFn } from "./musicbrainz.tool.js";

const DEFAULT_BASE_URL = "https://api.setlist.fm";
// setlist.fm allows 2 req/sec, but this limiter is per-process and the SQS
// event source mapping runs up to 2 concurrent containers. At 500ms those two
// would put ~4 req/sec against a 2 req/sec limit; at 1000ms the worst case
// lands on their limit instead of double it. Throughput is not the binding
// constraint here — the 1,440 req/day cap is.
const MIN_INTERVAL_MS = 1000;
const TIMEOUT_MS = 15_000;
const MAX_ERROR_BODY = 200;

/** A setlist older than this is not "what they have been playing". */
export const MAX_SETLIST_AGE_DAYS = 180;

function truncate(text: string, limit = MAX_ERROR_BODY): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length <= limit ? flat : `${flat.slice(0, limit)}…`;
}

export interface SetlistSong {
  name: string;
  encore?: number;
  cover_of?: string;
  tape?: boolean;
  info?: string;
}

export interface RecentSetlist {
  setlistId: string;
  setlistUrl: string;
  tourName?: string;
  songs: SetlistSong[];
  observedDate: string; // YYYY-MM-DD
  observedVenue?: string;
  observedCity?: string;
}

interface RawSong { name?: string; info?: string; tape?: boolean; cover?: { name?: string } }
interface RawSet { name?: string; encore?: number; song?: RawSong[] }
interface RawSetlist {
  id?: string;
  eventDate?: string;
  url?: string;
  tour?: { name?: string };
  venue?: { name?: string; city?: { name?: string } };
  // XML-derived JSON: the sets live under `sets.set`, not a bare `set`.
  sets?: { set?: RawSet[] };
}

/** setlist.fm reports dd-MM-yyyy. Returns YYYY-MM-DD, or null for anything
 * else. Never hand this to `new Date()`: "08-12-2026" silently parses as
 * August 12th under US interpretation and "23-08-1964" is Invalid Date. */
export function parseEventDate(s: string): string | null {
  const m = /^(\d{2})-(\d{2})-(\d{4})$/.exec(s ?? "");
  if (!m) return null;
  const [, dd, mm, yyyy] = m;
  const month = Number(mm);
  const day = Number(dd);
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  return `${yyyy}-${mm}-${dd}`;
}

function flattenSongs(raw: RawSetlist): SetlistSong[] {
  const out: SetlistSong[] = [];
  for (const set of raw.sets?.set ?? []) {
    for (const song of set.song ?? []) {
      if (!song.name) continue;
      const entry: SetlistSong = { name: song.name };
      if (set.encore) entry.encore = set.encore;
      if (song.cover?.name) entry.cover_of = song.cover.name;
      if (song.tape) entry.tape = true;
      if (song.info) entry.info = song.info;
      out.push(entry);
    }
  }
  return out;
}

/** Newest setlist that actually has songs and is within MAX_SETLIST_AGE_DAYS.
 * Sorts client-side: the endpoint documentation does not state a sort order. */
export function pickRecentSetlist(raw: RawSetlist[], now = Date.now()): RecentSetlist | null {
  const cutoff = now - MAX_SETLIST_AGE_DAYS * 24 * 3600_000;

  const candidates = raw
    .map((r) => ({ raw: r, date: parseEventDate(r.eventDate ?? "") }))
    .filter((c): c is { raw: RawSetlist; date: string } => c.date !== null)
    .sort((a, b) => b.date.localeCompare(a.date)); // ISO dates sort lexically

  for (const c of candidates) {
    if (Date.parse(`${c.date}T00:00:00Z`) < cutoff) break; // sorted, so all later are older
    const songs = flattenSongs(c.raw);
    // Entries logged for attendance without a setlist are common. A songless
    // entry is an absent setlist, not a present empty one.
    if (songs.length === 0) continue;
    return {
      setlistId: c.raw.id ?? "",
      setlistUrl: c.raw.url ?? "",
      tourName: c.raw.tour?.name,
      songs,
      observedDate: c.date,
      observedVenue: c.raw.venue?.name,
      observedCity: c.raw.venue?.city?.name,
    };
  }
  return null;
}

export interface SetlistFmOptions {
  baseUrl?: string;
  apiKey: string;
  fetchFn?: FetchFn;
  /** Set to 0 in tests to disable throttling. */
  minIntervalMs?: number;
}

export interface SetlistFmClient {
  /** Null means the artist genuinely has no usable recent setlist. Throws only
   * on transport/limit failures, which the caller records as status 'error'. */
  recentSetlist(mbid: string, now?: number): Promise<RecentSetlist | null>;
}

export function createSetlistFmClient(options: SetlistFmOptions): SetlistFmClient {
  const baseUrl = options.baseUrl ?? DEFAULT_BASE_URL;
  const doFetch = options.fetchFn ?? globalThis.fetch;
  const minIntervalMs = options.minIntervalMs ?? MIN_INTERVAL_MS;

  // Slot-reservation limiter, same shape as musicbrainz.tool.ts:65-72: each
  // caller claims the next free instant so concurrent callers queue rather than
  // all firing at once. Per-process, i.e. per Lambda container — NOT global.
  let nextSlot = 0;
  async function throttle(): Promise<void> {
    if (minIntervalMs <= 0) return;
    const now = Date.now();
    const at = Math.max(now, nextSlot);
    nextSlot = at + minIntervalMs;
    if (at > now) await new Promise((resolve) => setTimeout(resolve, at - now));
  }

  return {
    async recentSetlist(mbid, now = Date.now()) {
      await throttle();
      // One page only: 20 items is ample recent history, and paging spends
      // daily budget fetching setlists the 180-day bound would reject.
      const res = await doFetch(`${baseUrl}/rest/1.0/artist/${mbid}/setlists?p=1`, {
        headers: {
          "x-api-key": options.apiKey,
          Accept: "application/json", // the API defaults to XML
        },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      // 404 is "this artist has no setlists" — a real answer, not a failure.
      if (res.status === 404) return null;
      if (!res.ok) throw new Error(`setlistfm ${res.status}: ${truncate(await res.text())}`);
      const payload = (await res.json()) as { setlist?: RawSetlist[] };
      return pickRecentSetlist(payload.setlist ?? [], now);
    },
  };
}
```

- [ ] **Step 5: Run the test**

Run: `pnpm vitest run src/mastra/tools/setlistfm.tool.test.ts`
Expected: PASS (all cases).

- [ ] **Step 6: Add the opt-in live check**

Append to `src/mastra/tools/live-apis.test.ts`, inside the existing `live`
describe or a new one:

```ts
live("live setlist.fm", () => {
  it("returns a recent setlist for a touring band", async () => {
    const apiKey = process.env.SETLISTFM_API_KEY;
    if (!apiKey) throw new Error("set SETLISTFM_API_KEY to run this");
    const client = createSetlistFmClient({ apiKey });
    const got = await client.recentSetlist("9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a");
    // May legitimately be null if the band has not toured in 180 days; assert
    // the SHAPE when present rather than requiring data we do not control.
    if (got) {
      expect(got.observedDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      expect(got.songs.length).toBeGreaterThan(0);
    }
  }, 30_000);
});
```

Add `import { createSetlistFmClient } from "./setlistfm.tool.js";` at the top.

- [ ] **Step 7: Commit**

```bash
git add lambda/mastra-handler/src/mastra/tools/setlistfm.tool.ts \
        lambda/mastra-handler/src/mastra/tools/setlistfm.tool.test.ts \
        lambda/mastra-handler/src/mastra/tools/live-apis.test.ts \
        lambda/mastra-handler/src/__fixtures__/setlistfm-artist-setlists.json
git commit -m "feat(lambda): setlist.fm client and recent-setlist selection

setlist.fm has no set times — it is a post-show archive — so this stores
the band's most recent setlist instead: what they have been playing.

Three of the four selection rules exist because the API does not guarantee
them: sort order is undocumented so we sort client-side, dd-MM-yyyy is
parsed explicitly because new Date() misreads it as US ordering, and
songless entries are common enough to treat as absent."
```

---

### Task 10: Wikipedia and MusicBrainz bio sourcing

**Files:**
- Create: `src/mastra/tools/artist-facts.tool.ts`
- Create: `src/mastra/tools/artist-facts.tool.test.ts`
- Modify: `src/mastra/tools/wikimedia.tool.ts:42-46` (expose `resolveQid` on the interface)

**Interfaces:**
- Consumes: `createWikimediaClient` (`src/mastra/tools/wikimedia.tool.ts:88`), `FetchFn`.
- Produces: `fetchWikipediaExtract(qid, opts)` returning `{ title, url, revisionId, text } | null`; `fetchReleaseGroups(mbid, opts)` returning `{ title, year }[]`; `MAX_EXTRACT_CHARS`.

- [ ] **Step 1: Expose resolveQid**

`resolveQid` is currently a closure inside `createWikimediaClient`. Add it to
the exported interface — additive, disturbing no existing behavior:

```ts
export interface WikimediaClient {
  resolveImageCandidates(mbid: string, opts?: { artistName?: string }): Promise<ImageCandidate[]>;
  /** Raw bytes of the candidate's thumbnail. The caller decides where they land. */
  fetchImageBytes(candidate: ImageCandidate): Promise<Buffer>;
  /** MBID -> Wikidata QID via the reverse P434 index. Exposed for the bio
   * workflow, which needs the QID to reach the enwiki sitelink. */
  resolveQid(mbid: string): Promise<string | null>;
}
```

Then add `resolveQid,` to the object literal the factory returns (the closure
already exists at line ~105).

- [ ] **Step 2: Write the failing test**

```ts
// src/mastra/tools/artist-facts.tool.test.ts
import { describe, expect, it } from "vitest";
import { stubFetch } from "./stub-fetch.js";
import { MAX_EXTRACT_CHARS, fetchReleaseGroups, fetchWikipediaExtract } from "./artist-facts.tool.js";

describe("fetchWikipediaExtract", () => {
  it("resolves the enwiki sitelink then fetches the extract and revision id", async () => {
    const fetchFn = stubFetch([
      {
        match: /wbgetentities/,
        json: { entities: { Q123: { sitelinks: { enwiki: { title: "La Luz (band)" } } } } },
      },
      {
        match: /en\.wikipedia\.org/,
        json: {
          query: {
            pages: {
              "42": {
                pageid: 42,
                title: "La Luz (band)",
                extract: "La Luz is an American surf rock band formed in Seattle in 2012.",
                revisions: [{ revid: 987654 }],
              },
            },
          },
        },
      },
    ]);

    const got = await fetchWikipediaExtract("Q123", { fetchFn });
    expect(got?.title).toBe("La Luz (band)");
    expect(got?.revisionId).toBe(987654);
    expect(got?.text).toContain("surf rock");
    expect(got?.url).toBe("https://en.wikipedia.org/wiki/La_Luz_(band)");
  });

  it("returns null when the entity has no enwiki sitelink", async () => {
    const fetchFn = stubFetch([{ match: /wbgetentities/, json: { entities: { Q123: { sitelinks: {} } } } }]);
    expect(await fetchWikipediaExtract("Q123", { fetchFn })).toBeNull();
  });

  it("truncates a very long extract", async () => {
    const long = "x".repeat(MAX_EXTRACT_CHARS * 2);
    const fetchFn = stubFetch([
      { match: /wbgetentities/, json: { entities: { Q1: { sitelinks: { enwiki: { title: "T" } } } } } },
      {
        match: /en\.wikipedia\.org/,
        json: { query: { pages: { "1": { title: "T", extract: long, revisions: [{ revid: 1 }] } } } },
      },
    ]);
    const got = await fetchWikipediaExtract("Q1", { fetchFn });
    expect(got!.text.length).toBeLessThanOrEqual(MAX_EXTRACT_CHARS);
  });
});

describe("fetchReleaseGroups", () => {
  // Album titles and years are the part of a generated bio most likely to be
  // confidently wrong, so they come from structured data rather than from a
  // model reading prose.
  it("returns albums with their first-release years, newest first", async () => {
    const fetchFn = stubFetch([
      {
        match: /release-groups/,
        json: {
          "release-groups": [
            { title: "It's Alive", "primary-type": "Album", "first-release-date": "2013-10-15" },
            { title: "Floating Features", "primary-type": "Album", "first-release-date": "2018-05-11" },
            { title: "Sure As Spring", "primary-type": "Single", "first-release-date": "2021-01-01" },
            { title: "Untitled", "primary-type": "Album" },
          ],
        },
      },
    ]);

    const got = await fetchReleaseGroups("mbid-1", { fetchFn, minIntervalMs: 0 });
    expect(got).toEqual([
      { title: "Floating Features", year: "2018" },
      { title: "It's Alive", year: "2013" },
    ]);
  });

  it("returns an empty list rather than throwing when there are none", async () => {
    const fetchFn = stubFetch([{ match: /release-groups/, json: { "release-groups": [] } }]);
    expect(await fetchReleaseGroups("mbid-1", { fetchFn, minIntervalMs: 0 })).toEqual([]);
  });
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `pnpm vitest run src/mastra/tools/artist-facts.tool.test.ts`
Expected: FAIL — cannot resolve `./artist-facts.tool.js`.

- [ ] **Step 4: Implement**

```ts
// src/mastra/tools/artist-facts.tool.ts
import { USER_AGENT } from "./band-image.js";
import type { FetchFn } from "./musicbrainz.tool.js";

const WIKIDATA_BASE = "https://www.wikidata.org";
const WIKIPEDIA_BASE = "https://en.wikipedia.org";
const MUSICBRAINZ_BASE = "https://musicbrainz.org";
const TIMEOUT_MS = 15_000;

/** Full plain-text extracts for major artists run to 100KB+. ~16KB is roughly
 * 4K tokens, which is ample for a 150-250 word bio. */
export const MAX_EXTRACT_CHARS = 16_000;

export interface WikipediaExtract {
  title: string;
  url: string;
  revisionId: number;
  text: string;
}

interface FactsOptions {
  fetchFn?: FetchFn;
  wikidataBaseUrl?: string;
  wikipediaBaseUrl?: string;
}

async function getJson(url: string, doFetch: FetchFn): Promise<any> {
  const res = await doFetch(url, {
    headers: { "User-Agent": USER_AGENT, Accept: "application/json" },
    signal: AbortSignal.timeout(TIMEOUT_MS),
  });
  if (!res.ok) throw new Error(`${new URL(url).host} ${res.status}`);
  return res.json();
}

/** QID -> enwiki article -> plain-text extract + revision id.
 * Null means the artist has no English Wikipedia article, which is a real
 * answer ('none') rather than a failure. */
export async function fetchWikipediaExtract(
  qid: string,
  opts: FactsOptions = {},
): Promise<WikipediaExtract | null> {
  const doFetch = opts.fetchFn ?? globalThis.fetch;
  const wikidata = opts.wikidataBaseUrl ?? WIKIDATA_BASE;
  const wikipedia = opts.wikipediaBaseUrl ?? WIKIPEDIA_BASE;

  const entities = await getJson(
    `${wikidata}/w/api.php?${new URLSearchParams({
      action: "wbgetentities",
      ids: qid,
      props: "sitelinks",
      sitefilter: "enwiki",
      format: "json",
    })}`,
    doFetch,
  );
  const title: string | undefined = entities?.entities?.[qid]?.sitelinks?.enwiki?.title;
  if (!title) return null;

  // extracts + revisions in ONE call rather than two round trips.
  const page = await getJson(
    `${wikipedia}/w/api.php?${new URLSearchParams({
      action: "query",
      prop: "extracts|revisions",
      rvprop: "ids",
      explaintext: "1",
      redirects: "1",
      titles: title,
      format: "json",
    })}`,
    doFetch,
  );
  const pages = page?.query?.pages ?? {};
  const first: any = Object.values(pages)[0];
  if (!first || first.missing !== undefined || !first.extract) return null;

  return {
    title: first.title ?? title,
    url: `${wikipedia}/wiki/${encodeURIComponent((first.title ?? title).replace(/ /g, "_"))}`,
    revisionId: first.revisions?.[0]?.revid ?? 0,
    text: String(first.extract).slice(0, MAX_EXTRACT_CHARS),
  };
}

export interface ReleaseGroup {
  title: string;
  year: string;
}

/** Albums with first-release years, newest first. Singles, EPs and undated
 * entries are dropped: this exists to give the model FACTS it cannot get wrong,
 * so anything ambiguous is worse than absent. */
export async function fetchReleaseGroups(
  mbid: string,
  opts: { fetchFn?: FetchFn; baseUrl?: string; minIntervalMs?: number } = {},
): Promise<ReleaseGroup[]> {
  const doFetch = opts.fetchFn ?? globalThis.fetch;
  const baseUrl = opts.baseUrl ?? MUSICBRAINZ_BASE;
  const wait = opts.minIntervalMs ?? 1000; // MusicBrainz allows ~1 req/sec
  if (wait > 0) await new Promise((r) => setTimeout(r, wait));

  const payload = await getJson(
    `${baseUrl}/ws/2/artist/${mbid}?${new URLSearchParams({ inc: "release-groups", fmt: "json" })}`,
    doFetch,
  );
  const groups: any[] = payload?.["release-groups"] ?? [];
  return groups
    .filter((g) => g["primary-type"] === "Album" && typeof g["first-release-date"] === "string")
    .map((g) => ({ title: String(g.title), year: String(g["first-release-date"]).slice(0, 4) }))
    .filter((g) => /^\d{4}$/.test(g.year))
    .sort((a, b) => b.year.localeCompare(a.year));
}
```

- [ ] **Step 5: Run and commit**

Run: `pnpm vitest run src/mastra/tools/artist-facts.tool.test.ts`
Expected: PASS.

```bash
git add lambda/mastra-handler/src/mastra/tools/artist-facts.tool.ts \
        lambda/mastra-handler/src/mastra/tools/artist-facts.tool.test.ts \
        lambda/mastra-handler/src/mastra/tools/wikimedia.tool.ts
git commit -m "feat(lambda): Wikipedia extract and MusicBrainz release groups

Reuses the existing MBID -> QID reverse P434 hop rather than spending
MusicBrainz budget. Album titles and years come from structured
release-group data because that is the part of a generated bio most
likely to be confidently wrong."
```

---

### Task 11: Bio and tour agents

**Files:**
- Create: `src/mastra/agents/bio-author.agent.ts`
- Create: `src/mastra/agents/tour-blurb.agent.ts`
- Create: `src/mastra/agents/agents.test.ts`

**Interfaces:**
- Produces: `bioAuthorAgent`, `BioOutputSchema`, `tourBlurbAgent`, `TourBlurbSchema`.

- [ ] **Step 1: Write the agents**

```ts
// src/mastra/agents/bio-author.agent.ts
import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const BioOutputSchema = z.object({
  bio: z
    .string()
    .describe("150-250 words of plain markdown covering the band's origins and notable releases. No headings."),
  usable: z
    .boolean()
    .describe("False when the supplied source text is too thin to write an accurate bio. Prefer false over inventing."),
});

export const bioAuthorAgent = new Agent({
  id: "artist-bio-author",
  name: "Artist Bio Author",
  instructions: `You write short, factual artist bios for a concert listings site.

You are given an artist name, an extract from their English Wikipedia article,
and a list of their albums with release years taken from MusicBrainz.

Write 150-250 words of plain markdown covering, in this order: where and when the
band formed and who is in it; their notable releases; and what they are known for
musically. No headings, no bullet lists, no preamble — just prose.

Rules you must not break:
- Use ONLY facts present in the supplied extract and album list. Invent nothing.
- The album list is authoritative for titles and years. If the extract disagrees
  with it, the album list wins.
- Write NOTHING about current or upcoming tours, dates, or venues. That
  information is not in your input and is handled elsewhere.
- If the extract is too thin to write an accurate bio, set usable to false and
  return an empty bio rather than padding it out.`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(BioOutputSchema) } },
});
```

```ts
// src/mastra/agents/tour-blurb.agent.ts
import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const TourBlurbSchema = z.object({
  blurb: z
    .string()
    .describe("One or two sentences on what this band has been playing live lately."),
  usable: z.boolean().describe("False when the supplied setlist data is too thin to say anything concrete."),
});

export const tourBlurbAgent = new Agent({
  id: "artist-tour-blurb",
  name: "Artist Tour Blurb Writer",
  instructions: `You write a one or two sentence note about what a band has been playing live.

You are given: a tour name (sometimes), the date, venue and city of one recent
show, the number of songs they played, and the first few song titles.

Write one or two sentences a fan would find useful before buying a ticket — what
they have been playing lately, and the tour name if there is one.

Rules you must not break:
- Use ONLY the facts supplied. Invent no dates, no venues, no song titles, no
  album references, no band history.
- Do not claim the band WILL play any particular song. The data is one past
  show, not a promise.
- If there is no tour name and fewer than three songs, set usable to false and
  return an empty blurb.`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(TourBlurbSchema) } },
});
```

- [ ] **Step 2: Write a test pinning the instruction constraints**

The instructions are the whole safety mechanism against fabricated tour news, so
pin the clauses that matter. This is a cheap regression net, not a model test —
no LLM call is made.

```ts
// src/mastra/agents/agents.test.ts
import { describe, expect, it } from "vitest";
import { bioAuthorAgent } from "./bio-author.agent.js";
import { tourBlurbAgent } from "./tour-blurb.agent.js";

// Agent.getInstructions() is async in @mastra/core (see
// node_modules/@mastra/core/dist/agent/agent.d.ts:584).
async function instructionsOf(agent: { getInstructions(): unknown }): Promise<string> {
  return String(await agent.getInstructions());
}

describe("bioAuthorAgent", () => {
  it("forbids tour claims — that grounding lives in the tour workflow", async () => {
    expect(await instructionsOf(bioAuthorAgent)).toMatch(/NOTHING about current or upcoming tours/);
  });

  it("makes the album list authoritative over the prose", async () => {
    expect(await instructionsOf(bioAuthorAgent)).toMatch(/album list wins/);
  });
});

describe("tourBlurbAgent", () => {
  it("forbids inventing facts", async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(/Invent no dates/);
  });

  it("forbids promising a future setlist", async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(/not a promise/);
  });
});
```

- [ ] **Step 3: Run and commit**

Run: `pnpm vitest run src/mastra/agents/agents.test.ts`
Expected: PASS.

```bash
git add lambda/mastra-handler/src/mastra/agents/bio-author.agent.ts \
        lambda/mastra-handler/src/mastra/agents/tour-blurb.agent.ts \
        lambda/mastra-handler/src/mastra/agents/agents.test.ts
git commit -m "feat(lambda): bio author and tour blurb agents

The bio agent is forbidden from writing about tours: Wikipedia has no
reliable current tour information, so asking for it there is the most
hallucination-prone thing this feature could do. Tour claims are grounded
in setlist.fm evidence by a separate agent instead."
```

---

### Task 12: The three enrichment workflows

**Files:**
- Create: `src/enrich-image.ts`
- Create: `src/enrich-bio.ts`
- Create: `src/enrich-tour.ts`
- Create: `src/enrich-workflows.test.ts`

**Interfaces:**
- Consumes: `ImageInfo`/`BioInfo`/`TourInfo`/`toWireCredit` (Task 7), `SetlistFmClient` (Task 9), `fetchWikipediaExtract`/`fetchReleaseGroups` (Task 10), `bioAuthorAgent`/`tourBlurbAgent` (Task 11), `imageAnalysisAgent` (`src/mastra/agents/image-analysis.agent.ts`), `resolveImageCandidates`/`fetchImageBytes`.
- Deliberately **not** `judgeBandImageStep`. That is a Mastra workflow step bound to the poster workflow's loop-state schema, and it writes candidate bytes into a per-run artifact directory that its caller must then clean up. Enrichment keeps no bytes at all, so `enrichImage` reuses the same *agent* while walking candidates itself. The step stays untouched for the shipped poster path.
- Produces: `enrichImage(deps, artist): Promise<ImageInfo>`, `enrichBio(deps, artist): Promise<BioInfo>`, `enrichTour(deps, artist, event): Promise<TourInfo>`. Each **never throws**.

- [ ] **Step 1: Write the failing tests**

```ts
// src/enrich-workflows.test.ts
import { describe, expect, it } from "vitest";
import { enrichBio } from "./enrich-bio.js";
import { enrichTour } from "./enrich-tour.js";

const artist = { mbid: "mbid-1", name: "La Luz", disambiguation: "US rock band" };

describe("enrichBio", () => {
  it("returns ok with sources when everything resolves", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => "Q123",
        fetchExtract: async () => ({
          title: "La Luz (band)",
          url: "https://en.wikipedia.org/wiki/La_Luz_(band)",
          revisionId: 987,
          text: "La Luz formed in Seattle in 2012.",
        }),
        fetchAlbums: async () => [{ title: "Floating Features", year: "2018" }],
        writeBio: async () => ({ bio: "La Luz formed in Seattle in 2012.", usable: true }),
        model: "test-model",
      },
      artist,
    );

    expect(got.status).toBe("ok");
    expect(got.bio_md).toContain("Seattle");
    expect(got.sources).toEqual([
      { kind: "wikipedia", title: "La Luz (band)", url: "https://en.wikipedia.org/wiki/La_Luz_(band)", revision_id: 987 },
      { kind: "musicbrainz", mbid: "mbid-1" },
    ]);
  });

  it("is 'none', not 'error', when the artist has no Wikipedia article", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => "Q123",
        fetchExtract: async () => null,
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: "", usable: false }),
        model: "test-model",
      },
      artist,
    );
    expect(got.status).toBe("none");
    expect(got.reason).toMatch(/no English Wikipedia article/);
  });

  it("is 'none' when the model judges the source too thin", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => "Q1",
        fetchExtract: async () => ({ title: "T", url: "u", revisionId: 1, text: "x" }),
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: "", usable: false }),
        model: "test-model",
      },
      artist,
    );
    expect(got.status).toBe("none");
  });

  // A provider outage must not escape: the orchestrator emits the message
  // regardless, and a throw here would take the whole event down with it.
  it("returns 'error' instead of throwing when a fetch fails", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => { throw new Error("wikidata 503"); },
        fetchExtract: async () => null,
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: "", usable: false }),
        model: "test-model",
      },
      artist,
    );
    expect(got.status).toBe("error");
    expect(got.reason).toMatch(/wikidata 503/);
  });

  it("is 'none' with no MBID and spends no calls", async () => {
    let called = false;
    const got = await enrichBio(
      {
        resolveQid: async () => { called = true; return null; },
        fetchExtract: async () => null,
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: "", usable: false }),
        model: "test-model",
      },
      { ...artist, mbid: "" },
    );
    expect(got.status).toBe("none");
    expect(called).toBe(false);
  });
});

describe("enrichTour", () => {
  const evt = { venue: "The Chapel", date: "2026-09-02" };
  const setlist = {
    setlistId: "s1",
    setlistUrl: "https://setlist.fm/x",
    tourName: "News of the Universe Tour",
    songs: [{ name: "Sure As Spring" }, { name: "Cicada" }, { name: "Strange World", encore: 1 }],
    observedDate: "2026-07-14",
    observedVenue: "The Greek",
    observedCity: "Berkeley",
  };

  it("returns ok with the setlist and blurb", async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => setlist,
        writeBlurb: async () => ({ blurb: "Out on the News of the Universe Tour.", usable: true }),
        model: "test-model",
      },
      artist,
      evt,
    );
    expect(got.status).toBe("ok");
    expect(got.tour_name).toBe("News of the Universe Tour");
    expect(got.songs).toHaveLength(3);
    expect(got.observed_date).toBe("2026-07-14");
    expect(got.blurb).toContain("News of the Universe");
  });

  // The setlist landed and is worth serving on its own.
  it("stays ok with a null blurb when the blurb call fails", async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => setlist,
        writeBlurb: async () => { throw new Error("529 overloaded"); },
        model: "test-model",
      },
      artist,
      evt,
    );
    expect(got.status).toBe("ok");
    expect(got.songs).toHaveLength(3);
    expect(got.blurb).toBeUndefined();
  });

  it("is 'none' when there is no qualifying setlist", async () => {
    const got = await enrichTour(
      { recentSetlist: async () => null, writeBlurb: async () => ({ blurb: "", usable: false }), model: "m" },
      artist,
      evt,
    );
    expect(got.status).toBe("none");
  });

  it("is 'error' when setlist.fm rate-limits", async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => { throw new Error("setlistfm 429: rate limited"); },
        writeBlurb: async () => ({ blurb: "", usable: false }),
        model: "m",
      },
      artist,
      evt,
    );
    expect(got.status).toBe("error");
    expect(got.reason).toMatch(/429/);
  });

  it("is 'none' with no MBID and spends no request", async () => {
    let called = false;
    const got = await enrichTour(
      {
        recentSetlist: async () => { called = true; return null; },
        writeBlurb: async () => ({ blurb: "", usable: false }),
        model: "m",
      },
      { ...artist, mbid: "" },
      evt,
    );
    expect(got.status).toBe("none");
    expect(called).toBe(false);
  });
});
```

- [ ] **Step 2: Run to verify they fail**

Run: `pnpm vitest run src/enrich-workflows.test.ts`
Expected: FAIL — cannot resolve `./enrich-bio.js`.

- [ ] **Step 3: Implement the bio workflow**

```ts
// src/enrich-bio.ts
import { bioAuthorAgent, type BioOutputSchema } from "./mastra/agents/bio-author.agent.js";
import type { z } from "zod";
import type { BioInfo } from "./enrichment-schema.js";
import { fetchReleaseGroups, fetchWikipediaExtract, type ReleaseGroup, type WikipediaExtract } from "./mastra/tools/artist-facts.tool.js";
import { wikimediaClient } from "./mastra/tools/wikimedia.tool.js";

type BioOutput = z.infer<typeof BioOutputSchema>;

export interface BioDeps {
  resolveQid(mbid: string): Promise<string | null>;
  fetchExtract(qid: string): Promise<WikipediaExtract | null>;
  fetchAlbums(mbid: string): Promise<ReleaseGroup[]>;
  writeBio(prompt: string): Promise<BioOutput>;
  model: string;
}

export interface ArtistRef {
  mbid: string;
  name: string;
  disambiguation?: string;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * Wikipedia prose for the narrative, MusicBrainz release-groups for the facts.
 * NEVER throws: the orchestrator emits the enriched message regardless of what
 * failed, and a throw here would take a perfectly good event down with it.
 */
export async function enrichBio(deps: BioDeps, artist: ArtistRef): Promise<BioInfo> {
  // No MBID means no Wikidata hop is possible. A real answer, not a failure —
  // and it spends no calls.
  if (!artist.mbid) {
    return { status: "none", reason: "no MusicBrainz match, so no Wikidata entity to resolve" };
  }

  try {
    const qid = await deps.resolveQid(artist.mbid);
    if (!qid) return { status: "none", reason: `no Wikidata entity for MBID ${artist.mbid}` };

    const extract = await deps.fetchExtract(qid);
    if (!extract) return { status: "none", reason: `no English Wikipedia article for ${qid}` };

    // Albums are best-effort: a bio from prose alone is still worth having.
    let albums: ReleaseGroup[] = [];
    try {
      albums = await deps.fetchAlbums(artist.mbid);
    } catch (e) {
      console.log(JSON.stringify({ msg: "release-groups-failed", mbid: artist.mbid, error: message(e) }));
    }

    const who = artist.disambiguation ? `${artist.name} (${artist.disambiguation})` : artist.name;
    const albumList = albums.length
      ? albums.map((a) => `- ${a.title} (${a.year})`).join("\n")
      : "(no album data available)";

    const out = await deps.writeBio(
      `Artist: ${who}\n\nWikipedia extract:\n${extract.text}\n\nAlbums (authoritative):\n${albumList}`,
    );

    if (!out.usable || !out.bio.trim()) {
      return { status: "none", reason: "source material too thin for an accurate bio" };
    }

    return {
      status: "ok",
      bio_md: out.bio.trim(),
      model: deps.model,
      sources: [
        { kind: "wikipedia", title: extract.title, url: extract.url, revision_id: extract.revisionId },
        { kind: "musicbrainz", mbid: artist.mbid },
      ],
    };
  } catch (e) {
    return { status: "error", reason: message(e) };
  }
}

/** Production deps. */
export function prodBioDeps(): BioDeps {
  const model = process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5";
  return {
    resolveQid: (mbid) => wikimediaClient.resolveQid(mbid),
    fetchExtract: (qid) => fetchWikipediaExtract(qid),
    fetchAlbums: (mbid) => fetchReleaseGroups(mbid),
    writeBio: async (prompt) => {
      const res = await bioAuthorAgent.generate([{ role: "user", content: prompt }]);
      return (res.object as BioOutput | undefined) ?? { bio: "", usable: false };
    },
    model,
  };
}
```

- [ ] **Step 4: Implement the tour workflow**

```ts
// src/enrich-tour.ts
import type { z } from "zod";
import type { TourInfo } from "./enrichment-schema.js";
import { tourBlurbAgent, type TourBlurbSchema } from "./mastra/agents/tour-blurb.agent.js";
import { createSetlistFmClient, type RecentSetlist } from "./mastra/tools/setlistfm.tool.js";
import type { ArtistRef } from "./enrich-bio.js";

type TourBlurb = z.infer<typeof TourBlurbSchema>;

export interface TourDeps {
  recentSetlist(mbid: string): Promise<RecentSetlist | null>;
  writeBlurb(prompt: string): Promise<TourBlurb>;
  model: string;
}

export interface EventRef {
  venue: string;
  date: string;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * setlist.fm has no set times — it is a post-show archive — so this records the
 * band's most recent qualifying setlist and a blurb grounded ONLY in that.
 * NEVER throws.
 */
export async function enrichTour(deps: TourDeps, artist: ArtistRef, event: EventRef): Promise<TourInfo> {
  // setlist.fm is keyed by MBID. No MBID, no request spent.
  if (!artist.mbid) {
    return { status: "none", reason: "no MusicBrainz match, so no setlist.fm lookup is possible" };
  }

  let setlist: RecentSetlist | null;
  try {
    setlist = await deps.recentSetlist(artist.mbid);
  } catch (e) {
    return { status: "error", reason: message(e) };
  }
  if (!setlist) {
    return { status: "none", reason: "no setlist with songs within the recency window" };
  }

  const base: TourInfo = {
    status: "ok",
    tour_name: setlist.tourName,
    songs: setlist.songs,
    observed_date: setlist.observedDate,
    observed_venue: setlist.observedVenue,
    observed_city: setlist.observedCity,
    setlist_url: setlist.setlistUrl,
  };

  // The blurb is a bonus. A failure here leaves the setlist, which is worth
  // serving on its own — hence status stays 'ok' with blurb absent.
  try {
    const out = await deps.writeBlurb(
      [
        `Band: ${artist.name}`,
        `Upcoming show: ${event.venue} on ${event.date}`,
        setlist.tourName ? `Tour: ${setlist.tourName}` : "Tour: (none listed)",
        `Most recent setlist: ${setlist.observedDate} at ${setlist.observedVenue ?? "unknown venue"}, ${setlist.observedCity ?? "unknown city"}`,
        `Songs played: ${setlist.songs.length}`,
        `First few: ${setlist.songs.slice(0, 5).map((s) => s.name).join(", ")}`,
      ].join("\n"),
    );
    if (out.usable && out.blurb.trim()) {
      base.blurb = out.blurb.trim();
      base.blurb_model = deps.model;
    }
  } catch (e) {
    console.log(JSON.stringify({ msg: "tour-blurb-failed", mbid: artist.mbid, error: message(e) }));
  }

  return base;
}

/** Production deps. Throws if the key was never loaded — the orchestrator
 * catches it and records status 'error'. */
export function prodTourDeps(apiKey: string): TourDeps {
  const client = createSetlistFmClient({ apiKey });
  const model = process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5";
  return {
    recentSetlist: (mbid) => client.recentSetlist(mbid),
    writeBlurb: async (prompt) => {
      const res = await tourBlurbAgent.generate([{ role: "user", content: prompt }]);
      return (res.object as TourBlurb | undefined) ?? { blurb: "", usable: false };
    },
    model,
  };
}
```

- [ ] **Step 5: Implement the image workflow**

```ts
// src/enrich-image.ts
import type { ImageInfo } from "./enrichment-schema.js";
import { toWireCredit } from "./enrichment-schema.js";
import { imageAnalysisAgent, type ImageAnalysisSchema } from "./mastra/agents/image-analysis.agent.js";
import type { z } from "zod";
import type { ImageCandidate } from "./mastra/tools/band-image.js";
import { fetchImageBytes, resolveImageCandidates } from "./mastra/tools/wikimedia.tool.js";
import type { ArtistRef } from "./enrich-bio.js";

type ImageAnalysis = z.infer<typeof ImageAnalysisSchema>;

export const MAX_IMAGE_ATTEMPTS = Number(process.env.MAX_IMAGE_ATTEMPTS ?? 3);

export interface ImageDeps {
  candidates(mbid: string, artistName: string): Promise<ImageCandidate[]>;
  bytes(candidate: ImageCandidate): Promise<Buffer>;
  judge(bytes: Buffer, contentType: string, who: string): Promise<ImageAnalysis | null>;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * Walks Commons candidates until the vision judge accepts one. Unlike the poster
 * path the BYTES are not kept — only the thumbnail URL and its credit are — so
 * nothing is written to disk or S3. NEVER throws.
 */
export async function enrichImage(deps: ImageDeps, artist: ArtistRef): Promise<ImageInfo> {
  if (!artist.mbid) {
    return { status: "none", reason: "no MusicBrainz match, so no Wikidata image to resolve" };
  }

  let candidates: ImageCandidate[];
  try {
    candidates = await deps.candidates(artist.mbid, artist.name);
  } catch (e) {
    return { status: "error", reason: message(e) };
  }
  if (candidates.length === 0) {
    return { status: "none", reason: `no Wikimedia image for '${artist.name}'` };
  }

  const who = artist.disambiguation ? `${artist.name} (${artist.disambiguation})` : artist.name;
  let lastReason = "no candidate accepted";

  for (const candidate of candidates.slice(0, MAX_IMAGE_ATTEMPTS)) {
    let bytes: Buffer;
    try {
      bytes = await deps.bytes(candidate);
    } catch (e) {
      lastReason = `could not fetch ${candidate.file}: ${message(e)}`;
      continue;
    }

    let analysis: ImageAnalysis | null;
    try {
      analysis = await deps.judge(bytes, candidate.contentType, who);
    } catch (e) {
      // A provider outage is an error, not a verdict — stop rather than burning
      // the remaining candidates against a service that is down.
      return { status: "error", reason: `image analysis failed: ${message(e)}` };
    }

    if (analysis?.acceptable) {
      return {
        status: "ok",
        url: candidate.url,
        width: candidate.width,
        height: candidate.height,
        file: candidate.file,
        source: candidate.source,
        credit: toWireCredit(candidate.credit),
      };
    }
    lastReason = analysis?.reason ?? "image analysis returned no result";
  }

  // Attempts exhausted with no acceptance is a real answer, not a failure.
  return { status: "none", reason: lastReason };
}

/** Production deps. */
export function prodImageDeps(): ImageDeps {
  return {
    candidates: (mbid, artistName) => resolveImageCandidates(mbid, { artistName }),
    bytes: (candidate) => fetchImageBytes(candidate),
    judge: async (bytes, contentType, who) => {
      const res = await imageAnalysisAgent.generate([
        {
          role: "user",
          content: [
            { type: "image", image: bytes, mimeType: contentType },
            { type: "text", text: `Performer: ${who}. Is this a usable photo of this performer?` },
          ],
        },
      ]);
      return (res.object as ImageAnalysis | undefined) ?? null;
    },
  };
}
```

- [ ] **Step 6: Add image workflow tests**

```ts
// append to src/enrich-workflows.test.ts
import { enrichImage } from "./enrich-image.js";
import type { ImageCandidate } from "./mastra/tools/band-image.js";

function candidate(file: string): ImageCandidate {
  return {
    file,
    url: `https://upload.wikimedia.org/${file}`,
    width: 640,
    height: 427,
    contentType: "image/jpeg",
    source: "p18",
    credit: { file, descriptionUrl: "https://commons/desc", attributionRequired: true },
  };
}

describe("enrichImage", () => {
  it("returns ok with a snake_case credit block", async () => {
    const got = await enrichImage(
      {
        candidates: async () => [candidate("a.jpg")],
        bytes: async () => Buffer.from("x"),
        judge: async () => ({ acceptable: true, reason: "good", dominantColors: [] }),
      },
      artist,
    );
    expect(got.status).toBe("ok");
    expect(got.url).toBe("https://upload.wikimedia.org/a.jpg");
    expect(got.credit?.description_url).toBe("https://commons/desc");
    expect(got.credit?.attribution_required).toBe(true);
  });

  it("advances to the next candidate after a rejection", async () => {
    let judged = 0;
    const got = await enrichImage(
      {
        candidates: async () => [candidate("a.jpg"), candidate("b.jpg")],
        bytes: async () => Buffer.from("x"),
        judge: async () => {
          judged += 1;
          return { acceptable: judged === 2, reason: "r", dominantColors: [] };
        },
      },
      artist,
    );
    expect(judged).toBe(2);
    expect(got.url).toContain("b.jpg");
  });

  it("is 'none' when every candidate is rejected", async () => {
    const got = await enrichImage(
      {
        candidates: async () => [candidate("a.jpg")],
        bytes: async () => Buffer.from("x"),
        judge: async () => ({ acceptable: false, reason: "album art", dominantColors: [] }),
      },
      artist,
    );
    expect(got.status).toBe("none");
    expect(got.reason).toBe("album art");
  });

  it("is 'error' when the vision provider is down", async () => {
    const got = await enrichImage(
      {
        candidates: async () => [candidate("a.jpg")],
        bytes: async () => Buffer.from("x"),
        judge: async () => { throw new Error("529 overloaded"); },
      },
      artist,
    );
    expect(got.status).toBe("error");
  });
});
```

- [ ] **Step 7: Run and commit**

Run: `pnpm vitest run src/enrich-workflows.test.ts`
Expected: PASS (all cases across the three workflows).

```bash
git add lambda/mastra-handler/src/enrich-image.ts lambda/mastra-handler/src/enrich-bio.ts \
        lambda/mastra-handler/src/enrich-tour.ts lambda/mastra-handler/src/enrich-workflows.test.ts
git commit -m "feat(lambda): the three enrichment workflows

Each is dependency-injected and NEVER throws — the orchestrator emits the
enriched message regardless of what failed, so a throw would take a
perfectly good event down with it.

'none' and 'error' are kept distinct because they drive different retry
TTLs: no Wikipedia article is a real answer, a 503 is not."
```

---

### Task 13: Orchestration

**Files:**
- Create: `src/enrichment.ts`
- Create: `src/enrichment.test.ts`

**Interfaces:**
- Consumes: everything from Tasks 6–12.
- Produces: `pickArtist(performer, deps)`, `enrichEvent(deps, message): Promise<EnrichedMessage>`, `WORKFLOW_BUDGET_MS`. Task 14 calls `enrichEvent`.

- [ ] **Step 1: Write the failing test**

```ts
// src/enrichment.test.ts
import { describe, expect, it, vi } from "vitest";
import { StubEnrichmentCache } from "./enrichment-cache.js";
import { enrichEvent, type EnrichDeps } from "./enrichment.js";
import type { EventMessage } from "./schema.js";

const NOW = Date.parse("2026-08-12T00:00:00Z");

function baseEvent(overrides: Partial<EventMessage> = {}): EventMessage {
  return {
    source_id: "ticketmaster",
    source_event_id: "tm-aaa",
    title: "La Luz at The Chapel",
    starts_at: "2026-09-02T20:00:00Z",
    venue: { name: "The Chapel" },
    performers: ["La Luz", "Opener"],
    ...overrides,
  };
}

function deps(over: Partial<EnrichDeps> = {}): EnrichDeps {
  return {
    cache: new StubEnrichmentCache(),
    searchArtists: async () => [
      { mbid: "mbid-1", name: "La Luz", score: 100, disambiguation: "US rock band", type: "Group", country: "US", beginYear: "2012" },
    ],
    enrichImage: async () => ({ status: "ok", url: "https://img/a.jpg", width: 640, height: 427 }),
    enrichBio: async () => ({ status: "ok", bio_md: "Formed in Seattle." }),
    enrichTour: async () => ({ status: "ok", tour_name: "T", songs: [{ name: "S" }] }),
    now: () => NOW,
    ...over,
  };
}

describe("enrichEvent", () => {
  it("enriches the FIRST performer — the headliner", async () => {
    const searchArtists = vi.fn(async () => [
      { mbid: "mbid-1", name: "La Luz", score: 100 },
    ]);
    const out = await enrichEvent(deps({ searchArtists }), baseEvent());
    expect(searchArtists).toHaveBeenCalledWith("La Luz", expect.anything());
    expect(out.enrichment?.artist?.performer).toBe("La Luz");
    expect(out.enrichment?.artist?.display_name).toBe("La Luz");
    expect(out.enrichment?.artist?.status).toBe("ok");
  });

  it("passes the original event fields through untouched", async () => {
    const out = await enrichEvent(deps(), baseEvent());
    expect(out.title).toBe("La Luz at The Chapel");
    expect(out.venue.name).toBe("The Chapel");
    expect(out.performers).toEqual(["La Luz", "Opener"]);
  });

  it("emits no enrichment at all when the event has no performers", async () => {
    const out = await enrichEvent(deps(), baseEvent({ performers: [] }));
    expect(out.enrichment).toBeUndefined();
  });

  // An unresolvable performer still gets an artist section so the attempt is
  // recorded rather than retried on every scrape.
  it("records status not_found when MusicBrainz has no match", async () => {
    const out = await enrichEvent(deps({ searchArtists: async () => [] }), baseEvent());
    expect(out.enrichment?.artist?.status).toBe("not_found");
    expect(out.enrichment?.artist?.mbid).toBeUndefined();
    expect(out.enrichment?.artist?.display_name).toBe("La Luz");
  });

  it("still emits the event when every workflow fails", async () => {
    const out = await enrichEvent(
      deps({
        enrichImage: async () => ({ status: "error", reason: "a" }),
        enrichBio: async () => ({ status: "error", reason: "b" }),
        enrichTour: async () => ({ status: "error", reason: "c" }),
      }),
      baseEvent(),
    );
    expect(out.title).toBe("La Luz at The Chapel");
    expect(out.enrichment?.bio?.status).toBe("error");
  });

  it("still emits the event when the prelude itself throws", async () => {
    const out = await enrichEvent(
      deps({ searchArtists: async () => { throw new Error("musicbrainz 503"); } }),
      baseEvent(),
    );
    expect(out.title).toBe("La Luz at The Chapel");
    expect(out.enrichment?.artist).toBeUndefined();
  });

  it("skips the image workflow when the event already has an image", async () => {
    const enrichImage = vi.fn(async () => ({ status: "ok" as const }));
    const out = await enrichEvent(
      deps({ enrichImage }),
      baseEvent({ image_url: "https://source/provided.jpg" }),
    );
    expect(enrichImage).not.toHaveBeenCalled();
    expect(out.image_url).toBe("https://source/provided.jpg");
  });

  // Skipping because THIS event had an image is a property of the event, not
  // the artist. Recording it would suppress the image workflow for every later
  // event by that band that has no image at all.
  it("writes no image cache entry when it skipped for that reason", async () => {
    const cache = new StubEnrichmentCache();
    await enrichEvent(deps({ cache }), baseEvent({ image_url: "https://source/provided.jpg" }));
    const entry = await cache.read("La Luz");
    expect(entry?.workflows.image).toBeUndefined();
    expect(entry?.workflows.bio).toBeDefined();
  });

  it("reuses fresh cached payloads instead of re-running", async () => {
    const cache = new StubEnrichmentCache();
    await cache.write("La Luz", {
      artist_key: "la luz",
      performer: "La Luz",
      artist: { performer: "La Luz", display_name: "La Luz", mbid: "mbid-1", status: "ok" },
      workflows: {
        image: { status: "ok", at: new Date(NOW - 1000).toISOString(), payload: { status: "ok", url: "https://cached.jpg" } },
        bio: { status: "ok", at: new Date(NOW - 1000).toISOString(), payload: { status: "ok", bio_md: "cached bio" } },
        tour: { status: "ok", at: new Date(NOW - 1000).toISOString(), payload: { status: "ok", tour_name: "cached tour" } },
      },
    });

    const searchArtists = vi.fn(async () => []);
    const enrichBio = vi.fn(async () => ({ status: "ok" as const, bio_md: "fresh" }));
    const out = await enrichEvent(deps({ cache, searchArtists, enrichBio }), baseEvent());

    // All three fresh AND the artist is cached, so the MusicBrainz call is
    // skipped entirely — that is the whole point of the gate.
    expect(searchArtists).not.toHaveBeenCalled();
    expect(enrichBio).not.toHaveBeenCalled();
    expect(out.enrichment?.bio?.bio_md).toBe("cached bio");
    expect(out.enrichment?.artist?.mbid).toBe("mbid-1");
  });

  it("re-runs a workflow whose cached record has expired", async () => {
    const cache = new StubEnrichmentCache();
    await cache.write("La Luz", {
      artist_key: "la luz",
      performer: "La Luz",
      artist: { performer: "La Luz", display_name: "La Luz", mbid: "mbid-1", status: "ok" },
      workflows: {
        // error TTL is 6h; this is 7h old
        bio: { status: "error", at: new Date(NOW - 7 * 3600_000).toISOString() },
      },
    });
    const enrichBio = vi.fn(async () => ({ status: "ok" as const, bio_md: "fresh" }));
    const out = await enrichEvent(deps({ cache, enrichBio }), baseEvent());
    expect(enrichBio).toHaveBeenCalled();
    expect(out.enrichment?.bio?.bio_md).toBe("fresh");
  });

  it("writes the merged result back to the cache once", async () => {
    const cache = new StubEnrichmentCache();
    const spy = vi.spyOn(cache, "write");
    await enrichEvent(deps({ cache }), baseEvent());
    expect(spy).toHaveBeenCalledTimes(1);
    const entry = await cache.read("La Luz");
    expect(entry?.workflows.bio?.status).toBe("ok");
    expect(entry?.workflows.tour?.payload).toBeDefined();
  });

  it("does not fail the event when the cache write fails", async () => {
    const cache = new StubEnrichmentCache();
    cache.write = async () => { throw new Error("s3 down"); };
    const out = await enrichEvent(deps({ cache }), baseEvent());
    expect(out.enrichment?.bio?.status).toBe("ok");
  });

  // AccessDenied on read must propagate, but the EVENT must still flow — a
  // misconfigured cache should be loud in logs, not a silent ingest outage.
  it("still emits the event when the cache read throws", async () => {
    const cache = new StubEnrichmentCache();
    cache.read = async () => { throw new Error("Access Denied"); };
    const out = await enrichEvent(deps({ cache }), baseEvent());
    expect(out.title).toBe("La Luz at The Chapel");
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/enrichment.test.ts`
Expected: FAIL — cannot resolve `./enrichment.js`.

- [ ] **Step 3: Implement**

```ts
// src/enrichment.ts
import { artistKey } from "./artist-key.js";
import type { CacheEntry, CacheRecord, EnrichmentCache, WorkflowName } from "./enrichment-cache.js";
import { isFresh } from "./enrichment-cache.js";
import type { ArtistInfo, BioInfo, EnrichedMessage, ImageInfo, TourInfo } from "./enrichment-schema.js";
import type { ArtistRef } from "./enrich-bio.js";
import type { ArtistMatch } from "./mastra/tools/band-image.js";
import type { EventMessage } from "./schema.js";

/** Per-workflow slice of the Lambda's 300s timeout, so one hung fetch cannot
 * starve the other two. */
export const WORKFLOW_BUDGET_MS = Number(process.env.ENRICH_BUDGET_MS ?? 120_000);

/** How many MusicBrainz matches to consider. Mirrors MAX_ARTIST_FALLTHROUGH. */
const ARTIST_SEARCH_LIMIT = 3;

export interface EnrichDeps {
  cache: EnrichmentCache;
  searchArtists(performer: string, opts: { limit: number }): Promise<ArtistMatch[]>;
  enrichImage(artist: ArtistRef): Promise<ImageInfo>;
  enrichBio(artist: ArtistRef): Promise<BioInfo>;
  enrichTour(artist: ArtistRef, event: { venue: string; date: string }): Promise<TourInfo>;
  now?(): number;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * Resolve ONE artist for all three workflows.
 *
 * Deliberately takes the best match rather than falling through candidates the
 * way resolveBandCandidatesStep does for posters. Falling through per-workflow
 * would let the bio describe "La Luz, US rock band" beside a photo of "La Luz,
 * Belgium based house group" — the disambiguation field exists because those
 * collide.
 */
export async function pickArtist(
  performer: string,
  deps: Pick<EnrichDeps, "searchArtists">,
): Promise<ArtistInfo> {
  const matches = await deps.searchArtists(performer, { limit: ARTIST_SEARCH_LIMIT });
  const best = matches[0];
  if (!best) {
    // A real answer, not a failure: it gets an artists row with status
    // not_found so the attempt is recorded and not retried every scrape.
    return { performer, display_name: performer, status: "not_found" };
  }
  return {
    performer,
    display_name: best.name,
    mbid: best.mbid,
    disambiguation: best.disambiguation,
    type: best.type,
    country: best.country,
    begin_year: best.beginYear,
    status: "ok",
  };
}

/** Run a workflow under its time budget. A timeout is an 'error', which the
 * cache retries in hours rather than suppressing for months. */
async function withBudget<T extends { status: string; reason?: string }>(
  name: string,
  run: () => Promise<T>,
  fallback: (reason: string) => T,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      run(),
      new Promise<T>((resolve) => {
        timer = setTimeout(
          () => resolve(fallback(`${name} exceeded ${WORKFLOW_BUDGET_MS}ms budget`)),
          WORKFLOW_BUDGET_MS,
        );
      }),
    ]);
  } catch (e) {
    // The workflows are documented never to throw, but a dependency injected by
    // a caller might. Belt and braces: never let this reach the invocation.
    return fallback(message(e));
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function cachedPayload<T>(entry: CacheEntry | null, name: WorkflowName, now: number): T | undefined {
  const rec = entry?.workflows?.[name];
  if (!rec || !isFresh(rec, now)) return undefined;
  return rec.payload as T | undefined;
}

function record(result: { status: string; reason?: string }, at: string): CacheRecord {
  return {
    status: result.status as CacheRecord["status"],
    at,
    reason: result.reason,
    payload: result as CacheRecord["payload"],
  };
}

/**
 * Enrich one normalized event. NEVER throws: the caller must republish the
 * event whatever happened, because enrichment failing is not a reason to stop
 * ingesting a perfectly good show.
 */
export async function enrichEvent(deps: EnrichDeps, event: EventMessage): Promise<EnrichedMessage> {
  const now = deps.now?.() ?? Date.now();
  const performer = event.performers?.[0];
  // Nothing to enrich against. Emit the event exactly as it arrived.
  if (!performer) return { ...event };

  // A cache failure must be loud but non-fatal: a misconfigured bucket should
  // show up in logs, not present as an ingest outage.
  let entry: CacheEntry | null = null;
  try {
    entry = await deps.cache.read(performer);
  } catch (e) {
    console.error(JSON.stringify({ msg: "enrichment-cache-read-failed", performer, error: message(e) }));
  }

  const cachedImage = cachedPayload<ImageInfo>(entry, "image", now);
  const cachedBio = cachedPayload<BioInfo>(entry, "bio", now);
  const cachedTour = cachedPayload<TourInfo>(entry, "tour", now);

  // The event's own image always wins, so there is nothing to resolve when the
  // source supplied one.
  const needsImage = !event.image_url;
  const runImage = needsImage && cachedImage === undefined;
  const runBio = cachedBio === undefined;
  const runTour = cachedTour === undefined;

  // Everything satisfied from cache AND an artist on file: skip the
  // MusicBrainz call entirely. This is the whole point of the gate.
  if (!runImage && !runBio && !runTour && entry?.artist) {
    return {
      ...event,
      enrichment: {
        artist: entry.artist,
        image: cachedImage,
        bio: cachedBio,
        tour: cachedTour,
        attempted_at: new Date(now).toISOString(),
      },
    };
  }

  let artist: ArtistInfo;
  try {
    artist = await pickArtist(performer, deps);
  } catch (e) {
    // The prelude failed, so nothing downstream can run. Emit the bare event;
    // no cache is written, so the next delivery retries immediately.
    console.error(JSON.stringify({ msg: "artist-prelude-failed", performer, error: message(e) }));
    return { ...event };
  }

  const ref: ArtistRef = { mbid: artist.mbid ?? "", name: artist.display_name, disambiguation: artist.disambiguation };
  const eventRef = { venue: event.venue.name, date: event.starts_at.slice(0, 10) };

  const [image, bio, tour] = await Promise.all([
    runImage
      ? withBudget("image", () => deps.enrichImage(ref), (reason) => ({ status: "error" as const, reason }))
      : Promise.resolve(cachedImage),
    runBio
      ? withBudget("bio", () => deps.enrichBio(ref), (reason) => ({ status: "error" as const, reason }))
      : Promise.resolve(cachedBio),
    runTour
      ? withBudget("tour", () => deps.enrichTour(ref, eventRef), (reason) => ({ status: "error" as const, reason }))
      : Promise.resolve(cachedTour),
  ]);

  // ONE write, after the fan-out, so the three workflows never
  // read-modify-write the same object against each other.
  const at = new Date(now).toISOString();
  const workflows: CacheEntry["workflows"] = { ...(entry?.workflows ?? {}) };
  // Note the image branch: a skip caused by the EVENT having its own picture
  // records nothing, because that fact says nothing about the artist.
  if (runImage && image) workflows.image = record(image, at);
  if (runBio && bio) workflows.bio = record(bio, at);
  if (runTour && tour) workflows.tour = record(tour, at);

  try {
    await deps.cache.write(performer, {
      artist_key: artistKey(performer),
      performer,
      artist,
      workflows,
    });
  } catch (e) {
    console.error(JSON.stringify({ msg: "enrichment-cache-write-failed", performer, error: message(e) }));
  }

  return {
    ...event,
    enrichment: { artist, image, bio, tour, attempted_at: at },
  };
}
```

- [ ] **Step 4: Run the test**

Run: `pnpm vitest run src/enrichment.test.ts`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add lambda/mastra-handler/src/enrichment.ts lambda/mastra-handler/src/enrichment.test.ts
git commit -m "feat(lambda): enrichment orchestration

One artist resolution shared by all three workflows: falling through
candidates per-workflow would pair a bio about one band with a photo of a
different band that shares its name.

Everything fresh in cache skips the MusicBrainz call entirely. A skip
caused by the event having its own image writes no cache entry, because
that fact is about the event, not the artist."
```

---

### Task 14: Handler SQS branch

**Files:**
- Modify: `src/handler.ts:72-142`
- Create: `src/handler.sqs.test.ts`
- Modify: `src/sqs.ts` (generalize `sendBatch`'s parameter type)

**Interfaces:**
- Consumes: `enrichEvent` (Task 13).
- Produces: `isSQSEvent(event)`, `handleSQS(event, deps)`, `prodEnrichDeps()`.

- [ ] **Step 1: Write the failing test**

```ts
// src/handler.sqs.test.ts
import { describe, expect, it, vi } from "vitest";
import { handleSQS, isFunctionUrlEvent, isSQSEvent } from "./handler.js";
import type { EventMessage } from "./schema.js";

const sqsEvent = {
  Records: [
    {
      messageId: "m1",
      eventSource: "aws:sqs",
      body: JSON.stringify({
        source_id: "ticketmaster",
        source_event_id: "tm-aaa",
        title: "La Luz",
        starts_at: "2026-09-02T20:00:00Z",
        venue: { name: "The Chapel" },
        performers: ["La Luz"],
      }),
    },
  ],
};

const s3Event = {
  Records: [{ eventSource: "aws:s3", s3: { bucket: { name: "b" }, object: { key: "k" } } }],
};

describe("isSQSEvent", () => {
  // The S3 branch also uses `Records`, so presence of that key cannot be the
  // discriminator — handler.ts previously cast anything non-FunctionURL to
  // S3Event, which would have happily accepted an SQS event.
  it("distinguishes SQS from S3 by eventSource", () => {
    expect(isSQSEvent(sqsEvent as never)).toBe(true);
    expect(isSQSEvent(s3Event as never)).toBe(false);
  });

  it("does not claim a Function URL event", () => {
    const url = { version: "2.0", requestContext: { http: { method: "POST" } } };
    expect(isSQSEvent(url as never)).toBe(false);
    expect(isFunctionUrlEvent(url as never)).toBe(true);
  });
});

describe("handleSQS", () => {
  it("enriches each record and emits it to the enriched queue", async () => {
    const emitted: EventMessage[] = [];
    await handleSQS(sqsEvent as never, {
      enrich: async (m) => ({ ...m, enrichment: { attempted_at: "2026-08-12T00:00:00Z", artist: { performer: "La Luz", display_name: "La Luz", status: "ok" } } }),
      emit: async (msgs) => { emitted.push(...msgs); },
    });

    expect(emitted).toHaveLength(1);
    expect(emitted[0].title).toBe("La Luz");
    expect((emitted[0] as { enrichment?: unknown }).enrichment).toBeDefined();
  });

  // Malformed body: drop it rather than throwing. A throw returns the message
  // to the queue, and a body that will never parse would cycle to the DLQ
  // three deliveries later having burned three enrichment attempts.
  it("drops an unparseable body without throwing", async () => {
    const emit = vi.fn(async () => {});
    await handleSQS({ Records: [{ eventSource: "aws:sqs", body: "not json" }] } as never, {
      enrich: async (m) => m,
      emit,
    });
    expect(emit).not.toHaveBeenCalled();
  });

  it("drops a body that is not a valid EventMessage", async () => {
    const emit = vi.fn(async () => {});
    await handleSQS({ Records: [{ eventSource: "aws:sqs", body: JSON.stringify({ nope: 1 }) }] } as never, {
      enrich: async (m) => m,
      emit,
    });
    expect(emit).not.toHaveBeenCalled();
  });

  // A send failure MUST throw: at batch_size 1 that returns the message to the
  // queue, which is the only retry mechanism this path has.
  it("propagates an emit failure", async () => {
    await expect(
      handleSQS(sqsEvent as never, {
        enrich: async (m) => m,
        emit: async () => { throw new Error("sqs down"); },
      }),
    ).rejects.toThrow(/sqs down/);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/handler.sqs.test.ts`
Expected: FAIL — `isSQSEvent` is not exported.

- [ ] **Step 3: Generalize sendBatch**

`sendBatch` is typed to `EventMessage[]`. Widen it so it can carry enriched
messages too — the function body already only calls `JSON.stringify`:

```ts
// src/sqs.ts — change the signature only
export async function sendBatch(
  sqs: SQSClient,
  queueUrl: string,
  messages: readonly unknown[],
): Promise<void> {
```

and delete the now-unused `EventMessage` import.

- [ ] **Step 4: Add the branch to handler.ts**

```ts
// add near isFunctionUrlEvent
import type { SQSEvent } from "aws-lambda";
import { EventMessageSchema } from "./schema.js";
import { enrichEvent, type EnrichDeps } from "./enrichment.js";
// NOT StubEnrichmentCache — the repo lints unused vars as an error.
import { S3EnrichmentCache } from "./enrichment-cache.js";
import { prodBioDeps, enrichBio } from "./enrich-bio.js";
import { prodTourDeps, enrichTour } from "./enrich-tour.js";
import { prodImageDeps, enrichImage } from "./enrich-image.js";
import { searchArtists } from "./mastra/tools/musicbrainz.tool.js";

/** True when the invocation came from the SQS event source mapping.
 * Discriminates on eventSource, NOT on the presence of Records: the S3 branch
 * uses Records too. */
export function isSQSEvent(event: HandlerEvent): event is SQSEvent {
  const recs = (event as SQSEvent).Records;
  return Array.isArray(recs) && recs.length > 0 && recs[0]?.eventSource === "aws:sqs";
}

export interface SQSDeps {
  enrich(message: EventMessage): Promise<unknown>;
  emit(messages: unknown[]): Promise<void>;
}

/** Enrichment path: one normalized event in, one enriched event out.
 * The event source mapping uses batch_size 1, so this loop sees a single
 * record in practice; it iterates anyway rather than assuming. */
export async function handleSQS(event: SQSEvent, deps: SQSDeps): Promise<void> {
  const out: unknown[] = [];
  for (const rec of event.Records) {
    let parsed;
    try {
      parsed = EventMessageSchema.parse(JSON.parse(rec.body));
    } catch (e) {
      // Unparseable bodies never become parseable. Throwing would return the
      // message and burn three enrichment attempts before the DLQ takes it.
      console.error(JSON.stringify({
        msg: "enrichment-bad-message",
        messageId: rec.messageId,
        error: e instanceof Error ? e.message : String(e),
      }));
      continue;
    }
    out.push(await deps.enrich(parsed));
  }
  if (out.length === 0) return;
  // A send failure throws: at batch_size 1 that returns the message to the
  // queue, which is this path's only retry mechanism.
  await deps.emit(out);
}

function prodEnrichDeps(): EnrichDeps {
  const bucket = requireEnv("ENRICHMENT_CACHE_BUCKET");
  const bio = prodBioDeps();
  const image = prodImageDeps();
  const tour = prodTourDeps(setlistFmKey);
  return {
    cache: new S3EnrichmentCache(new S3Client({ region: process.env.AWS_REGION }), bucket),
    searchArtists: (performer, opts) => searchArtists(performer, opts),
    enrichImage: (artist) => enrichImage(image, artist),
    enrichBio: (artist) => enrichBio(bio, artist),
    enrichTour: (artist, evt) => enrichTour(tour, artist, evt),
  };
}
```

- [ ] **Step 5: Load the setlist.fm key and wire the branch**

At module scope in `handler.ts`, beside the existing model-key bootstrap:

```ts
let setlistFmKey = "";
```

In the `handler` body, after the `loadModelKey` line:

```ts
    const setlistSecret = process.env.SETLISTFM_API_KEY_SECRET;
    if (setlistSecret && !setlistFmKey) {
      setlistFmKey = await new AwsSecretReader(process.env.AWS_REGION).getSecretValue(setlistSecret);
    }
```

Then add the branch **between** the Function URL check and the S3
fallback:

```ts
    if (isSQSEvent(event)) {
      const region = requireEnv("AWS_REGION");
      const queueUrl = requireEnv("ENRICHED_EVENTS_QUEUE_URL");
      const sqs = new SQSClient({ region, endpoint: process.env.SQS_ENDPOINT || undefined });
      const enrichDeps = prodEnrichDeps();
      await handleSQS(event, {
        enrich: (m) => enrichEvent(enrichDeps, m),
        emit: (msgs) => sendBatch(sqs, queueUrl, msgs),
      });
      responseStream.end();
      return;
    }
```

- [ ] **Step 6: Run the Lambda suite**

Run: `pnpm test`
Expected: PASS — every existing test plus the new ones. Then
`pnpm typecheck` must also pass.

- [ ] **Step 7: Commit**

```bash
git add lambda/mastra-handler/src/handler.ts lambda/mastra-handler/src/handler.sqs.test.ts \
        lambda/mastra-handler/src/sqs.ts
git commit -m "feat(lambda): SQS enrichment branch

isSQSEvent discriminates on Records[0].eventSource, not on the presence of
Records — the S3 branch uses Records too, and the previous unguarded cast
would have accepted an SQS event as an S3 one.

An unparseable body is dropped rather than thrown: it will never become
parseable, and throwing would burn three enrichment attempts before the
DLQ takes it."
```

---

# Phase 3 — Infrastructure and cutover

### Task 15: Terraform, apply 1

Everything here is inert: new queues nobody reads, a new bucket nobody writes,
and env vars on a Lambda whose new branch is unreachable without the event
source mapping added in Task 17.

**Files:**
- Modify: `terraform/prod/sqs.tf`
- Create: `terraform/prod/enrichment.tf`
- Modify: `terraform/prod/lambda_mastra_handler.tf:29-97`
- Modify: `terraform/prod/iam.tf` (ECS task role)
- Modify: `terraform/prod/ecs_api.tf:22` (api_env_vars)
- Modify: `terraform/prod/outputs.tf`

**Interfaces:**
- Produces: `aws_sqs_queue.events_enriched`, `aws_s3_bucket.enrichment_cache`, `aws_secretsmanager_secret.setlistfm_key`. Task 17 consumes the queue ARN.

- [ ] **Step 1: Raise the events queue visibility timeout**

In `terraform/prod/sqs.tf`, change `aws_sqs_queue.events`:

```hcl
resource "aws_sqs_queue" "events" {
  name = "${var.app_name_prefix}-events-queue"
  # Lambda refuses an event source mapping unless this is at least the function
  # timeout (300s). AWS guidance is 6x, and enrichment runs three LLM workflows
  # per message, so a generous window costs nothing here.
  visibility_timeout_seconds = 900
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 345600

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.events_dlq.arn
    maxReceiveCount     = 3
  })
}
```

- [ ] **Step 2: Add the enriched queue, cache bucket and secret**

```hcl
# terraform/prod/enrichment.tf

resource "aws_sqs_queue" "events_enriched_dlq" {
  name                      = "${var.app_name_prefix}-events-enriched-dlq"
  message_retention_seconds = 1209600 # 14 days
}

# Enriched events, read by the ECS ingest consumer. Same shape as the events
# queue: the consumer is an ordinary long-polling reader, not a Lambda trigger,
# so 30s visibility is right here.
resource "aws_sqs_queue" "events_enriched" {
  name                       = "${var.app_name_prefix}-events-enriched-queue"
  visibility_timeout_seconds = 30
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 345600

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.events_enriched_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_cloudwatch_metric_alarm" "events_enriched_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-events-enriched-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.events_enriched_dlq.name }
  alarm_description   = "Messages landed in the enriched-events DLQ. Check consumer logs."
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

# Skip cache for enrichment attempts. Its OWN bucket rather than a prefix in the
# posters bucket: iam.tf grants the ECS task role ${posters.arn}/* , so a prefix
# there would hand the API read/write on a cache it has no business touching.
resource "aws_s3_bucket" "enrichment_cache" {
  bucket = "${var.app_name_prefix}-enrichment-cache-${data.aws_caller_identity.current.account_id}"
  tags   = { App = var.app_name_prefix }
}

resource "aws_s3_bucket_public_access_block" "enrichment_cache" {
  bucket                  = aws_s3_bucket.enrichment_cache.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "enrichment_cache" {
  bucket = aws_s3_bucket.enrichment_cache.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# setlist.fm API key. NOTE: the free key is non-commercial only and capped at
# 1,440 requests/day; the upgrade path is a manual request to setlist.fm.
resource "aws_secretsmanager_secret" "setlistfm_key" {
  name = "${var.app_name_prefix}-setlistfm-api-key"
}
```

- [ ] **Step 3: Extend the Lambda IAM policy and environment**

In `terraform/prod/lambda_mastra_handler.tf`, add to
`data.aws_iam_policy_document.mastra_handler`:

```hcl
  statement {
    sid = "ConsumeEventsQueue"
    # The event source mapping polls with the FUNCTION's role, so these three
    # actions are what make the trigger work at all.
    actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.events.arn]
  }
  statement {
    sid       = "SendToEnrichedQueue"
    actions   = ["sqs:SendMessage", "sqs:SendMessageBatch", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.events_enriched.arn]
  }
  statement {
    sid       = "ReadWriteEnrichmentCache"
    actions   = ["s3:GetObject", "s3:PutObject"]
    resources = ["${aws_s3_bucket.enrichment_cache.arn}/*"]
  }
  statement {
    sid       = "ReadSetlistFmKey"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.setlistfm_key.arn]
  }
```

and to the function's `environment.variables`:

```hcl
      ENRICHED_EVENTS_QUEUE_URL = aws_sqs_queue.events_enriched.url
      ENRICHMENT_CACHE_BUCKET   = aws_s3_bucket.enrichment_cache.bucket
      SETLISTFM_API_KEY_SECRET  = aws_secretsmanager_secret.setlistfm_key.arn
```

- [ ] **Step 4: Grant the ECS task role the enriched queue**

`terraform/prod/iam.tf:63-66` already lists the four queue ARNs the task role
may use. Add the two new ones:

```hcl
    resources = [
      aws_sqs_queue.events.arn,
      aws_sqs_queue.events_dlq.arn,
      aws_sqs_queue.events_enriched.arn,
      aws_sqs_queue.events_enriched_dlq.arn,
      aws_sqs_queue.interests.arn,
      aws_sqs_queue.interests_dlq.arn,
    ]
```

Keep the old queue in place — the consumer only switches in Task 16, and a
rollback needs the permission still there.

- [ ] **Step 5: Add the env var to the ECS task family**

In `terraform/prod/ecs_api.tf`, beside the existing `EVENTS_QUEUE_URL` entry:

```hcl
    { name = "ENRICHED_EVENTS_QUEUE_URL", value = aws_sqs_queue.events_enriched.url },
```

`local.scheduled_env_vars = local.api_env_vars`, so the scheduled families
inherit this automatically.

- [ ] **Step 6: Add outputs**

In `terraform/prod/outputs.tf`:

```hcl
output "events_enriched_queue_url" {
  description = "URL of the enriched-events queue the ingest consumer reads."
  value       = aws_sqs_queue.events_enriched.url
}

output "enrichment_cache_bucket" {
  description = "Bucket holding the per-artist enrichment skip cache."
  value       = aws_s3_bucket.enrichment_cache.id
}
```

- [ ] **Step 7: Validate**

Run:
```bash
cd terraform/prod && terraform fmt -check && terraform validate
```
Expected: both exit 0. Then `terraform plan` and confirm the plan **creates**
the queue/bucket/secret/alarm and **modifies** the events queue's visibility
timeout — and does not replace the events queue. A visibility-timeout change is
an in-place update; if the plan says "must be replaced", stop and investigate,
because replacing that queue drops in-flight messages.

- [ ] **Step 8: Commit**

```bash
git add terraform/prod/
git commit -m "feat(infra): enriched queue, enrichment cache bucket, setlist.fm secret

Inert until Task 17 adds the event source mapping. The events queue's
visibility timeout goes to 900s because Lambda refuses an ESM unless it is
at least the 300s function timeout.

The cache gets its own bucket rather than a posters/ prefix: the ECS task
role already holds posters/* and has no business touching this."
```

---

### Task 16: Consumer cutover

**Files:**
- Modify: `internal/config/config.go:202` and the `Config` struct
- Modify: `internal/config/config_test.go`
- Modify: `cmd/app/main.go:133-136`
- Modify: `.env.example`, `lambda/mastra-handler/.env.example`
- Modify: `scripts/elasticmq.conf`

**Interfaces:**
- Produces: `cfg.EnrichedEventsQueueURL`.

- [ ] **Step 1: Write the failing config test**

Add to `internal/config/config_test.go`, inside the test that already sets
`EVENTS_QUEUE_URL`:

```go
	t.Setenv("ENRICHED_EVENTS_QUEUE_URL", "http://localhost:9324/000000000000/events-enriched-queue")
```

and the matching assertion:

```go
	require.Equal(t, "http://localhost:9324/000000000000/events-enriched-queue", cfg.EnrichedEventsQueueURL)
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `cfg.EnrichedEventsQueueURL undefined`.

- [ ] **Step 3: Add the field**

In `internal/config/config.go`, add to the `Config` struct beside
`EventsQueueURL`:

```go
	// EnrichedEventsQueueURL is the queue the ingest consumer reads. Events
	// reach it after the mastra-handler Lambda enriches them off
	// EventsQueueURL, which scrapers still publish to.
	EnrichedEventsQueueURL string
```

and to the struct literal in `Load`:

```go
		EnrichedEventsQueueURL: os.Getenv("ENRICHED_EVENTS_QUEUE_URL"),
```

- [ ] **Step 4: Run the config test**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Point the consumer at the enriched queue**

In `cmd/app/main.go`, the SQS client is built when *either* queue URL is set;
widen that guard and switch the consumer:

```go
	var qClient *queue.Client
	if cfg.EventsQueueURL != "" || cfg.EnrichedEventsQueueURL != "" || cfg.InterestsQueueURL != "" {
		qClient, err = queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
		if err != nil {
			return fmt.Errorf("queue client: %w", err)
		}
	}

	var consumer *ingest.Consumer
	// Reads ENRICHED events. Scrapers still publish to EventsQueueURL; the
	// mastra-handler Lambda enriches from there onto this queue. A message
	// without an enrichment block still applies exactly as it used to, which is
	// what makes this switch reversible.
	if cfg.EnrichedEventsQueueURL != "" {
		h := ingest.NewEventHandler(q, city.ID)
		consumer = ingest.NewConsumer(qClient, cfg.EnrichedEventsQueueURL, h, cfg.IngestWorkers, "events-enriched")
	}
```

`EventsQueueURL` stays in the config — the scrapers still publish to it via
`main.go:308`.

- [ ] **Step 6: Update the local queue definitions**

In `scripts/elasticmq.conf`, add beside the existing `events-queue` block:

```
  events-enriched-queue {
    defaultVisibilityTimeout = 30 seconds
    receiveMessageWait       = 20 seconds
    deadLettersQueue {
      name              = events-enriched-dlq
      maxReceiveCount   = 3
    }
  }
  events-enriched-dlq {}
```

Place it after the `events-dlq {}` line, matching the existing `events-queue`
block's shape exactly.

- [ ] **Step 7: Update the env examples**

In `.env.example`, beside `EVENTS_QUEUE_URL`:

```
# Scrapers publish here; the mastra-handler Lambda enriches from it.
EVENTS_QUEUE_URL=http://localhost:9324/000000000000/events-queue
# The ingest consumer reads here.
ENRICHED_EVENTS_QUEUE_URL=http://localhost:9324/000000000000/events-enriched-queue
```

In `lambda/mastra-handler/.env.example`:

```
EVENTS_QUEUE_URL=
ENRICHED_EVENTS_QUEUE_URL=
ENRICHMENT_CACHE_BUCKET=
SETLISTFM_API_KEY_SECRET=
```

- [ ] **Step 8: Run everything and commit**

Run: `make test`
Expected: PASS.

```bash
git add internal/config/config.go internal/config/config_test.go cmd/app/main.go \
        .env.example lambda/mastra-handler/.env.example scripts/elasticmq.conf
git commit -m "feat(ingest): consume enriched events

The consumer reads ENRICHED_EVENTS_QUEUE_URL; scrapers still publish to
EVENTS_QUEUE_URL, which the Lambda now enriches from. A message with no
enrichment block applies exactly as before, so this switch is reversible
by pointing the env var back."
```

---

### Task 17: Event source mapping, apply 2, and the runbook

**Files:**
- Modify: `terraform/prod/enrichment.tf`
- Modify: `README.md`

- [ ] **Step 1: Add the event source mapping**

```hcl
# Enrichment trigger. Added in a SEPARATE apply, after an image that handles SQS
# events is live: with this in place, the first scrape invokes the function with
# an SQS event, and an older image would fall through to its S3 branch.
resource "aws_lambda_event_source_mapping" "enrichment" {
  event_source_arn = aws_sqs_queue.events.arn
  function_name    = aws_lambda_function.mastra_handler.arn

  # One event per invocation. On the merits: one event is three workflows with
  # LLM calls, and batching ten into a 300s timeout blows the budget.
  # Structurally: it avoids partial-batch-failure reporting, which SQS reads
  # from a JSON response body — not something to depend on through the
  # streamifyResponse wrapper. At 1, success deletes and a throw returns.
  batch_size = 1

  # Bounds the SQS path ONLY. reserved_concurrent_executions would be wrong:
  # this function also serves the poster Function URL and the email S3 path, and
  # reserving would starve interactive poster requests behind enrichment.
  # 2 is the minimum AWS allows.
  scaling_config {
    maximum_concurrency = 2
  }
}
```

- [ ] **Step 2: Validate and plan**

Run:
```bash
cd terraform/prod && terraform fmt -check && terraform validate && terraform plan
```
Expected: exactly one resource to add. If the plan errors with
`InvalidParameterValueException` about visibility timeout, Task 15's Step 1 did
not apply — fix that first.

- [ ] **Step 3: Document the deployment order**

Add to `README.md` near the existing pipeline description:

```markdown
### Event enrichment

Scrapers publish to `hwh-events-queue`. The mastra-handler Lambda consumes it
(SQS trigger, `batch_size = 1`), resolves one MusicBrainz artist per event, runs
three enrichment workflows — band image, Wikipedia/MusicBrainz bio, setlist.fm
tour snapshot — and republishes onto `hwh-events-enriched-queue`, which the ECS
ingest consumer reads.

Enrichment is artist-scoped: one band playing five dates is one bio, one photo,
one setlist. An S3 skip cache (`*-enrichment-cache` bucket) gates repeat work,
which matters because the daily scrape republishes every event unconditionally.

**Deploying a change to this path is two applies:**

1. Apply queues, bucket, secret, IAM and Lambda env vars.
2. Populate the setlist.fm secret:
   `aws secretsmanager put-secret-value --secret-id hwh-setlistfm-api-key --secret-string '<key>'`
3. Deploy the Lambda image (`ci/buildspec-lambda.yml`).
4. Apply `aws_lambda_event_source_mapping.enrichment`.
5. Deploy the API image, then point the consumer at the enriched queue with
   `scripts/taskdef-edit.sh --set-env ENRICHED_EVENTS_QUEUE_URL=... --deploy`.

Step 5 needs the script because `container_definitions` carries
`ignore_changes`, so a terraform-only env var change never reaches a running
task.

**setlist.fm caveats:** the free key is non-commercial only, and capped at 1,440
requests/day. The skip cache is what keeps steady-state usage inside that cap;
the documented upgrade path is a manual request to setlist.fm.
```

- [ ] **Step 4: Commit**

```bash
git add terraform/prod/enrichment.tf README.md
git commit -m "feat(infra): enrichment event source mapping

batch_size 1 because one event is three LLM workflows, and because it
avoids partial-batch-failure reporting through the streamifyResponse
wrapper. maximum_concurrency bounds the SQS path only — reserving
function concurrency would starve interactive poster requests."
```

- [ ] **Step 5: Post-deploy verification**

After the full deploy, confirm the pipeline end to end:

```bash
# 1. Trigger a scrape and watch the Lambda pick events up.
aws logs tail /aws/lambda/hwh-mastra-handler --follow --since 5m

# 2. Enriched messages should be arriving, and both DLQs stay empty.
aws sqs get-queue-attributes --queue-url "$(terraform output -raw events_enriched_queue_url)" \
  --attribute-names ApproximateNumberOfMessages

# 3. Cache objects should appear after the first pass.
aws s3 ls "s3://$(terraform output -raw enrichment_cache_bucket)/enrichment/v1/" | head

# 4. The database should have artist rows.
#    (via the bastion tunnel — see `make bastion-tunnel`)
psql -c "SELECT status, count(*) FROM artists GROUP BY status;"
psql -c "SELECT status, count(*) FROM artist_bios GROUP BY status;"
```

Set `AWS_PROFILE=servant` for all of the above.

**The signal that the gate works:** run the scrape twice and confirm the second
run produces far fewer LLM invocations than the first. If both runs cost the
same, the cache is not being consulted — check for `AccessDenied` in the Lambda
logs, which `S3EnrichmentCache.read` deliberately throws rather than swallowing.
