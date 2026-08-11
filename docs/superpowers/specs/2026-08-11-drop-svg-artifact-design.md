# Dropping the SVG poster artifact

**Date:** 2026-08-11
**Status:** Approved (design)

## Problem

The poster pipeline persists two artifacts per poster: a PNG and the
LLM-authored SVG. Only the PNG is wanted. The SVG costs an S3 object, a column,
a presigned URL, and a field in every layer of the contract — and it carries the
one unresolved security finding on this work.

**Finding I5** (from the band-image branch's final review): the SVG is authored
by an LLM from user-controlled `performer`/`venue`/`date`, validated only for
XML well-formedness (`svg-parse.tool.ts` checks `XMLValidator.validate` and the
presence of an `<svg>` root), then stored and served as `image/svg+xml`.
`<script>`, event handlers, `<foreignObject>`, and external `href`s all pass.

## Goal

Remove the SVG as a persisted artifact and from every contract. Keep the PNG.

## Why removal rather than sanitization

An allowlist sanitizer was designed and is hereby **abandoned**. Removing the
artifact is strictly better:

- It eliminates the vulnerability rather than mitigating it. There is no
  sanitizer to keep correct as SVG's attack surface evolves, and no parser whose
  round-trip fidelity has to be trusted not to corrupt the render.
- It removes the coupling a sanitizer would have created between the allowlist
  and the author agent's prompt — the same class of coupling that made the font
  bug (C1) invisible: the agent emits something, the pipeline silently drops it,
  and the poster degrades for no visible reason.
- It is less code, not more.

Sanitization would have been the right answer only if the SVG were wanted.
It is not.

## Scope

The **substituted SVG string remains** as a transient. resvg takes a string, so
it is still how the PNG is produced. `substituteAndValidateSvg` and
`svg-parse.tool.ts` are untouched. What disappears is the *persisted artifact*
and every reference to it — which is precisely the part that was reachable.

### Lambda

| File | Change |
| --- | --- |
| `compose-poster.step.ts` | stop writing `poster-N.svg`; the substituted string goes straight to `rasterizeSvg` and is then dropped |
| `poster.schemas.ts` | `render` becomes `{ png: ArtifactRefSchema }` in both `PosterLoopStateSchema` and `PosterWorkflowOutputSchema`; `authoredSvg` removed |
| `poster-sink.ts` | `put(req, png, provenance)`; `PosterArtifacts` and `find()` drop `svgKey` |
| `poster-schema.ts` | `PosterResult` ok branch drops `svgKey` |
| `poster.ts` | 200 body drops `svgKey` |
| `scripts/invoke-poster-local.ts` | writes `poster.png` only |

`authoredSvg` goes too. It existed only so a bad poster could be inspected in
Studio; with the artifact gone there is no SVG-shaped thing left in state, which
is the cleaner end state.

**S3 keys become `.png` + `.json`.** The provenance sidecar stays — it carries
attribution *and* is the commit marker `find()` keys off. Write ordering becomes
png → json, sidecar still last, and the existing `maxInFlight` assertion that
pins sequential writes still applies unchanged.

### Go API

| File | Change |
| --- | --- |
| `internal/poster/client.go` | `Result` drops `SvgKey`; the 200 decode drops `svgKey` |
| `internal/http/handlers/posters.go` | ready response drops `svgUrl`; only the PNG key is `ValidateKey`d |
| `sql/migrations/0023_drop_poster_svg_key.{up,down}.sql` | drop the column |
| `sql/queries/poster_jobs.sql` | `MarkPosterJobReady` drops `svg_key`; claim's reset list drops it |

**A NEW migration, not an amendment to `0022`.** `0022` has already been applied
to the local dev database (verified: `appdb` is at version 22 with `svg_key`
present). `golang-migrate` tracks only an integer version with no content
checksum, so editing an applied migration leaves that database silently holding
the old schema while reporting itself up to date. `0023` drops the column; its
down migration re-adds it as nullable TEXT so the pair is reversible.

Then `sqlc generate`, committing `internal/store/models.go` alongside the
regenerated `poster_jobs.sql.go`.

### Docs

`README.md` and the two prior specs
(`2026-08-09-file-backed-poster-artifacts-design.md`,
`2026-08-10-poster-proxy-design.md`) describe a `svgKey`/`svgUrl` contract that
ceases to exist. Update the README to the live contract; add a short superseding
note to each prior spec rather than rewriting them — they are the record of what
was decided at the time, and silently editing them would erase the reasoning.

## What this closes and what it does not

**Closes I5 entirely.** No SVG is stored, so nothing is served as
`image/svg+xml`, so there is no stored-XSS vector and no `Content-Disposition`
question. The PNG is inherently safe: resvg ignores scripts, and the output is
raster.

**Does not change** the remaining open item from that review: `performer`,
`venue`, and `date` still have no length bound, and now persist into unbounded
`TEXT` columns via a body with no `MaxBytesReader`.

## Error handling

No new failure modes. Two existing paths simplify:

- `compose-poster.step.ts`'s "could not store the render" catch now covers one
  write instead of two.
- `posters.go` validated both keys before marking a job ready; it now validates
  one. The behavior on a bad key is unchanged — the job is marked `failed`
  rather than reaching `ready` and 500ing every subsequent GET.

## Testing

**Removal is verified by absence, which needs an explicit assertion or it is not
verified at all.** A test that simply stops mentioning `svgKey` proves nothing.

- `compose-poster.step.test.ts` — the run directory contains `poster-N.png` and
  **no** `.svg` file; `render` has no `svg` key; state has no `authoredSvg`.
- `poster-sink.s3.test.ts` — `put` issues exactly **two** `PutObjectCommand`s,
  keyed `.png` then `.json`, and the `maxInFlight` sequential assertion still
  holds; `find` returns `pngKey` and no `svgKey`.
- `poster.test.ts` / `handler.poster.test.ts` — `"svgKey" in body === false`.
- `posters_test.go` — a ready GET returns `pngUrl` and no `svgUrl`.
- `poster_jobs` store — a ready row round-trips without `svg_key`.
- A repo-wide grep for `svgKey|svg_key|SvgKey|svgUrl` returns hits only in the
  historical plan/spec documents, never in code.

## Out of scope

- Input length bounds on `performer`/`venue`/`date`.
- Rendering a CC BY-SA credit line onto the poster.
- Any change to how the PNG is produced, stored, or presigned.
