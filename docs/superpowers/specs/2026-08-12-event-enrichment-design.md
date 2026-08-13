# Event enrichment via the mastra-handler Lambda

**Date:** 2026-08-12
**Status:** Approved (design)

## Problem

Events reach the database with only what the scraper saw: a title, a start
time, a venue, sometimes an image URL. There is nothing about the *band* — no
photo when the source omitted one, no history, no sense of what the show will
actually be like.

The mastra-handler Lambda already knows how to resolve a performer to a
MusicBrainz artist and find a usable photo of them (`resolveBandCandidatesStep`,
`judgeBandImageStep`), but that machinery only runs for on-demand poster
generation. Nothing enriches the ingest path.

## Goal

Insert an enrichment stage between the scrapers and the database. The
mastra-handler Lambda picks up normalized events off `hwh-events-queue`, runs
three independent enrichment workflows, and republishes the event — enriched or
not — onto a new `hwh-events-enriched-queue` that the ECS ingest consumer reads
instead.

Three enrichments:

1. **Band image** — a photo when the source gave no `image_url`.
2. **Recent tour setlist** — what the band has been playing, from setlist.fm.
3. **Band bio** — origins and discography, from Wikipedia and MusicBrainz,
   summarized by an LLM.

## Non-goals

- **No frontend work.** Every new API field is additive and `omitempty`, so the
  SPA's payloads are unchanged until someone chooses to render them.
- **No change to how scrapers publish.** `Runner.Run` keeps publishing every
  event every cycle; that is what keeps `last_seen_at` fresh for
  `ArchiveStaleEvents`.
- **No merge of `artist_genre_cache` into the new `artists` table.** They
  duplicate `name_key`/`mbid`/`status`/`resolved_at`, but that cache is owned by
  the Spotify scraper path and folding it in is a separate change that touches
  code this feature otherwise never opens.

## Topology

```
BEFORE
  scrapers ──> hwh-events-queue ──> ECS ingest consumer ──> Postgres
  email S3 ──> mastra-handler ──┘

AFTER
  scrapers ──> hwh-events-queue ──> mastra-handler ──> hwh-events-enriched-queue
  email S3 ──> mastra-handler ──┘   (SQS trigger)              │
                                                               v
                                                    ECS ingest consumer ──> Postgres
```

The Lambda both **produces to** and **consumes from** `hwh-events-queue`: the
email path still publishes extracted events there, and those messages then flow
back through the same function's enrichment branch. That is intentional — email
events get enriched exactly like scraped ones.

## Why enrichment is artist-scoped, not event-scoped

All three enrichments are properties of the *performer*, not the show. One band
playing five dates is one bio, one photo, one setlist.

This matters more than it first appears. `ecs_schedules.tf:10` runs the
Ticketmaster scrape **daily**, `adapter.go:48` requests `size=200`, and
`runner.go:33-41` publishes every event unconditionally. Event-scoped
enrichment would therefore re-run three LLM workflows for ~200 events every
single day, regenerating byte-identical content forever — roughly $8/day of
model spend, ~200 MusicBrainz searches against a 1 req/sec limit, and ~200
setlist.fm calls against a 1,440/day cap.

Artist-scoping plus the skip cache (below) reduces steady state to genuinely new
artists.

---

# Data model

## `artists` — identity

Keyed on `events.NormalizeString(performer)`, joining the key space that
`event_performers.normalized_name` and `artist_genre_cache.name_key` already
use. No link table is needed.

```sql
CREATE TABLE artists (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name_key       TEXT NOT NULL UNIQUE,   -- events.NormalizeString(performer)
    display_name   TEXT NOT NULL,          -- MusicBrainz canonical name, else raw performer
    mbid           TEXT,                   -- NULL when status = 'not_found'
    disambiguation TEXT,                   -- "US rock band"
    artist_type    TEXT,                   -- Group | Person
    country        TEXT,
    begin_year     TEXT,                   -- TEXT to match ArtistMatch.beginYear
    status         TEXT NOT NULL CHECK (status IN ('ok','not_found')),
    resolved_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Every column below `display_name` is exactly the `ArtistMatchSchema` the Lambda
already produces and currently discards.

`artists.status` uses `ok`/`not_found` rather than the `ok`/`none`/`error`
vocabulary of the enrichment tables below. That is deliberate: this column
describes MusicBrainz *resolution*, and it deliberately mirrors
`artist_genre_cache.status`, which answers the same question about the same key
space.

## Enrichment tables

Each is 1:1 with `artists`, with its own status and clock so one workflow
failing never blocks another.

```sql
CREATE TABLE artist_images (
    artist_id   UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    url         TEXT,    -- upload.wikimedia.org thumbnail
    width       INT,
    height      INT,
    file        TEXT,    -- Commons file name, the stable identifier
    source      TEXT,    -- 'p18' | 'category', matching ImageCandidate.source
    credit      JSONB,   -- ImageCredit: artist, license, licenseUrl, descriptionUrl
    reason      TEXT,    -- why nothing was accepted, from the vision judge
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE artist_bios (
    artist_id     UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status        TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    bio_md        TEXT,    -- ~150-250 words: origins, releases. No tour claims.
    sources       JSONB NOT NULL DEFAULT '[]'::jsonb,
                           -- [{kind:'wikipedia', title, url, revision_id},
                           --  {kind:'musicbrainz', mbid, url}]
    model         TEXT,    -- which model wrote it, for regeneration decisions
    reason        TEXT,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE artist_tour_snapshots (
    artist_id      UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    status         TEXT NOT NULL CHECK (status IN ('ok','none','error')),
    tour_name      TEXT,
    songs          JSONB NOT NULL DEFAULT '[]'::jsonb,
                            -- [{name, encore:int, cover_of, tape:bool, info}]
    observed_date  DATE,    -- the past show this setlist came from
    observed_venue TEXT,
    observed_city  TEXT,
    setlist_url    TEXT,    -- link back to setlist.fm; their attribution ask
    blurb          TEXT,    -- 1-2 LLM sentences grounded ONLY in the rows above
    blurb_model    TEXT,
    reason         TEXT,
    fetched_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

The setlist and the tour blurb share one table because one workflow produces
both and they have identical lifetimes. A NULL `blurb` with `status = 'ok'`
means the setlist landed but the blurb call did not — no second status column.

### The three-state status

This is what makes "succeed, fail, or exhaust max attempts" persistable:

| status | meaning | retry |
| --- | --- | --- |
| `ok` | we have data | rarely (90d) |
| `none` | we looked; the artist genuinely has no article / photo / setlist | occasionally (14d) |
| `error` | the attempt broke or ran out of attempts | soon (6h) |

TTLs mirror `internal/scraper/spotify/genres.go:17-19` (90d ok / 14d not_found)
so the two artist caches behave alike.

## `events` — one new column

```sql
ALTER TABLE events ADD COLUMN headline_artist_id UUID REFERENCES artists(id) ON DELETE SET NULL;
CREATE INDEX events_headline_artist_id_idx ON events (headline_artist_id);
```

The Lambda picks which performer to enrich and names it in the message; this
records that choice so the API knows which artist row belongs to the event.
`event_performers` has no ordering column and cannot answer this itself.

The index exists for the same reason `poster_jobs_user_id_idx` does: Postgres
does not index a foreign key automatically, and the `ON DELETE SET NULL` would
otherwise seq-scan `events`.

**The upsert must not blank it:**

```sql
headline_artist_id = COALESCE(EXCLUDED.headline_artist_id, events.headline_artist_id)
```

Without `COALESCE`, a re-scrape whose enrichment happened to fail would wipe a
good link.

---

# Wire contract

`hwh-events-enriched-queue` carries a strict superset of `events.Message`, so a
plain message still decodes during cutover and stale DLQ messages stay
replayable.

```go
// EnrichedMessage is what the Lambda writes and the ingest consumer reads.
type EnrichedMessage struct {
    Message                           // embedded: every existing field, unchanged
    Enrichment *Enrichment `json:"enrichment,omitempty"`
}

type Enrichment struct {
    Artist      *ArtistInfo `json:"artist,omitempty"`
    Image       *ImageInfo  `json:"image,omitempty"`
    Bio         *BioInfo    `json:"bio,omitempty"`
    Tour        *TourInfo   `json:"tour,omitempty"`
    AttemptedAt time.Time   `json:"attempted_at"`
}
```

Each sub-object mirrors its table one-for-one and carries its own `status` and
`reason`.

`Artist` is present whenever the prelude **ran**, including when it found
nothing: `status: 'not_found'` with a null `mbid` is how an unresolvable
performer gets an `artists` row, so the cache and the database both record that
we tried. `Artist` is nil only when the prelude itself failed or never ran, and
in that case the other three sections are nil too.

`ArtistInfo` carries the **raw performer string**, not a normalized key. The
consumer computes `name_key = events.NormalizeString(performer)` itself. This
keeps the one normalization that reaches the database in a single language — see
the artist-key note under the skip cache for why that distinction matters.

The TypeScript mirror extends `schema.ts`, which already documents the rule:
*"Wire shape — MUST match Go internal/events.Message JSON tags exactly."*
Note that `contract_test.go` decodes with `DisallowUnknownFields`, so the Go
types and the Zod schemas must land in the same change.

---

# API surface

`calendarEvent` gains one optional object, populated identically in
`GetMyCalendar`, `GetCityCalendar`, and `GetEventByIDForUser`:

```json
"artist": {
  "name": "La Luz",
  "disambiguation": "US rock band",
  "mbid": "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a",
  "image": {
    "url": "https://upload.wikimedia.org/...",
    "width": 640, "height": 427,
    "credit": { "artist": "…", "license": "CC BY-SA 4.0", "license_url": "…",
                "description_url": "…", "attribution_required": true }
  },
  "bio": {
    "text": "…",
    "sources": [{ "kind": "wikipedia", "title": "…", "url": "…" }]
  },
  "tour": {
    "name": "…", "blurb": "…", "setlist_url": "…",
    "songs": [{ "name": "Sure As Spring", "encore": 0 }],
    "observed": { "date": "2026-07-14", "venue": "…", "city": "…" }
  }
}
```

Everything is `omitempty`. Rather than widen the three calendar queries with
four left joins each, the page queries gain a single `e.headline_artist_id`
column and the handler makes one batched follow-up call — the pattern
`ListEventPerformersBatch` and `ListEventGenresBatch` already establish:

```sql
-- name: GetArtistEnrichmentBatch :many
SELECT … FROM artists a
  LEFT JOIN artist_images         i ON i.artist_id = a.id
  LEFT JOIN artist_bios           b ON b.artist_id = a.id
  LEFT JOIN artist_tour_snapshots t ON t.artist_id = a.id
WHERE a.id = ANY($1::uuid[]);
```

Left joins throughout, so a resolved artist with no successful enrichment still
returns a row.

**One free win:** `image_url` falls back to the artist image when the event has
none — applied in the handler after the batch call, not in SQL. The existing
frontend renders band photos with no change at all, and the source-provided
image always wins, so nothing regresses.

The image credit must be surfaced even though the FE does not render it yet —
Commons images are predominantly CC-BY/CC-BY-SA and attribution is a licence
condition, so the data has to be there before anything displays the photo.

---

# Lambda: trigger and branching

```hcl
resource "aws_lambda_event_source_mapping" "enrichment" {
  event_source_arn = aws_sqs_queue.events.arn
  function_name    = aws_lambda_function.mastra_handler.arn
  batch_size       = 1
  scaling_config { maximum_concurrency = 2 }
}
```

**`batch_size = 1`** on the merits — one event is three workflows with LLM
calls, and batching ten into a 300s timeout blows the budget — and
structurally, because it sidesteps partial-batch-failure reporting entirely.
SQS learns which items failed by reading a JSON response body, and this handler
is `awslambda.streamifyResponse`-wrapped; rather than bet on that surviving the
streaming wrapper, batch size 1 means success deletes the message and a throw
returns it, with the existing `maxReceiveCount = 3` redrive doing the rest.

**`maximum_concurrency = 2`** bounds only the SQS path.
`reserved_concurrent_executions` would be wrong: this one function also serves
the poster Function URL and the email S3 path, and reserving would starve
interactive poster requests behind enrichment. 2 is the minimum AWS allows.

**Required change to an existing resource:** `hwh-events-queue` currently has
`visibility_timeout_seconds = 30` (`sqs.tf:8`). Lambda refuses an event source
mapping unless the queue's visibility timeout is at least the function timeout
(300s). Raise it to 900 (AWS's 6× guidance).

## Branch order

```
isFunctionUrlEvent(event)  -> poster HTTP      (existing)
isSQSEvent(event)          -> enrichment       (new)
otherwise                  -> handleS3         (existing fallback)
```

`isSQSEvent` must discriminate on `Records[0].eventSource === "aws:sqs"`, **not**
on the presence of `Records` — the S3 branch uses `Records` too, and
`handler.ts:139` is currently an unguarded cast that would happily accept an SQS
event.

---

# Lambda: orchestration

The three workflows are independent of each other but all three need the MBID —
image for Commons, bio for the Wikidata hop, tour for setlist.fm's `artistMbid`.
So: a shared prelude, then a parallel fan-out.

```
pickArtist(performers[0])  ──┬──> image workflow
   MusicBrainz search       ├──> bio workflow
   ONE ArtistMatch chosen   └──> tour workflow
                                      │
                            Promise.allSettled
                                      │
                            emit EnrichedMessage
```

**The prelude must choose one artist for all three.**
`resolveBandCandidatesStep` currently falls through up to three MusicBrainz
matches until one yields a Commons image. If each enrichment did its own
fall-through you could get a bio about "La Luz, US rock band" beside a photo of
"La Luz, Belgium based house group" — the `disambiguation` field exists
precisely because those collide.

Extract a `pickArtist()` helper for the prelude and **leave
`resolveBandCandidatesStep` untouched**, so the shipped-and-tested poster path
keeps its exact current behavior. The small duplication is worth the zero
regression risk.

**No workflow throws.** Each returns a typed `{status, reason, …}`, the same
discipline `runPosterWorkflow` documents at `handler.ts:145-152`. The enriched
message is emitted regardless of what failed — that is what makes "or fail, or
exhaust their max attempt count" work. Each workflow gets a ~120s budget of the
300s timeout via `AbortSignal.timeout` so one hung fetch cannot starve the
others.

Enrichment failure never blocks ingest: an event whose enrichment wholly failed
is still republished, with `Enrichment.Artist` nil.

---

# The skip cache

A decision-and-result cache in S3, read once per invocation and written once.

## Bucket

Its **own** bucket, mirroring `posters.tf` (bucket + public access block + SSE,
~20 lines). Not an `enrichment/` prefix in the posters bucket: `iam.tf:110`
grants the ECS task role `${posters.arn}/*`, so a prefix there would silently
give the API read/write on a cache it has no business touching.

Note the posters bucket has no lifecycle rule, and neither does this one — the
objects are ~2KB and a few thousand artists is single-digit megabytes.

## Key

```
enrichment/v1/<sha256(artistKey(performer))>.json
```

`v1/` so a schema change is a prefix bump rather than a migration. The key is
hashed because normalized band names are not S3-safe — `"ac/dc"` would create a
nested prefix, and `"Sunn O)))"` and emoji names exist. The readable
`artist_key` goes inside the body so an object is identifiable once fetched.

**`artistKey()` is a new function, not `hash.ts`'s `normalize()`.** That
existing helper's own comment says it is *"Independent of Go's NormalizeString;
only needs to be stable within this Lambda"*, and it is not equivalent — it
strips punctuation, so `"AC/DC"` → `"acdc"` where Go's `NormalizeString`
(`genres.go:64`) gives `"ac/dc"`. Reusing it would merge two artists in the
cache that stay distinct in the database. It also cannot be *changed* to match,
because it feeds `contentHash()` → `source_event_id`, and re-keying that would
break dedup for every email-ingested event.

So `artistKey()` mirrors `NormalizeString` exactly — lowercase, NFD-strip
diacritics, trim, no punctuation removal — pinned by a fixture read by **both**
test suites, the way `testdata/event-message-contract/` already pins the message
shape:

```
testdata/artist-key-contract/cases.json
  [{"in": "AC/DC", "out": "ac/dc"}, {"in": "Sigur Rós", "out": "sigur ros"}, …]
```

## Object body

```json
{
  "artist_key": "la luz",
  "performer": "La Luz",
  "artist": { "mbid": "9b5ae4cc-…", "display_name": "La Luz",
              "disambiguation": "US rock band", "type": "Group",
              "country": "US", "begin_year": "2012", "status": "ok" },
  "workflows": {
    "image": { "status": "ok",   "at": "2026-08-12T04:11:22Z",
               "payload": { "url": "…", "width": 640, "height": 427,
                            "file": "La_Luz_2019.jpg", "source": "p18",
                            "credit": { … } } },
    "bio":   { "status": "none", "at": "2026-08-12T04:11:19Z",
               "reason": "no Wikipedia article linked from Wikidata" },
    "tour":  { "status": "error","at": "2026-08-12T04:11:31Z",
               "reason": "setlist.fm 429" }
  }
}
```

**The payload is cached, not just the verdict.** This makes every enriched
message complete regardless of cache state, so the consumer's rule stays uniform
— always upsert what is in the message — instead of the subtler "an omitted
field means leave the existing row alone". It also self-heals drift: if a
database row is missing because the first event's message hit the DLQ, the next
event for that artist repopulates it from cache rather than waiting out a 90-day
TTL.

## Flow

```
GET enrichment/v1/<sha>.json
  ├─ NoSuchKey     -> full miss, run all three
  ├─ AccessDenied  -> THROW. Not a miss.
  └─ hit           -> per workflow: fresh(entry) ? reuse payload : run
  ...fan out, then merge(fresh cached entries, new results) -> single PUT
```

Treating `AccessDenied` as a miss would silently re-run every workflow on every
event forever — the exact failure this gate exists to prevent, presenting as
"the cache just isn't working". `lambda_mastra_handler.tf:57-59` already carries
a comment warning about this confusion on the poster path.

One writer per invocation, after the fan-out, so the three workflows never
read-modify-write against each other. Two invocations racing on the same new
artist remains possible at `maximum_concurrency = 2`; last write wins and the
cost is one duplicate generation.

```ts
const TTL_MS = { ok: 90 * DAY, none: 14 * DAY, error: 6 * HOUR } as const;
```

## Code surface

Follows `poster-sink.ts` exactly — an `EnrichmentCache` interface with
`S3EnrichmentCache` and `StubEnrichmentCache`. No new docker-compose service;
the stub covers local dev and unit tests.

---

# Workflow 1 — band image

Reuses `resolveImageCandidates(mbid)` and the existing `judgeBandImageStep` loop
bounded by `MAX_IMAGE_ATTEMPTS` (already 3).

Runs only when the event has no `image_url` of its own, per the requirement —
but the result is stored on the **artist**, so a later event for the same band
that also lacks an image gets it from cache for free.

**Skipping because the event already had an image writes no cache entry.** That
skip is a property of the *event*, not the artist. Recording it would make the
first well-illustrated event for a band suppress the image workflow for every
later event of theirs that has no image at all — for up to 90 days.

Unlike the poster path, this workflow does not need the bytes to survive. It
holds them in memory just long enough for the vision call, then records only the
Commons thumbnail URL and its credit. Nothing is written to disk or uploaded to
S3, so there is no run directory and no cleanup to get wrong.

| outcome | status |
| --- | --- |
| a candidate accepted by the vision judge | `ok` |
| no MBID, or no Commons candidates | `none` |
| all candidates rejected, or attempts exhausted | `none` (reason carries the judge's verdict) |
| fetch/provider failure | `error` |

---

# Workflow 2 — band bio

**Sourcing uses both APIs, each for what it is actually good at.**

MusicBrainz has no prose; Wikipedia has no reliable structure. The part of a bio
most likely to be confidently wrong is the discography, so that comes from
MusicBrainz's structured data rather than from an LLM reading prose:

```
mbid ──> resolveQid(mbid)                  Wikidata reverse P434 index
          (already implemented in wikimedia.tool.ts:105 — avoids a
           rate-limited MusicBrainz call)
     ──> wbgetentities?props=sitelinks&sitefilter=enwiki  -> article title
     ──> en.wikipedia.org action API: prop=extracts|revisions,
         explaintext, rvprop=ids                -> prose + revision_id
     ──> /ws/2/artist/{mbid}?inc=release-groups -> album titles + first-release dates
     ──> LLM: summarize into ~150-250 words of markdown
```

`resolveQid` is currently internal to `createWikimediaClient`; exposing it on
the `WikimediaClient` interface is an additive change that disturbs no existing
behavior.

**Bound the extract.** Full plain-text extracts for major artists run to
100KB+. Truncate to ~16KB (~4K tokens) before the model call.

**The bio contains no tour claims.** Origins and releases only — those are
stable for years, which is why this table's TTL can be long. Anything
tour-related lives in workflow 3, grounded in real evidence.

`sources` records the Wikipedia article title, URL and `revision_id`, plus the
MusicBrainz MBID — enough to tell later whether a bio is worth regenerating.

| outcome | status |
| --- | --- |
| bio generated | `ok` |
| no MBID, no QID, or no enwiki sitelink | `none` |
| Wikipedia/MusicBrainz fetch failure, or LLM failure after retries | `error` |

---

# Workflow 3 — recent tour setlist

**setlist.fm has no set times.** Its resources are `/artist/{mbid}/setlists`,
`/venue/{id}/setlists`, `/setlist/{id}` and search; the data model is
artist / venue / tour / set / song. There is no door time, set time or start
time anywhere, and setlists are created *after* a show by attendees, so an
upcoming event has no entry at all. What the API does provide, keyed by the MBID
we already resolve, is the band's recent setlists — "what they have been playing
on this tour" — which is the content this workflow stores.

## Client

Mirrors `musicbrainz.tool.ts` — same `createXClient({ baseUrl, fetchFn, minIntervalMs })`
options shape, so it drops into the existing `stub-fetch.ts` unit tests and the
opt-in `live-apis.test.ts` (`LIVE_API_TESTS=1`) with no new convention.

```
GET https://api.setlist.fm/rest/1.0/artist/{mbid}/setlists?p=1
  x-api-key: <secret>
  Accept: application/json      <- REQUIRED; the API defaults to XML
```

**Key handling:** Secrets Manager, mirroring
`aws_secretsmanager_secret.email_llm_key` — new secret,
`SETLISTFM_API_KEY_SECRET` env var, one `GetSecretValue` IAM statement, loaded
once per container beside the existing `loadModelKey()` call at
`handler.ts:128-129`.

**Throttle:** the slot-reservation limiter from `musicbrainz.tool.ts:65-72` with
`minIntervalMs = 1000`, not the 500 that 2 req/sec would allow. The limiter is
per-process, so at `maximum_concurrency = 2` two containers at 500ms would put
~4 req/sec against a 2 req/sec limit. At 1000ms the worst case lands on their
limit instead of double it. The binding constraint is the daily cap anyway.

**One page, never paginate.** Twenty items is ample recent history, and paging
spends daily budget fetching older setlists we would reject.

**Free skip:** no MBID from the prelude means `status: 'none'` without spending
a request.

## Choosing which setlist to store

Three of these four rules exist because of things the documentation does not
guarantee.

1. **Sort client-side by `eventDate` descending.** The endpoint documentation
   does not state a sort order. It is newest-first in practice, but building on
   an undocumented ordering produces a bug that appears months later with no
   code change.
2. **Parse `dd-MM-yyyy` explicitly.** Never hand it to `new Date()` —
   `"08-12-2026"` silently parses as August 12th under US interpretation, and
   `"23-08-1964"` yields `Invalid Date`. Both failures produce a
   plausible-looking wrong answer.
3. **Skip songless setlists.** Entries logged for attendance without a setlist
   are common, leaving `sets.set` empty or every set's `song` array empty. The
   docs are silent; treat a songless entry as absent.
4. **Reject anything older than 180 days.** A 2019 setlist is not "what they
   have been playing". Nothing qualifying on page 1 → `status: 'none'`, reason
   `"no setlist within 180 days"`.

Take the newest qualifying setlist and flatten `sets.set[]` into the ordered
`songs` array, carrying the `encore` index, `cover.name` → `cover_of`, and
`tape`.

**Pin the parser against a recorded fixture.** The JSON is XML-derived: the
docs table lists a `set` field while real responses nest `sets: { set: [...] }`.
Rather than trust either reading, record one real response into `testdata/` and
pin the parser to it — as `internal/scraper/ticketmaster/testdata` already does
for that API.

## The blurb

A second LLM call whose entire job is to not invent. Its input is only what was
fetched — tour name, observed date/venue/city, song count, the first few song
titles, and this event's own date and venue — with an explicit instruction to
add no facts beyond those. This is the one place we hold real tour evidence,
which is why the blurb lives here rather than in the bio workflow.

Blurb failure leaves `blurb` NULL with `status` still `ok`: the setlist landed
and is worth serving on its own.

## Status mapping

| outcome | status | retry |
| --- | --- | --- |
| No MBID from the prelude | `none` | 14d, no request spent |
| 404 — artist has no setlists | `none` | 14d |
| 200, nothing qualifying | `none` | 14d |
| Setlist found, blurb failed | `ok` (blurb NULL) | 90d |
| Setlist + blurb | `ok` | 90d |
| 429 / 5xx / timeout | `error` | 6h |

---

# Consumer changes

`EventHandler.Handle` decodes `EnrichedMessage`. A nil `Enrichment` behaves
exactly as today — the cutover safety net, and what keeps old DLQ messages
replayable.

When `Enrichment` is present the handler additionally:

1. Upserts `artists` on `name_key = events.NormalizeString(performer)`.
2. Upserts each of `artist_images`, `artist_bios`, `artist_tour_snapshots` for
   which the message carries a section.
3. Sets `events.headline_artist_id`, guarded by the `COALESCE` above.

**Order matters, a transaction does not.** The artist upsert must precede the
event upsert, because `events.headline_artist_id` has a foreign key to
`artists.id`. Beyond that, every write here is an idempotent upsert and a
handler error leaves the message on the queue for redelivery, so a partial
failure self-heals on retry. The worst case is an orphan `artists` row with no
event pointing at it, which the next delivery repairs. Wrapping this in a
transaction would mean threading a `*pgxpool.Pool` through `NewEventHandler`
purely to buy something retry already provides.

`cmd/app/main.go:133` reads `cfg.EnrichedEventsQueueURL` instead of
`cfg.EventsQueueURL`, via a new `ENRICHED_EVENTS_QUEUE_URL` in `config.go`.

---

# Terraform changes

| Resource | Change |
| --- | --- |
| `aws_sqs_queue.events` | `visibility_timeout_seconds` 30 → 900 |
| `aws_sqs_queue.events_enriched` + `_dlq` | new, mirroring `sqs.tf` |
| `aws_cloudwatch_metric_alarm` | new DLQ-depth alarm for the enriched DLQ |
| `aws_lambda_event_source_mapping.enrichment` | new |
| `aws_s3_bucket` enrichment cache | new, mirroring `posters.tf` |
| `aws_secretsmanager_secret` setlist.fm key | new |
| `mastra_handler` IAM | + `SendMessage` on enriched queue, + `ReceiveMessage`/`DeleteMessage`/`GetQueueAttributes` on `hwh-events-queue`, + `GetObject`/`PutObject` on the cache bucket, + `GetSecretValue` on the new secret |
| ECS task role IAM | + receive/delete on the enriched queue |
| `mastra_handler` env | + `ENRICHED_EVENTS_QUEUE_URL`, `ENRICHMENT_CACHE_BUCKET`, `SETLISTFM_API_KEY_SECRET` |
| `local.api_env_vars` | + `ENRICHED_EVENTS_QUEUE_URL` |

## Deployment order

**Lambda first, consumer second.** Messages then pile up harmlessly on the new
queue until the consumer arrives. Flipping the consumer first stalls ingest
until the Lambda ships.

This is **two terraform applies**, not one — the event source mapping must not
exist until an image that can handle SQS events is live, or the first scrape
will invoke the old handler with an SQS event and fall through its unguarded
`handleS3` cast.

1. **Apply 1:** queues (incl. the visibility-timeout bump), cache bucket,
   secret, IAM, and the Lambda env vars. No behavior change yet.
2. Populate the setlist.fm secret.
3. Deploy the Lambda image via the CI lane.
4. **Apply 2:** add `aws_lambda_event_source_mapping.enrichment`. Enrichment
   starts here; messages accumulate on the enriched queue with no consumer yet.
5. Point the consumer at the enriched queue **before** deploying the API image:
   `scripts/taskdef-edit.sh --set-env ENRICHED_EVENTS_QUEUE_URL=<url> --deploy`,
   then ship the image. Reversing these stops ingestion silently — the new
   binary starts its consumer only when that var is set, with no fallback to the
   raw queue, so deploying first means no consumer runs at all. The currently
   deployed binary ignores the var harmlessly, and the CI deploy inherits it
   (`ci/buildspec-app.yml` describes the current revision and swaps only the
   image).

**The ECS env var will not reach the running task by terraform alone** —
`container_definitions` carries `ignore_changes`. Use
`scripts/taskdef-edit.sh --set-env --deploy`.

---

# Testing

| Layer | Approach |
| --- | --- |
| `artistKey()` ↔ `NormalizeString` | shared `testdata/artist-key-contract/cases.json`, asserted by a Go test and a vitest test |
| Wire contract | a **sibling** `testdata/enriched-message-contract/` — the existing directory is decoded into a plain `Message` with `DisallowUnknownFields`, so an enriched fixture there would fail. The new fixture is decoded by a Go test and parsed by the Zod schema, so a divergence fails on both sides |
| setlist.fm parser | recorded real response as a fixture; unit tests via `stub-fetch.ts` |
| setlist.fm live | opt-in in `live-apis.test.ts` under `LIVE_API_TESTS=1` |
| Enrichment cache | `StubEnrichmentCache`; hit/miss/stale/`AccessDenied`-throws cases |
| Orchestration | stubbed workflows; assert one failing workflow neither blocks the others nor prevents the message |
| Consumer | `internal/ingest` tests against the real test DB, incl. a nil-`Enrichment` message behaving as today and the `COALESCE` guard |
| API | handler tests asserting `omitempty` absence for unenriched events and the `image_url` fallback |

Note the worktree test-DB caveat: all worktrees share one local `appdb_test`, so
a new migration here will strand other branches' tests until they migrate.

---

# Accepted risks

**setlist.fm is non-commercial-only.** The free key forbids commercial use;
commercial licensing requires contacting them. This app is not commercial today,
but this dependency changes status the day the product does.

**The setlist.fm daily cap is 1,440 requests.** Comfortable at ~200 events/day
with the cache absorbing repeats; day-one backfill is ~200 calls. It stops being
comfortable if a second scrape source or a second city lands, and the documented
upgrade path is a manual request that forum threads report waiting months on.
The cache is what keeps this inside the cap, which is why it is not optional.

**MusicBrainz throttling is per-process.** `musicbrainz.tool.ts:65` says so
explicitly. At `maximum_concurrency = 2`, two containers can put ~2 req/sec
against MusicBrainz's ~1 req/sec ask. MusicBrainz signals with 503 and the
client already retries once, so this degrades rather than breaks.

**Hotlinked Commons images can 404.** A file deleted for a licence problem
becomes a dead image. The event's own `image_url` always wins, so this only
affects images we supplied, and the stored `file` identifies what vanished.

**Two artist-keyed caches now exist.** `artist_genre_cache` and `artists`
duplicate `name_key`/`mbid`/`status`/`resolved_at`. Deliberate — see Non-goals.

**Cache/database drift is bounded by TTL.** If the cache says fresh and the
database row is missing, the cached payload repopulates it on the next event for
that artist. Only an artist with no further events stays unrepaired, until TTL.
