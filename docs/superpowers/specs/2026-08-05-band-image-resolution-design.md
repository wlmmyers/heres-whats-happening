# Sourcing band photos from MusicBrainz + Wikimedia

**Date:** 2026-08-05
**Status:** Approved (design)

## Problem

`scrapeBandImage` in `lambda/mastra-handler/src/mastra/tools/web-scrape.tool.ts`
is a stub. It discards its `refinement` argument (`void refinement`) and returns
the same canned 48x48 JPEG on every call.

The poster workflow's first `.dountil` loop is built to retry image acquisition,
feeding each rejection's `reason` back into the next scrape. That plumbing is
correct — `acquire-band-image.step.ts` does pass `inputData.reason` into
`scrapeBandImage`, and the loop does re-execute. But because the stub ignores the
hint and returns identical bytes, every iteration hands the vision agent the same
unusable thumbnail and gets the same rejection. A run burns three vision calls to
arrive at a guaranteed `failureStage: "image"`.

Observed on a real Studio run for "la luz": `attempts: 3`, `accepted: false`,
`reason: "Not a photo of the performer..."`. The loop worked; there was simply
nothing new to look at.

Note the loop itself is **not** defective and needs no repair. This was verified
with an isolated reproduction of the `.map(...).dountil(...)` shape: the step body
executed three times and the condition was evaluated after each. Mastra's run
record keys `steps` by step id, so each iteration overwrites the previous one and
only the final iteration is visible; `steps["acquire-band-image"].metadata.iterationCount`
is the field that reports the true count. That display behavior is what makes a
working loop look like a single execution.

## Goal

Replace the stub with real image sourcing, and in doing so give the retry loop
genuinely different candidates to judge on each attempt.

## Scope boundary

Confined to `lambda/mastra-handler`. The compose/critique loop's logic, the
poster sink, and the S3 artifact layout are untouched.

`PosterLoopStateSchema`, `finalizeStep`, `PosterWorkflowOutputSchema`,
`PosterResult`, and `posterHttpResponse` each gain **additive** fields so that
provenance (which MusicBrainz artist, which Commons image, under what licence)
reaches the API response. No existing field changes shape or meaning.
`BandImageSchema` keeps its existing fields and gains an optional `credit`.

## Upstream API behavior (verified 2026-08-05)

Confirmed against the live services rather than assumed:

- **MB artist search** for `artist:"la luz"` returns 21 matches. The top hit
  (score 100) is `9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a`, "La Luz",
  disambiguation "US rock band", type Group, country US, begun 2012. Lower hits
  include a Japanese salsa band, a Belgian house group, and a chillout act —
  exactly the ambiguity the disambiguation fields exist to resolve.
- **MBID -> QID** works two ways, both returning `Q21485176`: a MusicBrainz
  lookup with `inc=url-rels` (a `wikidata` relation), or a Wikidata search for
  `haswbstatement:P434=<mbid>`.
- **Q21485176 -> P18** = `La Luz performing at Siberia in New Orleans, LA 5 March 2015.jpg`,
  a 1600x1200 live band photo.
- **Q21485176 -> P373** = `La Luz (band)`, a Commons category holding 19 files of
  mixed relevance: some the band performing, several solo shots of individual
  members, several Pop Conference panel photos.
- **Commons `imageinfo`** accepts batched `titles`, returns `mime`, original
  `width`/`height`, and a server-rendered thumbnail via `iiurlwidth`. It returns
  `pages` keyed by pageid **in arbitrary order**, so results must be re-joined by
  title, never by position. `iiurlwidth=1080` downscales (4320x2432 -> 1080x608)
  and never upscales.

## Data flow

```
resolveBandCandidatesStep (once per run)
  searchArtists(performer, limit=3)          [1 MusicBrainz call]
    -> ArtistMatch[] ordered by score
  for each match, up to 3:
    resolveImageCandidates(mbid, artistName) [Wikimedia only, no MB call]
      mbid --[wikidata haswbstatement:P434]--> QID      (skip artist if none)
      QID  --[wbgetclaims P18]-->  canonical file
      QID  --[wbgetclaims P373]--> category --[categorymembers]--> files
      files --[commons imageinfo, batched, iiurlwidth=1080]
              -> url/w/h/mime + descriptionurl + extmetadata (licensing)
    stop at the first artist yielding >= 1 candidate
  -> { artist, candidates[] }   metadata only, NO image bytes

judgeBandImageStep (per loop iteration)
  candidate = candidates[candidateIndex]
  fetchImageBytes(candidate.url) -> BandImage
  imageAnalysisAgent judges it, with artist disambiguation in the prompt
  -> { attempts+1, candidateIndex+1, accepted, reason, image, colors }
```

The resolver returns **metadata only**. Downloading all candidates upfront to
reject most of them would be wasteful; the loop fetches exactly the one candidate
it is about to judge.

Resolution is deterministic for a given performer, which is why it runs once
ahead of the loop rather than inside it. Doing it per-iteration would re-spend
the 1 req/sec MusicBrainz budget recomputing an identical answer.

## Components

### a) `src/mastra/tools/band-image.ts` (new)

Shared shapes, extracted so the tools and the workflow do not import each other.

```ts
BandImageSchema      // moved from web-scrape.tool.ts, gains `credit`
  = { imageBase64, contentType, width, height, sourceUrl?, credit? }

ImageCreditSchema
  = { file, descriptionUrl, artist?, credit?, license?, licenseShortName?,
      licenseUrl?, usageTerms?, attributionRequired: boolean }

ImageCandidateSchema
  = { file, url, width, height, contentType, source: "p18" | "category",
      credit: ImageCreditSchema }

ArtistMatchSchema
  = { mbid, name, score, disambiguation?, type?, country?, beginYear? }
```

### b) `src/mastra/tools/musicbrainz.tool.ts` (new)

Owns all MusicBrainz traffic. Mirrors `internal/musicbrainz/client.go`:
injectable base URL, required User-Agent, in-process ~1 req/sec limiter, 15s
timeout.

```ts
searchArtists(performer: string, opts?: { limit?: number }): Promise<ArtistMatch[]>
```

Escapes `"` and `\` in the Lucene query, as `client.go:81-83` does. Returns `[]`
on zero matches. Retries once after 1s on a 503 (MusicBrainz's rate-limit
response). A `createTool` wrapper is exported for Studio.

### c) `src/mastra/tools/wikimedia.tool.ts` (new)

Owns all Wikidata/Commons traffic. No MusicBrainz call.

```ts
resolveImageCandidates(mbid: string, opts?: { artistName?: string; limit?: number })
  : Promise<ImageCandidate[]>

fetchImageBytes(url: string): Promise<BandImage>
```

MBID -> QID uses Wikidata's reverse `P434` lookup rather than a second
MusicBrainz call. Both routes were verified to work; Wikidata is chosen because
probing up to three artist matches then costs three cheap Wikidata calls instead
of three seconds of MusicBrainz rate-limit budget. It also keeps the module
boundary clean: tool (b) owns MusicBrainz, tool (c) owns Wikimedia.

**Candidate assembly:**

1. P18 file first, `source: "p18"` — Wikidata's canonical choice.
2. Dedupe category files against P18 by normalized title. They do collide: for
   La Luz the P18 file appears in the category verbatim.
3. Drop anything not `image/jpeg` or `image/png`. Commons categories carry PDFs,
   video, and SVG.
4. Sort remaining category files: titles containing the artist name first, then
   alphabetically.
5. Cap the pool at 12 candidates **total, P18 inclusive**, to bound loop-state
   size.

`ImageCandidate.width`/`height` are the **thumbnail** dimensions returned as
`thumbwidth`/`thumbheight`, not the original `width`/`height`, because the
thumbnail URL is what gets fetched and embedded. For the La Luz P18 file that is
1080x810, not 1600x1200. `fetchImageBytes` carries those dimensions and the
candidate URL through into `BandImage.width`/`height`/`sourceUrl`, so the value
`svg-author.agent.ts` receives as `imageWidth`/`imageHeight` describes the bytes
actually embedded.

Rule 4 is not cosmetic. Commons category order is alphabetical, which for La Luz
puts `Jenn Ghetto, Lena Simon & Shana Cleveland - Pop Conference 2015 - 01.jpg`
in slot 1 — a panel-discussion photo. With rule 4 the pool becomes:

```
[0] p18       La Luz performing at Siberia ... 5 March 2015.jpg
[1] category  La Luz performing at Siberia ... 16 August 2015.jpg
[2] category  WideAwake250524 (74 of 209).jpg
```

so attempt 2 gets another live band photo instead of the panel shot.

**Licensing capture.** The same batched `imageinfo` call carries the attribution
data, so it costs no extra request:

```
iiprop=url|size|mime|extmetadata
iiextmetadatafilter=License|LicenseShortName|LicenseUrl|UsageTerms
                   |Artist|Credit|AttributionRequired|Restrictions
```

Verified against the three La Luz candidates — all three returned complete
licensing, and all three had `AttributionRequired: true`:

| file | license | artist |
|---|---|---|
| P18, Siberia 5 March 2015 | CC BY-SA 4.0 | Shark2000br |
| Shana Cleveland.jpg | CC BY-SA 2.0 | Joe Mabel |
| WideAwake250524 (74 of 209) | CC BY 2.0 | Raph_PH |

`extmetadata` values may contain HTML — `Credit` for `Shana Cleveland.jpg` comes
back as `<a rel="nofollow" class="external free" href="...">...</a>`, and `Artist`
is commonly wrapped in `<a>` or `<span>` too. Both fields are run through
`html-to-text` (already a dependency, used by the email path) rather than a
regex.

Fields are optional because public-domain files legitimately lack them;
`attributionRequired` defaults to `false` when the key is absent. `descriptionUrl`
comes from `iiprop=url` as `descriptionurl` and is the human-readable Commons
file page — the durable link for attribution, as opposed to the thumbnail URL.

Thumbnails are requested at `iiurlwidth=1080`, matching the poster canvas width
in `svg-author.agent.ts` (1080x1350). The image is embedded as a base64 data URI
inside the SVG, so anything wider is carried through workflow state and into
resvg for nothing.

### d) `src/mastra/workflows/resolve-band-candidates.step.ts` (new)

Runs once, before the loop. Calls (b), then (c) with bounded fall-through, and
writes `artist` + `candidates` into loop state.

### e) `src/mastra/workflows/judge-band-image.step.ts` (new)

Replaces `acquire-band-image.step.ts`. Fetches bytes for
`candidates[candidateIndex]` and runs `imageAnalysisAgent`. Advances
`candidateIndex` on every iteration regardless of verdict. Short-circuits without
incrementing `attempts` when there is nothing to judge — the same cheap-exit
pattern `compose-poster.step.ts:20` already uses.

Artist disambiguation goes into the prompt, so the agent judges against
"La Luz — US rock band, Group, US, formed 2012" rather than the bare string
`la luz`.

### f) Schema and workflow changes

`ImageLoopStateSchema` gains three fields; nothing is removed:

```ts
artist:         ArtistMatchSchema.optional(),
candidates:     z.array(ImageCandidateSchema).default([]),
candidateIndex: z.number().default(0),
```

`poster.workflow.ts`:

```ts
.map(seed)
.then(resolveBandCandidatesStep)
.dountil(judgeBandImageStep, async ({ inputData }) =>
  inputData.accepted ||
  inputData.attempts >= MAX_IMAGE_ATTEMPTS ||
  inputData.candidateIndex >= inputData.candidates.length)
// unchanged from here: seed2, compose loop, finalize
```

The exhaustion clause is required. Without it, running out of candidates would
spin the step against an empty slot until `MAX_IMAGE_ATTEMPTS`.

`MAX_IMAGE_ATTEMPTS` stays at 3, so the 12-candidate pool bounds reachable
candidates, not vision calls.

### g) Surfacing provenance in the output

Which artist was matched and which image was used must not stay buried in loop
state. This matters most in the fall-through case: if the top MusicBrainz match
has no Wikidata entry, the resolver advances to the next, and for "la luz" that
means a Belgian house group. Without `artist` in the output, the pipeline would
return a confident poster for the wrong band with no signal that it substituted.

Threaded additively along the existing path:

```
ImageLoopState { artist, image.credit }
  -> seed2 .map()      carries artist + credit into PosterLoopState
  -> PosterLoopStateSchema  gains  artist?, credit?
  -> finalizeStep      emits both on BOTH branches
  -> PosterWorkflowOutputSchema  gains  artist?, credit?
  -> processPosterRequest (poster.ts:34)  passes through
  -> PosterResult      gains artist?/credit? on ok, artist? on failure
  -> posterHttpResponse (poster.ts:44)    includes them in the body
```

`finalizeStep` emitting on the **failure** branch is deliberate: a
`failureStage: "image"` result is far more actionable when it says which MB
artist was resolved and which candidates were rejected than when it says only
"no acceptable band image found".

The 200 body gains `artist` and `credit`; the 422 body gains `artist` when one
was resolved. Both are additive, so existing consumers are unaffected.

### h) Attribution is captured, not rendered

This design **returns** licensing data; it does not draw a credit line on the
poster. `svg-author.agent.ts` and the compose loop are unchanged.

Flagged rather than decided, because it is a judgment call outside the scope of
wiring up the tools: every La Luz candidate carries `AttributionRequired: true`,
and two of the three are CC BY-**SA**. Share-Alike is copyleft — a derivative
work incorporating the photo is expected to be licensed on the same terms, and a
generated poster is plainly a derivative. Capturing the metadata is a
precondition for handling that; it is not by itself compliance. Options worth
considering later: render a credit line into the SVG, prefer CC BY / public
domain candidates over CC BY-SA during ordering, or emit attribution alongside
the artifact in the sink.

### i) Deletions

- `web-scrape.tool.ts` and `web-scrape.tool.test.ts` — deleted.
- `acquire-band-image.step.ts` — replaced by (d) and (e).
- `stub-band-image.ts` — **kept**, demoted to a test fixture. `rasterize.tool.test.ts:3`
  uses `STUB_BAND_IMAGE_BASE64` as an embedded-image fixture for SVG
  rasterization, which is unrelated to scraping. It only leaves the production
  path.

## Error handling

Both new steps follow the `rasterizeSvg` convention (`rasterize.tool.ts:28`):
never throw, convert failures into state. This preserves the existing terminal
path — `imageOk: false` reaches `finalizeStep` and surfaces as
`failureStage: "image"` with a real reason instead of a stack trace.

| Failure | Behavior |
|---|---|
| MB search network error / non-2xx | `candidates: []`, reason `"musicbrainz search failed: ..."` |
| MB 503 | one retry after 1s, then as above |
| Zero MB matches | reason `"no MusicBrainz match for '<performer>'"` |
| Artist has no Wikidata QID | skip to next MB match (bounded, 3) |
| QID has no P18 and no/empty P373 | skip to next MB match |
| Wikidata/Commons call errors | that artist yields nothing; fall-through continues; last error retained for the reason |
| All 3 matches exhausted | `candidates: []`, reason names who was tried, with disambiguations |
| Byte fetch fails mid-loop | counts as a used attempt, advances `candidateIndex`, loop continues |
| Vision agent returns no object | existing behavior, plus index advance |

### Rate limiting

A ~1 req/sec in-process limiter applies to MusicBrainz only. Wikimedia imposes no
comparable limit and the Commons calls are batched.

This limiter is **per Lambda container**, not global. Concurrent invocations
behind a shared NAT address could exceed 1 req/sec at MusicBrainz. The
resolve-once structure keeps that exposure small: exactly one MB call per poster
run, plus at most two more on the fall-through path. Stated explicitly so the
limitation is not mistaken for a global guarantee.

### Configuration

User-Agent is the string the Go side already registers with MusicBrainz:
`heres-whats-happening/1.0 ( wlmmyers@gmail.com )`. Both MusicBrainz and
Wikimedia's UA policy require identifying the app and a contact.

Base URLs and `fetch` are constructor-injected (mirroring
`musicbrainz.New(baseURL, userAgent)`), not env vars. Pool cap, thumbnail width,
and fall-through bound are module constants. Only `MAX_IMAGE_ATTEMPTS` and
`MAX_SVG_ATTEMPTS` remain env-driven, as today.

## Testing

Injectable `fetch` means no port binding and no network access in CI.

**`musicbrainz.tool.test.ts`** — parses hints from a recorded payload; sends UA
and Accept headers; escapes `"` and `\` in the query; `[]` on no matches; non-2xx
surfaces as an error; retries once on 503; limiter spaces successive calls >= 1s
under fake timers.

**`wikimedia.tool.test.ts`** — MBID -> QID, and null on empty search; P18 ranked
first with `source: "p18"`; joins imageinfo by title with deliberately shuffled
`pages`; dedupes the P18 file out of the category; filters non-image mimes;
applies the rule-4 ordering; caps at 12; returns `[]` when there is neither P18
nor P373.

Licensing, from a recorded `extmetadata` payload: maps License /
LicenseShortName / LicenseUrl / UsageTerms / Artist / Credit;
`html-to-text`-strips the anchor-wrapped `Credit` from `Shana Cleveland.jpg` down
to the bare URL; sets `attributionRequired` from the flag and defaults it to
`false` when the key is absent; tolerates a public-domain file with no `Artist`
or `License` without dropping the candidate; populates `descriptionUrl` from
`descriptionurl`, not from `thumburl`.

**`resolve-band-candidates.step.test.ts`** — falls through when artist 1 has no
QID; stops at the first artist yielding >= 1 candidate; bounded at 3 artists;
network failure sets a reason and does not throw.

**`judge-band-image.step.test.ts`** — short-circuits without incrementing
`attempts` on an empty pool; advances `candidateIndex` on reject; byte-fetch
failure still counts as an attempt; artist hints reach the agent prompt.

**`poster.workflow.test.ts`** — the regression test for the reported symptom:
three rejections judge **three distinct candidates**, `iterationCount` is 3, and
the terminal output is `failureStage: "image"`. Plus accept-on-attempt-2 exits
the loop with the image carried into compose.

Provenance: `artist` and `credit` survive the seed2 `.map()` into
`PosterLoopState` — the boundary where loop-1 state is rebuilt and fields are
easiest to drop silently. `artist` is present on the `failureStage: "image"`
output, not only on success. And when fall-through substitutes artist 2, the
`artist` in the output is artist 2, not the top match.

**`poster.test.ts`** (existing, extended) — `processPosterRequest` passes
`artist`/`credit` through to `PosterResult`; the 200 body carries both and the
422 body carries `artist`.

**Live integration test**, gated behind an env var and skipped by default, hitting
real MusicBrainz/Wikidata/Commons for "La Luz" and asserting >= 2 candidates with
P18 first. Kept out of CI so the build never depends on external APIs or spends
MusicBrainz budget.

### CI

`ci/buildspec-lambda.yml` runs an explicit list of test files. Deleting
`web-scrape.tool.test.ts` breaks the build unless the list is updated, and any new
test omitted from the list silently never runs. The list must drop
`web-scrape.tool.test.ts` and add the four new unit/step specs plus the workflow
spec.

## Out of scope

- Caching resolved MBIDs or candidate lists. The Go genre resolver caches in
  Postgres; the Lambda has no equivalent store on this path, and poster
  generation is on-demand and low-volume.
- Image search beyond Wikimedia. Performers without a Wikidata entry will fail
  the image stage with a clear reason, which is the existing, working behavior.
- Any change to compose/critique *logic*. `PosterLoopStateSchema` gains two
  optional carrier fields, but `composePosterStep` neither reads nor writes
  them.
- Acting on the licensing data: no credit line rendered into the SVG, no
  licence-aware candidate ordering, no attribution written to the sink. See
  component (h) — the data is captured so those choices become possible, and
  capture alone is not CC BY-SA compliance.
