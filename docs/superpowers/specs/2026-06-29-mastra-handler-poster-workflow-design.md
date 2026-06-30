# Bifurcate the email-parser Lambda into a generic `mastra-handler` that generates concert posters

**Date:** 2026-06-29
**Status:** Approved, ready for implementation plan

## Problem

The `email-parser` Lambda (`lambda/email-parser/`) today does exactly one thing:
an S3 `ObjectCreated` event for a raw email triggers `processEmail`, which parses
the email and emits events onto the events-queue. We want the same Lambda to grow
a **second, unrelated capability**: a synchronous HTTP endpoint that takes a
performer/venue/date and runs a Mastra **agentic workflow** to produce a concert
**poster as SVG**.

Because the Lambda is no longer email-specific, it is renamed `mastra-handler`
(see §7). The handler **bifurcates** on event shape: S3 events keep the existing
email path; Lambda Function URL requests take the new poster path.

The poster workflow is an ordered, multi-stage agentic pipeline with bounded
"iterate until acceptable" retry loops:

1. **Acquire a band image** — web-scrape a candidate (stubbed for now), then a
   vision agent validates it's actually a usable photo of the performer; retry
   until acceptable or capped.
2. **Compose the poster** — an agent authors an SVG embedding the band photo
   (as an inline base64 JPEG), the venue, the date, and a snazzy background
   pattern in colors drawn from the photo.
3. **Validate the poster** — substitute + parse the SVG, rasterize it to PNG,
   then a vision agent judges whether the *rendered* poster is cool and legible;
   on any failure (unparseable, won't render, not cool) regenerate, capped.

The output is a valid SVG poster, returned in the HTTP response and persisted to
S3 alongside its rendered PNG.

## Decisions (chosen during brainstorming)

- **Orchestration: the Mastra Workflow primitive**, not a single autonomous
  agent loop. `createWorkflow`/`createStep` with `.then()`/`.map()`/`.dountil()`
  gives deterministic ordering, bounded retries, Studio graph-view observability,
  and unit-testable steps — matching the "ordered / iterate-until" spec exactly.
- **Rasterization is real**, via `@resvg/resvg-wasm` (pure WASM). The
  "is this a cool poster?" check feeds a *rendered PNG* to a vision model, so we
  must actually rasterize. WASM behaves identically in Studio on macOS and in the
  Node 24 Lambda container — no native-binary matching.
- **Web scraping is stubbed (v1).** The scrape tool returns a canned band image
  behind a clearly-marked real-API seam. Everything downstream is real.
- **Transport: Lambda Function URL behind CloudFront**, not API Gateway. The
  workflow makes several LLM/vision calls plus retries plus rasterization and can
  exceed API Gateway's hard 29s integration timeout. A Function URL with
  `InvokeMode = RESPONSE_STREAM` (handler wrapped in `awslambda.streamifyResponse`)
  runs up to the Lambda timeout and streams its response, sidestepping both the
  29s cap and CloudFront's buffered-origin read timeout.
- **Output sink: S3 + HTTP response.** The handler writes `svg` + `png` to a
  posters bucket and returns the SVG (plus the object URLs) in the response so the
  frontend can render it.
- **Same Lambda function, branching handler** ("bifurcate the handler"), renamed
  `mastra-handler`, rather than a separate `poster-generator` function. Trade-off:
  two concerns share one function/role/config. Splitting later is straightforward.
- **Full rename including live AWS resource names** (`hwh-email-parser` →
  `hwh-mastra-handler`). On apply (run by the operator, not in this work) the
  Lambda, ECR repo, IAM role, DLQ, alarm, and LLM-key secret are **replaced**;
  §7 documents the bootstrap-first ordering. Email-domain resources (inbound
  bucket, SES, events queue) keep their names.
- **The four "iterate until" conditions collapse into one bounded compose/validate
  loop**, not nested loops. Fixing "unparseable", "won't render", or "not cool"
  all require regenerating the SVG, so they are one loop with failure-specific
  feedback. Mastra loop primitives also operate on single steps, making true
  nesting awkward.

## 1. Architecture & request flow

One Lambda, two entrypaths. `handler.ts` keeps `processEmail` untouched and
branches on event shape:

- `event.Records?.[0]?.eventSource === "aws:s3"` → existing email path.
- Otherwise (Lambda Function URL request, payload format v2.0) → poster path.

Public topology:

```
Browser → CloudFront (existing frontend distribution)
        → ordered cache behavior "/api/poster*"
        → Lambda Function URL (AuthType=AWS_IAM, InvokeMode=RESPONSE_STREAM)
        → mastra-handler Lambda → processPosterRequest → posterWorkflow
                                                        → S3 (svg + png) + HTTP body
```

The endpoint is added as a **new origin + ordered cache behavior on the existing
frontend CloudFront distribution**, so it shares the domain/cert and there is **no
CORS**. The Function URL is locked to `AWS_IAM` and reachable only via
CloudFront's OAC-signed (SigV4) requests.

Side effects (S3 writes) live in the handler's injected `PosterDeps`, mirroring how
`emit`/`sendBatch` is kept out of the agent today. The workflow stays pure and
Studio-observable.

### Request / response contract

```
POST /api/poster
{ "performer": "Khruangbin", "venue": "The Fillmore", "date": "2026-08-15" }

200 → { "svg": "<svg…>", "svgUrl": "https://…", "pngUrl": "https://…" }
400 → { "error": "invalid request: <detail>" }            // bad/missing body fields
422 → { "error": "<reason>", "stage": "image" | "svg" }   // loop exhausted, no acceptable result
500 → { "error": "internal error" }                       // sink failure / unexpected
```

The body is validated with a Zod `PosterRequestSchema`. `date` is accepted as a
free string (passed to the agent verbatim); we do not constrain its format in v1.

The handler emits **only 400/422/500 — never 403/404.** This is a hard constraint:
the shared CloudFront distribution rewrites 403/404 to `index.html` (see the §8
coupling caveat), so those codes would be masked on the `/api/poster*` path.

### Timeout & streaming

- Lambda `timeout = 300`, `memory_size = 1536`.
- Handler wrapped in `awslambda.streamifyResponse`. For v1 it performs a single
  final stream write of the JSON body via
  `awslambda.HttpResponseStream.from(responseStream, { statusCode, headers })`.
  SSE progress events are a noted future enhancement, not in scope.
- `awslambda` is a runtime-provided global absent from `@types/aws-lambda`; a
  small ambient `.d.ts` declares `streamifyResponse` and `HttpResponseStream`.

## 2. The Mastra workflow

`posterWorkflow` is registered in `mastra/index.ts`:
`inputSchema { performer, venue, date }` → `outputSchema { svg: string, pngBase64: string }`.
Binary payloads (the band photo, the rendered PNG) are carried through step I/O as
**base64 strings**, never raw `Uint8Array` — Zod step schemas are validated and
Studio generates input forms from them, so JSON-serializable values are required.
Steps decode to `Buffer` only at the point of use (rasterize, multimodal agent
calls); the handler's sink decodes `pngBase64` and writes it to S3.

Both loops use the documented `.dountil` idiom where the **loop body step's
`inputSchema === outputSchema`**, seeded by a preceding `.map()`, so each
iteration's output feeds back as the next iteration's input — carrying feedback
(rejection reason / critique) forward.

### Loop 1 — Acquire band image (`MAX_IMAGE_ATTEMPTS = 3`)

`.map()` seeds `{ performer, venue, date, attempts: 0, accepted: false }`. The
loop-body step:

1. `webScrapeTool` (**stubbed**) → `{ imageBase64, contentType, width, height, sourceUrl }`,
   refining its query with the prior `reason` when retrying.
2. `imageAnalysisAgent` (vision) → `{ acceptable, reason, dominantColors: string[] }`
   (hex colors) judging "is this actually a photo of *performer*, usable for a poster?"
3. Returns the same-shaped object with `attempts + 1`, `accepted`, `reason`, and
   the carried `image` (`{ imageBase64, contentType, width, height }`) + `colors`.

`.dountil(step, ({ inputData }) => inputData.accepted || inputData.attempts >= MAX_IMAGE_ATTEMPTS)`.
On exhaustion without acceptance the workflow result is a failure carrying the
last `reason` → handler returns 422 `stage: "image"`.

`.map()` → `{ performer, venue, date, image, colors }` for loop 2.

### Loop 2 — Compose & validate poster (`MAX_SVG_ATTEMPTS = 3`)

`.map()` seeds `{ …, attempts: 0, accepted: false }`. The loop-body step:

1. `svgAuthorAgent` → SVG source embedding venue + date + a snazzy background
   pattern using `colors`, and an `<image href="__BAND_IMAGE__" …>` **placeholder**.
   The base64 JPEG is **not** sent through the LLM (kept cheap and reliable); the
   agent is given the image's width/height so it can lay out the `<image>` box.
   It also receives the prior `critique` when regenerating.
2. `svgParseTool` (deterministic) → substitute the real
   `data:image/jpeg;base64,…` URI for `__BAND_IMAGE__`, then validate the SVG is
   well-formed/parseable. Parse failure → `accepted = false`, feedback = the parse
   error. *(iterate-until-parseable)*
3. `rasterizeTool` (`@resvg/resvg-wasm`) → render SVG → PNG bytes. A render error →
   `accepted = false`, feedback = the render error. *(iterate-until-renders)*
4. `posterCritiqueAgent` (vision) → judge the **rendered PNG**: cool, legible,
   shows image + venue + date → `{ acceptable, critique }`. *(iterate-until-cool)*
5. Returns same-shaped object with `attempts + 1`,
   `accepted = parseOk && renderOk && critiqueOk`, the failing check's message as
   `critique`, plus `svg` and `pngBase64`.

`.dountil(step, ({ inputData }) => inputData.accepted || inputData.attempts >= MAX_SVG_ATTEMPTS)`.
On exhaustion → failure carrying the last `critique` → handler returns 422
`stage: "svg"`.

Final step returns `{ svg, pngBase64 }`.

### Agents

Three new agents, each reusing the configured model (`LLM_MODEL`, default
`anthropic/claude-sonnet-4-5`, multimodal), constructed like the existing
`emailExtractorAgent` (router-string model; `ANTHROPIC_API_KEY` read at
generate() time):

- `imageAnalysisAgent` — vision; structured output `{ acceptable, reason, dominantColors }`.
- `svgAuthorAgent` — text; returns SVG source (structured `{ svg }`).
- `posterCritiqueAgent` — vision; structured output `{ acceptable, critique }`.

Multimodal calls pass a `content` array `[{ type: "image", image: bytes, mimeType }, { type: "text", text }]`.

## 3. Tools

- `web-scrape.tool.ts` — **stub.** Given a performer (+ optional refinement),
  returns a canned band image (bytes + contentType + dims). A clearly-marked seam
  (`// TODO: real image search/scrape API`) isolates the future real call.
- `svg-parse.tool.ts` — deterministic. Substitutes the `__BAND_IMAGE__`
  placeholder with the JPEG data URI; validates well-formed/parseable SVG; returns
  `{ ok, svg, error? }`.
- `rasterize.tool.ts` — `@resvg/resvg-wasm`. Lazy `initWasm(...)` behind a
  module-level flag (once per cold start). `new Resvg(svg).render().asPng()`.
  Returns `{ ok, pngBase64, width, height, error? }`.

## 4. New dependencies

- `@resvg/resvg-wasm` (runtime) — rasterizer. WASM asset resolved at runtime via
  `require.resolve('@resvg/resvg-wasm/index_bg.wasm')` and passed to `initWasm`.

**Risk to validate during implementation:** resvg must render an **embedded
base64 JPEG** `<image>` element. The rasterize unit test (§6) asserts this works;
if resvg-wasm cannot, fall back to `@resvg/resvg-js` (native, prebuilt per-platform
binaries — works in the AL2023 container and on macOS via the darwin binary).

## 5. File layout

Under `lambda/mastra-handler/src/` (mirroring the existing pure-core +
injected-deps split):

- `handler.ts` — branch S3 vs Function URL; `processEmail` kept; streaming-wrapped
  `handler` export.
- `poster.ts` — `PosterRequestSchema`, `PosterResult`, `processPosterRequest(input, deps)`,
  `PosterDeps { runWorkflow, sink }`.
- `poster-sink.ts` — `PosterSink` interface + `S3PosterSink` (writes svg/png to S3,
  returns URLs) + `StubPosterSink` (records calls; mirrors `sqs.ts`'s test double).
- `mastra/index.ts` — register `posterWorkflow` + the three new agents.
- `mastra/workflows/poster.workflow.ts`
- `mastra/agents/{image-analysis,svg-author,poster-critique}.agent.ts`
- `mastra/tools/{web-scrape,svg-parse,rasterize}.tool.ts`
- `awslambda.d.ts` — ambient declaration for the streaming global.
- `scripts/invoke-poster-local.ts` — run the workflow with a performer/venue/date
  and write SVG + PNG to disk (mirrors `invoke-local.ts`).

## 6. Testing

TDD on the deterministic pieces; the LLM agents + full quality loop are exercised
via Studio (`pnpm dev`) and the invoke-local harness (not unit-tested — they need
`ANTHROPIC_API_KEY`), consistent with the existing `MastraExtractor` precedent.

Unit tests:

- `rasterize.tool.test.ts` — render a minimal SVG → assert PNG magic bytes
  (`\x89PNG`) + nonzero dims; **and** an SVG embedding a tiny base64 JPEG.
- `svg-parse.tool.test.ts` — placeholder substitution works; valid SVG passes;
  malformed SVG is rejected with a message.
- `web-scrape.tool.test.ts` — stub returns image bytes + contentType for a performer.
- `poster.test.ts` — `processPosterRequest` with a stub `runWorkflow` (canned
  svg/png) + `StubPosterSink`: asserts the sink is called and a 200 body shape;
  failed workflow → 422; invalid body → 400.
- `handler` routing test — an S3-shaped event takes the email path; a
  Function-URL-shaped event takes the poster path.

CI (`ci/buildspec-lambda.yml`) gains the new test files in its `vitest run` list.

## 7. The rename: `email-parser` → `mastra-handler`

Full rename including live AWS resource names. Scope is the Lambda and its
build/deploy/owned resources; genuinely email-domain resources keep their names.

**Mechanical (code/repo):**
- `lambda/email-parser/` → `lambda/mastra-handler/` (directory).
- `package.json` `name: "mastra-handler"`; `README.md`.
- `ci/buildspec-lambda.yml` — `cd lambda/mastra-handler` paths, comments, and the
  vitest file list.
- Terraform resource labels: `aws_lambda_function.email_parser` → `.mastra_handler`,
  etc. `lambda_email.tf` → `lambda_mastra_handler.tf`.

**Live AWS resource names (replaced on apply):**
- ECR repo `hwh-email-parser` → `hwh-mastra-handler` (bootstrap `ecr.tf`,
  `data.tf` lookup, `codebuild.tf` env `LAMBDA_ECR_REPO`/`FUNCTION_NAME`, `iam.tf`
  CodeBuild role ARNs, `outputs.tf`).
- Lambda function, IAM role, DLQ, DLQ-depth alarm → `-mastra-handler`.
- Secret `hwh/email-llm-api-key` → `hwh/mastra-handler-llm-api-key` (and the
  `outputs.tf` operator steps).

**Unchanged (email-domain):** `inbound-email` bucket, SES receipt rules, `events`
SQS queue.

**Apply ordering (operator-run; this work does not run `terraform apply`):**
1. Apply bootstrap stack → creates the new `hwh-mastra-handler` ECR repo.
2. Run the lambda CI lane (or manual build/push) → push an image to the new repo.
3. Re-seed the renamed secret `hwh/mastra-handler-llm-api-key`.
4. Apply prod stack → recreates the Lambda/role/DLQ/alarm under the new name,
   re-wires the S3 notification, and creates the Function URL + CloudFront wiring.

## 8. New infrastructure (Terraform)

In the renamed `lambda_mastra_handler.tf` (or a sibling `poster.tf`):

- `aws_s3_bucket.posters` (`hwh-posters-<account_id>`) + public-access block + SSE,
  matching the `frontend` bucket pattern. Optional lifecycle expiry for old posters.
- IAM: add an `s3:PutObject` statement to the Lambda role for the posters bucket.
- `aws_lambda_function_url.mastra_handler` — `authorization_type = "AWS_IAM"`,
  `invoke_mode = "RESPONSE_STREAM"`.
- `aws_lambda_permission` — allow the CloudFront service principal to
  `lambda:InvokeFunctionUrl`, scoped to the distribution ARN.
- Lambda env additions: `POSTERS_BUCKET`, `MAX_IMAGE_ATTEMPTS`, `MAX_SVG_ATTEMPTS`.
  Bump `timeout` 120 → 300, `memory_size` 1024 → 1536.

In `frontend.tf` (extend the existing distribution):

- A 2nd `origin` = the Function URL domain (`custom_origin_config`, HTTPS-only)
  with a **lambda** `aws_cloudfront_origin_access_control` (`origin_access_control_origin_type = "lambda"`, sigv4, always).
- An `ordered_cache_behavior` for `/api/poster*`: `allowed_methods` incl. `POST`,
  managed **`CachingDisabled`** cache policy + managed **`AllViewerExceptHostHeader`**
  origin-request policy (so SigV4 signs against the Function URL host, not the
  viewer host — required for OAC to a Function URL), `viewer_protocol_policy = redirect-to-https`.

> **Coupling caveat — SPA error-rewrite leaks into the API path.** The frontend
> distribution's `custom_error_response` rules map **403 and 404 → `/index.html`
> with `200`** (for SPA client-side routing). CloudFront custom error responses are
> **distribution-wide and cannot be scoped per cache behavior**, so they also apply
> to `/api/poster*`. Consequence: a `403`/`404` from the Function URL origin would
> be silently rewritten to a `200` serving `index.html`, masking the real error.
> Mitigations (chosen, since per-behavior scoping isn't available on a shared
> distribution): (a) the poster handler emits **only 400/422/500** — never 403/404
> (see §1); and (b) OAC must be configured correctly so the Function URL never
> returns auth `403`s in normal operation. If endpoint error fidelity (truthful
> 403/404) later becomes important, split the API onto a dedicated distribution.

## Out of scope (v1)

- Real web-scraping / image-search API (stubbed).
- SSE progress streaming to the browser (single final write for now).
- Authn/rate-limiting on the endpoint beyond CloudFront/OAC.
- Frontend wiring (the user wires the client later).
- Caching posters / dedup of identical requests.
