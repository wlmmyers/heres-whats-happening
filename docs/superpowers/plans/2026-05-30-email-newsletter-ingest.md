# Email-Newsletter Event Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ingest live-music events from promoter email newsletters (including flyer-image-only emails) into the existing events pipeline via SES → S3 → a Node+Mastra parser Lambda → the events-queue.

**Architecture:** A new push-based producer. SES receives mail at `shows@inbound.<domain>`, drops the raw MIME into S3, and S3 triggers a Node+Mastra Lambda. The Lambda parses the email (text-first, multimodal image fallback), emits canonical `events.Message` JSON, and `SendMessageBatch`es it to the events-queue. The existing Go `internal/ingest` consumer upserts unchanged. Dedup rides the existing `UNIQUE (source_id, source_event_id)` key with `source_event_id` = a deterministic content hash; the single shared `email_newsletter` source makes dedup span promoters; that source is exempt from the 7-day stale-archive sweep.

**Tech Stack:** Go 1.24 (sqlc/pgx, golang-migrate), Postgres; Node 20 + TypeScript, Mastra (`@mastra/core`) + Vercel AI SDK Anthropic provider, Zod, `mailparser`, AWS SDK v3 (SQS/S3), Vitest; Terraform (SES, S3, Lambda container image via ECR), CodeBuild.

**Spec:** `docs/superpowers/specs/2026-05-30-email-newsletter-ingest-design.md`

---

## File Structure

**Phase 1 — DB contract (Go)**
- Create: `sql/migrations/0012_email_source.up.sql`, `sql/migrations/0012_email_source.down.sql` — seed the `email_newsletter` source + add `exempt_from_stale_archive`.
- Modify: `sql/queries/events.sql` — `ArchiveStaleEvents` skips exempt sources.
- Modify (generated): `internal/store/events.sql.go` — via `sqlc generate`.
- Create: `internal/ingest/archive_test.go` — exemption behavior test.
- Create: `testdata/event-message-contract/*.json` — shared Go↔TS golden fixtures.
- Create: `internal/events/contract_test.go` — Go validates fixtures unmarshal into `events.Message`.

**Phase 2 — Parser Lambda (Node/TS), new dir `lambda/email-parser/`**
- `package.json`, `tsconfig.json`, `vitest.config.ts` — scaffold.
- `src/schema.ts` — Zod `EventMessage` (wire) + `EventDraft` (LLM output).
- `src/schema.test.ts` — validates the shared golden fixtures.
- `src/hash.ts` + `src/hash.test.ts` — `normalize`, `contentHash`, `eventDateYMD`.
- `src/map.ts` + `src/map.test.ts` — `toMessage(draft)`.
- `src/email.ts` + `src/email.test.ts` — `parseEmail`, `gate`; `.eml` fixtures under `src/__fixtures__/`.
- `src/extractor.ts` — `EventExtractor` interface, `StubExtractor`, `MastraExtractor`.
- `src/sqs.ts` + `src/sqs.test.ts` — `sendBatch` (chunks of ≤10) against ElasticMQ.
- `src/handler.ts` — S3-event handler wiring + env factory.
- `src/handler.e2e.test.ts` — handler → ElasticMQ → read-back assertion.
- `scripts/invoke-local.ts` — pipe a local `.eml` through the handler against ElasticMQ.

**Phase 3 — Deploy (Terraform + CI), operator-applied**
- `lambda/email-parser/Dockerfile` — Lambda container image.
- `terraform/prod/lambda_ecr.tf`, `ses.tf`, `s3_inbound.tf`, `lambda_email.tf`, `secrets_email.tf` (or extend `secrets.tf`), plus `outputs.tf` additions.
- `ci/buildspec-lambda.yml` — build/push the Lambda image.

---

# Phase 1 — DB contract (Go)

## Task 1: Migration 0012 — seed email source + exemption column

**Files:**
- Create: `sql/migrations/0012_email_source.up.sql`
- Create: `sql/migrations/0012_email_source.down.sql`

- [ ] **Step 1: Write the up migration**

`sql/migrations/0012_email_source.up.sql`:

```sql
-- Email-newsletter ingestion source. A single shared source so the content-hash
-- dedup (source_event_id) spans all promoters.
ALTER TABLE event_sources
    ADD COLUMN exempt_from_stale_archive BOOLEAN NOT NULL DEFAULT false;

INSERT INTO event_sources (name, adapter_kind, config, exempt_from_stale_archive)
VALUES ('email_newsletter', 'email_inbound', '{}'::jsonb, true);
```

- [ ] **Step 2: Write the down migration**

`sql/migrations/0012_email_source.down.sql`:

```sql
DELETE FROM event_sources WHERE name = 'email_newsletter';
ALTER TABLE event_sources DROP COLUMN IF EXISTS exempt_from_stale_archive;
```

- [ ] **Step 3: Apply to dev + test DBs and verify**

Run:
```bash
make db-up && make migrate && make migrate-test
docker exec hwh_postgres psql -U app -d appdb -c \
  "SELECT name, adapter_kind, exempt_from_stale_archive FROM event_sources ORDER BY name;"
```
Expected: two rows — `email_newsletter | email_inbound | t` and `ticketmaster | ticketmaster_api | f`.

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/0012_email_source.up.sql sql/migrations/0012_email_source.down.sql
git commit -m "feat(db): email_newsletter source + exempt_from_stale_archive column"
```

## Task 2: Exempt the email source from ArchiveStaleEvents

**Files:**
- Modify: `sql/queries/events.sql:60-64` (`ArchiveStaleEvents`)
- Modify (generated): `internal/store/events.sql.go`
- Create: `internal/ingest/archive_test.go`

- [ ] **Step 1: Write the failing test**

`internal/ingest/archive_test.go`:

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
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// backdateLastSeen forces an event's last_seen_at into the past so the
// 7-day stale sweep would normally archive it.
func backdateLastSeen(t *testing.T, pool testdb.Pool, sourceEventID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx,
		`UPDATE events SET last_seen_at = NOW() - INTERVAL '10 days' WHERE source_event_id = $1`,
		sourceEventID)
	require.NoError(t, err)
}

func TestArchiveStaleEvents_ExemptsEmailSource(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	cityID := defaultCityID(t, q)
	h := ingest.NewEventHandler(q, cityID)
	ctx := context.Background()

	// A ticketmaster event (non-exempt) and an email event (exempt), both stale.
	tm := sampleMessage()
	tm.SourceEventID = "tm-stale"
	tmBody, _ := json.Marshal(tm)
	require.NoError(t, h.Handle(ctx, tmBody))

	em := sampleMessage()
	em.SourceID = "email_newsletter"
	em.SourceEventID = "email-stale"
	emBody, _ := json.Marshal(em)
	require.NoError(t, h.Handle(ctx, emBody))

	backdateLastSeen(t, pool, "tm-stale")
	backdateLastSeen(t, pool, "email-stale")

	require.NoError(t, q.ArchiveStaleEvents(ctx))

	tmSrc, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	tmEv, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: tmSrc.ID, SourceEventID: "tm-stale"})
	require.NoError(t, err)
	require.True(t, tmEv.ArchivedAt.Valid, "ticketmaster event should be archived")

	emSrc, _ := q.GetEventSourceByName(ctx, "email_newsletter")
	emEv, err := q.GetEventBySourceKey(ctx, store.GetEventBySourceKeyParams{
		SourceID: emSrc.ID, SourceEventID: "email-stale"})
	require.NoError(t, err)
	require.False(t, emEv.ArchivedAt.Valid, "email event must NOT be archived (exempt)")
}
```

Note: `testdb.Pool` — if `testdb` exposes the pool under a different name, use that type (check `internal/testdb/testdb.go`; `MustOpen` returns the pool). If no exported alias exists, accept `*pgxpool.Pool`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/ -run TestArchiveStaleEvents_ExemptsEmailSource -count=1 -v`
Expected: FAIL — the email event is archived (current query has no exemption).

- [ ] **Step 3: Edit the query**

`sql/queries/events.sql`, replace the `ArchiveStaleEvents` block:

```sql
-- name: ArchiveStaleEvents :exec
UPDATE events
SET archived_at = NOW(), updated_at = NOW()
WHERE archived_at IS NULL
  AND last_seen_at < NOW() - INTERVAL '7 days'
  AND source_id NOT IN (
      SELECT id FROM event_sources WHERE exempt_from_stale_archive
  );
```

- [ ] **Step 4: Regenerate sqlc**

Run: `sqlc generate`
Expected: `internal/store/events.sql.go`'s `archiveStaleEvents` const now contains the `source_id NOT IN (...)` clause. No signature change.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/ingest/ -run TestArchiveStaleEvents_ExemptsEmailSource -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Run the full ingest package + commit**

Run: `go test -p 1 ./internal/ingest/ ./internal/store/ -count=1`
Expected: PASS.

```bash
git add sql/queries/events.sql internal/store/events.sql.go internal/ingest/archive_test.go
git commit -m "feat(db): exempt email source from stale-archive sweep"
```

## Task 3: Shared Go↔TS contract fixtures + Go validation test

**Files:**
- Create: `testdata/event-message-contract/minimal.json`
- Create: `testdata/event-message-contract/full.json`
- Create: `testdata/event-message-contract/flyer.json`
- Create: `internal/events/contract_test.go`

Rationale: a repo-root `testdata/` dir is read by **both** the Go test (`../../testdata/...` from `internal/events`) and the Vitest test (`../../testdata/...` from `lambda/email-parser`). Editing the schema on one side without the other breaks a fixture on that side.

- [ ] **Step 1: Write the fixtures**

`testdata/event-message-contract/minimal.json` (only the always-present fields; `starts_at` and `venue` are non-`omitempty` in Go):

```json
{
  "source_id": "email_newsletter",
  "source_event_id": "0000000000000000000000000000000000000000000000000000000000000000",
  "title": "Some Show",
  "starts_at": "2026-06-15T20:00:00Z",
  "venue": { "name": "The Bowl" }
}
```

`testdata/event-message-contract/full.json` (every field populated):

```json
{
  "source_id": "email_newsletter",
  "source_event_id": "1111111111111111111111111111111111111111111111111111111111111111",
  "title": "Phoebe Bridgers",
  "description": "Indie rock at the Bowl",
  "starts_at": "2026-06-15T20:00:00Z",
  "ends_at": "2026-06-15T23:00:00Z",
  "venue": {
    "name": "The Bowl",
    "address": "100 Main St",
    "lat": 40.1,
    "lng": -74.2,
    "website_url": "https://thebowl.example.com"
  },
  "performers": ["Phoebe Bridgers", "MUNA"],
  "genres": ["indie", "rock"],
  "image_url": "https://example.com/p.jpg",
  "url": "https://example.com/event/aaa"
}
```

`testdata/event-message-contract/flyer.json` (flyer-derived: no performers list, no url/image):

```json
{
  "source_id": "email_newsletter",
  "source_event_id": "2222222222222222222222222222222222222222222222222222222222222222",
  "title": "DIY Punk Night",
  "starts_at": "2026-07-04T19:30:00Z",
  "venue": { "name": "Garage 51", "address": "51 Side Alley" },
  "genres": ["punk"]
}
```

- [ ] **Step 2: Write the failing test**

`internal/events/contract_test.go`:

```go
package events_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

func TestContractFixtures_UnmarshalIntoMessage(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "event-message-contract")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)

			var m events.Message
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields() // catches a TS field the Go struct lacks
			require.NoError(t, dec.Decode(&m))

			require.NotEmpty(t, m.SourceID)
			require.NotEmpty(t, m.SourceEventID)
			require.NotEmpty(t, m.Title)
			require.False(t, m.StartsAt.IsZero(), "starts_at must parse")
			require.NotEmpty(t, m.Venue.Name)
		})
	}
}
```

- [ ] **Step 3: Run test to verify it passes (fixtures already valid)**

Run: `go test ./internal/events/ -run TestContractFixtures_UnmarshalIntoMessage -count=1 -v`
Expected: PASS for all three fixtures. (If `DisallowUnknownFields` trips, a fixture has a field not on `events.Message` — fix the fixture.)

- [ ] **Step 4: Commit**

```bash
git add testdata/event-message-contract internal/events/contract_test.go
git commit -m "test(events): shared Go/TS contract fixtures + Go validation"
```

---

# Phase 2 — Parser Lambda (Node + Mastra)

All Phase 2 work is under `lambda/email-parser/`. It is fully testable locally; the only external service is ElasticMQ (already in `docker-compose`), used by two tasks. The LLM is never called in tests — it sits behind the `EventExtractor` interface (Task 9) and is stubbed.

## Task 4: Scaffold the Lambda package

**Files:**
- Create: `lambda/email-parser/package.json`
- Create: `lambda/email-parser/tsconfig.json`
- Create: `lambda/email-parser/vitest.config.ts`
- Create: `lambda/email-parser/.gitignore`

- [ ] **Step 1: Write package.json**

`lambda/email-parser/package.json`:

```json
{
  "name": "email-parser",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "tsc -p tsconfig.json",
    "test": "vitest run",
    "test:watch": "vitest",
    "typecheck": "tsc -p tsconfig.json --noEmit",
    "invoke-local": "tsx scripts/invoke-local.ts"
  },
  "dependencies": {
    "@ai-sdk/anthropic": "^1.0.0",
    "@aws-sdk/client-s3": "^3.600.0",
    "@aws-sdk/client-sqs": "^3.600.0",
    "@mastra/core": "^0.10.0",
    "mailparser": "^3.7.0",
    "zod": "^3.23.0"
  },
  "devDependencies": {
    "@types/aws-lambda": "^8.10.140",
    "@types/mailparser": "^3.4.4",
    "@types/node": "^20.14.0",
    "tsx": "^4.16.0",
    "typescript": "^5.5.0",
    "vitest": "^2.0.0"
  }
}
```

(If exact published versions differ at install time, accept the nearest compatible; pin whatever `pnpm install` resolves.)

- [ ] **Step 2: Write tsconfig.json**

`lambda/email-parser/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ES2022",
    "moduleResolution": "bundler",
    "lib": ["ES2022"],
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "resolveJsonModule": true,
    "outDir": "dist",
    "rootDir": "src",
    "types": ["node"]
  },
  "include": ["src/**/*.ts"]
}
```

- [ ] **Step 3: Write vitest.config.ts and .gitignore**

`lambda/email-parser/vitest.config.ts`:

```ts
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
```

`lambda/email-parser/.gitignore`:

```
node_modules/
dist/
```

- [ ] **Step 4: Install and verify the toolchain runs**

Run:
```bash
cd lambda/email-parser && pnpm install && pnpm typecheck
```
Expected: install succeeds; `tsc --noEmit` exits 0 (no source files yet → no errors).

- [ ] **Step 5: Commit**

```bash
git add lambda/email-parser/package.json lambda/email-parser/tsconfig.json \
  lambda/email-parser/vitest.config.ts lambda/email-parser/.gitignore \
  lambda/email-parser/pnpm-lock.yaml
git commit -m "chore(lambda): scaffold email-parser Node/TS package"
```

## Task 5: Zod schemas + TS contract test against shared fixtures

**Files:**
- Create: `lambda/email-parser/src/schema.ts`
- Create: `lambda/email-parser/src/schema.test.ts`

- [ ] **Step 1: Write the schema**

`lambda/email-parser/src/schema.ts`:

```ts
import { z } from "zod";

// Wire shape — MUST match Go internal/events.Message JSON tags exactly.
// Fields tagged `omitempty` in Go are `.optional()` here.
export const VenueSchema = z.object({
  name: z.string(),
  address: z.string().optional(),
  lat: z.number().optional(),
  lng: z.number().optional(),
  website_url: z.string().optional(),
});

export const EventMessageSchema = z.object({
  source_id: z.string(),
  source_event_id: z.string(),
  title: z.string(),
  description: z.string().optional(),
  starts_at: z.string(), // RFC3339, e.g. "2026-06-15T20:00:00Z"
  ends_at: z.string().optional(),
  venue: VenueSchema,
  performers: z.array(z.string()).optional(),
  genres: z.array(z.string()).optional(),
  image_url: z.string().optional(),
  url: z.string().optional(),
});
export type EventMessage = z.infer<typeof EventMessageSchema>;

// LLM output shape — what the Mastra agent returns per event. Distinct from the
// wire shape: no source_id/source_event_id (computed downstream), camelCase,
// performers headliner-first.
export const EventDraftSchema = z.object({
  title: z.string(),
  description: z.string().optional(),
  startsAt: z.string(), // ISO 8601 with timezone offset or Z
  endsAt: z.string().optional(),
  venue: z.object({
    name: z.string(),
    address: z.string().optional(),
    websiteUrl: z.string().optional(),
  }),
  performers: z.array(z.string()).default([]), // headliner first
  genres: z.array(z.string()).default([]),
  url: z.string().optional(),
});
export type EventDraft = z.infer<typeof EventDraftSchema>;

export const EventDraftsSchema = z.object({ events: z.array(EventDraftSchema) });
```

- [ ] **Step 2: Write the failing test**

`lambda/email-parser/src/schema.test.ts`:

```ts
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { EventMessageSchema } from "./schema.js";

// ESM has no __dirname; derive it from import.meta.url. From src/, three levels
// up is the repo root holding testdata/.
const here = fileURLToPath(new URL(".", import.meta.url));
const FIXTURE_DIR = join(here, "..", "..", "..", "testdata", "event-message-contract");

describe("EventMessageSchema accepts shared contract fixtures", () => {
  const files = readdirSync(FIXTURE_DIR).filter((f) => f.endsWith(".json"));
  it("finds fixtures", () => expect(files.length).toBeGreaterThan(0));
  for (const f of files) {
    it(`validates ${f}`, () => {
      const data = JSON.parse(readFileSync(join(FIXTURE_DIR, f), "utf8"));
      const r = EventMessageSchema.safeParse(data);
      expect(r.success, r.success ? "" : JSON.stringify(r.error.issues)).toBe(true);
    });
  }
});
```

- [ ] **Step 3: Run test**

Run: `cd lambda/email-parser && pnpm test -- src/schema.test.ts`
Expected: PASS — all three fixtures validate. (A failure means the Zod schema and Go struct disagree — fix whichever is wrong.)

- [ ] **Step 4: Commit**

```bash
git add lambda/email-parser/src/schema.ts lambda/email-parser/src/schema.test.ts
git commit -m "feat(lambda): Zod event schemas + contract test vs shared fixtures"
```

## Task 6: Hashing — normalize, contentHash, eventDateYMD

**Files:**
- Create: `lambda/email-parser/src/hash.ts`
- Create: `lambda/email-parser/src/hash.test.ts`

- [ ] **Step 1: Write the failing test**

`lambda/email-parser/src/hash.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { contentHash, eventDateYMD, normalize } from "./hash.js";

describe("normalize", () => {
  it("lowercases, trims, collapses whitespace, strips punctuation/diacritics", () => {
    expect(normalize("  Phoebe   Bridgers!! ")).toBe("phoebe bridgers");
    expect(normalize("Café Tacvba")).toBe("cafe tacvba");
  });
});

describe("eventDateYMD", () => {
  it("returns UTC YYYYMMDD (day granularity, time ignored)", () => {
    expect(eventDateYMD("2026-06-15T20:00:00Z")).toBe("20260615");
    expect(eventDateYMD("2026-06-15T23:59:00Z")).toBe(eventDateYMD("2026-06-15T08:00:00Z"));
  });
});

describe("contentHash", () => {
  it("is deterministic and order-/case-/punctuation-insensitive on inputs", () => {
    const a = contentHash("Phoebe Bridgers", "The Bowl", "20260615");
    const b = contentHash("phoebe   bridgers", "the bowl!", "20260615");
    expect(a).toBe(b);
    expect(a).toMatch(/^[0-9a-f]{64}$/);
  });
  it("differs when the date differs (the new show at the bottom)", () => {
    expect(contentHash("X", "Y", "20260615")).not.toBe(contentHash("X", "Y", "20260616"));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd lambda/email-parser && pnpm test -- src/hash.test.ts`
Expected: FAIL — `./hash.js` not found.

- [ ] **Step 3: Write the implementation**

`lambda/email-parser/src/hash.ts`:

```ts
import { createHash } from "node:crypto";

/** Deterministic normalization for hashing. Independent of Go's NormalizeString;
 * only needs to be stable within this Lambda. Lowercase, strip diacritics &
 * punctuation, collapse whitespace, trim. */
export function normalize(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // combining diacritics
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s]/gu, "") // drop punctuation/symbols
    .replace(/\s+/g, " ")
    .trim();
}

/** UTC day of an ISO timestamp, as YYYYMMDD. Time-of-day is intentionally dropped
 * so "doors 8pm" vs "9pm" don't split the same show. */
export function eventDateYMD(startsAtISO: string): string {
  const d = new Date(startsAtISO);
  if (Number.isNaN(d.getTime())) throw new Error(`invalid startsAt: ${startsAtISO}`);
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${y}${m}${day}`;
}

/** source_event_id = sha256(normHeadliner | normVenue | eventDate). */
export function contentHash(headliner: string, venue: string, dateYMD: string): string {
  return createHash("sha256")
    .update([normalize(headliner), normalize(venue), dateYMD].join("|"))
    .digest("hex");
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd lambda/email-parser && pnpm test -- src/hash.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lambda/email-parser/src/hash.ts lambda/email-parser/src/hash.test.ts
git commit -m "feat(lambda): deterministic content hash + normalization"
```

## Task 7: Map an EventDraft to an EventMessage

**Files:**
- Create: `lambda/email-parser/src/map.ts`
- Create: `lambda/email-parser/src/map.test.ts`

- [ ] **Step 1: Write the failing test**

`lambda/email-parser/src/map.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { contentHash, eventDateYMD } from "./hash.js";
import { toMessage } from "./map.js";
import type { EventDraft } from "./schema.js";

const draft: EventDraft = {
  title: "Phoebe Bridgers Live",
  startsAt: "2026-06-15T20:00:00Z",
  venue: { name: "The Bowl", address: "100 Main St" },
  performers: ["Phoebe Bridgers", "MUNA"],
  genres: ["indie"],
};

describe("toMessage", () => {
  it("sets the shared email source and a headliner-based content hash", () => {
    const m = toMessage(draft);
    expect(m.source_id).toBe("email_newsletter");
    expect(m.source_event_id).toBe(
      contentHash("Phoebe Bridgers", "The Bowl", eventDateYMD(draft.startsAt)),
    );
    expect(m.venue.website_url).toBeUndefined();
    expect(m.performers).toEqual(["Phoebe Bridgers", "MUNA"]);
  });

  it("falls back to title when there are no performers", () => {
    const m = toMessage({ ...draft, performers: [] });
    expect(m.source_event_id).toBe(
      contentHash("Phoebe Bridgers Live", "The Bowl", eventDateYMD(draft.startsAt)),
    );
    expect(m.performers).toBeUndefined();
  });

  it("re-mapping the same draft yields the same hash (idempotent re-sends)", () => {
    expect(toMessage(draft).source_event_id).toBe(toMessage(draft).source_event_id);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd lambda/email-parser && pnpm test -- src/map.test.ts`
Expected: FAIL — `./map.js` not found.

- [ ] **Step 3: Write the implementation**

`lambda/email-parser/src/map.ts`:

```ts
import { contentHash, eventDateYMD } from "./hash.js";
import { EventMessageSchema, type EventDraft, type EventMessage } from "./schema.js";

export const EMAIL_SOURCE_ID = "email_newsletter";

/** Convert one LLM draft into the canonical wire message. Headliner = performers[0],
 * falling back to title when the draft has no performers. */
export function toMessage(d: EventDraft): EventMessage {
  const headliner = d.performers[0] ?? d.title;
  const msg: EventMessage = {
    source_id: EMAIL_SOURCE_ID,
    source_event_id: contentHash(headliner, d.venue.name, eventDateYMD(d.startsAt)),
    title: d.title,
    description: d.description,
    starts_at: d.startsAt,
    ends_at: d.endsAt,
    venue: {
      name: d.venue.name,
      address: d.venue.address,
      website_url: d.venue.websiteUrl,
    },
    performers: d.performers.length ? d.performers : undefined,
    genres: d.genres.length ? d.genres : undefined,
    url: d.url,
  };
  // Defensive: guarantee what we emit satisfies the wire contract.
  return EventMessageSchema.parse(msg);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd lambda/email-parser && pnpm test -- src/map.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lambda/email-parser/src/map.ts lambda/email-parser/src/map.test.ts
git commit -m "feat(lambda): map EventDraft -> canonical EventMessage"
```

## Task 8: Email parsing + the verdict/text/image gate

**Files:**
- Create: `lambda/email-parser/src/email.ts`
- Create: `lambda/email-parser/src/email.test.ts`
- Create: `lambda/email-parser/src/__fixtures__/text-newsletter.eml`
- Create: `lambda/email-parser/src/__fixtures__/flyer-only.eml`
- Create: `lambda/email-parser/src/__fixtures__/spam.eml`

- [ ] **Step 1: Write the fixtures**

`lambda/email-parser/src/__fixtures__/text-newsletter.eml` (substantive text body; spam PASS):

```
From: promoter@venue.example
To: shows@inbound.example.com
Subject: This week at The Bowl
Date: Mon, 29 Dec 2025 09:00:00 -0500
X-SES-Spam-Verdict: PASS
X-SES-Virus-Verdict: PASS
Content-Type: text/plain; charset=utf-8

Live music this Friday, Jan 2nd! Phoebe Bridgers with MUNA at The Bowl,
100 Main St. Doors 8pm. Tickets at https://example.com/tix
```

`lambda/email-parser/src/__fixtures__/flyer-only.eml` (empty text body, one image attachment; spam PASS). A 1x1 PNG base64 stands in for a flyer:

```
From: promoter@venue.example
To: shows@inbound.example.com
Subject: Show flyer
Date: Mon, 29 Dec 2025 09:00:00 -0500
X-SES-Spam-Verdict: PASS
X-SES-Virus-Verdict: PASS
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8

--b1
Content-Type: image/png; name="flyer.png"
Content-Transfer-Encoding: base64
Content-Disposition: attachment; filename="flyer.png"

iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==
--b1--
```

`lambda/email-parser/src/__fixtures__/spam.eml` (spam FAIL — must be skipped):

```
From: spammer@bad.example
To: shows@inbound.example.com
Subject: cheap meds
Date: Mon, 29 Dec 2025 09:00:00 -0500
X-SES-Spam-Verdict: FAIL
X-SES-Virus-Verdict: PASS
Content-Type: text/plain; charset=utf-8

buy now
```

- [ ] **Step 2: Write the failing test**

`lambda/email-parser/src/email.test.ts`:

```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { gate, parseEmail } from "./email.js";

const dir = fileURLToPath(new URL("./__fixtures__/", import.meta.url));
const load = (f: string) => readFileSync(join(dir, f));

describe("parseEmail + gate", () => {
  it("text newsletter -> 'text' with body and Date", async () => {
    const p = await parseEmail(load("text-newsletter.eml"));
    expect(p.spamFail).toBe(false);
    expect(p.text).toMatch(/Phoebe Bridgers/);
    expect(p.date).toBeTypeOf("string");
    expect(gate(p)).toBe("text");
  });

  it("flyer-only -> 'image' with at least one image attachment", async () => {
    const p = await parseEmail(load("flyer-only.eml"));
    expect(p.text.trim().length).toBeLessThan(40);
    expect(p.images.length).toBe(1);
    expect(p.images[0].contentType).toBe("image/png");
    expect(gate(p)).toBe("image");
  });

  it("spam-FAIL -> 'skip'", async () => {
    const p = await parseEmail(load("spam.eml"));
    expect(p.spamFail).toBe(true);
    expect(gate(p)).toBe("skip");
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd lambda/email-parser && pnpm test -- src/email.test.ts`
Expected: FAIL — `./email.js` not found.

- [ ] **Step 4: Write the implementation**

`lambda/email-parser/src/email.ts`:

```ts
import { simpleParser } from "mailparser";

export const TEXT_MIN_CHARS = 40;

export interface EmailImage {
  contentType: string;
  data: Buffer;
}

export interface ParsedEmail {
  spamFail: boolean;
  virusFail: boolean;
  date?: string; // RFC string of the Date header, for LLM year context
  text: string;
  images: EmailImage[];
}

function verdictFails(value: string | undefined): boolean {
  return (value ?? "").trim().toUpperCase() === "FAIL";
}

export async function parseEmail(raw: Buffer): Promise<ParsedEmail> {
  const parsed = await simpleParser(raw);
  const spam = parsed.headers.get("x-ses-spam-verdict") as string | undefined;
  const virus = parsed.headers.get("x-ses-virus-verdict") as string | undefined;

  const images: EmailImage[] = (parsed.attachments ?? [])
    .filter((a) => a.contentType?.startsWith("image/"))
    .map((a) => ({ contentType: a.contentType, data: a.content }));

  return {
    spamFail: verdictFails(spam),
    virusFail: verdictFails(virus),
    date: parsed.date ? parsed.date.toUTCString() : undefined,
    text: (parsed.text ?? "").trim(),
    images,
  };
}

export type GateDecision = "skip" | "text" | "image";

/** Decide how (or whether) to parse: drop spam/virus, prefer the text body,
 * fall back to images only when the body is too thin to parse. */
export function gate(p: ParsedEmail): GateDecision {
  if (p.spamFail || p.virusFail) return "skip";
  if (p.text.length >= TEXT_MIN_CHARS) return "text";
  if (p.images.length > 0) return "image";
  return "skip";
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd lambda/email-parser && pnpm test -- src/email.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add lambda/email-parser/src/email.ts lambda/email-parser/src/email.test.ts \
  lambda/email-parser/src/__fixtures__
git commit -m "feat(lambda): MIME parse + verdict/text/image gate"
```

## Task 9: EventExtractor interface + Stub + Mastra implementation

**Files:**
- Create: `lambda/email-parser/src/extractor.ts`
- Create: `lambda/email-parser/src/extractor.test.ts`

The extractor is the LLM boundary. Everything else depends on the **interface**, so tests inject `StubExtractor` and never call a real model (mirrors the Go matcher's `fakeEmbedder`). `MastraExtractor` is the real impl; it is exercised only via the manual `invoke-local` harness or a skipped integration test (needs `ANTHROPIC_API_KEY`).

- [ ] **Step 1: Write the failing test**

`lambda/email-parser/src/extractor.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { StubExtractor } from "./extractor.js";
import type { EventDraft } from "./schema.js";

const drafts: EventDraft[] = [
  {
    title: "Phoebe Bridgers",
    startsAt: "2026-01-02T20:00:00-05:00",
    venue: { name: "The Bowl" },
    performers: ["Phoebe Bridgers"],
    genres: [],
  },
];

describe("StubExtractor", () => {
  it("returns the canned drafts and records the input it was given", async () => {
    const stub = new StubExtractor(drafts);
    const out = await stub.extract({ mode: "text", text: "hi", images: [], receivedAt: "x" });
    expect(out).toEqual(drafts);
    expect(stub.calls).toHaveLength(1);
    expect(stub.calls[0].mode).toBe("text");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd lambda/email-parser && pnpm test -- src/extractor.test.ts`
Expected: FAIL — `./extractor.js` not found.

- [ ] **Step 3: Write the implementation**

`lambda/email-parser/src/extractor.ts`:

```ts
import { anthropic } from "@ai-sdk/anthropic";
import { Agent } from "@mastra/core/agent";
import type { EmailImage } from "./email.js";
import { EventDraftsSchema, type EventDraft } from "./schema.js";

export interface ExtractInput {
  mode: "text" | "image";
  text: string;
  images: EmailImage[];
  receivedAt?: string; // Date header — injected for relative-date year resolution
}

export interface EventExtractor {
  extract(input: ExtractInput): Promise<EventDraft[]>;
}

/** Test double. Returns canned drafts; records inputs for assertions. */
export class StubExtractor implements EventExtractor {
  public calls: ExtractInput[] = [];
  constructor(private readonly drafts: EventDraft[]) {}
  async extract(input: ExtractInput): Promise<EventDraft[]> {
    this.calls.push(input);
    return this.drafts;
  }
}

const INSTRUCTIONS = `You extract live-music events from a concert promoter's email.
Return ONE entry per distinct show. Each show: title, ISO 8601 startsAt (with timezone),
venue name (+ address if present), and performers ordered HEADLINER FIRST.
If the email lists the same lineup as a prior week plus one new show, still return every
show you can see — downstream dedup handles repeats.
If the email is not about events, return an empty list.`;

export class MastraExtractor implements EventExtractor {
  private readonly agent: Agent;
  constructor(model = "claude-3-5-sonnet-20241022") {
    this.agent = new Agent({
      name: "email-event-extractor",
      instructions: INSTRUCTIONS,
      model: anthropic(model),
    });
  }

  async extract(input: ExtractInput): Promise<EventDraft[]> {
    const dateLine = input.receivedAt
      ? `This email was received on ${input.receivedAt}. Use it to resolve the correct year for relative dates such as "this Friday".`
      : "";

    const content: Array<Record<string, unknown>> =
      input.mode === "image"
        ? [
            { type: "text", text: `${dateLine}\nExtract the events shown in the attached flyer image(s).` },
            ...input.images.map((img) => ({
              type: "image",
              image: img.data,
              mimeType: img.contentType,
            })),
          ]
        : [{ type: "text", text: `${dateLine}\n\n${input.text}` }];

    const res = await this.agent.generate(
      [{ role: "user", content: content as never }],
      { output: EventDraftsSchema },
    );
    return res.object.events;
  }
}
```

Note: confirm the `@mastra/core` agent API at install time — the structured-output call is `agent.generate(messages, { output: zodSchema })` returning `{ object }`. If the installed version differs, adjust this file only; the interface and all consumers are unaffected. Multimodal image parts use the Vercel AI SDK content-part shape (`{ type: "image", image, mimeType }`).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd lambda/email-parser && pnpm test -- src/extractor.test.ts`
Expected: PASS. (Only `StubExtractor` is tested; `MastraExtractor` is not instantiated, so no API key is needed.)

- [ ] **Step 5: Typecheck (catches Mastra API mismatch early) + commit**

Run: `cd lambda/email-parser && pnpm typecheck`
Expected: exits 0.

```bash
git add lambda/email-parser/src/extractor.ts lambda/email-parser/src/extractor.test.ts
git commit -m "feat(lambda): EventExtractor interface, stub, Mastra impl"
```

## Task 10: SQS batch sender

**Files:**
- Create: `lambda/email-parser/src/sqs.ts`
- Create: `lambda/email-parser/src/sqs.test.ts`

- [ ] **Step 1: Write the failing test (runs against ElasticMQ)**

`lambda/email-parser/src/sqs.test.ts`:

```ts
import {
  CreateQueueCommand,
  DeleteQueueCommand,
  ReceiveMessageCommand,
  SQSClient,
} from "@aws-sdk/client-sqs";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { sendBatch } from "./sqs.js";
import type { EventMessage } from "./schema.js";

const ENDPOINT = process.env.SQS_ENDPOINT ?? "http://localhost:9324";

function client() {
  return new SQSClient({
    region: "us-east-1",
    endpoint: ENDPOINT,
    credentials: { accessKeyId: "local", secretAccessKey: "local" },
  });
}

function msg(i: number): EventMessage {
  return {
    source_id: "email_newsletter",
    source_event_id: `hash-${i}`,
    title: `Show ${i}`,
    starts_at: "2026-06-15T20:00:00Z",
    venue: { name: "The Bowl" },
  };
}

describe("sendBatch (ElasticMQ)", () => {
  const sqs = client();
  let queueUrl: string;

  beforeAll(async () => {
    const r = await sqs.send(new CreateQueueCommand({ QueueName: `sqs-test-${Date.now()}` }));
    queueUrl = r.QueueUrl!;
  });
  afterAll(async () => {
    if (queueUrl) await sqs.send(new DeleteQueueCommand({ QueueUrl: queueUrl }));
  });

  it("chunks >10 messages and delivers all of them", async () => {
    const messages = Array.from({ length: 23 }, (_, i) => msg(i));
    await sendBatch(sqs, queueUrl, messages);

    const received = new Set<string>();
    for (let i = 0; i < 6 && received.size < 23; i++) {
      const out = await sqs.send(
        new ReceiveMessageCommand({ QueueUrl: queueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 1 }),
      );
      for (const m of out.Messages ?? []) {
        received.add(JSON.parse(m.Body!).source_event_id);
      }
    }
    expect(received.size).toBe(23);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
make queue-up            # from repo root, if not already running
cd lambda/email-parser && pnpm test -- src/sqs.test.ts
```
Expected: FAIL — `./sqs.js` not found.

- [ ] **Step 3: Write the implementation**

`lambda/email-parser/src/sqs.ts`:

```ts
import { SendMessageBatchCommand, type SQSClient } from "@aws-sdk/client-sqs";
import type { EventMessage } from "./schema.js";

const MAX_BATCH = 10; // SQS SendMessageBatch hard limit

/** Send messages to the events-queue in batches of <=10. Throws if any entry
 * fails after the batch call (the caller fails the invocation -> retry/DLQ;
 * safe because source_event_id is a deterministic hash + the consumer upserts). */
export async function sendBatch(
  sqs: SQSClient,
  queueUrl: string,
  messages: EventMessage[],
): Promise<void> {
  for (let i = 0; i < messages.length; i += MAX_BATCH) {
    const chunk = messages.slice(i, i + MAX_BATCH);
    const out = await sqs.send(
      new SendMessageBatchCommand({
        QueueUrl: queueUrl,
        Entries: chunk.map((m, j) => ({
          Id: String(i + j),
          MessageBody: JSON.stringify(m),
        })),
      }),
    );
    if (out.Failed && out.Failed.length > 0) {
      throw new Error(`SendMessageBatch failed for ${out.Failed.length} entr(ies): ${JSON.stringify(out.Failed)}`);
    }
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd lambda/email-parser && pnpm test -- src/sqs.test.ts`
Expected: PASS — all 23 delivered.

- [ ] **Step 5: Commit**

```bash
git add lambda/email-parser/src/sqs.ts lambda/email-parser/src/sqs.test.ts
git commit -m "feat(lambda): SQS SendMessageBatch sender (chunks of 10)"
```

## Task 11: Handler wiring + env factory + local-invoke harness

**Files:**
- Create: `lambda/email-parser/src/handler.ts`
- Create: `lambda/email-parser/src/handler.test.ts`
- Create: `lambda/email-parser/scripts/invoke-local.ts`

- [ ] **Step 1: Write the failing test (handler with injected deps, no AWS)**

`lambda/email-parser/src/handler.test.ts`:

```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { processEmail } from "./handler.js";
import { StubExtractor } from "./extractor.js";
import type { EventDraft } from "./schema.js";

const dir = fileURLToPath(new URL("./__fixtures__/", import.meta.url));
const load = (f: string) => readFileSync(join(dir, f));

const draft: EventDraft = {
  title: "Phoebe Bridgers",
  startsAt: "2026-01-02T20:00:00-05:00",
  venue: { name: "The Bowl" },
  performers: ["Phoebe Bridgers"],
  genres: [],
};

describe("processEmail", () => {
  it("text email -> extractor called in 'text' mode, mapped message emitted", async () => {
    const stub = new StubExtractor([draft]);
    const sent: unknown[] = [];
    await processEmail(load("text-newsletter.eml"), {
      extractor: stub,
      emit: async (msgs) => void sent.push(...msgs),
    });
    expect(stub.calls[0].mode).toBe("text");
    expect(stub.calls[0].receivedAt).toBeTypeOf("string"); // Date header injected
    expect(sent).toHaveLength(1);
    expect((sent[0] as { source_id: string }).source_id).toBe("email_newsletter");
  });

  it("spam email -> extractor NOT called, nothing emitted", async () => {
    const stub = new StubExtractor([draft]);
    const emit = vi.fn();
    await processEmail(load("spam.eml"), { extractor: stub, emit });
    expect(stub.calls).toHaveLength(0);
    expect(emit).not.toHaveBeenCalled();
  });

  it("flyer-only email -> extractor called in 'image' mode", async () => {
    const stub = new StubExtractor([draft]);
    await processEmail(load("flyer-only.eml"), { extractor: stub, emit: async () => {} });
    expect(stub.calls[0].mode).toBe("image");
    expect(stub.calls[0].images).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd lambda/email-parser && pnpm test -- src/handler.test.ts`
Expected: FAIL — `./handler.js` not found.

- [ ] **Step 3: Write the implementation**

`lambda/email-parser/src/handler.ts`:

```ts
import { GetObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { SQSClient } from "@aws-sdk/client-sqs";
import type { S3Event } from "aws-lambda";
import { gate, parseEmail } from "./email.js";
import { MastraExtractor, type EventExtractor } from "./extractor.js";
import { toMessage } from "./map.js";
import type { EventMessage } from "./schema.js";
import { sendBatch } from "./sqs.js";

export interface ProcessDeps {
  extractor: EventExtractor;
  emit: (messages: EventMessage[]) => Promise<void>;
}

/** Core, dependency-injected pipeline for one raw email. Pure of AWS wiring so
 * it is unit-testable; the Lambda entrypoint supplies real deps. */
export async function processEmail(raw: Buffer, deps: ProcessDeps): Promise<void> {
  const parsed = await parseEmail(raw);
  const decision = gate(parsed);
  if (decision === "skip") {
    console.log(JSON.stringify({ msg: "skip", spamFail: parsed.spamFail, virusFail: parsed.virusFail }));
    return;
  }
  const drafts = await deps.extractor.extract({
    mode: decision,
    text: parsed.text,
    images: parsed.images,
    receivedAt: parsed.date,
  });
  if (drafts.length === 0) {
    console.log(JSON.stringify({ msg: "no-events", mode: decision }));
    return;
  }
  await deps.emit(drafts.map(toMessage));
  console.log(JSON.stringify({ msg: "emitted", count: drafts.length, mode: decision }));
}

function requireEnv(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`missing env var ${name}`);
  return v;
}

/** Build production deps from the environment. */
function prodDeps(): ProcessDeps {
  const region = requireEnv("AWS_REGION");
  const queueUrl = requireEnv("EVENTS_QUEUE_URL");
  const endpoint = process.env.SQS_ENDPOINT || undefined; // set for local/ElasticMQ
  const sqs = new SQSClient({ region, endpoint });
  return {
    extractor: new MastraExtractor(process.env.LLM_MODEL || undefined),
    emit: (messages) => sendBatch(sqs, queueUrl, messages),
  };
}

const s3 = new S3Client({ region: process.env.AWS_REGION });

async function getObject(bucket: string, key: string): Promise<Buffer> {
  const out = await s3.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
  const bytes = await out.Body!.transformToByteArray();
  return Buffer.from(bytes);
}

/** AWS Lambda entrypoint: S3 ObjectCreated -> fetch raw email -> process. */
export async function handler(event: S3Event): Promise<void> {
  const deps = prodDeps();
  for (const rec of event.Records) {
    const bucket = rec.s3.bucket.name;
    const key = decodeURIComponent(rec.s3.object.key.replace(/\+/g, " "));
    const raw = await getObject(bucket, key);
    await processEmail(raw, deps);
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd lambda/email-parser && pnpm test -- src/handler.test.ts`
Expected: PASS (all three cases).

- [ ] **Step 5: Write the local-invoke harness**

`lambda/email-parser/scripts/invoke-local.ts`:

```ts
/* Pipe a local .eml through the REAL extractor to ElasticMQ.
 * Usage: ANTHROPIC_API_KEY=... EVENTS_QUEUE_URL=... SQS_ENDPOINT=http://localhost:9324 \
 *        pnpm invoke-local path/to/email.eml
 * Requires `make queue-up` and a created queue. */
import { readFileSync } from "node:fs";
import { SQSClient } from "@aws-sdk/client-sqs";
import { MastraExtractor } from "../src/extractor.js";
import { processEmail } from "../src/handler.js";
import { sendBatch } from "../src/sqs.js";

const file = process.argv[2];
if (!file) throw new Error("usage: pnpm invoke-local <email.eml>");

const region = process.env.AWS_REGION ?? "us-east-1";
const queueUrl = process.env.EVENTS_QUEUE_URL;
if (!queueUrl) throw new Error("set EVENTS_QUEUE_URL");
const sqs = new SQSClient({ region, endpoint: process.env.SQS_ENDPOINT || undefined });

await processEmail(readFileSync(file), {
  extractor: new MastraExtractor(process.env.LLM_MODEL || undefined),
  emit: (msgs) => {
    console.log(JSON.stringify(msgs, null, 2));
    return sendBatch(sqs, queueUrl, msgs);
  },
});
console.log("done");
```

- [ ] **Step 6: Typecheck + commit**

Run: `cd lambda/email-parser && pnpm typecheck`
Expected: exits 0.

```bash
git add lambda/email-parser/src/handler.ts lambda/email-parser/src/handler.test.ts \
  lambda/email-parser/scripts/invoke-local.ts
git commit -m "feat(lambda): S3 handler pipeline + local-invoke harness"
```

## Task 12: Local end-to-end — handler → ElasticMQ → read back

**Files:**
- Create: `lambda/email-parser/src/handler.e2e.test.ts`

This proves the first leg (email → correct `events.Message` on the queue). The second leg (`events.Message` on the queue → Postgres rows) is already covered by the Go `TestConsumer_E2E_ElasticMQToPostgres`; the shared contract fixtures (Tasks 3 & 5) guarantee the two legs agree on shape.

- [ ] **Step 1: Write the e2e test**

`lambda/email-parser/src/handler.e2e.test.ts`:

```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import {
  CreateQueueCommand,
  DeleteQueueCommand,
  ReceiveMessageCommand,
  SQSClient,
} from "@aws-sdk/client-sqs";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { StubExtractor } from "./extractor.js";
import { processEmail } from "./handler.js";
import { sendBatch } from "./sqs.js";
import { EventMessageSchema, type EventDraft } from "./schema.js";

const dir = fileURLToPath(new URL("./__fixtures__/", import.meta.url));
const ENDPOINT = process.env.SQS_ENDPOINT ?? "http://localhost:9324";
const sqs = new SQSClient({
  region: "us-east-1",
  endpoint: ENDPOINT,
  credentials: { accessKeyId: "local", secretAccessKey: "local" },
});

const draft: EventDraft = {
  title: "Phoebe Bridgers",
  startsAt: "2026-01-02T20:00:00-05:00",
  venue: { name: "The Bowl", address: "100 Main St" },
  performers: ["Phoebe Bridgers"],
  genres: ["indie"],
};

describe("handler e2e (ElasticMQ)", () => {
  let queueUrl: string;
  beforeAll(async () => {
    const r = await sqs.send(new CreateQueueCommand({ QueueName: `e2e-${Date.now()}` }));
    queueUrl = r.QueueUrl!;
  });
  afterAll(async () => {
    if (queueUrl) await sqs.send(new DeleteQueueCommand({ QueueUrl: queueUrl }));
  });

  it("text email -> a valid EventMessage lands on the queue", async () => {
    await processEmail(readFileSync(join(dir, "text-newsletter.eml")), {
      extractor: new StubExtractor([draft]),
      emit: (msgs) => sendBatch(sqs, queueUrl, msgs),
    });

    const out = await sqs.send(
      new ReceiveMessageCommand({ QueueUrl: queueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 2 }),
    );
    expect(out.Messages).toHaveLength(1);
    const body = JSON.parse(out.Messages![0].Body!);
    expect(EventMessageSchema.safeParse(body).success).toBe(true);
    expect(body.source_id).toBe("email_newsletter");
    expect(body.title).toBe("Phoebe Bridgers");
  });
});
```

- [ ] **Step 2: Run it (ElasticMQ up)**

Run:
```bash
make queue-up
cd lambda/email-parser && pnpm test -- src/handler.e2e.test.ts
```
Expected: PASS.

- [ ] **Step 3: Run the whole Lambda suite + commit**

Run: `cd lambda/email-parser && pnpm test`
Expected: all tests PASS.

```bash
git add lambda/email-parser/src/handler.e2e.test.ts
git commit -m "test(lambda): local e2e handler -> ElasticMQ"
```

---

# Phase 3 — Deploy (Terraform + CI) — operator-applied

**Constraint:** Per project policy this machine does not run `terraform plan`/`apply` (state backend touches company AWS). Each task is **create-file + `terraform fmt` (local, safe) + visual review**. The actual apply is run by the operator via the infra pipeline; verification is the operator post-apply checklist in Task 18. `terraform validate`/`init` are NOT run here (they reach the backend).

## Task 13: Lambda container image + ECR repo

**Files:**
- Create: `lambda/email-parser/Dockerfile`
- Create: `terraform/prod/lambda_ecr.tf`

- [ ] **Step 1: Write the Dockerfile**

`lambda/email-parser/Dockerfile` (AWS base image for Node 20; build TS, ship JS):

```dockerfile
FROM public.ecr.aws/lambda/nodejs:20 AS build
WORKDIR /build
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY tsconfig.json ./
COPY src ./src
RUN pnpm build && pnpm install --prod --frozen-lockfile

FROM public.ecr.aws/lambda/nodejs:20
WORKDIR ${LAMBDA_TASK_ROOT}
COPY --from=build /build/dist ./
COPY --from=build /build/node_modules ./node_modules
# handler.js exports `handler`
CMD ["handler.handler"]
```

- [ ] **Step 2: Write the ECR repo terraform**

`terraform/prod/lambda_ecr.tf`:

```hcl
resource "aws_ecr_repository" "email_parser" {
  name                 = "${var.app_name_prefix}-email-parser"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = { App = var.app_name_prefix }
}

# Keep only the most recent images.
resource "aws_ecr_lifecycle_policy" "email_parser" {
  repository = aws_ecr_repository.email_parser.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Expire untagged images after 14 days"
      selection    = { tagStatus = "untagged", countType = "sinceImagePushed", countUnit = "days", countNumber = 14 }
      action       = { type = "expire" }
    }]
  })
}
```

- [ ] **Step 3: Format + commit**

Run: `cd terraform/prod && terraform fmt lambda_ecr.tf`
Expected: file already canonical (no diff) or reformatted.

```bash
git add lambda/email-parser/Dockerfile terraform/prod/lambda_ecr.tf
git commit -m "feat(infra): Lambda container image + ECR repo for email-parser"
```

## Task 14: SES receiving — identity, DKIM, MX, rule set + rule

**Files:**
- Create: `terraform/prod/ses.tf`

- [ ] **Step 1: Write ses.tf**

`terraform/prod/ses.tf`:

```hcl
locals {
  inbound_domain    = "inbound.${var.domain_name}"
  ingest_recipient  = "shows@inbound.${var.domain_name}"
  ses_inbound_host  = "inbound-smtp.${var.aws_region}.amazonaws.com"
}

# Verify the receiving subdomain as an SES domain identity.
resource "aws_ses_domain_identity" "inbound" {
  domain = local.inbound_domain
}

resource "aws_ses_domain_dkim" "inbound" {
  domain = aws_ses_domain_identity.inbound.domain
}

# DKIM CNAMEs in the existing hosted zone (data.aws_route53_zone.main from data.tf).
resource "aws_route53_record" "inbound_dkim" {
  count   = 3
  zone_id = data.aws_route53_zone.main.zone_id
  name    = "${aws_ses_domain_dkim.inbound.dkim_tokens[count.index]}._domainkey.${local.inbound_domain}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.inbound.dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# MX so mail to *@inbound.<domain> is delivered to SES inbound.
resource "aws_route53_record" "inbound_mx" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = local.inbound_domain
  type    = "MX"
  ttl     = 600
  records = ["10 ${local.ses_inbound_host}"]
}

# One active receipt rule set per account. NOTE: if the account already has an
# active rule set, do NOT apply this resource — instead add the rule below to the
# existing set. Confirm with: aws ses describe-active-receipt-rule-set
resource "aws_ses_receipt_rule_set" "main" {
  rule_set_name = "${var.app_name_prefix}-inbound"
}

resource "aws_ses_active_receipt_rule_set" "main" {
  rule_set_name = aws_ses_receipt_rule_set.main.rule_set_name
}

resource "aws_ses_receipt_rule" "store_to_s3" {
  name          = "${var.app_name_prefix}-store-newsletter"
  rule_set_name = aws_ses_receipt_rule_set.main.rule_set_name
  recipients    = [local.ingest_recipient]
  enabled       = true
  scan_enabled  = true # populates X-SES-Spam-Verdict / X-SES-Virus-Verdict

  s3_action {
    bucket_name       = aws_s3_bucket.inbound_email.bucket
    object_key_prefix = "raw/"
    position          = 1
  }

  depends_on = [aws_s3_bucket_policy.inbound_email]
}
```

Note: `data.aws_route53_zone.main` and `var.aws_region` already exist (`terraform/prod/data.tf`, `variables.tf`). Confirm the data source name matches `data.tf` (line 13 references `var.domain_name`); if the existing zone data source has a different local name, use that.

- [ ] **Step 2: Format + commit**

Run: `cd terraform/prod && terraform fmt ses.tf`

```bash
git add terraform/prod/ses.tf
git commit -m "feat(infra): SES inbound identity, DKIM, MX, receipt rule -> S3"
```

## Task 15: Raw-email S3 bucket + policy + lifecycle + Lambda notification

**Files:**
- Create: `terraform/prod/s3_inbound.tf`

- [ ] **Step 1: Write s3_inbound.tf**

`terraform/prod/s3_inbound.tf`:

```hcl
resource "aws_s3_bucket" "inbound_email" {
  bucket = "${var.app_name_prefix}-inbound-email"
  tags   = { App = var.app_name_prefix }
}

resource "aws_s3_bucket_public_access_block" "inbound_email" {
  bucket                  = aws_s3_bucket.inbound_email.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Raw emails are an audit trail, not permanent storage.
resource "aws_s3_bucket_lifecycle_configuration" "inbound_email" {
  bucket = aws_s3_bucket.inbound_email.id
  rule {
    id     = "expire-raw"
    status = "Enabled"
    filter { prefix = "raw/" }
    expiration { days = 90 }
  }
}

# Allow SES to write inbound mail into the bucket (scoped to this account).
data "aws_iam_policy_document" "inbound_email" {
  statement {
    sid     = "AllowSESPuts"
    effect  = "Allow"
    actions = ["s3:PutObject"]
    principals {
      type        = "Service"
      identifiers = ["ses.amazonaws.com"]
    }
    resources = ["${aws_s3_bucket.inbound_email.arn}/*"]
    condition {
      test     = "StringEquals"
      variable = "aws:Referer"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

resource "aws_s3_bucket_policy" "inbound_email" {
  bucket = aws_s3_bucket.inbound_email.id
  policy = data.aws_iam_policy_document.inbound_email.json
}

# S3 -> Lambda on new object under raw/.
resource "aws_s3_bucket_notification" "inbound_email" {
  bucket = aws_s3_bucket.inbound_email.id
  lambda_function {
    lambda_function_arn = aws_lambda_function.email_parser.arn
    events              = ["s3:ObjectCreated:*"]
    filter_prefix       = "raw/"
  }
  depends_on = [aws_lambda_permission.allow_s3_invoke]
}
```

Note: `data.aws_caller_identity.current` already exists (used in `terraform/prod/outputs.tf`/`ecs_schedules.tf`).

- [ ] **Step 2: Format + commit**

Run: `cd terraform/prod && terraform fmt s3_inbound.tf`

```bash
git add terraform/prod/s3_inbound.tf
git commit -m "feat(infra): inbound-email S3 bucket, SES write policy, Lambda notify"
```

## Task 16: The Lambda function + IAM role + async DLQ

**Files:**
- Create: `terraform/prod/lambda_email.tf`

Ordering note: this file references `aws_secretsmanager_secret.email_llm_key` (defined in Task 17) and `aws_sqs_queue.events` (existing). All Phase 3 Terraform is applied **together by the operator** after every Phase 3 file exists — not task-by-task — so the forward reference to Task 17's secret resolves at apply time. The per-task `terraform fmt` is local-only and does not resolve references.

- [ ] **Step 1: Write lambda_email.tf**

`terraform/prod/lambda_email.tf`:

```hcl
# Image tag the app/lambda pipeline pushes (see ci/buildspec-lambda.yml).
variable "email_parser_image_tag" {
  type    = string
  default = "bootstrap"
}

# DLQ for failed async invocations (poison emails).
resource "aws_sqs_queue" "email_parser_dlq" {
  name                      = "${var.app_name_prefix}-email-parser-dlq"
  message_retention_seconds = 1209600 # 14 days
}

data "aws_iam_policy_document" "email_parser_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "email_parser" {
  name               = "${var.app_name_prefix}-email-parser"
  assume_role_policy = data.aws_iam_policy_document.email_parser_assume.json
}

data "aws_iam_policy_document" "email_parser" {
  statement {
    sid       = "ReadRawEmail"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.inbound_email.arn}/*"]
  }
  statement {
    sid       = "SendToEventsQueue"
    actions   = ["sqs:SendMessage", "sqs:SendMessageBatch", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.events.arn]
  }
  statement {
    sid       = "WriteDLQ"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.email_parser_dlq.arn]
  }
  statement {
    sid       = "ReadModelKey"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.email_llm_key.arn]
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["arn:aws:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:*"]
  }
}

resource "aws_iam_role_policy" "email_parser" {
  name   = "${var.app_name_prefix}-email-parser"
  role   = aws_iam_role.email_parser.id
  policy = data.aws_iam_policy_document.email_parser.json
}

resource "aws_lambda_function" "email_parser" {
  function_name = "${var.app_name_prefix}-email-parser"
  role          = aws_iam_role.email_parser.arn
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.email_parser.repository_url}:${var.email_parser_image_tag}"
  timeout       = 120
  memory_size   = 1024

  environment {
    variables = {
      AWS_REGION_OVERRIDE = var.aws_region # AWS_REGION is reserved; read this in code if needed
      EVENTS_QUEUE_URL    = aws_sqs_queue.events.url
      LLM_API_KEY_SECRET  = aws_secretsmanager_secret.email_llm_key.arn
      LLM_MODEL           = "claude-3-5-sonnet-20241022"
    }
  }
}

# Async retries + DLQ for poison emails.
resource "aws_lambda_function_event_invocation_config" "email_parser" {
  function_name                = aws_lambda_function.email_parser.function_name
  maximum_retry_attempts       = 2
  maximum_event_age_in_seconds = 3600
  destination_config {
    on_failure {
      destination = aws_sqs_queue.email_parser_dlq.arn
    }
  }
}

resource "aws_lambda_permission" "allow_s3_invoke" {
  statement_id  = "AllowExecutionFromS3"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.email_parser.function_name
  principal     = "s3.amazonaws.com"
  source_arn    = aws_s3_bucket.inbound_email.arn
}

resource "aws_cloudwatch_metric_alarm" "email_parser_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-email-parser-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.email_parser_dlq.name }
  alarm_description   = "Emails failed parsing and landed in the email-parser DLQ. Check Lambda logs."
}
```

Important: `AWS_REGION` is a **reserved** Lambda env var and cannot be set in the `environment` block — the runtime sets it automatically, and the handler reads `process.env.AWS_REGION` directly (already does). So do NOT add `AWS_REGION` here; the `AWS_REGION_OVERRIDE` line above is only if you ever need an explicit override. If unused, delete that line. The SDK clients in `handler.ts` already pick up the runtime-provided `AWS_REGION`.

Also: the code reads the model key from `ANTHROPIC_API_KEY`. Wire the secret to that env var via a small startup read, OR set `ANTHROPIC_API_KEY` from the secret. Simplest: in Task 17 store the key and have the handler read `LLM_API_KEY_SECRET` at cold start and set `process.env.ANTHROPIC_API_KEY`. See Task 17 Step 3.

- [ ] **Step 2: Format + commit**

Run: `cd terraform/prod && terraform fmt lambda_email.tf`

```bash
git add terraform/prod/lambda_email.tf
git commit -m "feat(infra): email-parser Lambda, IAM, async DLQ + alarm"
```

## Task 17: Model API-key secret + cold-start wiring

**Files:**
- Modify: `terraform/prod/secrets.tf:2-8` (add to `secret_names`) — or create `terraform/prod/secrets_email.tf`
- Modify: `lambda/email-parser/src/extractor.ts` (cold-start key load)
- Modify: `lambda/email-parser/src/handler.ts` (call the loader)

- [ ] **Step 1: Add the secret (Terraform)**

Create `terraform/prod/secrets_email.tf` (keeps it isolated from the ECS `secret_names` loop, which is wired to the task role, not the Lambda):

```hcl
resource "aws_secretsmanager_secret" "email_llm_key" {
  name                    = "${var.app_name_prefix}/email-llm-api-key"
  description             = "Anthropic API key for the email-parser Lambda. Seeded out-of-band."
  recovery_window_in_days = 7
  tags                    = { App = var.app_name_prefix }
}

resource "aws_secretsmanager_secret_version" "email_llm_key_placeholder" {
  secret_id     = aws_secretsmanager_secret.email_llm_key.id
  secret_string = "REPLACE_ME_AFTER_APPLY"
  lifecycle {
    ignore_changes = [secret_string]
  }
}
```

Run: `cd terraform/prod && terraform fmt secrets_email.tf`

- [ ] **Step 2: Write the failing test for the key loader**

Append to `lambda/email-parser/src/extractor.test.ts`:

```ts
import { vi } from "vitest";
import { loadModelKey } from "./extractor.js";

describe("loadModelKey", () => {
  it("reads the secret and sets ANTHROPIC_API_KEY", async () => {
    delete process.env.ANTHROPIC_API_KEY;
    const fakeSecrets = { getSecretValue: vi.fn().mockResolvedValue("sk-test-123") };
    await loadModelKey(fakeSecrets, "arn:secret");
    expect(process.env.ANTHROPIC_API_KEY).toBe("sk-test-123");
    expect(fakeSecrets.getSecretValue).toHaveBeenCalledWith("arn:secret");
  });

  it("no-ops when ANTHROPIC_API_KEY is already set", async () => {
    process.env.ANTHROPIC_API_KEY = "preset";
    const fakeSecrets = { getSecretValue: vi.fn() };
    await loadModelKey(fakeSecrets, "arn:secret");
    expect(fakeSecrets.getSecretValue).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 3: Run to verify it fails, then implement**

Run: `cd lambda/email-parser && pnpm test -- src/extractor.test.ts`
Expected: FAIL — `loadModelKey` not exported.

Append to `lambda/email-parser/src/extractor.ts`:

```ts
/** Minimal seam over Secrets Manager so the loader is unit-testable. */
export interface SecretReader {
  getSecretValue(arn: string): Promise<string>;
}

/** Cold-start: populate ANTHROPIC_API_KEY from Secrets Manager if not already set.
 * Idempotent and cheap to call on every invocation. */
export async function loadModelKey(reader: SecretReader, secretArn: string): Promise<void> {
  if (process.env.ANTHROPIC_API_KEY) return;
  process.env.ANTHROPIC_API_KEY = await reader.getSecretValue(secretArn);
}
```

Add a real `SecretReader` near the bottom of `extractor.ts`:

```ts
import { GetSecretValueCommand, SecretsManagerClient } from "@aws-sdk/client-secrets-manager";

export class AwsSecretReader implements SecretReader {
  private readonly client: SecretsManagerClient;
  constructor(region?: string) {
    this.client = new SecretsManagerClient({ region });
  }
  async getSecretValue(arn: string): Promise<string> {
    const out = await this.client.send(new GetSecretValueCommand({ SecretId: arn }));
    if (!out.SecretString) throw new Error(`secret ${arn} has no string value`);
    return out.SecretString;
  }
}
```

Add `@aws-sdk/client-secrets-manager` to `package.json` dependencies (`pnpm add @aws-sdk/client-secrets-manager`).

- [ ] **Step 4: Wire it into the handler entrypoint**

In `lambda/email-parser/src/handler.ts`, modify the `handler` function to load the key first:

```ts
import { AwsSecretReader, loadModelKey } from "./extractor.js";

export async function handler(event: S3Event): Promise<void> {
  const secretArn = process.env.LLM_API_KEY_SECRET;
  if (secretArn) await loadModelKey(new AwsSecretReader(process.env.AWS_REGION), secretArn);
  const deps = prodDeps();
  // ...unchanged loop...
}
```

- [ ] **Step 5: Run tests + typecheck**

Run: `cd lambda/email-parser && pnpm test && pnpm typecheck`
Expected: all PASS, typecheck clean.

- [ ] **Step 6: Commit**

```bash
git add terraform/prod/secrets_email.tf lambda/email-parser/src/extractor.ts \
  lambda/email-parser/src/extractor.test.ts lambda/email-parser/src/handler.ts \
  lambda/email-parser/package.json lambda/email-parser/pnpm-lock.yaml
git commit -m "feat(lambda): load Anthropic key from Secrets Manager at cold start"
```

## Task 18: CI lane for the Lambda image + outputs + operator checklist

**Files:**
- Create: `ci/buildspec-lambda.yml`
- Modify: `terraform/prod/outputs.tf` (append outputs)

- [ ] **Step 1: Write the buildspec**

`ci/buildspec-lambda.yml`:

```yaml
version: 0.2

# Builds the email-parser Lambda container image and pushes it to its ECR repo.
# Wire this to a CodeBuild project (its own pipeline stage or a standalone
# project) with env: AWS_ACCOUNT_ID, AWS_REGION, LAMBDA_ECR_REPO, FUNCTION_NAME,
# DOCKERHUB_USER, DOCKERHUB_TOKEN.

phases:
  install:
    runtime-versions:
      nodejs: 20
    commands:
      - corepack enable

  build:
    commands:
      - SHORT_SHA=$(echo "${CODEBUILD_RESOLVED_SOURCE_VERSION}" | cut -c1-7)
      - IMAGE_URI="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/${LAMBDA_ECR_REPO}:${SHORT_SHA}"
      - echo "${DOCKERHUB_TOKEN}" | docker login --username "${DOCKERHUB_USER}" --password-stdin
      - aws ecr get-login-password --region "${AWS_REGION}" | docker login --username AWS --password-stdin "${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

      # Run the Lambda unit + contract tests before building the image.
      # ElasticMQ-dependent specs (sqs / e2e) are excluded here — they need a
      # broker; they run in local dev. Adjust if CI provides ElasticMQ.
      - cd lambda/email-parser
      - pnpm install --frozen-lockfile
      - pnpm typecheck
      - pnpm vitest run src/schema.test.ts src/hash.test.ts src/map.test.ts src/email.test.ts src/extractor.test.ts src/handler.test.ts
      - cd ../..

      - docker build -t "${IMAGE_URI}" lambda/email-parser
      - docker push "${IMAGE_URI}"

      # Point the live function at the new image.
      - aws lambda update-function-code --function-name "${FUNCTION_NAME}" --image-uri "${IMAGE_URI}" --region "${AWS_REGION}"
      - echo "${IMAGE_URI}" > lambda_image_uri.txt

artifacts:
  files:
    - lambda_image_uri.txt
```

Note: this assumes test deps install in CI. If CI cannot reach ElasticMQ, the excluded specs (`sqs.test.ts`, `handler.e2e.test.ts`) are the only ElasticMQ-dependent ones — they stay out of this lane intentionally; the contract/unit specs give full logic coverage.

- [ ] **Step 2: Append outputs**

Append to `terraform/prod/outputs.tf`:

```hcl
output "email_ingest_recipient" {
  description = "Subscribe promoter newsletters to this address."
  value       = local.ingest_recipient
}

output "email_parser_ecr_repo" {
  description = "ECR repo URL for the email-parser Lambda image."
  value       = aws_ecr_repository.email_parser.repository_url
}

output "email_inbound_bucket" {
  value = aws_s3_bucket.inbound_email.bucket
}

output "email_post_apply_steps" {
  value = <<-EOT
    Email-newsletter ingestion — operator steps after apply:

    1. Seed the model key:
       aws secretsmanager put-secret-value \
         --secret-id ${var.app_name_prefix}/email-llm-api-key \
         --secret-string "<your-anthropic-api-key>"

    2. Confirm DNS: the inbound.${var.domain_name} MX + 3 DKIM CNAMEs resolve.
       Wait for SES domain status = "verified" in the SES console.

    3. Build + push the Lambda image (CodeBuild project running ci/buildspec-lambda.yml),
       then it auto-runs `aws lambda update-function-code`. For the FIRST apply,
       a bootstrap image must already be pushed to ${aws_ecr_repository.email_parser.repository_url}
       (use the nginx-style placeholder push pattern from the api bootstrap if needed),
       and set -var email_parser_image_tag=<sha> on the real deploy.

    4. Verify end to end: send a test newsletter to ${local.ingest_recipient};
       check the email-parser CloudWatch logs, then:
         docker is not available in prod — query RDS via a one-off ECS task, or check
         the events table through the API. Confirm a new row with source = email_newsletter.

    5. Watch the ${var.app_name_prefix}-email-parser-dlq alarm — any depth >=1 means
       emails are failing to parse.
  EOT
}
```

- [ ] **Step 3: Format + commit**

Run: `cd terraform/prod && terraform fmt outputs.tf`

```bash
git add ci/buildspec-lambda.yml terraform/prod/outputs.tf
git commit -m "feat(infra): CI lane for email-parser image + ingestion outputs"
```

- [ ] **Step 4: Update the README quickstart**

Add an "Email-newsletter ingest quickstart" section to `README.md` documenting: `make queue-up`, set `ANTHROPIC_API_KEY` + `EVENTS_QUEUE_URL` + `SQS_ENDPOINT`, create a local queue, `cd lambda/email-parser && pnpm install`, `pnpm test`, and `pnpm invoke-local src/__fixtures__/text-newsletter.eml` to watch messages flow to ElasticMQ and then through `make run`'s consumer into Postgres. Mirror the tone of the existing "Event ingest quickstart" section.

```bash
git add README.md
git commit -m "docs: email-newsletter ingest quickstart"
```

---

## Final verification (run from repo root)

- [ ] Go suite: `docker compose up -d --wait postgres elasticmq && go test -p 1 ./... -count=1` → PASS (includes the new archive + contract tests).
- [ ] Lambda suite: `cd lambda/email-parser && pnpm install && pnpm test && pnpm typecheck` → PASS (ElasticMQ up for sqs/e2e specs).
- [ ] Terraform format: `cd terraform/prod && terraform fmt -check` → no diff.
- [ ] Manual smoke (optional, needs API key): `pnpm invoke-local src/__fixtures__/text-newsletter.eml` with `EVENTS_QUEUE_URL`/`SQS_ENDPOINT` set, then `make run` and confirm a row appears: `docker exec hwh_postgres psql -U app -d appdb -c "SELECT title FROM events e JOIN event_sources s ON s.id=e.source_id WHERE s.name='email_newsletter';"`
