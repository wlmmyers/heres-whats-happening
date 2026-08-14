# Here's What's Happening

An AI-backed live-event calendar based on your interests, currently focused on Seattle area events

Check out the live app at [hereswhatshappening.app](https://hereswhatshappening.app/).

- 🎯 **Rich ingestion of events.** We pull events from the major ticketing platforms and from local promoter newsletters and concert flyers.
- 🎧 **Interests from your listening history and more.** Link your Spotify account and we'll read the artists and genres you already listen to, then turn them into interests that get matched against local events.
- ✍️ **Add your own interests.** Love a band or genre we don't see in your listening history? Add your own tags and we'll watch for it.
- 🧠 **Intelligent matching.** Every event is scored against your taste using a blend of keyword and AI semantic matching so "indie rock" still finds the show even when the listing never says those exact words.
- 📅 **Integrate with calendar apps.** Generate a personal calendar feed and add it to Apple Calendar, Google Calendar, or Fantastical.
- 💻 **A clean, fast web app.** Sign up, manage your interests, connect Spotify, and browse your personalized calendar from any browser.

## Coming Soon

- **Custom day-calendar UI builder** — design your own at-a-glance view and render it onto an always-on screen, so today's plans are always in sight.
- **Support for more cities** — expand support past Seattle to cover more major cities.
- **Better live sports support** — never miss your team, with richer coverage of games and matchups.

## Architecture Diagram

<img width="1264" height="801" alt="image" src="https://github.com/user-attachments/assets/ba035e2f-bb01-49ee-89b9-b61aafcd7ad3" />

## Contributing - Prerequisites

Go 1.24+, Docker, pnpm 9+, make, psql

## Git hooks

```bash
make hooks
```

Points `core.hooksPath` at `.githooks/`, enabling a pre-commit hook that runs
gofmt, `go vet`, the Go test suite, eslint, `tsc`, prettier, and vitest before
each commit (~35s). `core.hooksPath` is per-clone local config, so every fresh
clone needs this once.

The Go tests are integration tests, so `make db-up queue-up` must be running or
the hook aborts the commit and tells you so. Bypass a single commit with
`git commit --no-verify`; remove the hooks with `make hooks-uninstall`.

Note the checks run against the working tree, not the staged snapshot — if you
stage only part of your changes, what runs is what is on disk.

To run the same checks without committing (and without installing the hooks):

```bash
make check
```

## Backend quickstart

```bash
cp .env.example .env
# Start Postgres + pgvector (creates appdb and appdb_test) and ElasticMQ (local
# SQS). The test suite includes end-to-end tests that need both services up.
make db-up
make queue-up

# Apply migrations to both databases
make migrate
make migrate-test

# Run the test suite (integration + e2e tests against appdb_test and ElasticMQ)
make test

# Run the server
make run
# In another shell:
curl http://localhost:8080/healthz
```

## Event ingest quickstart

```bash
# Set your Ticketmaster API key (free, https://developer.ticketmaster.com)
# Edit .env: TICKETMASTER_API_KEY and TICKETMASTER_CITY

# Run a one-shot scrape (publishes EventMessage records to events-queue).
# `make scrape` is a shortcut for this exact command.
go run ./cmd/app scrape events --source=ticketmaster

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

## Spotify integration quickstart

Register a Spotify app at https://developer.spotify.com/dashboard with the
redirect URI `http://localhost:5173/integrations/spotify/callback`. Then set
`SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET` in `.env` — everything else
already has dev-safe defaults in `.env.example`.

```bash
make db-up && make queue-up
make migrate && make migrate-test
make run    # starts api + events consumer + interests consumer
```

### Connect a user

1. Sign up + log in via the auth flow (see Backend quickstart above).
2. In another shell, start the frontend: `cd web && pnpm dev`
3. Open `http://localhost:5173`, log in, navigate to Settings, and click
   **Connect Spotify**. The SPA calls the API to start the OAuth flow,
   Spotify redirects back to the SPA callback, and the server stores the
   encrypted tokens and publishes an InterestMessage to the interests-queue.
   The consumer drains it.
4. Verify:

```bash
docker exec hwh_postgres psql -U app -d appdb -c \
  "SELECT kind, value, weight FROM user_interests \
   WHERE kind LIKE 'spotify%' ORDER BY weight DESC LIMIT 10;"
```

### Periodic scrape

```bash
go run ./cmd/app scrape spotify   # iterates all connected users, publishes fresh InterestMessages
```

## Match-job quickstart

```bash
# Start the TEI sidecar (BAAI/bge-small-en-v1.5)
make tei-up
# First run downloads the model; takes ~2 minutes. Subsequent runs are fast.

# Verify TEI is healthy
curl -s http://localhost:8081/health

# Run the match-job (`make match` is a shortcut for this)
go run ./cmd/app match
# Steps it runs:
#  1. Embed any events whose embedding column is NULL
#  2. Embed any users whose interests changed since last embedding
#  3. Score every (user, event) pair; upsert above-threshold matches
#  4. Archive events not seen in the last 7 days
```

### Tuning weights

Match weights live in the `match_config` table; change them with SQL and the
next `go run ./cmd/app match` picks them up — no rebuild needed.

```sql
UPDATE match_config SET value = '0.7'::jsonb WHERE key = 'w_string';
UPDATE match_config SET value = '0.3'::jsonb WHERE key = 'w_embedding';
```

### Inspect a user's matches

```bash
docker exec hwh_postgres psql -U app -d appdb -c "
  SELECT e.title, m.score, m.score_breakdown
  FROM user_event_match m
  JOIN events e ON e.id = m.event_id
  WHERE m.user_id = (SELECT id FROM users WHERE email = 'you@example.com')
  ORDER BY m.score DESC, e.starts_at ASC
  LIMIT 20;
"
```

## Calendar API + iCal feed quickstart

```bash
# Make sure ICAL_BASE_URL is set in .env (default http://localhost:8080)
make db-up && make run
```

### Read your matched calendar

Both `/me/calendar` and `/calendar/{cityId}` are cursor-paginated: at most 20
events per response. Omit `cursor` for the first page; when more results
exist the response includes `next_cursor`, which you pass back as
`?cursor=...` to fetch the next page. A malformed cursor is a 400 with code
`bad_cursor`.

```bash
ACCESS=...  # JWT from /auth/login
curl -s -H "Authorization: Bearer $ACCESS" \
  "http://localhost:8080/me/calendar" \
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
Calendar, or Google Calendar → Other Calendars → From URL. Your calendar
app will pull the feed roughly hourly (the `X-PUBLISHED-TTL: PT1H` hint).

### Revoke

```bash
curl -s -X DELETE -H "Authorization: Bearer $ACCESS" \
  http://localhost:8080/me/ical-token  # → 204
```

The old URL stops working immediately. Generate a new one via POST.

## Frontend quickstart

The SPA lives in `web/`. In dev it runs on Vite (port 5173) and proxies API
calls to the Go backend on port 8080.

```bash
# One-time setup
cd web
pnpm install

# Daily dev (alongside `make run` for the API)
pnpm dev
# Open http://localhost:5173
```

### Run tests

```bash
cd web
pnpm test          # one-shot
pnpm test:watch    # watch mode
```

## Email-newsletter ingest quickstart

SES receives promoter newsletters at `shows@inbound.<domain>` → S3 → a
Node/Mastra Lambda (`lambda/mastra-handler`) that parses plain text and flyer
images into `EventMessage` records on the events-queue. The existing consumer
(started by `make run`) drains the queue and upserts events into Postgres
exactly as it does for Ticketmaster or Spotify events; the `source` column is
`email_newsletter`.

```bash
cd lambda/mastra-handler
pnpm install
pnpm test
```

### Run Mastra Studio to test the extractor agent interactively

The `emailExtractor` Mastra agent is the core of the Lambda. You can drive it
from the Studio UI without sending a real email:

```bash
cp lambda/mastra-handler/.env.example lambda/mastra-handler/.env
# Edit lambda/mastra-handler/.env and set ANTHROPIC_API_KEY

cd lambda/mastra-handler
pnpm dev   # starts Mastra Studio
# Open http://localhost:4111 — the emailExtractor agent appears in the sidebar.
```

### Full end-to-end locally

```bash
# Start ElasticMQ (local SQS) from the repo root
make queue-up

# Configure the Lambda env
export ANTHROPIC_API_KEY=<your-key>
export EVENTS_QUEUE_URL=http://localhost:9324/000000000000/events-queue
export SQS_ENDPOINT=http://localhost:9324

# Run a real .eml through the agent → ElasticMQ
cd lambda/mastra-handler
pnpm invoke-local src/__fixtures__/text-newsletter.eml

# In another terminal: start the consumer so it drains the queue into Postgres
make run   # from repo root

# Verify
docker exec hwh_postgres psql -U app -d appdb \
  -c "SELECT e.title FROM events e JOIN event_sources s ON s.id = e.source_id WHERE s.name = 'email_newsletter' LIMIT 5;"
```

### Poster endpoint

The Lambda also serves an internal **`POST /api/poster`** endpoint on its AWS_IAM-protected Function URL, reachable only by the API service's ECS task role (see `docs/superpowers/specs/2026-08-10-poster-proxy-design.md`). It takes `{ userId, performer, venue, date, force? }` — `performer` and `venue` are bounded to 200 characters and `date` to 100 (measured after trimming, in Unicode code points, so non-Latin names get the full budget; the whole request body is capped at 8 KB) — and returns `{ pngKey, cached, artist?, credit? }` — an S3 object key, not a URL, because the API service presigns at read time. Clients use the API's `/posters` endpoints instead, whose ready response is `{ status, pngUrl, artist?, credit? }`. No SVG is produced, stored, or served anywhere in this pipeline (see `docs/superpowers/specs/2026-08-11-drop-svg-artifact-design.md`).

`userId` is supplied by the API service from the authenticated session and must be a UUID. It scopes the S3 object key (`posters/v2/u-{userId}/…`), which is what stops one user's `force: true` regeneration — `force` skips the cache read and always re-writes — from overwriting another user's poster. The key also ends in a digest of the three normalized fields: the readable slugs drop every non-Latin character, so without it `"La Luz"` and `"La Luz Мумий"` collide on one object.

### Event enrichment

Scrapers publish to `hwh-events-queue`. The mastra-handler Lambda consumes it
(SQS trigger, `batch_size = 1`), resolves one MusicBrainz artist per event, runs
three enrichment workflows — band image, Wikipedia/MusicBrainz bio, setlist.fm
tour snapshot — and republishes onto `hwh-events-enriched-queue`, which the ECS
ingest consumer reads.

Enrichment is artist-scoped: one band playing five dates is one bio, one photo,
one setlist. An S3 skip cache (`*-enrichment-cache` bucket) gates repeat work,
which matters because the daily scrape republishes every event unconditionally.
