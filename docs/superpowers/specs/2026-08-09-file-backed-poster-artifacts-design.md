# File-backed poster artifacts, URL-only responses, and repeat-request caching

**Date:** 2026-08-09
**Status:** Approved (design)

## Problem

Large binaries travel through Mastra workflow state as base64 strings. Measured
against the live pipeline for "la luz" (a 251 KB source photo):

| State field | Size |
| --- | --- |
| `ImageLoopState.image.imageBase64` | 335 KB |
| `PosterLoopState.image.imageBase64` | 335 KB (copied by the seed-2 map) |
| `PosterLoopState.svg` | 335 KB+ (the image is inlined as a data URI) |
| `PosterLoopState.pngBase64` | larger still (a 1080x1350 render) |

Every one of these is rendered in Mastra Studio, which makes a trace unreadable
precisely when you are trying to debug a bad poster. They are also copied
between loop states and persisted into the run snapshot.

Two further problems on the same path:

**Redundant response payload.** The `POST /api/poster` 200 body returns
`{ svg, svgUrl, pngUrl }` — the full SVG string **and** a presigned URL pointing
at the same bytes.

**Repeat requests regenerate everything.** `posterKeyBase` is deterministic, but
`processPosterRequest` reruns the whole workflow — 6+ LLM calls — even when the
artifacts already exist in S3. Because presigned URLs expire after 3600s, any
user returning to the same event an hour later pays a full regeneration purely
to get a fresh URL.

## Goal

1. Keep large binaries out of workflow state. Bytes live in files; state carries
   references. They still exist transiently in memory — the vision agent needs
   the image, resvg needs an SVG string — but they are never *stored* in state,
   copied between loops, or persisted into a run snapshot.
2. Return URLs, not bytes, from the HTTP endpoint.
3. Serve an existing poster without rerunning the workflow, with an explicit
   `force` escape hatch for regenerating a result the user dislikes.

## Scope boundary

Confined to `lambda/mastra-handler`. The MusicBrainz and Wikimedia resolvers,
the candidate-ordering rules, the licensing capture, and the artist provenance
work are all untouched — `ImageCredit` and `ArtistMatch` keep their exact
shapes and continue to flow to the API response.

`posterKeyBase` gains a version segment (see component (g)); its
performer/venue/date slugging is otherwise unchanged.

## Existing behavior worth stating

Presigned URL generation **already exists**. `S3PosterSink.put`
(`poster-sink.ts:37-40`) already signs both artifacts with `expiresIn: 3600`,
and `posterHttpResponse` already returns `svgUrl` and `pngUrl`. This design does
not add signing; it removes the redundant `svg` field that ships alongside those
URLs. Expiry stays at 3600s.

Signing stays in `S3PosterSink`, which is already the last stage of the
pipeline and the only component that knows about S3. It is deliberately **not**
moved into the Mastra workflow: doing so would require AWS credentials for every
Studio debug run, write to the real posters bucket on each one, and put a
side-effecting step inside an otherwise pure workflow.

## Components

### a) `src/mastra/tools/artifact-store.ts` (new)

The only module that touches the filesystem.

```ts
ArtifactRefSchema = {
  path: string,          // absolute path on local disk
  contentType: string,
  bytes: number,         // SIZE in bytes, not the content — feeds S3 ContentLength
}

ImageRefSchema = ArtifactRefSchema.extend({
  width, height, sourceUrl?, credit?
})

artifactStore(runId: string, opts?: { root?: string }): {
  dir: string                                        // {tmpdir}/hwh-poster/{runId}
  write(name, data: Buffer, contentType): Promise<ArtifactRef>
  read(ref: ArtifactRef): Promise<Buffer>
}
```

`BandImageSchema` is **removed**, replaced by `ImageRefSchema`:
`imageBase64: string` becomes `path: string` + `bytes: number`. Every other
field — `contentType`, `width`, `height`, `sourceUrl`, `credit` — carries over
unchanged, so the licensing work is unaffected.

`runId` comes from the step's execute params — verified present, and it matches
the id on the run object, so run-scoped directories need no id threaded through
state.

Artifacts are named `band-{attempt}.jpg`, `poster-{attempt}.svg`,
`poster-{attempt}.png`. A Studio trace showing `band-3.jpg` therefore says which
attempt produced it, and one run's artifacts sit together for inspection.

`root` defaults to `path.join(os.tmpdir(), "hwh-poster")` and exists so tests can
point at a scratch directory. It is a parameter, not an env var.

### b) `rasterizeSvg` returns bytes, not base64

`rasterize.tool.ts` currently does `Buffer.from(png).toString("base64")` and the
sink immediately reverses it with `Buffer.from(pngBase64, "base64")`. With a file
destination that round-trip is waste, so `RasterizeResult` carries
`png?: Buffer` instead of `pngBase64?: string`.

The `rasterizeTool` Studio wrapper keeps emitting base64, since a Buffer does not
render usefully there.

### c) Loop state

`ImageLoopStateSchema.image` becomes `ImageRefSchema`. `PosterLoopStateSchema`
changes more:

```ts
image:       ImageRefSchema.optional(),      // was BandImageSchema
authoredSvg: z.string().optional(),          // was `svg`, now the ~2 KB version
render:      z.object({ svg: ArtifactRefSchema, png: ArtifactRefSchema }).optional(),
// `pngBase64` removed
```

`authoredSvg` is kept in state on purpose. It is the SVG *before* substitution,
so it still contains the literal `__BAND_IMAGE__` and is ~2 KB — and it is the
thing you actually want to read in Studio when a poster comes out wrong.

`PosterWorkflowOutputSchema` changes to match, dropping both blob fields:

```ts
{
  ok: boolean,
  render: z.object({ svg: ArtifactRefSchema, png: ArtifactRefSchema }).optional(),
  artifactDir: z.string().optional(),   // so processPosterRequest can clean up
  failureStage: z.enum(["image", "svg"]).optional(),
  reason: z.string().optional(),
  artist: ArtistMatchSchema.optional(),
  credit: ImageCreditSchema.optional(),
}
// `svg` and `pngBase64` removed
```

`artifactDir` is emitted on **both** branches — a failed run still has artifacts
worth deleting, and worth inspecting from Studio.

### d) Data flow

```
judge-band-image (per iteration)
  bytes = download(candidate.url)                     transient
  ref   = store.write(`band-${n}.jpg`, bytes, candidate.contentType)
  vision agent judges bytes                           transient
  state.image = ImageRef

compose-poster (per iteration)
  authored    = svgAuthorAgent(...)                   ~2 KB -> state.authoredSvg
  imgBytes    = store.read(state.image)               transient
  dataUri     = `data:${ct};base64,${imgBytes.toString("base64")}`   transient
  substituted = substituteAndValidateSvg(authored, dataUri)          transient
  svgRef      = store.write(`poster-${n}.svg`, Buffer.from(substituted), "image/svg+xml")
  png         = rasterizeSvg(substituted) -> Buffer
  pngRef      = store.write(`poster-${n}.png`, png, "image/png")
  critique agent judges png Buffer                    transient
  state.render = { svg: svgRef, png: pngRef }

finalize -> { ok, render?, artifactDir, failureStage?, reason?, artist?, credit? }

poster.ts -> sink.put(req, render.svg, render.png) -> { svgUrl, pngUrl }
          -> finally: rm -rf artifactDir
```

`resolve-band-candidates` is unchanged: it already returns metadata only.

### e) Cleanup

`artifactDir` is carried on the workflow output so `processPosterRequest` can
delete it explicitly, rather than inferring it with `dirname()`.

Because only `processPosterRequest` performs cleanup, and Studio invokes the
workflow directly without going through it, **Lambda runs clean up and Studio
runs retain their files** for inspection. The entry point decides; no env var
is involved.

As a backstop against a local dev loop filling `tmpdir`, `artifactStore` sweeps
sibling run directories older than one hour. The sweep runs once per process,
not on every call.

Capacity: a run costs roughly 335 KB + 335 KB + ~1 MB per attempt, so ~6 MB
worst case. Lambda's default 512 MB `/tmp` is ample **given cleanup** — without
it, a warm container would survive about 85 runs. No terraform change is needed,
but this is why cleanup is not optional.

### f) API contract

| | Before | After |
| --- | --- | --- |
| request body | `{ performer, venue, date }` | `{ performer, venue, date, force? }` |
| 200 body | `{ svg, svgUrl, pngUrl, artist?, credit? }` | `{ svgUrl, pngUrl, cached, artist?, credit? }` |
| `PosterResult` (ok) | `{ ok, svg, svgUrl, pngUrl, artist?, credit? }` | `{ ok, svgUrl, pngUrl, cached, artist?, credit? }` |
| `PosterSink.put` | `(req, svg: string, pngBase64: string)` | `(req, svg: ArtifactRef, png: ArtifactRef, provenance)` |
| `PosterSink.find` | — | `(req) => PosterArtifacts \| null` (new) |
| S3 key prefix | `posters/{performer}/…` | `posters/v{N}/{performer}/…` |

The 422 body is unchanged: `{ error, stage, artist? }`.

`S3PosterSink` streams each artifact from disk with
`Body: createReadStream(ref.path)` and `ContentLength: ref.bytes`, so the PNG
never becomes a base64 string in Lambda memory. Supplying `ContentLength` from
the already-known byte count is what lets a stream body work without adding
`@aws-sdk/lib-storage`.

Dropping `svg` from the body is what makes the pipeline fully streaming. An
earlier draft had `processPosterRequest` reading the SVG file back into a string
purely to satisfy the response; with the field gone, that read disappears and
the SVG travels disk -> S3 without ever becoming a JS string again after
rasterization.

The README line documenting `{ svg, svgUrl, pngUrl }` must be updated.

### g) Skipping regeneration for a repeat request

`posterKeyBase` is deterministic, but `processPosterRequest` reruns the entire
workflow — 6+ LLM calls — even when the artifacts already exist in S3. The most
concrete case is not hypothetical traffic: presigned URLs expire after 3600s, so
any user returning to the same event more than an hour later triggers a full
regeneration purely to obtain a fresh URL for bytes that are already stored.

**A provenance sidecar, written last, is the cache index.** A naive existence
check would return `{ svgUrl, pngUrl }` with no `artist` or `credit`, silently
dropping attribution that CC BY-SA requires — a cache hit must be
indistinguishable from a fresh generation. So `put` writes a third small object:

```
posters/v1/{performer}/{venue}-{date}.svg
posters/v1/{performer}/{venue}-{date}.png
posters/v1/{performer}/{venue}-{date}.json   <- { artist?, credit? }, written LAST
```

Write ordering makes the sidecar a commit marker: svg, then png, then json. If
the json exists, the other two are complete, so a partially-written poster is
never served. The lookup is therefore a single `GetObject` on the json — no
separate `HeadObject` needed.

**Key versioning.** `posterKeyBase` gains a `v{POSTER_SCHEMA_VERSION}` segment,
a hand-bumped module constant. Without it the cache freezes output quality
permanently: the key encodes only performer/venue/date, so improving
`svg-author.agent.ts` would never reach any poster that already exists. Bumping
the constant invalidates every cached poster at once. Existing objects under the
old unversioned prefix are orphaned, which is acceptable — there are currently
no callers of `/api/poster` anywhere in the repo.

**Force flag.** Poster generation is LLM-driven and nondeterministic, so a user
who dislikes a result needs a re-roll. `PosterRequestSchema` (which is `.strict()`)
gains `force: z.boolean().optional().default(false)`. It is not part of
`posterKeyBase`, so a forced run overwrites the same keys rather than creating a
parallel copy.

**Sink interface:**

```ts
interface PosterArtifacts { svgUrl, pngUrl, artist?, credit? }

interface PosterSink {
  /** Signed URLs + provenance when a COMPLETE poster exists, else null. */
  find(req: PosterRequest): Promise<PosterArtifacts | null>;
  put(req, svg: ArtifactRef, png: ArtifactRef,
      provenance: { artist?: ArtistMatch; credit?: ImageCredit }): Promise<PosterArtifacts>;
}
```

**Flow:**

```
processPosterRequest(req):
  if (!req.force):
      hit = await sink.find(req)
      if (hit): return { ok: true, ...hit, cached: true }    // no workflow, no LLM calls
  out = await runWorkflow(req)
  ...
  artifacts = await sink.put(req, out.render.svg, out.render.png,
                             { artist: out.artist, credit: out.credit })
  return { ok: true, ...artifacts, cached: false }
```

A cache hit costs one `GetObject` plus two local `getSignedUrl` calls (crypto
only, no network). A miss costs one wasted `GetObject`, ~10-20ms against a run
that takes tens of seconds.

**When `find` throws** — S3 unreachable, permissions — it is treated as a miss
and the workflow runs. Availability beats efficiency; a cache must never be able
to fail a request that would otherwise succeed.

`cached: boolean` is included in the 200 body. It costs nothing and makes it
possible to tell a served-from-cache response from a fresh one without reading
logs.

**Honest scoping:** the value of this depends on repeat requests actually
happening, and the endpoint currently has no callers. The expiry case above
justifies it on its own; the "many viewers of one popular event" case would make
it dramatic but is speculative. If usage turns out to be one-shot per poster,
this is ~15ms of dead weight per request and nothing worse.

## Error handling

Store failures follow the convention already established at
`rasterize.tool.ts:28` — never throw out of a step, convert to state:

| Failure | Behavior |
| --- | --- |
| `store.write` fails in `judge-band-image` | counts as a used attempt, advances `candidateIndex`, reason records the write error |
| `store.read` fails in `compose-poster` | `accepted: false` with a critique naming the missing artifact |
| `store.write` fails in `compose-poster` | `accepted: false` with a critique |
| cleanup `rm` fails | swallowed and logged; must never fail an already-successful request |
| sweep fails | swallowed; it is a backstop, not a correctness requirement |
| `sink.find` throws | treated as a cache miss; the workflow runs. A cache must never fail a request that would otherwise succeed |
| sidecar write fails after svg/png succeeded | the request still succeeds — the caller has working URLs. The poster is simply not cacheable and the next request regenerates it |

## Testing

**`artifact-store.test.ts`** — write/read round-trip; paths land under the
run-scoped directory; two different `runId`s do not collide; the sweep deletes
directories older than one hour and spares fresh ones; sweep errors are
swallowed.

**`judge-band-image.step.test.ts`** — `state.image` carries a `path` and has **no**
`imageBase64` key; the file exists on disk with the expected bytes; a failed
write is recorded as a used attempt rather than thrown.

**`compose-poster.step.test.ts`** — `authoredSvg` is retained and still contains
`__BAND_IMAGE__`; the substituted SVG and PNG are written to disk; `state.render`
carries both refs; a failed `store.read` produces a critique.

**`poster.workflow.test.ts`** — a guard test: serialize the finished run record
and assert no string field exceeds ~10 KB. This is the test that actually pins
the goal; if anyone reintroduces a base64 blob into state, it fails.

**`poster.test.ts` / `handler.poster.test.ts`** — the 200 body has no `svg` key
and does carry `svgUrl` and `pngUrl`; `processPosterRequest` deletes
`artifactDir` after the sink write, including when the sink throws.

**`poster-sink.test.ts`** — `StubPosterSink` records `ArtifactRef`s;
`posterKeyBase` includes the version segment; `find` returns null when the
sidecar is absent and `PosterArtifacts` when present; the sidecar is written
**after** the svg and png.

**Repeat-request tests** (`poster.test.ts`) —
a hit returns without calling `runWorkflow` at all;
a hit carries the same `artist`/`credit` as a fresh generation, from the sidecar;
`force: true` bypasses `find` and reruns the workflow;
`find` throwing falls through to generation rather than failing the request;
a request whose svg and png exist but whose sidecar does not is treated as a
miss (the half-written case);
`cached` is `true` on a hit and `false` on a fresh run.

## Out of scope

- Moving upload or signing into the Mastra workflow.
- Changing the presigned expiry (stays 3600s) or the S3 key layout.
- Any change to candidate resolution, ordering, licensing capture, or artist
  provenance.
- Caching MusicBrainz or Wikimedia lookups. Component (g) caches the finished
  poster, which subsumes the value of caching the individual API calls: a hit
  skips those lookups along with everything else.
- Automatic invalidation. `POSTER_SCHEMA_VERSION` is bumped by hand; nothing
  detects that a prompt changed.
- Deleting orphaned objects under the old unversioned `posters/` prefix.
