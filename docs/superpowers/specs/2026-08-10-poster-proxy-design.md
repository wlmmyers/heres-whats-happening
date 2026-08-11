# Proxying poster generation through the authenticated API

**Date:** 2026-08-10
**Status:** Approved (design)

## Problem

`POST /api/poster` is served publicly by CloudFront with no authentication, no
rate limiting, and no bound on input size. Each call can drive up to nine LLM
requests (3 vision + 3 SVG-author + 3 critique). This is finding **I4** from the
final review of the band-image branch.

The Lambda Function URL itself is *not* the exposure: it is already
`authorization_type = "AWS_IAM"` (`terraform/prod/posters.tf:26-30`) and
invocable only by the CloudFront distribution
(`aws_lambda_permission.allow_cloudfront_invoke_url`). What is public is the
`/api/poster*` cache behavior in front of it.

## Goal

Make poster generation reachable only through the authenticated Go API, which
already has the auth, confirmation, and per-user rate-limiting machinery the
Lambda endpoint lacks.

## Topology

```
BEFORE
  browser ─→ CloudFront (app domain) ─ /api/poster* ─[OAC SigV4]─→ Function URL   PUBLIC, NO AUTH
  browser ─→ api.<domain> ─→ ALB ─→ Go service                                    authenticated

AFTER
  browser ─→ api.<domain> ─→ ALB ─→ Go service ─┬─[SigV4]──→ Function URL   (generate)
                                                └─[presign]─→ S3            (read)
```

Two facts that shape this and were verified rather than assumed:

- **CloudFront has no `/api/*` behavior.** Its only origins are `s3-frontend`
  and `lambda-poster`; the Go API lives on a separate hostname
  (`api.<domain>` → ALB → ECS). The poster path is the only thing CloudFront
  proxies to a backend, so removing it removes the entire public surface.
- **Go routes carry no `/api` prefix.** Vite's dev proxy strips it
  (`web/vite.config.ts`) and production uses the bare `api.<domain>` host. The
  new routes are therefore `/posters`, not `/api/posters`.

**No frontend change is required.** The SPA has zero callers of the poster
endpoint today, so nothing breaks when the CloudFront behavior disappears.

## Why asynchronous

A first generation exceeds the request budget at three layers:

| Layer | Limit |
| --- | --- |
| Lambda function timeout | 300s |
| ALB `idle_timeout` | 60s |
| chi global middleware (`internal/http/server.go:76`) | 30s |

A synchronous proxy would be cut off by the 30s chi timeout before the ALB's
60s even mattered. Raising both to accommodate a job that can legitimately run
for minutes trades one problem for a worse one — long-held connections that a
task restart drops silently.

Asynchronous is also the honest shape for the *failure* modes. "No MusicBrainz
match" and "no Wikimedia image" are ordinary outcomes here, not rare errors. A
design that cannot represent `failed` leaves a client polling forever on an
obscure performer.

## Components

### a) Lambda: return S3 keys, stop signing

The Lambda currently mints presigned URLs. Those expire after 3600s, so anything
that persists them serves dead links. Instead the Lambda returns the **object
keys** and the Go service signs at read time, moments before responding.

200 body becomes:

```
{ svgKey, pngKey, cached, artist?, credit? }    // keys, not URLs
```

`S3PosterSink` drops `getSignedUrl`, and `@aws-sdk/s3-request-presigner` leaves
the Lambda's dependencies entirely. `find()` returns keys on a cache hit.

This also means **Go never re-implements `posterKeyBase`**. The slugging rules —
including the sha256 fallback for names with no ASCII alphanumerics — stay in
one language. Cross-language duplication of that logic was the main risk in the
alternative where Go derived keys itself.

It also retires a mechanism an earlier draft needed: with Go holding the keys, a
ready-poll signs locally and never calls the Lambda at all, so there is no
round-trip on poll and no way for a poll to trigger a regeneration. No
"lookup-only" Lambda mode is required.

### b) Restricting the public surface

Delete from `terraform/prod`:

- the `/api/poster*` `ordered_cache_behavior` (`frontend.tf`)
- the `lambda-poster` origin (`frontend.tf`)
- `aws_cloudfront_origin_access_control.poster_fn` (`frontend.tf`)
- `aws_lambda_permission.allow_cloudfront_invoke_url` (`posters.tf`)

The Function URL keeps `authorization_type = "AWS_IAM"` and ends up with exactly
one caller: the ECS task role.

The comment at `frontend.tf:97-99` explains that CloudFront's distribution-wide
403/404 → `index.html` rewrites also cover `/api/poster*`, which is why the
poster handler emits only 400/422/500. That constraint no longer binds once the
behavior is gone; the note should be updated, but the handler's status codes are
left alone — they are correct on their own terms.

### c) ECS task role IAM

Two statements added in `terraform/prod/iam.tf`:

```hcl
statement {
  sid       = "InvokePosterFunctionUrl"
  actions   = ["lambda:InvokeFunctionUrl"]
  resources = [aws_lambda_function.mastra_handler.arn]
  condition {
    test     = "StringEquals"
    variable = "lambda:FunctionUrlAuthType"
    values   = ["AWS_IAM"]
  }
}
statement {
  sid       = "ReadPosters"
  actions   = ["s3:GetObject"]
  resources = ["${aws_s3_bucket.posters.arn}/*"]
}
```

`s3:GetObject` is required to **sign**, not merely to fetch: a presigned URL
cannot grant more than the signing principal holds. Omitting it is exactly the
bug (C2) that made every URL 403 in the previous branch.

### d) Go: outbound clients

Two new module dependencies; the service touches neither AWS service today:

- `github.com/aws/aws-sdk-go-v2/service/s3` — `NewPresignClient` for read URLs.
- `github.com/aws/aws-sdk-go-v2/aws/signer/v4` — SigV4 for the Function URL
  call (service name `lambda`).

New package `internal/poster`:

```go
type Client interface {
    // Generate SigV4-signs a POST to the Function URL and blocks until the
    // workflow finishes. Callers run this off the request goroutine.
    Generate(ctx context.Context, req Request) (Result, error)
}

type Result struct {
    SvgKey, PngKey string
    Cached         bool
    Artist         *ArtistMatch
    Credit         *ImageCredit
    // Controlled failure (the Lambda's 422), as opposed to an error return.
    FailureStage, FailureReason string
}
```

Signing/presigning lives here; the HTTP handlers stay thin.

Two safeguards, because Go now signs on the Lambda's say-so:

1. **The bucket comes from Go's own config**, never from the response. Only the
   keys are taken from the Lambda, so a buggy or compromised Lambda cannot
   induce Go to sign URLs for an arbitrary bucket.
2. **Keys are validated against the expected `posters/v` prefix** before
   signing, and rejected otherwise.

New config: `POSTER_FUNCTION_URL` and `POSTERS_BUCKET`, both supplied from
terraform in `ecs_api.tf` alongside the existing `API_BASE_URL` /
`ICAL_BASE_URL` entries. `config.Load()` **requires both** — see
[DEPLOYMENT](#deployment), which is not optional reading for this change.

### e) Go: routes

Registered inside the existing authenticated + confirmed group in
`internal/http/server.go`, so they inherit `RequireAuth`, `RequireConfirmed`,
and the 120/min `authedLimiter` net without further wiring.

| Route | Behavior |
| --- | --- |
| `POST /posters` | Claim the job, start generation in the background, `202 {status:"pending"}` |
| `GET /posters?performer=&venue=&date=` | `200` ready · `202` pending · `200` failed + stage/reason · `404` never requested |

Both handlers read the caller from `middleware.UserIDFromContext` and feed it
to `poster.JobID`; jobs are per user (see below). The group guarantees an id is
present, but both check the boolean and answer `401` rather than falling
through to a row keyed on the zero uuid.

A ready response presigns both keys at that moment and returns
`{ status:"ready", svgUrl, pngUrl, artist?, credit? }`.

`POST` gets a dedicated tight limiter — each allowed call can cost nine LLM
requests:

```go
posterCreateLimiter := ratelimit.NewMemory(10, time.Hour)
```

with `EndpointPosterCreate = "poster_create"` added to the constant block in
`internal/http/middleware/ratelimit.go`. Per the convention documented there,
this key is **also mirrored by hand** into the alarm map in
`terraform/prod/observability.tf` — it belongs in the mirrored subset, since an
allowed call costs real LLM spend. `GET` is covered by the group net alone.

### f) `poster_jobs` table

Migration `sql/migrations/0022_poster_jobs.{up,down}.sql`, queries in
`sql/queries/poster_jobs.sql`, then `sqlc generate` — which also regenerates
`internal/store/models.go`, and that file must be committed alongside the new
`*.sql.go`.

```
id              text primary key   -- sha256 hex of the normalized natural key
user_id         uuid not null references users(id) on delete cascade
performer, venue, date  text not null   -- `date` is free text, matching the
                                        -- Lambda's contract ("Thursday, August 20")
status          text not null      -- pending | ready | failed
svg_key, png_key        text       -- keys, never URLs
artist          jsonb              -- provenance, as returned by the Lambda
credit          jsonb
failure_stage, failure_reason  text
created_at, updated_at  timestamptz not null
```

`id` is the sha256 hex digest of the four natural-key fields lower-cased and
trimmed. Each field is hashed to a fixed 32-byte block before the blocks are
concatenated and hashed again, so no byte of one field can be mistaken for a
neighbour's — a separator byte cannot do that job, since any separator can be
smuggled in through the request body. Keying on the natural
`(user, performer, venue, date)` means POST and GET agree with no job id for
the client to track, and a repeat request joins the existing job rather than
starting a second one.

**Jobs are scoped per user**, and `user_id` is part of the key, not just an
owner column. `POST` with `force:true` re-claims a `ready` row and blanks its
artifacts; on a row shared by every user, that lets any confirmed user destroy
any other user's poster and then read the regenerated one as their own. The
accepted trade — deliberate, do not "optimise" it away — is that two users
wanting the same show each generate their own copy.

`ClaimPosterJob` and `GetPosterJob` additionally carry `AND user_id = $n`. That
is redundant while `poster.JobID` is correct, and it is there precisely because
that is a property of one function: a digest regression (one has already
happened — fields used to be joined with a NUL that a JSON body can contain)
must not be sufficient on its own to hand one user another's poster.

`artist` and `credit` **are** persisted, as jsonb. A ready `GET` presigns
locally and never calls the Lambda, so the provenance has to be readable from
the row — and attribution is legally load-bearing (most Commons candidates are
CC BY-SA with `attributionRequired`), so a ready response that omitted it would
be worse than one that duplicates the S3 sidecar. The sidecar remains the system
of record; this row is the API's read copy.

## Correctness details

Three ways this shape classically breaks, each addressed explicitly:

1. **The background goroutine must use `context.Background()`**, not the request
   context. The request context is cancelled the instant the 202 is written, so
   inheriting it would kill generation immediately. The goroutine's HTTP client
   gets a ~310s timeout, just above the Lambda's own 300s cap.
2. **A task restart strands rows in `pending` forever.** `POST` therefore claims
   a job only when the row is absent, `failed`, or `pending` with `updated_at`
   older than 6 minutes (300s Lambda cap plus margin). A stale pending row is
   re-fired rather than becoming a permanent dead end.
3. **Concurrent POSTs for the same poster must fire one Lambda, not two.** The
   claim is a conditional upsert; only the caller whose statement actually
   transitions the row starts generation. The other returns `202` and joins.

## Error handling

| Condition | Result |
| --- | --- |
| Lambda returns 422 (controlled failure) | row `failed` + stage + reason; `GET` returns `200 {status:"failed", …}` |
| Lambda returns 5xx, times out, or is unreachable | row `failed`, reason `"poster service unavailable"`; the upstream detail is logged, not returned |
| SigV4 signing fails (missing credentials) | row `failed`; logged at error — this is a deployment fault, not a user fault |
| Key fails the `posters/v` prefix check | row `failed`; logged at error |
| Presign fails on `GET` | `500` — the row stays `ready`, since the artifacts are fine and the fault is local |
| `GET` for a row that never existed | `404` |

Upstream error text is never echoed to the client. The Lambda already truncates
MusicBrainz bodies at 200 chars, but the API boundary should not depend on the
Lambda's discipline for that.

## Testing

**`internal/poster`** — SigV4 headers are present and name service `lambda`;
keys outside the `posters/v` prefix are rejected before signing; the bucket used
is the configured one and not any value from the response; a 422 body maps to a
controlled `Result` rather than an error; a 5xx maps to an error.

**Handlers** — `POST` returns 202 and claims the row; a second concurrent `POST`
does not start a second generation; `GET` returns 202/200-ready/200-failed/404
across the four states; a ready `GET` presigns fresh URLs on every call
(two calls yield two distinct signatures); `POST` re-fires a `pending` row older
than 6 minutes but not a fresh one.

**The context test that matters** — assert generation still completes after the
request context is cancelled. This is the one that catches the goroutine
inheriting the wrong context, which would otherwise look fine in any test that
does not cancel.

**Store** — round-trip each status transition; the conditional claim returns
"not claimed" when a fresh pending row exists; `artist`/`credit` jsonb survive a
write/read round-trip, so a ready `GET` can return attribution without calling
the Lambda.

**Routing** — a request without a token gets 401, and an unconfirmed user gets
the confirmation gate, proving the routes really are inside the guarded group.

## DEPLOYMENT

**Merging this branch is not sufficient. The feature is dead in production
until a human runs `scripts/taskdef-edit.sh` against the live service.**

`POSTER_FUNCTION_URL` and `POSTERS_BUCKET` are new env vars, and this stack has
no path by which a new env var reaches a running task on its own:

- `aws_ecs_task_definition.api` (and `aws_ecs_task_definition.scheduled`) carry
  `lifecycle { ignore_changes = [container_definitions] }`, and the env vars
  live inside that jsonencoded block — so `terraform apply` will not touch
  them. The entries in `ecs_api.tf` are correct, but they only take effect on a
  from-scratch bootstrap.
- `ci/buildspec-app.yml` registers each new revision from the **current live**
  task definition, swapping only `.containerDefinitions[0].image`. It can
  never introduce a name that is not already live.

So after merging, run — for `hwh-api` **and each scheduled family**, because
`ecs_schedules.tf` sets `scheduled_env_vars = local.api_env_vars` and the
scheduled commands call the same `config.Load()`:

```sh
scripts/taskdef-edit.sh --set-env POSTER_FUNCTION_URL="$(terraform -chdir=terraform/prod output -raw poster_function_url)" \
                        --set-env POSTERS_BUCKET="$(terraform -chdir=terraform/prod output -raw posters_bucket)" \
                        --deploy

for f in hwh-scrape-events-ticketmaster hwh-scrape-spotify hwh-match; do
  scripts/taskdef-edit.sh --family "$f" \
    --set-env POSTER_FUNCTION_URL=... --set-env POSTERS_BUCKET=...
done
```

(The scheduled families take no `--deploy`; they pick up `:LATEST` on their
next firing.)

`config.Load()` returns `poster generation requires POSTER_FUNCTION_URL,
POSTERS_BUCKET` when either is empty, so a task that missed this step
crash-loops and fails the ECS rolling deploy with the previous revision still
serving. That is deliberate: an empty `POSTER_FUNCTION_URL` is otherwise
**silent** — `POST /posters` still returns `202`, and the background goroutine
fails with `unsupported protocol scheme ""`, so every `GET` reports
`{"status":"failed"}` with no 5xx and no alarm anywhere.

## Out of scope

- Frontend work. The SPA has no poster UI today.
- Rendering a CC BY-SA credit line onto the poster. Still open from the previous
  spec.
- Sanitizing the LLM-authored SVG before it is served as `image/svg+xml`
  (finding **I5**). This design narrows *who* can reach generation, which
  reduces exposure, but the stored-XSS question is untouched and still needs its
  own decision.
- Bounding input length on `performer`/`venue`/`date`. Worth doing, but it is a
  validation change to `PosterRequestSchema` and this spec's own request
  validation, not part of the proxy.
