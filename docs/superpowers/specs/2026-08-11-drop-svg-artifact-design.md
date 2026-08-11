# Dropping the SVG artifact, and bounding poster inputs

**Date:** 2026-08-11
**Status:** Approved (design)

## Problem

This closes BOTH remaining open findings from the band-image branch's final
review. They are handled together because they share a code path — the same
three user-controlled fields feed the SVG the first one is about.

**Finding I5 — unsanitized SVG.** The poster pipeline persists two artifacts: a
PNG and the LLM-authored SVG. Only the PNG is wanted. The SVG is authored from
user-controlled `performer`/`venue`/`date`, validated only for XML
well-formedness (`svg-parse.tool.ts` checks `XMLValidator.validate` and the
presence of an `<svg>` root), then stored and served as `image/svg+xml`.
`<script>`, event handlers, `<foreignObject>`, and external `href`s all pass. It
also costs an S3 object, a column, a presigned URL, and a field in every layer
of the contract.

**Unbounded inputs.** `performer`, `venue`, and `date` have no length bound
anywhere. They reach an LLM prompt, a MusicBrainz query URL, an S3 key, and
unbounded `TEXT` columns, through a request body with no `MaxBytesReader`.

## Goal

Remove the SVG as a persisted artifact and from every contract, keeping the PNG;
and bound the three request fields at every layer that stores or spends on them.

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
| `sql/migrations/0023_poster_jobs_svg_and_bounds.{up,down}.sql` | drop `svg_key`; add the CHECK constraints (see Bounding the request inputs) |
| `sql/queries/poster_jobs.sql` | `MarkPosterJobReady` drops `svg_key`; claim's reset list drops it |

**A NEW migration, not an amendment to `0022`.** `0022` has already been applied
to the local dev database (verified: `appdb` is at version 22 with `svg_key`
present). `golang-migrate` tracks only an integer version with no content
checksum, so editing an applied migration leaves that database silently holding
the old schema while reporting itself up to date. `0023` drops the column AND adds the CHECK constraints described below; its down migration re-adds `svg_key` as nullable TEXT and drops the checks, so the pair is reversible.

Then `sqlc generate`, committing `internal/store/models.go` alongside the
regenerated `poster_jobs.sql.go`.

### Docs

`README.md` and the two prior specs
(`2026-08-09-file-backed-poster-artifacts-design.md`,
`2026-08-10-poster-proxy-design.md`) describe a `svgKey`/`svgUrl` contract that
ceases to exist. Update the README to the live contract; add a short superseding
note to each prior spec rather than rewriting them — they are the record of what
was decided at the time, and silently editing them would erase the reasoning.

## Bounding the request inputs

A multi-KB performer name is accepted today and fails LATE — after the LLM
spend — when S3 rejects a key over its own 1024-byte limit. Bounding it turns a
wasted generation into a clean 400.

### Limits

| Field | Max | Rationale |
| --- | --- | --- |
| `performer` | 200 | "The Presidents of the United States of America" is 44; 200 is far past any real act |
| `venue` | 200 | same shape of value |
| `date` | 100 | free text like "Thursday, August 20" |
| whole request body | 8 KB | those three fields plus `force`, with generous headroom |

Bounds apply to the **trimmed** value, so trailing whitespace cannot consume the
budget. This matters because `poster.JobID` normalizes by trimming and
lowercasing — bounding the raw string would let `"a" + 199 spaces` and `"a"`
differ on acceptance while producing the same job.

### Where they are enforced

**Three places, and the duplication is deliberate:**

1. **`internal/http/handlers/posters.go`** — the real boundary, since the Go API
   is now the only publicly reachable entry point. Rejects with `400`
   `invalid_body` naming the field and the limit, matching the existing
   `httperr.Write` pattern. Applied to **both** `POST` (JSON body) and `GET`
   (query string) — otherwise a 1 MB query string still reaches `JobID`.
   `http.MaxBytesReader` wraps the POST body before decoding.
2. **`lambda/mastra-handler/src/poster-schema.ts`** — `.max()` on the existing
   `.trim().min(1)` chain. The Lambda is IAM-only and unreachable from the
   internet, so this is defense in depth rather than the boundary — but it is
   the component that spends the LLM tokens, so it should refuse work it can see
   is malformed.
3. **`sql/migrations/0023`** — `CHECK` constraints on the three columns, added
   alongside the `svg_key` drop that migration already performs.

**The limits MUST be identical across all three.** If Go accepts something the
Lambda rejects, the request gets a `202` and then fails at generation time
instead of a clean `400` — the failure mode is a silently failed job rather than
a rejected request. A shared constant is not possible across three languages, so
this is enforced by a test in each layer asserting the same numbers, and the
numbers are stated once here as the source of truth.

The `CHECK` constraints mean raising a limit later requires a migration. That is
the intended trade: a storage-level guarantee should not be silently loosened by
an edit to a handler, and nothing can reach those columns except through a
validated handler today — the constraint exists for the second writer that does
not exist yet.

## What this closes

**Closes I5 entirely.** No SVG is stored, so nothing is served as
`image/svg+xml`, so there is no stored-XSS vector and no `Content-Disposition`
question. The PNG is inherently safe: resvg ignores scripts, and the output is
raster.

**Closes the input-bounds item.** All three fields are bounded at the public
edge, again at the component that spends tokens, and structurally in storage.

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

**Input bounds** — each layer asserts the SAME numbers, so a drift between them
fails a test rather than producing silently-failed jobs:

- `posters_test.go` — a 201-char `performer` gets `400` on POST **and** on GET;
  200 chars is accepted; a 9 KB body is rejected by `MaxBytesReader` before
  decoding; the trimmed value is what is measured (200 chars plus surrounding
  whitespace is accepted, 201 is not).
- `poster-schema.test.ts` — the same boundary values against the zod schema.
- Store — inserting a 201-char `performer` violates the `CHECK`, proving the
  constraint is real rather than declared.

## Out of scope

- Rendering a CC BY-SA credit line onto the poster.
- Any change to how the PNG is produced, stored, or presigned.
- Bounding any OTHER user input in the codebase. The three poster fields are
  bounded here because they were the finding; a general audit is separate work.
