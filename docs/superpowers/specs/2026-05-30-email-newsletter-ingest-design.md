# Ingest live-music events from promoter email newsletters

**Date:** 2026-05-30
**Status:** Approved, ready for implementation plan

## Problem

Event data today comes only from pull-based scrapers (`internal/scraper`,
e.g. Ticketmaster) that `Fetch` a snapshot on a schedule. A large source of
local live-music data is concert-promoter **email newsletters** — free text,
and sometimes nothing but an image of a flyer. We want a push-based ingestion
path: an email arrives, an LLM parses it into events, and those events land in
the same database the existing pipeline serves from.

Three sub-problems the source forces on us:

- **Flyer-only emails.** Some promoters send an image poster with an empty text
  body. Text parsing alone fails; we need a multimodal model fallback.
- **Duplicates.** Promoters re-send the same lineup weekly, adding one new show
  at the bottom. Re-ingesting must not create clones or clobber existing rows.
- **Relative dates.** "Live music this Friday, Jan 5th!" sent in December needs
  the email's own date to resolve the year.

## How it fits the existing pipeline

The existing seam is already the right one. Scrapers and any other producer
never touch the database — they emit canonical `events.Message`
(`internal/events/message.go`) onto the events-queue (SQS in prod, ElasticMQ in
dev), and the `internal/ingest` consumer upserts them. **This feature is a new
queue producer that conforms to that wire contract; the consumer is reused
unchanged.**

It deliberately does **not** implement `scraper.Adapter` — that interface is
pull/snapshot (`Fetch(ctx) ([]events.Message, error)`), whereas email is
event-driven. The Lambda is a producer, not an adapter.

Two existing contracts already solve two of the three sub-problems, so we lean
on them rather than build anew:

- **Dedup is the DB unique key.** `events` has `UNIQUE (source_id,
  source_event_id)` and `UpsertEvent` does `ON CONFLICT (source_id,
  source_event_id) DO UPDATE ... last_seen_at = NOW(), archived_at = NULL`. We
  make the email producer set `source_event_id` to a deterministic content hash
  (§5). Re-sends collapse onto one row; weekly re-sends also keep the row fresh
  and un-archived for free.
- **The consumer resolves the source by name.** `events.Message.SourceID` is the
  `event_sources.name` string; the consumer calls `GetEventSourceByName`. The
  producer just sets `SourceID = "email_newsletter"`.

## Decisions (chosen during brainstorming)

- **Parser runtime: Node + Mastra Lambda.** Chosen over an all-Go Lambda despite
  adding a second runtime/toolchain to an all-Go repo, to get Mastra's agentic
  framework for future tasks. Costs this design must manage: (a) the event schema
  is duplicated in TS and must not drift from the Go struct — see §4; (b) the
  Lambda gets its own build/deploy lane.
- **Trust model: straight-through (v1).** The Lambda publishes parsed events
  directly to the events-queue, identical to scrapers. No review/staging table.
  Add review later only if quality proves poor.
- **Source identity: single shared `email_newsletter` source.** One
  `event_sources` row; the content-hash dedup therefore spans promoters (the same
  show announced by two promoters collapses to one row). No per-sender source
  provisioning. Original-email attribution is preserved by retaining the raw MIME
  in S3 — not by a per-event column (§9).
- **Lifecycle: exempt the email source from stale-archive.** Email cannot express
  "this show was delisted/cancelled," so the 7-day `ArchiveStaleEvents` sweep is
  meaningless for it and would wrongly archive one-shot announcements. Exempt it;
  past events still drop out of matching/calendar via existing `starts_at >
  NOW()` filters.
- **Inbound filter: accept-all + SES verdict gate.** SES accepts mail to the
  ingest address; the Lambda skips messages failing SES spam/virus checks; non-
  event mail simply parses to zero events (no-op). No sender allowlist in v1.

## Design

### 1. AWS infrastructure (new files under `terraform/prod/`)

Prod is `us-east-1`, one of the three SES inbound-capable regions — no cross-
region workaround needed.

- **`ses.tf`**
  - SES domain identity + DKIM on a dedicated receiving subdomain
    (`inbound.<domain>`) so the apex is undisturbed.
  - Route53 **MX record** for `inbound.<domain>` → the SES inbound endpoint
    (`inbound-smtp.us-east-1.amazonaws.com`).
  - SES receipt rule set + rule: recipient `shows@inbound.<domain>`, action =
    store raw email to the S3 bucket below.
  - ⚠️ **Constraint:** AWS allows only one *active* SES receipt rule set per
    account. Confirm none exists before creating/activating; if one does, add a
    rule to it rather than replace it.
- **`s3.tf` (or extend existing)** — raw-email bucket: private (block all public
  access), bucket policy granting `ses.amazonaws.com` `s3:PutObject`, lifecycle
  rule expiring objects after **90 days**. This bucket is the audit trail.
- **`lambda.tf`** — the parser Lambda packaged as a **container image via ECR**
  (mirrors the existing Docker→ECR app pattern; the Mastra + MIME-parser
  dependency set is awkward under the 250 MB zip limit). Config:
  - Env: `EVENTS_QUEUE_URL`, `AWS_REGION`, model/provider settings.
  - Secret: the model-provider API key from Secrets Manager.
  - **Async invoke config: 2 retry attempts + an SQS dead-letter queue** for
    poison emails. The raw email remains in S3 regardless.
  - S3 `ObjectCreated` → Lambda notification + the `aws_lambda_permission`
    allowing S3 to invoke it.
  - IAM role: read the raw-email bucket, `sqs:SendMessage`/`SendMessageBatch` to
    the events-queue, read the API-key secret, CloudWatch Logs.
- **CI lane** — a buildspec analogous to `ci/buildspec-app.yml` that builds and
  pushes the Lambda image to a new ECR repo; Terraform references the pushed tag.

### 2. The Node + Mastra Lambda (new top-level dir, e.g. `lambda/email-parser/`)

Per-email handler flow:

1. Fetch the raw MIME from S3 (key from the S3 event) and parse it
   (`mailparser`).
2. **Verdict gate** — read the `X-SES-Spam-Verdict` / `X-SES-Virus-Verdict`
   headers SES injects; if either is `FAIL`, return success without parsing (the
   message is effectively dropped).
3. **Date context** — extract the `Date` header and inject it verbatim into the
   prompt: *"This email was received on `<date>`. Use it to resolve the correct
   year for relative dates such as 'this Friday'."*
4. **Text-first / image-fallback** (also the cost guard): if the text body is
   substantive, parse the text. Only if the body is empty/sparse **and** image
   attachments exist do we invoke the multimodal path, passing the flyer
   image(s) to the model.
5. The Mastra agent returns **structured output validated against a Zod schema**
   — an array of event drafts.
6. Map each draft → `events.Message`: `SourceID = "email_newsletter"`,
   `source_event_id` = content hash (§5), plus title/description/starts_at/
   venue/performers/genres/etc.
7. `SendMessageBatch` to `EVENTS_QUEUE_URL` in chunks of ≤10. Locally, the SQS
   client uses the ElasticMQ endpoint override (the same pattern as
   `queue.NewClient`).

The LLM call is the only non-deterministic boundary and is stubbed in tests
(§8).

### 3. Go-side changes (one migration + one query tweak)

- **New migration `0012_email_source.up.sql` / `.down.sql`** (0011 is the current
  highest):
  - `INSERT INTO event_sources (name, adapter_kind, config) VALUES
    ('email_newsletter', 'email_inbound', '{}'::jsonb);`
  - `ALTER TABLE event_sources ADD COLUMN exempt_from_stale_archive BOOLEAN NOT
    NULL DEFAULT false;` then set it `true` for the new row.
- **`sql/queries/events.sql` — `ArchiveStaleEvents`:** add a guard so exempt
  sources are skipped, e.g. `AND source_id NOT IN (SELECT id FROM event_sources
  WHERE exempt_from_stale_archive)`. Regenerate sqlc (`internal/store`).
- **No changes** to `events.Message`, the `ingest` consumer, or the `queue`
  wrapper.

### 4. Schema contract — Go ↔ TS drift prevention

The canonical type is Go's `events.Message`; the Lambda re-declares it as a Zod
schema. To stop silent drift, a directory of **golden JSON fixtures**
(`events.Message` examples: minimal, fully-populated, and edge cases):

- **Go test** unmarshals every fixture into `events.Message` and round-trips it,
  asserting required fields populate and no unknown-field loss.
- **TS test** validates every fixture against the Zod schema.

Changing one schema without the other breaks a fixture on one side. (Codegen
from the Go struct is the heavier alternative; fixtures are the v1 choice.)

### 5. Dedup & content hashing

```
source_event_id = sha256( normHeadliner | normVenue | eventDate(YYYYMMDD) )
```

The consumer never recomputes this hash — it only upserts on `(source_id,
source_event_id)` — so the normalization need only be **deterministic within the
Lambda**, documented explicitly in TS (lowercase, trim, collapse internal
whitespace, strip punctuation) and independent of Go's `events.NormalizeString`.

- Identical re-sends → identical hash → one row (and `last_seen_at` refreshed).
- New show at the bottom → new hash → new row.
- Single shared source → the same show from two promoters dedups too.
- **Idempotency:** S3→Lambda is at-least-once, but deterministic hash + upsert
  makes reprocessing the same email a no-op.

`eventDate` uses day granularity (not time) so "doors 8pm" vs "9pm" variants of
the same show don't split. **Headliner is defined as `Performers[0]`**: the
Mastra agent is instructed to return performers headliner-first. If a draft has
no performers, the hash falls back to the normalized `Title`. This removes any
ambiguity about which artist seeds the hash.

### 6. Error handling

- Unparseable email / handler error → retried (async config), then to the
  Lambda DLQ; raw email retained in S3 for inspection.
- LLM returns zero events (non-event mail, or spam past SES) → no-op success.
- Partial `SendMessageBatch` failure → retry the failed entries; if still
  failing, fail the invocation (safe to retry because of idempotency).
- Malformed bytes on the queue → existing consumer behavior (log + delete).

### 7. Testing

- **Go:** golden-fixture contract test (§4); a migration test asserting
  `ArchiveStaleEvents` leaves `email_newsletter` events untouched while still
  archiving a stale non-exempt event.
- **Lambda unit tests:** `.eml` fixtures — a text newsletter, a flyer-only
  (image, empty body), a multi-show lineup, and a non-event/spam message — run
  through the handler with the **LLM stubbed** (recorded/fake model response: no
  real API calls, deterministic, free in CI). Assert the emitted
  `events.Message` set and the computed hashes (including: re-running the same
  fixture yields identical hashes).
- **Local e2e** (matches the existing ElasticMQ e2e style): a local harness
  pipes a fixture through the handler → ElasticMQ → run the Go consumer → assert
  rows in `appdb_test`.

## Out of scope (v1 — YAGNI)

- Confidence-gated review and any `pending_events` staging table.
- Per-promoter `event_sources` rows and a per-event sender/attribution column —
  the raw MIME in S3 is the audit trail.
- Uploading the flyer as the event's `image_url` (left empty unless the body
  carries an image URL).
- Sender allowlist / SPF-DKIM hard rejection (SES verdict gate only).
- Any Mastra multi-step agentic workflow beyond the single parse agent — the
  framework is in place for when it's wanted.

## Prerequisites (operator, before/at apply)

- SES domain verification (DKIM) on `inbound.<domain>`.
- A model-provider API key seeded into Secrets Manager.
- Confirm no conflicting active SES receipt rule set exists in the account.
