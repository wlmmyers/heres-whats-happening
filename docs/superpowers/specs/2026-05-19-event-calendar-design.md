# Event Calendar — v1 Design

**Status:** Draft for review
**Date:** 2026-05-19
**Working name:** Here's What's Happening (event-calendar)

## 1. Overview

A public web product that builds each user a personalized calendar of local live events (concerts, theater, musicals, comedy, etc.) by matching scraped event data against signals about the user's interests. Events come from venue and aggregator websites; interest signals come from the user's Spotify listening history plus manual tag entry. The matching pipeline runs as a nightly batch job. Users consume their calendar either through a web app or by subscribing to a personalized iCal feed URL in their existing calendar app (iOS Calendar, Google Calendar, Fantastical, etc.).

## 2. Goals and Non-Goals

### Goals (v1)

- Aggregate event listings from a curated set of venue and aggregator sources for a single hardcoded city.
- Collect user interest signals from Spotify and from a manual tag/category picker.
- Score each (user, event) pair using a hybrid scheme: explicit string/genre matches *plus* semantic similarity from text embeddings.
- Precompute matches once daily and surface the results through a web UI and an authenticated iCal subscription feed.
- Run end-to-end on AWS-native infrastructure.

### Non-Goals (explicitly deferred)

1. Multi-city support
2. Email scraper / any email integration
3. Push notifications / email digests
4. Past-attendance import (Eventbrite, Bandsintown saved events, etc.)
5. Thumbs up / thumbs down feedback loop
6. Social features (sharing, friends, public profiles)
7. Mobile apps (iOS / Android)
8. Ticket purchase / commerce
9. Per-user notification preferences
10. Physical/e-ink "frame" delivery
11. Real-time matching (the contract is nightly batch)
12. LLM-based ranking
13. Multi-language / i18n

## 3. Architecture

### Components

**External:**
- Venue and aggregator websites / public APIs (Bandsintown, Songkick, individual venue pages, etc.)
- Spotify Web API

**Compute (AWS ECS Fargate):**
- `api` — long-running Go service behind an Application Load Balancer. Runs the HTTP request handlers *and* the SQS queue consumer goroutines in a single binary.
- `tei` — internal-only Hugging Face `text-embeddings-inference` (TEI) service, accessed via Cloud Map service discovery. Hosts `BAAI/bge-small-en-v1.5` (384-dim) for v1.
- `event-scraper` — one ECS scheduled task per source adapter. Runs daily at 00:00 local time.
- `spotify-scraper` — ECS scheduled task. Runs daily at 00:00 local time.
- `match-job` — ECS scheduled task. Runs daily at 02:00 local time, after both scrapers.

**Messaging:**
- SQS `events-queue` (+ DLQ): receives normalized event records from scrapers.
- SQS `interests-queue` (+ DLQ): receives normalized interest records from `spotify-scraper`.

**Storage:**
- RDS PostgreSQL 16 with the `pgvector` extension. Single db.t4g.small instance for v1.

**Frontend / delivery:**
- React + Vite SPA, built locally and deployed to S3.
- CloudFront in front of the S3 bucket; Route53 DNS records resolve `example.com` to CloudFront.
- iCal feed served from the `api` service (no auth header, signed token in the URL path).

### Data flow

```
            ┌──────────────────────┐   ┌─────────────────────┐
            │ Venue / aggregator   │   │ Spotify Web API     │
            │ websites & APIs      │   └──────────┬──────────┘
            └──────────┬───────────┘              │
                       │                          │
              ┌────────▼────────┐         ┌───────▼────────┐
              │ event-scraper   │         │ spotify-       │
              │ (per source,    │         │ scraper        │
              │  daily 00:00)   │         │ (daily 00:00)  │
              └────────┬────────┘         └───────┬────────┘
                       │                          │
              ┌────────▼────────┐         ┌───────▼────────┐
              │ events-queue    │         │ interests-queue│
              │ (SQS + DLQ)     │         │ (SQS + DLQ)    │
              └────────┬────────┘         └───────┬────────┘
                       │                          │
                       └────────────┬─────────────┘
                                    │
                          ┌─────────▼────────┐
                          │ api service      │◀────── HTTP from web app
                          │ (consumers +     │◀────── HTTP for iCal feed
                          │  HTTP handlers)  │
                          └─────────┬────────┘
                                    │
                          ┌─────────▼────────┐         ┌─────────────┐
                          │ Postgres +       │◀────────│ match-job   │
                          │ pgvector         │         │ (daily 02:00)│
                          └──────────────────┘         └──────┬──────┘
                                                              │
                                                       ┌──────▼──────┐
                                                       │ tei sidecar │
                                                       │ (embedding) │
                                                       └─────────────┘
```

### Architectural decisions worth stating explicitly

1. **One Go service for ingest + HTTP.** The queue consumers and the HTTP handlers run in the same binary. Cleanest unless and until scaling profiles diverge.
2. **TEI is called *only* by the `match-job`.** The ingest path writes events and interests with `embedding = NULL`. The match-job embeds anything still null at the start of its run, then scores. This means TEI availability never blocks ingest.
3. **`match-job` is a separate scheduled task**, not a goroutine inside `api`. Keeps long-running batch work off the request-path autoscaler.
4. **SQS is retained even though scrapers could write directly to the DB.** Decouples scraper failure modes from DB availability and gives free retries / DLQs. At v1 scale it's nearly free.
5. **`cities` is a real table even though v1 has one row.** Cheap abstraction now; no migration when v2 adds cities.

## 4. Data Model

### Identity

- `cities(id, name, slug, timezone)` — seeded with one row for v1 via a migration. The scraper adapter configs (in `event_sources.config`) and the default `users.city_id` reference this row's id by slug, so "the v1 city" lives in exactly one place.
- `users(id, email UNIQUE, password_hash, city_id FK, interest_embedding vector(384), interest_embedding_updated_at, created_at, deleted_at)`.
- `refresh_tokens(id, user_id FK, token_hash, expires_at, revoked_at, created_at)`.
- `user_spotify_tokens(user_id PK FK, access_token_enc, refresh_token_enc, expires_at, scope, last_synced_at)` — both tokens AES-GCM encrypted at rest with a key from Secrets Manager.
- `ical_tokens(user_id PK FK, token_hash, created_at, last_accessed_at)` — stores `sha256(token)`; raw token returned to user once at generation time.

### Event side

- `event_sources(id, name UNIQUE, adapter_kind, config jsonb)` — registry of scraper adapters (e.g., `bandsintown_api`, `songkick_api`, `venue_<name>_html`).
- `venues(id, city_id FK, name, normalized_name, address, lat, lng, website_url, UNIQUE(city_id, normalized_name))`.
- `genres(slug PK, label)` — controlled vocabulary (`rock`, `jazz`, `electronic`, `theater`, `comedy`, `musical`, `food`, `sports`, `art`, etc.). Scrapers map source-specific tags into this vocab.
- `events(id, source_id FK, source_event_id, title, description, starts_at, ends_at, venue_id FK, image_url, url, embedding vector(384), embedding_updated_at, last_seen_at, archived_at, created_at, updated_at, UNIQUE(source_id, source_event_id))`. `last_seen_at` is updated on every ingest of that source-event pair; the archival rule in section 5 keys off this column.
- `event_performers(event_id FK, performer_name, normalized_name, PK(event_id, normalized_name))` — many-to-many; performer is just a string for v1.
- `event_genres(event_id FK, genre_slug FK, PK(event_id, genre_slug))` — many-to-many.

### Interest side

- `user_interests(id, user_id FK, kind, value, normalized_value, weight, created_at, updated_at, UNIQUE(user_id, kind, normalized_value))`.
  - `kind` ∈ {`spotify_top_artist`, `spotify_top_genre`, `manual_tag`}
  - `value` — raw artist name, genre slug, or free-text tag.
  - `normalized_value` — lowercased, diacritic-stripped form used for matching.
  - `weight` — rank-derived (e.g., top-1 artist = 1.0, top-50 = 0.5; manual tag = 1.0).

### Match side

- `user_event_match(user_id FK, event_id FK, score, score_breakdown jsonb, computed_at, PK(user_id, event_id))`.
  - `score_breakdown` is `{string_score, embedding_score, matched_performers: [...], matched_genres: [...]}` — used by the UI to render "matched because" hints.
  - Only rows with `score > 0.3` are written. Absence in the table means below threshold.

### Config

- `match_config(key PK, value jsonb, updated_at)` — single row per key. v1 keys: `w_string` (default `0.6`), `w_embedding` (default `0.4`), `score_threshold` (default `0.3`), `artist_factor` (default `1.0`), `genre_factor` (default `0.3`), `string_max` (default `3.0`). Match-job reads these at the start of each run.

### Indexes

- `events`: `(starts_at)`, `(venue_id)`, `(archived_at)`, ivfflat index on `embedding` for cosine search.
- `user_event_match`: `(user_id, event_id)` PK, secondary `(user_id, score DESC)` for top-N queries with date filter.
- `user_interests`: `(user_id, kind)`, `(normalized_value)`.
- `event_performers`: `(normalized_name)`.

## 5. Ingest Pipeline

### Event scrapers

- Each adapter is a stateless Go binary running as its own ECS scheduled task at 00:00 daily.
- An adapter: (1) fetches from one source; (2) maps source records into a canonical `EventMessage` JSON; (3) publishes one SQS message per event to `events-queue`. Adapters never touch Postgres.
- The canonical `EventMessage` schema:
  ```json
  {
    "source_id": "bandsintown_api",
    "source_event_id": "abc123",
    "title": "...",
    "description": "...",
    "starts_at": "2026-06-15T20:00:00-04:00",
    "ends_at": null,
    "venue": { "name": "...", "address": "...", "lat": 40.7, "lng": -74.0 },
    "performers": ["..."],
    "genres": ["..."],            // mapped into our controlled vocab
    "image_url": "...",
    "url": "..."
  }
  ```
- Adapters are required to map source genre tags into our controlled `genres` vocabulary. Unrecognized tags are dropped (not stored verbatim).
- Each scheduled run republishes the source's full current view. Ingest deduplicates via `UNIQUE(source_id, source_event_id)`.

### Spotify scraper

- ECS scheduled task at 00:00 daily. Iterates over users with non-revoked Spotify tokens.
- For each user: refreshes the access token if expired, fetches top artists / top genres / recently played, publishes one `InterestMessage` to `interests-queue`.
- On-connect immediate sync: the Spotify OAuth callback handler in `api` also enqueues an `InterestMessage` so new connections don't wait until tomorrow.
- `InterestMessage` schema:
  ```json
  {
    "user_id": "...",
    "spotify_top_artists": [{ "name": "...", "rank": 1 }, ...],
    "spotify_top_genres":  [{ "name": "...", "rank": 1 }, ...],
    "fetched_at": "2026-05-19T00:05:00Z"
  }
  ```

### Manual tags

- Written through `POST /me/interests` directly to `user_interests`. No queue indirection — single-row request-path writes are appropriate here.

### Ingest consumer (inside `api`)

- Goroutine pool consumes both queues with configurable concurrency.
- **Events:** upsert `venues` by `(city_id, normalized_name)`, upsert `events` by `(source_id, source_event_id)`, replace `event_performers` and `event_genres` for that event in a single transaction. **`embedding` is left as NULL** — TEI is not called here.
- **Interests:** in a single transaction, delete existing `user_interests` rows for that user where `kind IN ('spotify_top_artist', 'spotify_top_genre')`, then insert the new ones. Manual tags are untouched.
- Idempotency: every operation is an upsert. Re-delivery from SQS is safe.
- DLQ: 3 receives → DLQ. CloudWatch alarm on DLQ depth.

### Archival

- Every ingested event has its `last_seen_at` set to `now()` on each upsert.
- A separate step at the end of the `match-job` run sets `archived_at = now()` on any `events` row where `archived_at IS NULL AND last_seen_at < now() - INTERVAL '7 days'`. Archived events are excluded from subsequent match-job runs but retained for audit/history.

## 6. Matching

### Scoring formula

Per `(user, event)` pair:

```
string_score    ∈ [0, 1]
embedding_score ∈ [0, 1]
total_score     = w_string * string_score + w_embedding * embedding_score
```

Default weights: `w_string = 0.6`, `w_embedding = 0.4`. Read from the `match_config` table (see section 4), not hardcoded, so they can be tuned without a deploy.

**`string_score`:**

- `artist_score` = sum over `user_interests` with `kind = 'spotify_top_artist'` where `normalized_value` appears in any `event_performers.normalized_name` for the event: `interest.weight * ARTIST_FACTOR`.
- `genre_score` = sum over `user_interests` with `kind IN ('spotify_top_genre', 'manual_tag')` where `value` matches any `event_genres.genre_slug`: `interest.weight * GENRE_FACTOR`.
- Combine: `string_score = clamp((artist_score + genre_score) / string_max, 0, 1)`. `artist_factor`, `genre_factor`, and `string_max` come from `match_config`.

**`embedding_score`:**

- `(1 + cosine_similarity(users.interest_embedding, events.embedding)) / 2`, mapping cosine ∈ [-1, 1] to [0, 1].
- Computed in-database via pgvector's `<=>` cosine-distance operator.

### Match-job procedure

Runs daily at 02:00. Single Fargate task. In order:

1. **Embed missing events.** Select `events` rows where `embedding IS NULL` AND `archived_at IS NULL` AND `starts_at > now()`. Build text input as `title + " — " + performers_joined + ". " + (genres_joined + ". ") + description_truncated`. Batch in chunks of 64, call TEI, write embeddings back.
2. **Embed changed users.** Select `users` rows where `interest_embedding_updated_at < max(user_interests.updated_at)` for that user, or `interest_embedding IS NULL`. Build text input by joining each user_interest's `value` in rank order, comma-separated, prefixed by kind label — e.g., `Top artists: Phoebe Bridgers, Big Thief, Mitski. Top genres: indie folk, indie rock. Interests: live music, theater.`. Batch, call TEI, write embeddings back.
3. **Compute and upsert matches.** Single SQL pass joining `users → user_interests`, `events → event_performers`, `events → event_genres`, plus `pgvector` for cosine. Upsert into `user_event_match` rows with `total_score > 0.3`.
4. **Delete obsolete match rows.** Remove `user_event_match` for events that are now in the past or archived, and for users that are soft-deleted.

If TEI is unavailable during steps 1 or 2, the job logs and continues with the embeddings it has. Events / users still missing embeddings get scored on `string_score` alone (the `embedding_score` term contributes 0). Next night's run will retry.

### Read path

- `GET /me/calendar?from=...&to=...` joins `user_event_match` with `events`, filters by `starts_at` range and `archived_at IS NULL`, returns sorted by `starts_at` ascending.
- The iCal feed serves the same data with a default lookahead window of 60 days.

## 7. Auth and Integrations

### Auth

- Signup / login flow uses email + password. Passwords hashed with argon2id (`golang.org/x/crypto/argon2`) at standard parameters.
- On successful login, the server issues:
  - **Access token:** JWT, HS256, 15-minute TTL, signed with a key from Secrets Manager. Returned in the response body; the SPA holds it in memory.
  - **Refresh token:** 32 bytes random, 30-day TTL, returned in an `httpOnly`, `Secure`, `SameSite=Lax` cookie. `sha256(token)` stored in `refresh_tokens`.
- `POST /auth/refresh` validates the cookie hash against the table and issues a new access token. No refresh rotation in v1.
- `POST /auth/logout` clears the cookie and sets `revoked_at` on the row.
- No JWT denylist in v1. Short access-token TTL is the mitigation. Compromised refresh tokens are killed by revoking the DB row.

### Spotify OAuth

- `GET /integrations/spotify/connect` constructs a Spotify authorize URL with PKCE + state. State stored in a short-TTL signed cookie.
- `GET /integrations/spotify/callback` validates state, exchanges code for access + refresh tokens, encrypts both with AES-GCM (key from Secrets Manager), upserts into `user_spotify_tokens`, and enqueues an immediate `InterestMessage` to `interests-queue`.
- Scopes requested: `user-top-read`, `user-read-recently-played`. No others.
- `DELETE /integrations/spotify` zeroes the row and deletes Spotify-derived `user_interests`.
- The `spotify-scraper` task refreshes access tokens as needed using the stored refresh token.

## 8. iCal Delivery

- `POST /me/ical-token` generates a 32-byte random token, stores `sha256(token)` in `ical_tokens`, returns the subscription URL exactly once: `https://api.example.com/ical/<token>.ics`. The raw token is never displayed again.
- `DELETE /me/ical-token` removes the row, immediately invalidating the URL.
- `GET /ical/:token.ics` (no auth header) hashes the path token, looks up the user, returns RFC 5545 `VCALENDAR` body:
  - One `VEVENT` per matched upcoming event in the next 60 days.
  - `UID` is `event-<event_id>@example.com` — stable across feed refreshes so calendar clients update rather than duplicate.
  - `SUMMARY` = event title.
  - `DTSTART` / `DTEND` from `events.starts_at` / `ends_at`.
  - `LOCATION` = `venues.name + ", " + venues.address`.
  - `URL` = `events.url`.
  - `DESCRIPTION` includes a "Matched because: ..." line derived from `score_breakdown` and the ticket URL.
- Response headers: `Content-Type: text/calendar; charset=utf-8`, `Cache-Control: max-age=3600`, `X-PUBLISHED-TTL: PT1H`.
- The endpoint MUST work without any custom request headers — iOS Calendar and Google Calendar do not support them on subscriptions.

## 9. API Surface

```
# Auth
POST   /auth/signup
POST   /auth/login
POST   /auth/refresh
POST   /auth/logout

# User
GET    /me
DELETE /me

# Interests (manual tags)
GET    /me/interests
POST   /me/interests
DELETE /me/interests/:id

# Spotify integration
GET    /integrations/spotify/connect       # 302 to Spotify
GET    /integrations/spotify/callback
DELETE /integrations/spotify

# Calendar
GET    /me/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD
GET    /events/:id

# iCal feed
POST   /me/ical-token
DELETE /me/ical-token
GET    /ical/:token.ics

# Health
GET    /healthz
GET    /readyz
```

## 10. Frontend

- **Stack:** React + Vite SPA, TypeScript.
- **Routing:** `react-router` (file-based router unnecessary for a project this size).
- **State:** TanStack Query for server state; minimal local state.
- **Pages:**
  - `/signup`, `/login`
  - `/onboarding` — interest tag picker plus optional "Connect Spotify" button.
  - `/calendar` — main view. Month grid + agenda toggle. Cards show matched events with a "Matched because: ..." line.
  - `/events/:id` — event detail with venue, time, description, match breakdown.
  - `/settings` — manage interests, manage Spotify connection, generate / regenerate iCal URL.
- **Build & deploy** (local script, single developer):
  ```
  pnpm build                         # outputs dist/
  aws s3 sync dist/ s3://<bucket>/ --delete
  aws cloudfront create-invalidation --distribution-id <id> --paths "/*"
  ```
- **CloudFront:** default behavior caches static assets; error response config returns `index.html` (HTTP 200) for 404s on the SPA paths so client-side routing works on hard refresh.

## 11. Infrastructure

### AWS resources

- **VPC** with public + private subnets across 2 AZs. ALB in public subnets; ECS tasks and RDS in private subnets. Single NAT gateway.
- **ECS Fargate cluster** hosting:
  - `api` service — autoscaled on CPU, behind public ALB on HTTPS via ACM cert.
  - `tei` service — internal-only, registered with Cloud Map for `api` and `match-job` to discover.
- **EventBridge Scheduler** rules:
  - `event-scraper-<name>` — daily at 00:00 local time, one rule per adapter.
  - `spotify-scraper` — daily at 00:00 local time.
  - `match-job` — daily at 02:00 local time.
- **SQS:** `events-queue` + `events-dlq`, `interests-queue` + `interests-dlq`. DLQ after 3 receives. CloudWatch alarms on DLQ message count.
- **RDS PostgreSQL 16** with `pgvector` extension. `db.t4g.small` for v1. Automated backups enabled before launch.
- **Secrets Manager:**
  - JWT signing key (HS256)
  - DB password
  - Spotify client ID + secret
  - App-level encryption key for `user_spotify_tokens`
  - IAM task roles grant per-secret access (api can read JWT signing key; spotify-scraper cannot, etc.).
- **ECR** for container images. **CloudWatch Logs** for all tasks with a uniform 30-day retention.
- **S3** for frontend assets, **CloudFront** distribution in front. **Route53** records: `example.com` → CloudFront (frontend), `api.example.com` → ALB.
- **ACM** certificates for both `example.com` and `api.example.com`.

### Go service stack

- Go 1.24.
- HTTP router: `chi`.
- DB driver: `pgx` v5.
- Type-safe queries: `sqlc`.
- Migrations: `golang-migrate`.
- AWS SDK: `aws-sdk-go-v2`.
- Password hashing: `golang.org/x/crypto/argon2`.

## 12. Open Questions and Future Work

These are explicit deferrals, not gaps in v1:

- **Score weight tuning.** v1 ships with `w_string = 0.6, w_embedding = 0.4` as initial defaults. Real user data will tell us whether the embedding signal needs more or less weight.
- **Per-interest embeddings.** v1 uses a single per-user embedding (concat all interests, embed once). A future revision could embed each interest separately and take the max similarity per event, which handles "user has one niche interest amid mainstream ones" better.
- **Match-job incrementality.** v1 fully recomputes the matrix each night. When per-run runtimes approach 30 minutes, add tracking of "events changed since last run" and "users with changed interests" to compute only deltas.
- **Refresh-token rotation.** v1 issues a refresh on login and reuses it for 30 days. Rotation (issue new refresh + invalidate old on each refresh call) is a cheap upgrade once the rest works.
- **City as a v2 abstraction.** The schema models cities as a table from v1, but the entire scraper and config layer is hardcoded to one city. Multi-city is a separate v2 project.
