# Plan 2 — Event Ingest Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** End-to-end ingest pipeline that scrapes events from the Ticketmaster Discovery API, publishes them as canonical messages to a local SQS queue (ElasticMQ), and consumes them into Postgres — runnable from a dev laptop with `make scrape` + `make run`.

**Architecture:** Two new flows in the existing single binary: (1) `app scrape events --source=ticketmaster` runs as a one-shot task that calls the Ticketmaster API, maps records into a canonical `EventMessage` JSON, and `SendMessageBatch`es them to ElasticMQ. (2) `app serve` (already in Plan 1) gains a background SQS consumer goroutine pool that long-polls `events-queue`, upserts each message into Postgres (`events` + `venues` + `event_performers` + `event_genres`), and deletes on success. Adapters are stateless and never touch Postgres. The DB layer is the only place that knows our schema.

**Tech Stack:** Go 1.24+ · `github.com/aws/aws-sdk-go-v2/service/sqs` · `softwaremill/elasticmq-native` Docker image for local SQS · `sqlc` for the new `events` / `venues` / `event_performers` / `event_genres` / `genres` / `event_sources` queries · `pgx/v5` for batch upserts · existing `chi`, `golang-migrate`, integration-tests-against-real-Postgres pattern from Plan 1.

---

## File Structure

```
.
├── cmd/app/main.go                                  # add `scrape` subcommand dispatch
├── internal/
│   ├── config/config.go                             # extend with SQS + scraper vars
│   ├── events/
│   │   ├── message.go                               # EventMessage canonical type
│   │   ├── message_test.go
│   │   ├── genres.go                                # controlled vocab + Normalize helper
│   │   └── genres_test.go
│   ├── queue/
│   │   ├── sqs.go                                   # SQS client wrapper (Send/Receive/Delete)
│   │   └── sqs_test.go                              # integration test against ElasticMQ
│   ├── scraper/
│   │   ├── adapter.go                               # Adapter interface
│   │   ├── runner.go                                # adapter.Fetch → publish loop
│   │   ├── runner_test.go
│   │   └── ticketmaster/
│   │       ├── adapter.go                           # Discovery API adapter
│   │       ├── adapter_test.go                      # HTTP-mocked unit tests
│   │       └── testdata/                            # canned API responses
│   ├── ingest/
│   │   ├── events.go                                # handler: EventMessage → DB upsert
│   │   ├── events_test.go
│   │   ├── consumer.go                              # SQS pull loop + dispatch
│   │   └── consumer_test.go                         # integration test against ElasticMQ
│   └── store/                                       # sqlc-generated; new tables added
├── sql/
│   ├── migrations/
│   │   ├── 0005_event_sources_venues_genres.up.sql/.down.sql
│   │   ├── 0006_events.up.sql/.down.sql
│   │   └── 0007_event_associations.up.sql/.down.sql
│   └── queries/
│       ├── event_sources.sql
│       ├── venues.sql
│       ├── genres.sql
│       ├── events.sql
│       └── event_associations.sql
├── docker-compose.yml                               # add elasticmq service + queue init
├── scripts/elasticmq.conf                           # ElasticMQ queue definitions
├── Makefile                                         # queue-up, queue-down, scrape targets
└── .env.example                                     # SQS + Ticketmaster vars
```

**Boundaries:**

- **`internal/events`** — pure types and value helpers. No DB, no HTTP, no SQS. Importable from any other package.
- **`internal/queue`** — SQS-only wrapper. Knows nothing about events. Takes a queue URL and `[]byte` payloads.
- **`internal/scraper/*`** — adapter contract + concrete adapters. Adapters do HTTP, produce `events.Message`, return slices. No DB, no SQS.
- **`internal/ingest`** — bridges SQS → DB. Has handlers and consumer loops. Knows about both `queue` and `store`.

---

## Prerequisites

- Plan 1 is merged to master (the working tree includes everything Plan 1 built).
- `docker compose up postgres` is running and migrations 0001–0004 are applied.
- For the smoke test at end of Task 14: a `TICKETMASTER_API_KEY` (free, register at https://developer.ticketmaster.com). Tests don't need a real key — they use HTTP-mocked responses.

---

### Task 1: ElasticMQ + AWS SDK v2 dependencies + docker-compose

**Files:**
- Modify: `docker-compose.yml`
- Create: `scripts/elasticmq.conf`
- Modify: `Makefile`
- Modify: `.env.example`
- Modify: `go.mod` / `go.sum`

- [ ] **Step 1: Add `scripts/elasticmq.conf`**

```hocon
include classpath("application.conf")

node-address {
  protocol  = http
  host      = "*"
  port      = 9324
  context-path = ""
}

rest-sqs {
  enabled       = true
  bind-port     = 9324
  bind-hostname = "0.0.0.0"
  sqs-limits    = strict
}

queues {
  events-queue {
    defaultVisibilityTimeout = 30 seconds
    receiveMessageWait       = 20 seconds
    deadLettersQueue {
      name              = events-dlq
      maxReceiveCount   = 3
    }
  }
  events-dlq {}
}
```

- [ ] **Step 2: Add elasticmq service to `docker-compose.yml`**

Append the service to the existing `services:` block (keep `postgres` as-is):

```yaml
  elasticmq:
    image: softwaremill/elasticmq-native:1.6.7
    container_name: hwh_elasticmq
    ports:
      - "9324:9324"   # SQS API
      - "9325:9325"   # UI (optional)
    volumes:
      - ./scripts/elasticmq.conf:/opt/elasticmq.conf:ro
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O - http://localhost:9324/?Action=ListQueues || exit 1"]
      interval: 5s
      timeout: 5s
      retries: 10
```

- [ ] **Step 3: Append to `.env.example`**

```
# SQS (ElasticMQ in dev; real SQS in prod)
AWS_REGION=us-east-1
AWS_ACCESS_KEY_ID=local
AWS_SECRET_ACCESS_KEY=local
SQS_ENDPOINT=http://localhost:9324
EVENTS_QUEUE_URL=http://localhost:9324/000000000000/events-queue

# Scrapers
TICKETMASTER_API_KEY=your-key-here
TICKETMASTER_CITY=New York
INGEST_WORKERS=4
```

- [ ] **Step 4: Add Make targets**

Append to `Makefile`:

```makefile
.PHONY: queue-up queue-down queue-reset scrape

queue-up:
	docker compose up -d elasticmq

queue-down:
	docker compose stop elasticmq

queue-reset:
	docker compose down elasticmq -v
	docker compose up -d elasticmq

scrape:
	go run ./cmd/app scrape events --source=ticketmaster
```

- [ ] **Step 5: Add AWS SDK deps**

```bash
go get github.com/aws/aws-sdk-go-v2@v1.32.6
go get github.com/aws/aws-sdk-go-v2/config@v1.28.6
go get github.com/aws/aws-sdk-go-v2/service/sqs@v1.37.2
go get github.com/aws/aws-sdk-go-v2/credentials@v1.17.47
```

- [ ] **Step 6: Verify ElasticMQ starts and exposes the queue**

```bash
make queue-up
sleep 3
curl -s "http://localhost:9324/?Action=ListQueues" | head
```

Expected: XML response listing `events-queue` and `events-dlq`.

- [ ] **Step 7: Commit**

```bash
git add docker-compose.yml scripts/elasticmq.conf Makefile .env.example go.mod go.sum
git commit -m "feat: ElasticMQ docker-compose service + AWS SDK v2 deps"
```

---

### Task 2: Extend config loader

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Add failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoad_QueueAndScraperFields(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("SQS_ENDPOINT", "http://localhost:9324")
	t.Setenv("EVENTS_QUEUE_URL", "http://localhost:9324/000000000000/events-queue")
	t.Setenv("INGEST_WORKERS", "8")
	t.Setenv("TICKETMASTER_API_KEY", "tm-key")
	t.Setenv("TICKETMASTER_CITY", "Brooklyn")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "us-east-1", cfg.AWSRegion)
	require.Equal(t, "http://localhost:9324", cfg.SQSEndpoint)
	require.Equal(t, "http://localhost:9324/000000000000/events-queue", cfg.EventsQueueURL)
	require.Equal(t, 8, cfg.IngestWorkers)
	require.Equal(t, "tm-key", cfg.TicketmasterAPIKey)
	require.Equal(t, "Brooklyn", cfg.TicketmasterCity)
}

func TestLoad_IngestWorkersDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SIGNING_KEY", "k")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 4, cfg.IngestWorkers) // default
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/config -v -run "TestLoad_QueueAndScraperFields|TestLoad_IngestWorkersDefault"
```

Expected: FAIL with `cfg.AWSRegion undefined`.

- [ ] **Step 3: Extend `internal/config/config.go`**

Add fields to the `Config` struct (preserve existing fields):

```go
type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	JWTSigningKey string
	JWTAccessTTL  time.Duration
	RefreshTTL    time.Duration
	LogLevel      string

	// Plan 2 additions
	AWSRegion          string
	SQSEndpoint        string
	EventsQueueURL     string
	IngestWorkers      int
	TicketmasterAPIKey string
	TicketmasterCity   string
}
```

Extend `Load()` to populate the new fields. Add this block after the existing `LogLevel` block, before the return:

```go
	workers := 4
	if v := os.Getenv("INGEST_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid INGEST_WORKERS=%q", v)
		}
		workers = n
	}

	cfg := &Config{
		DatabaseURL:        dbURL,
		HTTPAddr:           addr,
		JWTSigningKey:      signingKey,
		JWTAccessTTL:       accessTTL,
		RefreshTTL:         refreshTTL,
		LogLevel:           logLevel,
		AWSRegion:          os.Getenv("AWS_REGION"),
		SQSEndpoint:        os.Getenv("SQS_ENDPOINT"),
		EventsQueueURL:     os.Getenv("EVENTS_QUEUE_URL"),
		IngestWorkers:      workers,
		TicketmasterAPIKey: os.Getenv("TICKETMASTER_API_KEY"),
		TicketmasterCity:   os.Getenv("TICKETMASTER_CITY"),
	}
	return cfg, nil
```

Replace the existing `return &Config{...}` block with the version above. Add `"strconv"` to the imports.

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/config -v
```

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add SQS + scraper env vars"
```

---

### Task 3: Migration 0005 — `event_sources`, `venues`, `genres` (with seeded vocab)

**Files:**
- Create: `sql/migrations/0005_event_sources_venues_genres.up.sql`
- Create: `sql/migrations/0005_event_sources_venues_genres.down.sql`

- [ ] **Step 1: Write `0005_event_sources_venues_genres.up.sql`**

```sql
CREATE TABLE event_sources (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL UNIQUE,
    adapter_kind  TEXT NOT NULL,
    config        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO event_sources (name, adapter_kind, config)
VALUES ('ticketmaster', 'ticketmaster_api', '{}'::jsonb);

CREATE TABLE venues (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id         UUID NOT NULL REFERENCES cities(id),
    name            TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    address         TEXT,
    lat             DOUBLE PRECISION,
    lng             DOUBLE PRECISION,
    website_url     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (city_id, normalized_name)
);

CREATE TABLE genres (
    slug   TEXT PRIMARY KEY,
    label  TEXT NOT NULL
);

INSERT INTO genres (slug, label) VALUES
    ('rock',         'Rock'),
    ('pop',          'Pop'),
    ('hip-hop',      'Hip-Hop'),
    ('electronic',   'Electronic'),
    ('jazz',         'Jazz'),
    ('classical',    'Classical'),
    ('folk',         'Folk'),
    ('country',      'Country'),
    ('metal',        'Metal'),
    ('indie',        'Indie'),
    ('rnb',          'R&B'),
    ('latin',        'Latin'),
    ('world',        'World'),
    ('blues',        'Blues'),
    ('reggae',       'Reggae'),
    ('theater',      'Theater'),
    ('musical',      'Musical'),
    ('comedy',       'Comedy'),
    ('dance',        'Dance'),
    ('opera',        'Opera'),
    ('film',         'Film'),
    ('sports',       'Sports'),
    ('food',         'Food'),
    ('art',          'Art'),
    ('family',       'Family'),
    ('other',        'Other');
```

- [ ] **Step 2: Write `0005_event_sources_venues_genres.down.sql`**

```sql
DROP TABLE IF EXISTS genres;
DROP TABLE IF EXISTS venues;
DROP TABLE IF EXISTS event_sources;
```

- [ ] **Step 3: Run migration against both DBs**

```bash
make migrate
make migrate-test
```

Expected: both runs print `5/u event_sources_venues_genres`.

- [ ] **Step 4: Verify**

```bash
docker exec hwh_postgres psql -U app -d appdb -c "SELECT count(*) FROM genres;"
docker exec hwh_postgres psql -U app -d appdb -c "SELECT name, adapter_kind FROM event_sources;"
```

Expected: `26` genres; one `ticketmaster | ticketmaster_api` source.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/0005_event_sources_venues_genres.up.sql sql/migrations/0005_event_sources_venues_genres.down.sql
git commit -m "feat: migration 0005 — event_sources, venues, genres"
```

---

### Task 4: Migration 0006 — `events`

**Files:**
- Create: `sql/migrations/0006_events.up.sql`
- Create: `sql/migrations/0006_events.down.sql`

- [ ] **Step 1: Write `0006_events.up.sql`**

```sql
CREATE TABLE events (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id                UUID NOT NULL REFERENCES event_sources(id),
    source_event_id          TEXT NOT NULL,
    title                    TEXT NOT NULL,
    description              TEXT NOT NULL DEFAULT '',
    starts_at                TIMESTAMPTZ NOT NULL,
    ends_at                  TIMESTAMPTZ,
    venue_id                 UUID NOT NULL REFERENCES venues(id),
    image_url                TEXT,
    url                      TEXT,
    embedding                vector(384),
    embedding_updated_at     TIMESTAMPTZ,
    last_seen_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at              TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_id, source_event_id)
);

CREATE INDEX events_starts_at        ON events (starts_at);
CREATE INDEX events_venue_id         ON events (venue_id);
CREATE INDEX events_archived_at      ON events (archived_at);
CREATE INDEX events_last_seen_at     ON events (last_seen_at);
```

- [ ] **Step 2: Write `0006_events.down.sql`**

```sql
DROP TABLE IF EXISTS events;
```

- [ ] **Step 3: Run migrations and verify**

```bash
make migrate
make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c "\d events"
```

Expected: `events` table with `embedding vector(384)` and the 4 indexes.

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/0006_events.up.sql sql/migrations/0006_events.down.sql
git commit -m "feat: migration 0006 — events table"
```

---

### Task 5: Migration 0007 — `event_performers`, `event_genres`

**Files:**
- Create: `sql/migrations/0007_event_associations.up.sql`
- Create: `sql/migrations/0007_event_associations.down.sql`

- [ ] **Step 1: Write `0007_event_associations.up.sql`**

```sql
CREATE TABLE event_performers (
    event_id         UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    performer_name   TEXT NOT NULL,
    normalized_name  TEXT NOT NULL,
    PRIMARY KEY (event_id, normalized_name)
);

CREATE INDEX event_performers_normalized_name ON event_performers (normalized_name);

CREATE TABLE event_genres (
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    genre_slug TEXT NOT NULL REFERENCES genres(slug),
    PRIMARY KEY (event_id, genre_slug)
);
```

- [ ] **Step 2: Write `0007_event_associations.down.sql`**

```sql
DROP TABLE IF EXISTS event_genres;
DROP TABLE IF EXISTS event_performers;
```

- [ ] **Step 3: Run migrations and verify**

```bash
make migrate
make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c "\d event_performers"
docker exec hwh_postgres psql -U app -d appdb -c "\d event_genres"
```

Expected: both tables exist with the documented PKs.

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/0007_event_associations.up.sql sql/migrations/0007_event_associations.down.sql
git commit -m "feat: migration 0007 — event_performers and event_genres"
```

---

### Task 6: Update testdb truncate list

**Files:**
- Modify: `internal/testdb/testdb.go`

- [ ] **Step 1: Update `tables` slice in `truncateAll`**

Find the `tables := []string{...}` block in `internal/testdb/testdb.go` and replace it with:

```go
	// Order matters: children before parents to avoid FK violations on TRUNCATE CASCADE.
	tables := []string{
		"event_genres",
		"event_performers",
		"events",
		"venues",
		"user_interests",
		"refresh_tokens",
		"users",
	}
```

Note: `event_sources` and `genres` are seeded vocabularies — DO NOT truncate.

- [ ] **Step 2: Verify build**

```bash
go build ./...
go test ./... -count=1
```

Expected: full suite still PASSes (no new tests added yet; existing tests should not have regressed).

- [ ] **Step 3: Commit**

```bash
git add internal/testdb/testdb.go
git commit -m "test(testdb): truncate new event tables between tests"
```

---

### Task 7: sqlc queries for new tables

**Files:**
- Create: `sql/queries/event_sources.sql`
- Create: `sql/queries/venues.sql`
- Create: `sql/queries/genres.sql`
- Create: `sql/queries/events.sql`
- Create: `sql/queries/event_associations.sql`
- Regenerate: `internal/store/*` via `sqlc generate`

- [ ] **Step 1: Write `sql/queries/event_sources.sql`**

```sql
-- name: GetEventSourceByName :one
SELECT id, name, adapter_kind, config
FROM event_sources
WHERE name = $1;
```

- [ ] **Step 2: Write `sql/queries/venues.sql`**

```sql
-- name: UpsertVenue :one
INSERT INTO venues (city_id, name, normalized_name, address, lat, lng, website_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (city_id, normalized_name)
DO UPDATE SET
    name        = EXCLUDED.name,
    address     = EXCLUDED.address,
    lat         = EXCLUDED.lat,
    lng         = EXCLUDED.lng,
    website_url = EXCLUDED.website_url,
    updated_at  = NOW()
RETURNING id;
```

- [ ] **Step 3: Write `sql/queries/genres.sql`**

```sql
-- name: ListGenres :many
SELECT slug, label FROM genres ORDER BY label ASC;

-- name: GenreExists :one
SELECT EXISTS (SELECT 1 FROM genres WHERE slug = $1) AS exists;
```

- [ ] **Step 4: Write `sql/queries/events.sql`**

```sql
-- name: UpsertEvent :one
INSERT INTO events (
    source_id, source_event_id, title, description, starts_at, ends_at,
    venue_id, image_url, url, last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (source_id, source_event_id)
DO UPDATE SET
    title         = EXCLUDED.title,
    description   = EXCLUDED.description,
    starts_at     = EXCLUDED.starts_at,
    ends_at       = EXCLUDED.ends_at,
    venue_id      = EXCLUDED.venue_id,
    image_url     = EXCLUDED.image_url,
    url           = EXCLUDED.url,
    last_seen_at  = NOW(),
    archived_at   = NULL,
    updated_at    = NOW()
RETURNING id;

-- name: GetEventByID :one
SELECT id, source_id, source_event_id, title, description, starts_at, ends_at,
       venue_id, image_url, url, last_seen_at, archived_at, created_at, updated_at
FROM events
WHERE id = $1;

-- name: GetEventBySourceKey :one
SELECT id, source_id, source_event_id, title, description, starts_at, ends_at,
       venue_id, image_url, url, last_seen_at, archived_at, created_at, updated_at
FROM events
WHERE source_id = $1 AND source_event_id = $2;
```

- [ ] **Step 5: Write `sql/queries/event_associations.sql`**

```sql
-- name: DeleteEventPerformersByEvent :exec
DELETE FROM event_performers WHERE event_id = $1;

-- name: InsertEventPerformer :exec
INSERT INTO event_performers (event_id, performer_name, normalized_name)
VALUES ($1, $2, $3)
ON CONFLICT (event_id, normalized_name) DO NOTHING;

-- name: ListEventPerformersByEvent :many
SELECT performer_name, normalized_name
FROM event_performers
WHERE event_id = $1
ORDER BY performer_name ASC;

-- name: DeleteEventGenresByEvent :exec
DELETE FROM event_genres WHERE event_id = $1;

-- name: InsertEventGenre :exec
INSERT INTO event_genres (event_id, genre_slug)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListEventGenresByEvent :many
SELECT genre_slug FROM event_genres WHERE event_id = $1 ORDER BY genre_slug ASC;
```

- [ ] **Step 6: Generate and verify build**

```bash
sqlc generate
go build ./...
```

Expected: no errors. New files appear under `internal/store/`.

- [ ] **Step 7: Commit**

```bash
git add sql/queries/ internal/store/
git commit -m "feat: sqlc queries for events, venues, performers, genres"
```

---

### Task 8: EventMessage canonical type

**Files:**
- Create: `internal/events/message.go`
- Create: `internal/events/message_test.go`

- [ ] **Step 1: Write failing test**

`internal/events/message_test.go`:

```go
package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMessage_JSONRoundTrip(t *testing.T) {
	m := Message{
		SourceID:      "ticketmaster",
		SourceEventID: "tm-123",
		Title:         "Phoebe Bridgers Live",
		Description:   "Indie rock concert",
		StartsAt:      time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
		EndsAt:        nil,
		Venue: Venue{
			Name:    "The Bowl",
			Address: "100 Main St",
			Lat:     ptr(40.7),
			Lng:     ptr(-74.0),
		},
		Performers: []string{"Phoebe Bridgers", "MUNA"},
		Genres:     []string{"indie", "rock"},
		ImageURL:   "https://example.com/p.jpg",
		URL:        "https://example.com/event/123",
	}

	data, err := json.Marshal(m)
	require.NoError(t, err)

	var out Message
	require.NoError(t, json.Unmarshal(data, &out))
	require.Equal(t, m.Title, out.Title)
	require.Equal(t, m.StartsAt.Unix(), out.StartsAt.Unix())
	require.Equal(t, m.Performers, out.Performers)
	require.Equal(t, m.Genres, out.Genres)
	require.NotNil(t, out.Venue.Lat)
	require.InDelta(t, 40.7, *out.Venue.Lat, 0.0001)
}

func TestMessage_OmitEmptyFields(t *testing.T) {
	m := Message{
		SourceID:      "x",
		SourceEventID: "y",
		Title:         "t",
		StartsAt:      time.Now(),
		Venue:         Venue{Name: "v"},
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"ends_at"`)
	require.NotContains(t, string(data), `"image_url"`)
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/events -v
```

Expected: FAIL — `package events; no Go files`.

- [ ] **Step 3: Implement**

`internal/events/message.go`:

```go
// Package events defines the canonical wire types used between scrapers, the
// events-queue (SQS), and the ingest consumer. No I/O lives here — just types
// and value helpers.
package events

import "time"

// Message is the canonical event record placed on the events-queue by scrapers
// and read by the ingest consumer.
type Message struct {
	SourceID      string     `json:"source_id"`
	SourceEventID string     `json:"source_event_id"`
	Title         string     `json:"title"`
	Description   string     `json:"description,omitempty"`
	StartsAt      time.Time  `json:"starts_at"`
	EndsAt        *time.Time `json:"ends_at,omitempty"`
	Venue         Venue      `json:"venue"`
	Performers    []string   `json:"performers,omitempty"`
	Genres        []string   `json:"genres,omitempty"`
	ImageURL      string     `json:"image_url,omitempty"`
	URL           string     `json:"url,omitempty"`
}

// Venue is denormalized inline on the Message. The ingest consumer is
// responsible for upserting it into the venues table and resolving venue_id.
type Venue struct {
	Name        string   `json:"name"`
	Address     string   `json:"address,omitempty"`
	Lat         *float64 `json:"lat,omitempty"`
	Lng         *float64 `json:"lng,omitempty"`
	WebsiteURL  string   `json:"website_url,omitempty"`
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/events -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/events/message.go internal/events/message_test.go
git commit -m "feat(events): canonical Message and Venue types"
```

---

### Task 9: Genre vocabulary + normalization helpers

**Files:**
- Create: `internal/events/genres.go`
- Create: `internal/events/genres_test.go`

- [ ] **Step 1: Write failing test**

`internal/events/genres_test.go`:

```go
package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGenre_KnownAlias(t *testing.T) {
	require.Equal(t, "rock", NormalizeGenre("Rock"))
	require.Equal(t, "rock", NormalizeGenre("ROCK"))
	require.Equal(t, "rock", NormalizeGenre("  Rock  "))
	require.Equal(t, "hip-hop", NormalizeGenre("Hip Hop"))
	require.Equal(t, "hip-hop", NormalizeGenre("Hip-Hop/Rap"))
	require.Equal(t, "rnb", NormalizeGenre("R&B"))
	require.Equal(t, "indie", NormalizeGenre("Alternative"))
	require.Equal(t, "electronic", NormalizeGenre("EDM"))
}

func TestNormalizeGenre_UnknownReturnsEmpty(t *testing.T) {
	require.Equal(t, "", NormalizeGenre("Nonexistent Genre"))
}

func TestNormalizeString(t *testing.T) {
	require.Equal(t, "phoebe bridgers", NormalizeString("Phoebe Bridgers"))
	require.Equal(t, "the bowl", NormalizeString(" The Bowl "))
	require.Equal(t, "cafe luca", NormalizeString("Café Luca"))
}
```

- [ ] **Step 2: Run test**

```bash
go test ./internal/events -v -run Genre
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/events/genres.go`:

```go
package events

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// genreAliases maps lowercase raw source tags to our canonical slug.
// Unrecognized tags map to "" (caller should drop).
var genreAliases = map[string]string{
	// Music — direct
	"rock":         "rock",
	"pop":          "pop",
	"hip-hop":      "hip-hop",
	"hip hop":      "hip-hop",
	"hip-hop/rap":  "hip-hop",
	"rap":          "hip-hop",
	"electronic":   "electronic",
	"edm":          "electronic",
	"dance/electronic": "electronic",
	"jazz":         "jazz",
	"classical":    "classical",
	"folk":         "folk",
	"country":      "country",
	"metal":        "metal",
	"hard rock":    "metal",
	"indie":        "indie",
	"alternative":  "indie",
	"r&b":          "rnb",
	"rnb":          "rnb",
	"latin":        "latin",
	"world":        "world",
	"blues":        "blues",
	"reggae":       "reggae",
	// Non-music
	"theater":      "theater",
	"theatre":      "theater",
	"musical":      "musical",
	"comedy":       "comedy",
	"dance":        "dance",
	"opera":        "opera",
	"film":         "film",
	"sports":       "sports",
	"food":         "food",
	"art":          "art",
	"family":       "family",
	"other":        "other",
	"miscellaneous":"other",
}

// NormalizeGenre maps a source tag (e.g., "Hip-Hop/Rap") into our controlled
// vocabulary slug ("hip-hop"). Returns "" if the tag is unrecognized.
func NormalizeGenre(s string) string {
	key := strings.ToLower(strings.TrimSpace(s))
	return genreAliases[key]
}

// NormalizeString returns a lowercased, trimmed, diacritic-stripped form
// suitable for comparison (e.g., venue and performer normalized_name columns).
func NormalizeString(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, s)
	return strings.ToLower(strings.TrimSpace(out))
}
```

- [ ] **Step 4: Add the `golang.org/x/text` dependency and run tests**

```bash
go get golang.org/x/text
go test ./internal/events -v
```

Expected: all genres tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/events/genres.go internal/events/genres_test.go go.mod go.sum
git commit -m "feat(events): genre alias map + string normalization"
```

---

### Task 10: SQS queue client wrapper

**Files:**
- Create: `internal/queue/sqs.go`
- Create: `internal/queue/sqs_test.go`

- [ ] **Step 1: Write failing integration test**

`internal/queue/sqs_test.go`:

```go
package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testQueueURL() string {
	if v := os.Getenv("EVENTS_QUEUE_URL"); v != "" {
		return v
	}
	return "http://localhost:9324/000000000000/events-queue"
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "local")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	c, err := NewClient(context.Background(), "us-east-1", "http://localhost:9324")
	require.NoError(t, err)
	return c
}

func TestClient_SendReceiveDelete_RoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Purge any prior messages so the test is deterministic.
	require.NoError(t, c.Purge(ctx, testQueueURL()))

	body := []byte(`{"ping":"pong"}`)
	require.NoError(t, c.Send(ctx, testQueueURL(), body))

	msgs, err := c.Receive(ctx, testQueueURL(), 10, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, body, msgs[0].Body)

	require.NoError(t, c.Delete(ctx, testQueueURL(), msgs[0].ReceiptHandle))

	// After delete, no message left.
	more, err := c.Receive(ctx, testQueueURL(), 10, 1*time.Second)
	require.NoError(t, err)
	require.Empty(t, more)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/queue -v
```

Expected: FAIL — `undefined: NewClient`.

- [ ] **Step 3: Implement**

`internal/queue/sqs.go`:

```go
// Package queue wraps the AWS SQS SDK with the minimal Send/Receive/Delete
// surface our ingest pipeline needs. Knows nothing about event semantics.
package queue

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type Client struct {
	sqs *sqs.Client
}

// Message is a received SQS message in our internal shape.
type Message struct {
	Body          []byte
	ReceiptHandle string
}

// NewClient builds an SQS client. If endpointURL is non-empty, it overrides
// the default AWS endpoint (used for ElasticMQ in dev). Region must be set.
func NewClient(ctx context.Context, region, endpointURL string) (*Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	var clientOpts []func(*sqs.Options)
	if endpointURL != "" {
		clientOpts = append(clientOpts, func(o *sqs.Options) {
			o.BaseEndpoint = &endpointURL
		})
	}
	return &Client{sqs: sqs.NewFromConfig(cfg, clientOpts...)}, nil
}

func (c *Client) Send(ctx context.Context, queueURL string, body []byte) error {
	s := string(body)
	_, err := c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: &s,
	})
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// Receive pulls up to maxMessages with long-polling for waitTime (max 20s).
func (c *Client) Receive(ctx context.Context, queueURL string, maxMessages int32, waitTime time.Duration) ([]Message, error) {
	waitSec := int32(waitTime / time.Second)
	if waitSec > 20 {
		waitSec = 20
	}
	out, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: maxMessages,
		WaitTimeSeconds:     waitSec,
	})
	if err != nil {
		return nil, fmt.Errorf("receive: %w", err)
	}
	msgs := make([]Message, 0, len(out.Messages))
	for _, m := range out.Messages {
		msgs = append(msgs, Message{
			Body:          []byte(*m.Body),
			ReceiptHandle: *m.ReceiptHandle,
		})
	}
	return msgs, nil
}

func (c *Client) Delete(ctx context.Context, queueURL, receiptHandle string) error {
	_, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Purge empties the queue. Test-only; production callers should not use it.
func (c *Client) Purge(ctx context.Context, queueURL string) error {
	_, err := c.sqs.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: &queueURL})
	if err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	// PurgeQueue is async; sleep briefly so subsequent operations see the cleared state.
	time.Sleep(500 * time.Millisecond)
	return nil
}

// Suppress unused-import warning for the types package; it's used implicitly
// by the SDK in receive options. Remove if not needed.
var _ = types.MessageSystemAttributeName("")
```

(Drop the `var _` line if `go vet` is happy without it.)

- [ ] **Step 4: Ensure ElasticMQ is up and run the test**

```bash
make queue-up
go test ./internal/queue -v
```

Expected: `TestClient_SendReceiveDelete_RoundTrip` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/queue/sqs.go internal/queue/sqs_test.go go.mod go.sum
git commit -m "feat(queue): SQS client wrapper (Send/Receive/Delete/Purge)"
```

---

### Task 11: Scraper Adapter interface

**Files:**
- Create: `internal/scraper/adapter.go`

This task is interface-only — no behavior to TDD. The first concrete adapter (Task 12) is its real test.

- [ ] **Step 1: Write `internal/scraper/adapter.go`**

```go
// Package scraper defines the contract event-source adapters must satisfy
// and the runner that drives them.
package scraper

import (
	"context"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

// Adapter pulls events from a single source and returns them in canonical form.
// Implementations are stateless. They never touch the database or the queue.
type Adapter interface {
	// Name is the event_sources.name value this adapter corresponds to (e.g., "ticketmaster").
	Name() string

	// Fetch returns the adapter's current view of the source's events.
	// Idempotency: callers may invoke Fetch repeatedly; results should be the
	// adapter's best snapshot at call time.
	Fetch(ctx context.Context) ([]events.Message, error)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/scraper
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/scraper/adapter.go
git commit -m "feat(scraper): Adapter interface"
```

---

### Task 12: Ticketmaster adapter

**Files:**
- Create: `internal/scraper/ticketmaster/adapter.go`
- Create: `internal/scraper/ticketmaster/adapter_test.go`
- Create: `internal/scraper/ticketmaster/testdata/sample_page.json`

- [ ] **Step 1: Capture a representative Ticketmaster response**

Create `internal/scraper/ticketmaster/testdata/sample_page.json` with a minimal but realistic Discovery API response (one page, two events, includes embedded venue + classifications):

```json
{
  "_embedded": {
    "events": [
      {
        "id": "tm-aaa",
        "name": "Phoebe Bridgers",
        "url": "https://www.ticketmaster.com/event/tm-aaa",
        "info": "Indie rock concert",
        "images": [{"url": "https://example.com/p.jpg", "ratio": "16_9", "width": 1024}],
        "dates": {
          "start": {"dateTime": "2026-06-15T20:00:00Z", "localDate": "2026-06-15"}
        },
        "classifications": [
          {"genre": {"name": "Rock"}, "subGenre": {"name": "Indie"}}
        ],
        "_embedded": {
          "venues": [{
            "name": "The Bowl",
            "address": {"line1": "100 Main St"},
            "location": {"latitude": "40.7", "longitude": "-74.0"},
            "url": "https://example.com/venue"
          }],
          "attractions": [
            {"name": "Phoebe Bridgers"},
            {"name": "MUNA"}
          ]
        }
      },
      {
        "id": "tm-bbb",
        "name": "Hamilton",
        "url": "https://www.ticketmaster.com/event/tm-bbb",
        "dates": {
          "start": {"dateTime": "2026-07-01T19:30:00Z"}
        },
        "classifications": [
          {"genre": {"name": "Theatre"}, "subGenre": {"name": "Musical"}}
        ],
        "_embedded": {
          "venues": [{
            "name": "Richard Rodgers Theatre",
            "address": {"line1": "226 W 46th St"}
          }],
          "attractions": [{"name": "Hamilton Cast"}]
        }
      }
    ]
  },
  "page": {"size": 200, "totalElements": 2, "totalPages": 1, "number": 0}
}
```

- [ ] **Step 2: Write failing test**

`internal/scraper/ticketmaster/adapter_test.go`:

```go
package ticketmaster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdapter_Fetch_ParsesSamplePage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "sample_page.json"))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/discovery/v2/events.json", r.URL.Path)
		require.Equal(t, "test-key", r.URL.Query().Get("apikey"))
		require.Equal(t, "Brooklyn", r.URL.Query().Get("city"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	a := New(srv.URL, "test-key", "Brooklyn")
	events, err := a.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 2)

	// First event: Phoebe Bridgers
	require.Equal(t, "ticketmaster", events[0].SourceID)
	require.Equal(t, "tm-aaa", events[0].SourceEventID)
	require.Equal(t, "Phoebe Bridgers", events[0].Title)
	require.Equal(t, "Indie rock concert", events[0].Description)
	require.Equal(t, "The Bowl", events[0].Venue.Name)
	require.Equal(t, "100 Main St", events[0].Venue.Address)
	require.NotNil(t, events[0].Venue.Lat)
	require.InDelta(t, 40.7, *events[0].Venue.Lat, 0.001)
	require.ElementsMatch(t, []string{"Phoebe Bridgers", "MUNA"}, events[0].Performers)
	require.Contains(t, events[0].Genres, "rock")
	require.Contains(t, events[0].Genres, "indie")
	require.Equal(t, "https://example.com/p.jpg", events[0].ImageURL)

	// Second event: Hamilton
	require.Equal(t, "tm-bbb", events[1].SourceEventID)
	require.Equal(t, "Hamilton", events[1].Title)
	require.Contains(t, events[1].Genres, "theater")
	require.Contains(t, events[1].Genres, "musical")
}

func TestAdapter_Fetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()
	a := New(srv.URL, "k", "X")
	_, err := a.Fetch(context.Background())
	require.Error(t, err)
}

func TestAdapter_Name(t *testing.T) {
	a := New("http://x", "k", "X")
	require.Equal(t, "ticketmaster", a.Name())
}
```

- [ ] **Step 3: Run test to confirm it fails**

```bash
go test ./internal/scraper/ticketmaster -v
```

Expected: FAIL — `undefined: New`.

- [ ] **Step 4: Implement**

`internal/scraper/ticketmaster/adapter.go`:

```go
// Package ticketmaster implements a scraper Adapter against the Ticketmaster
// Discovery API v2.
package ticketmaster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

const defaultBaseURL = "https://app.ticketmaster.com"

// Adapter fetches events from the Discovery API for a single city.
type Adapter struct {
	baseURL string
	apiKey  string
	city    string
	http    *http.Client
}

// New builds an Adapter. baseURL is overridable for tests (use httptest.Server.URL).
// In production, pass "" to get the default Ticketmaster URL.
func New(baseURL, apiKey, city string) *Adapter {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Adapter{
		baseURL: baseURL,
		apiKey:  apiKey,
		city:    city,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *Adapter) Name() string { return "ticketmaster" }

func (a *Adapter) Fetch(ctx context.Context) ([]events.Message, error) {
	q := url.Values{}
	q.Set("apikey", a.apiKey)
	q.Set("city", a.city)
	q.Set("size", "200")

	endpoint := a.baseURL + "/discovery/v2/events.json?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ticketmaster %d: %s", resp.StatusCode, string(body))
	}
	var payload discoveryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	out := make([]events.Message, 0, len(payload.Embedded.Events))
	for _, e := range payload.Embedded.Events {
		msg, ok := e.toMessage()
		if !ok {
			continue
		}
		out = append(out, msg)
	}
	return out, nil
}

// ---- Discovery API DTO ----------------------------------------------------

type discoveryResponse struct {
	Embedded struct {
		Events []discoveryEvent `json:"events"`
	} `json:"_embedded"`
}

type discoveryEvent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Info  string `json:"info"`
	Images []struct {
		URL string `json:"url"`
	} `json:"images"`
	Dates struct {
		Start struct {
			DateTime string `json:"dateTime"`
		} `json:"start"`
		End struct {
			DateTime string `json:"dateTime"`
		} `json:"end"`
	} `json:"dates"`
	Classifications []struct {
		Genre    struct{ Name string `json:"name"` } `json:"genre"`
		SubGenre struct{ Name string `json:"name"` } `json:"subGenre"`
	} `json:"classifications"`
	Embedded struct {
		Venues []struct {
			Name    string `json:"name"`
			Address struct {
				Line1 string `json:"line1"`
			} `json:"address"`
			Location struct {
				Latitude  string `json:"latitude"`
				Longitude string `json:"longitude"`
			} `json:"location"`
			URL string `json:"url"`
		} `json:"venues"`
		Attractions []struct {
			Name string `json:"name"`
		} `json:"attractions"`
	} `json:"_embedded"`
}

func (e *discoveryEvent) toMessage() (events.Message, bool) {
	if e.ID == "" || e.Name == "" {
		return events.Message{}, false
	}
	startsAt, err := time.Parse(time.RFC3339, e.Dates.Start.DateTime)
	if err != nil {
		return events.Message{}, false
	}
	if len(e.Embedded.Venues) == 0 {
		return events.Message{}, false
	}
	v := e.Embedded.Venues[0]

	venue := events.Venue{
		Name:       v.Name,
		Address:    v.Address.Line1,
		WebsiteURL: v.URL,
	}
	if v.Location.Latitude != "" {
		if lat, err := strconv.ParseFloat(v.Location.Latitude, 64); err == nil {
			venue.Lat = &lat
		}
	}
	if v.Location.Longitude != "" {
		if lng, err := strconv.ParseFloat(v.Location.Longitude, 64); err == nil {
			venue.Lng = &lng
		}
	}

	performers := make([]string, 0, len(e.Embedded.Attractions))
	for _, a := range e.Embedded.Attractions {
		if a.Name != "" {
			performers = append(performers, a.Name)
		}
	}

	genreSet := map[string]struct{}{}
	for _, c := range e.Classifications {
		if g := events.NormalizeGenre(c.Genre.Name); g != "" {
			genreSet[g] = struct{}{}
		}
		if g := events.NormalizeGenre(c.SubGenre.Name); g != "" {
			genreSet[g] = struct{}{}
		}
	}
	genres := make([]string, 0, len(genreSet))
	for g := range genreSet {
		genres = append(genres, g)
	}

	msg := events.Message{
		SourceID:      "ticketmaster",
		SourceEventID: e.ID,
		Title:         e.Name,
		Description:   e.Info,
		StartsAt:      startsAt,
		Venue:         venue,
		Performers:    performers,
		Genres:        genres,
		URL:           e.URL,
	}
	if e.Dates.End.DateTime != "" {
		if endsAt, err := time.Parse(time.RFC3339, e.Dates.End.DateTime); err == nil {
			msg.EndsAt = &endsAt
		}
	}
	if len(e.Images) > 0 {
		msg.ImageURL = e.Images[0].URL
	}
	return msg, true
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/scraper/ticketmaster -v
```

Expected: all three tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scraper/ticketmaster/
git commit -m "feat(scraper): Ticketmaster Discovery API adapter"
```

---

### Task 13: Scraper runner

**Files:**
- Create: `internal/scraper/runner.go`
- Create: `internal/scraper/runner_test.go`

- [ ] **Step 1: Write failing test**

`internal/scraper/runner_test.go`:

```go
package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

type fakeAdapter struct {
	msgs []events.Message
	err  error
}

func (f *fakeAdapter) Name() string { return "fake" }
func (f *fakeAdapter) Fetch(ctx context.Context) ([]events.Message, error) {
	return f.msgs, f.err
}

type fakePublisher struct {
	sent [][]byte
}

func (p *fakePublisher) Send(ctx context.Context, queueURL string, body []byte) error {
	p.sent = append(p.sent, body)
	return nil
}

func TestRunner_Run_PublishesEachEvent(t *testing.T) {
	a := &fakeAdapter{msgs: []events.Message{
		{SourceID: "fake", SourceEventID: "1", Title: "A", StartsAt: time.Now(), Venue: events.Venue{Name: "v"}},
		{SourceID: "fake", SourceEventID: "2", Title: "B", StartsAt: time.Now(), Venue: events.Venue{Name: "v"}},
	}}
	p := &fakePublisher{}
	r := NewRunner(a, p, "http://localhost/queue")
	require.NoError(t, r.Run(context.Background()))
	require.Len(t, p.sent, 2)

	var m1 events.Message
	require.NoError(t, json.Unmarshal(p.sent[0], &m1))
	require.Equal(t, "A", m1.Title)
}

func TestRunner_Run_AdapterError_Propagates(t *testing.T) {
	a := &fakeAdapter{err: errors.New("boom")}
	p := &fakePublisher{}
	r := NewRunner(a, p, "http://localhost/queue")
	require.Error(t, r.Run(context.Background()))
	require.Empty(t, p.sent)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/scraper -v
```

Expected: FAIL — `undefined: NewRunner`.

- [ ] **Step 3: Implement**

`internal/scraper/runner.go`:

```go
package scraper

import (
	"context"
	"encoding/json"
	"fmt"
)

// Publisher is the minimal queue interface the runner needs. Implemented by
// *queue.Client; mockable in tests.
type Publisher interface {
	Send(ctx context.Context, queueURL string, body []byte) error
}

// Runner orchestrates: pull events from one Adapter, publish each as a JSON
// message to a queue.
type Runner struct {
	adapter  Adapter
	pub      Publisher
	queueURL string
}

func NewRunner(adapter Adapter, pub Publisher, queueURL string) *Runner {
	return &Runner{adapter: adapter, pub: pub, queueURL: queueURL}
}

// Run executes one full fetch-and-publish cycle.
func (r *Runner) Run(ctx context.Context) error {
	msgs, err := r.adapter.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", r.adapter.Name(), err)
	}
	for _, m := range msgs {
		body, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal message: %w", err)
		}
		if err := r.pub.Send(ctx, r.queueURL, body); err != nil {
			return fmt.Errorf("publish: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/scraper -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scraper/runner.go internal/scraper/runner_test.go
git commit -m "feat(scraper): Runner orchestrates Adapter.Fetch → Publisher.Send"
```

---

### Task 14: `app scrape events --source=<name>` subcommand

**Files:**
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Add the subcommand dispatch**

Replace the `switch os.Args[1]` block in `cmd/app/main.go` with:

```go
	switch os.Args[1] {
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
	case "scrape":
		if err := scrape(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "scrape: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
```

And update `usage()` to include `scrape`:

```go
func usage() {
	fmt.Fprintf(os.Stderr, `usage: app <subcommand>

subcommands:
  serve                       run the HTTP API server
  scrape events --source=NAME run a one-shot scraper for one source
`)
}
```

- [ ] **Step 2: Add the `scrape` function and `runTicketmasterScrape` helper**

Append to `cmd/app/main.go`:

```go
func scrape(args []string) error {
	if len(args) == 0 || args[0] != "events" {
		return fmt.Errorf(`expected "app scrape events --source=NAME"`)
	}
	fs := flag.NewFlagSet("scrape events", flag.ExitOnError)
	source := fs.String("source", "", "adapter name (e.g., ticketmaster)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("--source is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	qClient, err := queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}

	switch *source {
	case "ticketmaster":
		return runTicketmasterScrape(ctx, cfg, qClient)
	default:
		return fmt.Errorf("unknown source: %s", *source)
	}
}

func runTicketmasterScrape(ctx context.Context, cfg *config.Config, q *queue.Client) error {
	if cfg.TicketmasterAPIKey == "" {
		return fmt.Errorf("TICKETMASTER_API_KEY is required")
	}
	if cfg.TicketmasterCity == "" {
		return fmt.Errorf("TICKETMASTER_CITY is required")
	}
	a := ticketmaster.New("", cfg.TicketmasterAPIKey, cfg.TicketmasterCity)
	r := scraper.NewRunner(a, q, cfg.EventsQueueURL)
	fmt.Printf("scraping %s for city=%s ...\n", a.Name(), cfg.TicketmasterCity)
	return r.Run(ctx)
}
```

Add to the import block:

```go
	"flag"

	"github.com/wmyers/heres-whats-happening/internal/queue"
	"github.com/wmyers/heres-whats-happening/internal/scraper"
	"github.com/wmyers/heres-whats-happening/internal/scraper/ticketmaster"
```

- [ ] **Step 3: Verify build and `--help`-like behavior**

```bash
go build ./cmd/app
./app
```

Expected: usage block with both `serve` and `scrape events --source=NAME` lines; exit 2.

```bash
./app scrape events
```

Expected: error message `--source is required`; exit 1.

- [ ] **Step 4: Manual smoke test (skip if no API key)**

```bash
./app scrape events --source=ticketmaster
# (will fail if TICKETMASTER_API_KEY unset)

# With a real key:
export TICKETMASTER_API_KEY=your-real-key
export TICKETMASTER_CITY="New York"
make queue-up
./app scrape events --source=ticketmaster
# Expected: "scraping ticketmaster for city=New York ..." then exits 0
```

Then verify messages landed:

```bash
curl -s "http://localhost:9324/?Action=GetQueueAttributes&AttributeName=ApproximateNumberOfMessages&QueueUrl=http://localhost:9324/000000000000/events-queue"
```

Expected: non-zero `ApproximateNumberOfMessages`.

- [ ] **Step 5: Commit**

```bash
git add cmd/app/main.go
git commit -m "feat(cmd): scrape events --source subcommand"
```

---

### Task 15: Ingest event handler (message → DB upsert)

**Files:**
- Create: `internal/ingest/events.go`
- Create: `internal/ingest/events_test.go`

- [ ] **Step 1: Write failing test**

`internal/ingest/events_test.go`:

```go
package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func defaultCityID(t *testing.T, q *store.Queries) pgtype.UUID {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	row, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	return row.ID
}

func sampleMessage() events.Message {
	return events.Message{
		SourceID:      "ticketmaster",
		SourceEventID: "tm-aaa",
		Title:         "Phoebe Bridgers",
		Description:   "Indie rock concert",
		StartsAt:      time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC),
		Venue: events.Venue{
			Name:    "The Bowl",
			Address: "100 Main St",
		},
		Performers: []string{"Phoebe Bridgers", "MUNA"},
		Genres:     []string{"indie", "rock"},
		ImageURL:   "https://example.com/p.jpg",
		URL:        "https://example.com/event/aaa",
	}
}

func TestHandle_InsertsEventVenuePerformersGenres(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewHandler(q, cityID)
	ctx := context.Background()
	require.NoError(t, h.Handle(ctx, sampleMessage()))

	// Event exists
	srcRow, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	ev, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: "tm-aaa",
	})
	require.NoError(t, err)
	require.Equal(t, "Phoebe Bridgers", ev.Title)

	// Performers
	performers, err := q.ListEventPerformersByEvent(ctx, ev.ID)
	require.NoError(t, err)
	require.Len(t, performers, 2)

	// Genres
	genres, err := q.ListEventGenresByEvent(ctx, ev.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"indie", "rock"}, genres)
}

func TestHandle_Reupsert_UpdatesLastSeenAndReplacesAssociations(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewHandler(q, cityID)
	ctx := context.Background()

	// First ingest
	require.NoError(t, h.Handle(ctx, sampleMessage()))

	// Modify performers + genres
	mod := sampleMessage()
	mod.Performers = []string{"Phoebe Bridgers"}      // dropped MUNA
	mod.Genres = []string{"folk"}                     // changed genre
	require.NoError(t, h.Handle(ctx, mod))

	srcRow, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, _ := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: "tm-aaa",
	})

	performers, _ := q.ListEventPerformersByEvent(ctx, ev.ID)
	require.Len(t, performers, 1)
	require.Equal(t, "Phoebe Bridgers", performers[0].PerformerName)

	genres, _ := q.ListEventGenresByEvent(ctx, ev.ID)
	require.ElementsMatch(t, []string{"folk"}, genres)
}

func TestHandle_UnknownGenre_SkipsSilently(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	h := ingest.NewHandler(q, cityID)
	ctx := context.Background()
	m := sampleMessage()
	m.Genres = []string{"rock", "nonexistent-genre"}
	require.NoError(t, h.Handle(ctx, m))

	srcRow, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	ev, _ := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID:      srcRow.ID,
		SourceEventID: m.SourceEventID,
	})
	genres, _ := q.ListEventGenresByEvent(ctx, ev.ID)
	require.ElementsMatch(t, []string{"rock"}, genres)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/ingest -v
```

Expected: FAIL — `package ingest; no Go files`.

- [ ] **Step 3: Implement**

`internal/ingest/events.go`:

```go
// Package ingest bridges the events-queue (SQS) into the database.
// Handler is the per-message logic; Consumer (in consumer.go) is the loop.
package ingest

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Handler applies a single events.Message to the database.
//
// Behavior:
//   - Resolves source_id from event_sources.name (assumed seeded).
//   - Upserts the venue by (city_id, normalized_name) using the configured cityID.
//   - Upserts the event by (source_id, source_event_id) — resets archived_at and
//     bumps last_seen_at.
//   - Replaces event_performers and event_genres for the event.
//   - Genres not in the controlled vocab are silently dropped.
type Handler struct {
	q      *store.Queries
	cityID pgtype.UUID
}

func NewHandler(q *store.Queries, cityID pgtype.UUID) *Handler {
	return &Handler{q: q, cityID: cityID}
}

func (h *Handler) Handle(ctx context.Context, m events.Message) error {
	src, err := h.q.GetEventSourceByName(ctx, m.SourceID)
	if err != nil {
		return fmt.Errorf("lookup source %q: %w", m.SourceID, err)
	}

	// Upsert venue
	venueID, err := h.q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID:         h.cityID,
		Name:           m.Venue.Name,
		NormalizedName: events.NormalizeString(m.Venue.Name),
		Address:        pgText(m.Venue.Address),
		Lat:            pgFloat(m.Venue.Lat),
		Lng:            pgFloat(m.Venue.Lng),
		WebsiteUrl:     pgText(m.Venue.WebsiteURL),
	})
	if err != nil {
		return fmt.Errorf("upsert venue: %w", err)
	}

	// Upsert event
	eventID, err := h.q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: m.SourceEventID,
		Title:         m.Title,
		Description:   m.Description,
		StartsAt:      pgtype.Timestamptz{Time: m.StartsAt, Valid: true},
		EndsAt:        pgTimePtr(m.EndsAt),
		VenueID:       venueID,
		ImageUrl:      pgText(m.ImageURL),
		Url:           pgText(m.URL),
	})
	if err != nil {
		return fmt.Errorf("upsert event: %w", err)
	}

	// Replace performers
	if err := h.q.DeleteEventPerformersByEvent(ctx, eventID); err != nil {
		return fmt.Errorf("delete performers: %w", err)
	}
	for _, p := range m.Performers {
		if p == "" {
			continue
		}
		if err := h.q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
			EventID:        eventID,
			PerformerName:  p,
			NormalizedName: events.NormalizeString(p),
		}); err != nil {
			return fmt.Errorf("insert performer %q: %w", p, err)
		}
	}

	// Replace genres (drop unknown ones)
	if err := h.q.DeleteEventGenresByEvent(ctx, eventID); err != nil {
		return fmt.Errorf("delete genres: %w", err)
	}
	for _, g := range m.Genres {
		slug := events.NormalizeGenre(g)
		if slug == "" {
			continue
		}
		exists, err := h.q.GenreExists(ctx, slug)
		if err != nil {
			return fmt.Errorf("check genre %q: %w", slug, err)
		}
		if !exists {
			continue
		}
		if err := h.q.InsertEventGenre(ctx, store.InsertEventGenreParams{
			EventID:   eventID,
			GenreSlug: slug,
		}); err != nil {
			return fmt.Errorf("insert genre %q: %w", slug, err)
		}
	}

	return nil
}

func pgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func pgFloat(f *float64) pgtype.Float8 {
	if f == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *f, Valid: true}
}

func pgTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
```

Add `"time"` to the imports. (If sqlc generated different field types than `pgtype.Text`/`pgtype.Float8`/`pgtype.Timestamptz`, adjust the helpers to match what `internal/store/events.sql.go` and `internal/store/venues.sql.go` actually expect.)

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ingest -v
```

Expected: all three tests PASS. (If type-mismatch compile errors appear, adjust the `pg*` helpers to match the sqlc-generated parameter types.)

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/events.go internal/ingest/events_test.go
git commit -m "feat(ingest): per-message handler — upserts venue, event, performers, genres"
```

---

### Task 16: Ingest consumer loop

**Files:**
- Create: `internal/ingest/consumer.go`
- Create: `internal/ingest/consumer_test.go`

- [ ] **Step 1: Write failing test**

`internal/ingest/consumer_test.go`:

```go
package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/ingest"
	"github.com/wmyers/heres-whats-happening/internal/queue"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func testQueueURL() string {
	return "http://localhost:9324/000000000000/events-queue"
}

func TestConsumer_E2E_ElasticMQToPostgres(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)

	qClient, err := queue.NewClient(context.Background(), "us-east-1", "http://localhost:9324")
	require.NoError(t, err)

	require.NoError(t, qClient.Purge(context.Background(), testQueueURL()))

	// Publish a message
	body, _ := json.Marshal(sampleMessage())
	require.NoError(t, qClient.Send(context.Background(), testQueueURL(), body))

	// Run consumer for a short window
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h := ingest.NewHandler(q, cityID)
	c := ingest.NewConsumer(qClient, testQueueURL(), h, 1)
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	// Wait until the event is in the DB or the context times out.
	require.Eventually(t, func() bool {
		src, err := q.GetEventSourceByName(context.Background(), "ticketmaster")
		if err != nil {
			return false
		}
		_, err = q.GetEventBySourceKey(context.Background(), store.GetEventBySourceKeyParams{
			SourceID:      src.ID,
			SourceEventID: "tm-aaa",
		})
		return err == nil
	}, 5*time.Second, 100*time.Millisecond)

	cancel()
	<-done
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
make db-up
make queue-up
go test ./internal/ingest -v -run TestConsumer
```

Expected: FAIL — `undefined: ingest.NewConsumer`.

- [ ] **Step 3: Implement**

`internal/ingest/consumer.go`:

```go
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/queue"
)

// QueueClient is the subset of *queue.Client the consumer needs. Mockable.
type QueueClient interface {
	Receive(ctx context.Context, queueURL string, max int32, wait time.Duration) ([]queue.Message, error)
	Delete(ctx context.Context, queueURL, receiptHandle string) error
}

// Consumer runs N worker goroutines, each long-polling SQS and dispatching
// to a Handler. Messages that handler() succeeds on are deleted; failures are
// left to be retried by SQS visibility timeout.
type Consumer struct {
	q        QueueClient
	queueURL string
	h        *Handler
	workers  int
}

func NewConsumer(q QueueClient, queueURL string, h *Handler, workers int) *Consumer {
	if workers < 1 {
		workers = 1
	}
	return &Consumer{q: q, queueURL: queueURL, h: h, workers: workers}
}

func (c *Consumer) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.workerLoop(ctx, id)
		}(i)
	}
	wg.Wait()
	return nil
}

func (c *Consumer) workerLoop(ctx context.Context, id int) {
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := c.q.Receive(ctx, c.queueURL, 10, 20*time.Second)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("ingest worker %d: receive: %v", id, err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, m := range msgs {
			c.handleOne(ctx, m, id)
		}
	}
}

func (c *Consumer) handleOne(ctx context.Context, m queue.Message, workerID int) {
	var em events.Message
	if err := json.Unmarshal(m.Body, &em); err != nil {
		// Malformed message — log and delete so we don't retry forever.
		log.Printf("ingest worker %d: bad message body: %v", workerID, err)
		_ = c.q.Delete(ctx, c.queueURL, m.ReceiptHandle)
		return
	}
	if err := c.h.Handle(ctx, em); err != nil {
		log.Printf("ingest worker %d: handle %s/%s: %v", workerID, em.SourceID, em.SourceEventID, err)
		// Leave on queue — SQS will redeliver after visibility timeout.
		return
	}
	if err := c.q.Delete(ctx, c.queueURL, m.ReceiptHandle); err != nil {
		log.Printf("ingest worker %d: delete %s: %v", workerID, em.SourceEventID, err)
	}
}

// guard against unused-import warning when we add fmt later
var _ = fmt.Errorf
```

(Drop the `var _` line if `go vet` is clean without it.)

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ingest -v
```

Expected: all ingest tests PASS — both the unit tests from Task 15 and the new E2E test.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/consumer.go internal/ingest/consumer_test.go
git commit -m "feat(ingest): SQS consumer loop with N workers and handler dispatch"
```

---

### Task 17: Wire consumer into `app serve`

**Files:**
- Modify: `internal/http/server.go` — extend `Server` to optionally start a consumer
- Modify: `cmd/app/main.go` — wire it up

The cleanest place to put the consumer is at the same level as the HTTP server — both are long-running things `app serve` runs. We extend `Server.Run` to launch the consumer alongside the HTTP server, both bound to the same context.

- [ ] **Step 1: Extend `Server` struct in `internal/http/server.go`**

Add the optional consumer field and update `Run`:

```go
// Add to the import block:
//   "github.com/wmyers/heres-whats-happening/internal/ingest"

// Add to the Server struct (after DefaultCityID):
type Server struct {
	Addr          string
	DB            *pgxpool.Pool
	Queries       *store.Queries
	JWTSigner     *auth.JWTSigner
	RefreshTTL    time.Duration
	DefaultCityID string

	// Optional. If non-nil, Run also starts the ingest consumer.
	IngestConsumer *ingest.Consumer
}

// Replace Run with:
func (s *Server) Run(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 2)
	go func() { errCh <- httpSrv.ListenAndServe() }()

	if s.IngestConsumer != nil {
		go func() { errCh <- s.IngestConsumer.Run(ctx) }()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
```

- [ ] **Step 2: Update `serve()` in `cmd/app/main.go` to construct the consumer**

Replace the `serve()` function body's tail (after `q := store.New(pool)` and city load) with:

```go
	// Build SQS client and consumer if EVENTS_QUEUE_URL is set.
	var consumer *ingest.Consumer
	if cfg.EventsQueueURL != "" {
		qClient, err := queue.NewClient(ctx, cfg.AWSRegion, cfg.SQSEndpoint)
		if err != nil {
			return fmt.Errorf("queue client: %w", err)
		}
		h := ingest.NewHandler(q, city.ID)
		consumer = ingest.NewConsumer(qClient, cfg.EventsQueueURL, h, cfg.IngestWorkers)
	}

	s := &hs.Server{
		Addr:           cfg.HTTPAddr,
		DB:             pool,
		Queries:        q,
		JWTSigner:      auth.NewJWTSigner(cfg.JWTSigningKey, cfg.JWTAccessTTL),
		RefreshTTL:     cfg.RefreshTTL,
		DefaultCityID:  cityIDString(city.ID),
		IngestConsumer: consumer,
	}
	fmt.Printf("listening on %s (ingest workers=%d)\n", cfg.HTTPAddr, cfg.IngestWorkers)
	return s.Run(ctx)
```

Add `"github.com/wmyers/heres-whats-happening/internal/ingest"` to the import block.

- [ ] **Step 3: Verify build and full test suite**

```bash
go build ./...
go test ./... -count=1
```

Expected: clean build, all tests pass.

- [ ] **Step 4: Manual smoke test**

```bash
make queue-up
./app serve &
SERVE_PID=$!
sleep 1
# scrape (this publishes to events-queue)
./app scrape events --source=ticketmaster   # requires API key
# wait a moment for the consumer to drain
sleep 3
docker exec hwh_postgres psql -U app -d appdb -c "SELECT count(*) FROM events;"
kill $SERVE_PID
```

Expected: `count` > 0.

- [ ] **Step 5: Commit**

```bash
git add internal/http/server.go cmd/app/main.go
git commit -m "feat: wire ingest consumer into app serve"
```

---

### Task 18: README updates

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append a Plan 2 quickstart section to `README.md`**

Insert before the "Try the auth flow" section (or append to the end if the structure has shifted):

````markdown
## Plan 2 quickstart — event ingest

```bash
# Start ElasticMQ (local SQS) alongside Postgres
make queue-up

# Set your Ticketmaster API key (free, https://developer.ticketmaster.com)
export TICKETMASTER_API_KEY=<your-key>
export TICKETMASTER_CITY="New York"

# Run a one-shot scrape (publishes EventMessage records to events-queue)
./app scrape events --source=ticketmaster

# Run the server with the ingest consumer enabled. The consumer drains
# events-queue and upserts into Postgres.
make run
# In another shell:
docker exec hwh_postgres psql -U app -d appdb -c "SELECT count(*) FROM events;"
```

The ingest pipeline is decoupled: scraping and serving are independent
processes that communicate through the queue. You can run the scraper without
the server (messages queue up) or the server without the scraper (consumer
sits idle, long-polling).
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: Plan 2 ingest quickstart"
```

---

## Self-Review

**Spec coverage check (Plan 2 scope only):**

| Spec requirement | Implemented in |
|---|---|
| ElasticMQ for local SQS | Task 1 |
| `events-queue` + `events-dlq` with redrive | Task 1 (elasticmq.conf) |
| Two SQS queues per spec, but interests-queue deferred to Plan 3 | Task 1 |
| `event_sources` table seeded with `ticketmaster` row | Task 3 |
| `venues` table with unique `(city_id, normalized_name)` | Task 3 |
| `genres` controlled vocab table | Task 3 (26 rows seeded) |
| `events` table with `vector(384)` embedding column (left null) | Task 4 |
| `event_performers` many-to-many | Task 5 |
| `event_genres` many-to-many | Task 5 |
| `last_seen_at` updated on every upsert | Task 7 (UpsertEvent query) |
| `archived_at` reset to NULL on upsert | Task 7 (UpsertEvent query) |
| Canonical `EventMessage` schema matching spec section 5 | Task 8 |
| Source-tag → controlled vocab mapping | Task 9 |
| SQS client (Send/Receive/Delete) | Task 10 |
| Adapter interface | Task 11 |
| Ticketmaster Discovery API adapter | Task 12 |
| Scraper runner: Fetch → Send | Task 13 |
| `app scrape events --source=NAME` subcommand | Task 14 |
| Ingest consumer with N workers | Tasks 15, 16 |
| Consumer wired into `app serve` | Task 17 |
| Idempotent upserts (re-delivery safe) | Tasks 7, 15 |
| DLQ after 3 receives | Task 1 (elasticmq.conf) |

**Deferred to later plans (per spec, not Plan 2 scope):**
- Embedding generation (Plan 4 / match-job)
- Spotify scraper + interests-queue (Plan 3)
- Match job + `user_event_match` table (Plan 4)
- Calendar API + iCal feed (Plan 5)
- Frontend (Plan 6)
- Terraform / CI/CD / production SQS (Plans 7, 8)
- Archival job (Plan 4 — `match-job` does this step)

**Placeholder scan:** no "TBD", "TODO", or generic "handle errors" steps. Every code-touching step has full code.

**Type consistency check:**

- `events.Message` and `events.Venue` are defined once in Task 8 and referenced in Tasks 9, 10, 12, 13, 14, 15.
- `events.NormalizeGenre` and `events.NormalizeString` defined in Task 9, used in Tasks 12 and 15.
- `queue.Client`, `queue.Message`, `Send/Receive/Delete/Purge` defined in Task 10; consumed in Tasks 13 (via Publisher interface), 14, 16 (via QueueClient interface), 17.
- `scraper.Adapter` interface defined in Task 11; implemented in Task 12; consumed in Task 13.
- `scraper.Runner` defined in Task 13; constructed in Task 14.
- `ingest.Handler` defined in Task 15; consumed in Tasks 16 (Consumer holds one) and 17 (constructed in serve()).
- `ingest.Consumer` defined in Task 16; consumed in Task 17 (`Server.IngestConsumer`).
- sqlc-generated parameter types: pgtype-based per Plan 1's pattern (Float8, Text, Timestamptz, UUID). Task 15's `pg*` helpers convert from Go-native types to pgtype.
- `pgtype.UUID` for primary-key arguments is consistent with Plan 1 (`uuid.UUID(.Bytes)` conversions stay inside the http handlers; the ingest path uses pgtype directly throughout).

**Plan-internal consistency notes:**

- Task 12's adapter `New(baseURL, apiKey, city)` signature is used in Task 14's `runTicketmasterScrape`. Pass `""` for baseURL to use the production endpoint.
- Task 14's `scrape` flag parsing uses `flag.NewFlagSet("scrape events", flag.ExitOnError)`; the subcommand is `app scrape events --source=NAME` (two positional words before flags). The dispatch handles this in Task 14 Step 2 by slicing `args[1:]` after asserting `args[0] == "events"`.
- Task 16's `Consumer.Run` returns nil after `wg.Wait()` regardless of why workers exited (errors are logged, not propagated). Task 17's `Server.Run` doesn't depend on consumer return values; the consumer's normal exit path is `ctx.Done()`.
- The `pg*` helpers in Task 15 produce zero-value pgtype values when input is empty/nil. sqlc-generated code with `emit_pointers_for_null_types: true` (Plan 1's sqlc.yaml) may have generated pointer types for nullable columns instead — if so, the helpers should return pointer-or-nil. Adjust at implementation time based on what `internal/store/events.sql.go` declares.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-19-plan-02-event-ingest.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
