# mastra-handler Poster-Generation Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bifurcate the `email-parser` Lambda into a generic `mastra-handler` that — in addition to the existing S3→email path — serves a Lambda Function URL (behind CloudFront) running a Mastra agentic workflow that turns `{performer, venue, date}` into a validated concert-poster SVG, persisted to S3 and returned in the HTTP response.

**Architecture:** One Lambda, one streaming entrypoint that branches on event shape (S3 event → email path; Function URL request → poster path). The poster path runs a registered Mastra `Workflow` with two bounded `.dountil` retry loops — acquire-band-image (web-scrape stub + vision validation) and compose-poster (SVG author + parse + rasterize + vision critique). Deterministic tools (scrape stub, SVG parse, rasterize) are unit-tested; LLM agents + the full loop are exercised via Studio and a local invoke harness, per the existing `MastraExtractor` precedent.

**Tech Stack:** TypeScript (ESM, Node 24), `@mastra/core` 1.37 (`createWorkflow`/`createStep`/`createTool`/`Agent`), `@resvg/resvg-wasm` (rasterization), `fast-xml-parser` (SVG well-formedness), `@aws-sdk/client-s3` + `@aws-sdk/s3-request-presigner` (output sink), `vitest`, Terraform (operator-applied).

## Global Constraints

Every task's requirements implicitly include this section.

- **Branch:** all commits land on `mastra-handler-poster-workflow` (already created). Subagents: verify `git rev-parse --abbrev-ref HEAD` is that branch (not detached HEAD) before committing.
- **ESM imports use `.js` extensions** in TypeScript source (e.g. `import { x } from "./poster-schema.js"`). The project is `"type": "module"`; omitting the extension breaks the runtime.
- **Model:** router string from `process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5"`. `ANTHROPIC_API_KEY` is read at `generate()` time, not construction. Verify Mastra APIs against `node_modules/@mastra/core/dist/docs/references/` — training knowledge is stale.
- **Binary payloads (images, PNG) cross step/tool boundaries as base64 strings**, never raw `Uint8Array` (Zod step schemas are validated and Studio renders forms from them). Decode to `Buffer` only at the point of use.
- **HTTP errors are 400/422/500 ONLY — never 403/404.** The shared CloudFront distribution rewrites 403/404 to `index.html`, which would mask them on `/api/poster*`.
- **Do NOT run `terraform plan`/`apply`/`init`** in this environment (machine is logged into company AWS). Terraform tasks verify with `terraform fmt` only; plan/apply are operator steps.
- **Retry caps:** `MAX_IMAGE_ATTEMPTS = 3`, `MAX_SVG_ATTEMPTS = 3` (read from env with these defaults).
- **SVG image placeholder token:** the SVG author emits `__BAND_IMAGE__` as the `<image href>`; the parse tool substitutes the real `data:` URI. Keeps the base64 out of the LLM round-trip.

---

### Task 1: Rename `email-parser` → `mastra-handler` (code/repo only)

Pure rename of the directory, package, and CI paths. Terraform rename + new infra is Task 12.

**Files:**
- Rename: `lambda/email-parser/` → `lambda/mastra-handler/` (whole directory, via `git mv`)
- Modify: `lambda/mastra-handler/package.json` (the `name` field)
- Modify: `ci/buildspec-lambda.yml` (the two `cd lambda/email-parser` lines + the leading comment)
- Modify: `README.md` (any `lambda/email-parser` path references)

- [ ] **Step 1: Move the directory**

```bash
cd /Users/wmyers/data/heres-whats-happening
git mv lambda/email-parser lambda/mastra-handler
```

- [ ] **Step 2: Rename the package**

In `lambda/mastra-handler/package.json` change:
```json
  "name": "email-parser",
```
to:
```json
  "name": "mastra-handler",
```

- [ ] **Step 3: Update the CI buildspec paths**

In `ci/buildspec-lambda.yml`, change both occurrences of `lambda/email-parser` to `lambda/mastra-handler`:
- `- cd lambda/email-parser` → `- cd lambda/mastra-handler`
- `docker build -t "${IMAGE_URI}" lambda/email-parser` → `... lambda/mastra-handler`

And update the leading comment's first line from `Builds the email-parser Lambda container image ...` to `Builds the mastra-handler Lambda container image ...`. (The `FUNCTION_NAME`/`LAMBDA_ECR_REPO` env values are injected by CodeBuild from Terraform — they are handled in Task 12, not here.)

- [ ] **Step 4: Update README path references**

In `README.md`, replace any `lambda/email-parser` with `lambda/mastra-handler`. (Leave historical doc files under `docs/superpowers/` unchanged — they are dated records.)

- [ ] **Step 5: Verify the existing build/tests still pass under the new path**

```bash
cd lambda/mastra-handler
pnpm install --frozen-lockfile
pnpm typecheck
pnpm vitest run src/schema.test.ts src/hash.test.ts src/map.test.ts src/email.test.ts src/handler.test.ts
```
Expected: typecheck clean; all listed test files PASS. (`sqs.test.ts` / `handler.e2e.test.ts` need ElasticMQ — skip them here, same as CI.)

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename email-parser lambda dir/package to mastra-handler"
```

---

### Task 2: Add dependencies, ambient streaming types, and the request schema

**Files:**
- Modify: `lambda/mastra-handler/package.json` (deps)
- Create: `lambda/mastra-handler/src/awslambda.d.ts`
- Create: `lambda/mastra-handler/src/poster-schema.ts`
- Test: `lambda/mastra-handler/src/poster-schema.test.ts`

**Interfaces:**
- Produces: `PosterRequestSchema` (Zod), `PosterRequest` type `{ performer: string; venue: string; date: string }`, and `PosterResult` type (discriminated union, below). Consumed by Tasks 6, 9, 10.

- [ ] **Step 1: Install runtime dependencies**

```bash
cd lambda/mastra-handler
pnpm add @resvg/resvg-wasm fast-xml-parser @aws-sdk/s3-request-presigner
```
Expected: all three added under `dependencies` in `package.json`.

- [ ] **Step 2: Write the failing schema test**

Create `src/poster-schema.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import { PosterRequestSchema } from "./poster-schema.js";

describe("PosterRequestSchema", () => {
  it("accepts a complete request", () => {
    const r = PosterRequestSchema.safeParse({ performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15" });
    expect(r.success).toBe(true);
  });

  it("rejects missing performer", () => {
    const r = PosterRequestSchema.safeParse({ venue: "The Fillmore", date: "2026-08-15" });
    expect(r.success).toBe(false);
  });

  it("rejects an empty venue", () => {
    const r = PosterRequestSchema.safeParse({ performer: "X", venue: "  ", date: "2026-08-15" });
    expect(r.success).toBe(false);
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `pnpm vitest run src/poster-schema.test.ts`
Expected: FAIL — cannot resolve `./poster-schema.js`.

- [ ] **Step 4: Write the schema**

Create `src/poster-schema.ts`:
```typescript
import { z } from "zod";

export const PosterRequestSchema = z
  .object({
    performer: z.string().trim().min(1, "performer is required"),
    venue: z.string().trim().min(1, "venue is required"),
    date: z.string().trim().min(1, "date is required"),
  })
  .strict();
export type PosterRequest = z.infer<typeof PosterRequestSchema>;

/** Result of the poster pipeline, mapped to HTTP by the handler. */
export type PosterResult =
  | { ok: true; svg: string; svgUrl: string; pngUrl: string }
  | { ok: false; stage: "image" | "svg"; reason: string };
```

- [ ] **Step 5: Write the ambient streaming declaration**

Create `src/awslambda.d.ts` (the Lambda runtime provides `awslambda` as a global; it is not in `@types/aws-lambda`):
```typescript
import type { Writable } from "node:stream";

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace awslambda {
    interface ResponseStream extends Writable {
      setContentType(contentType: string): void;
    }
    interface HttpResponseStreamMeta {
      statusCode?: number;
      headers?: Record<string, string>;
    }
    const HttpResponseStream: {
      from(stream: ResponseStream, meta: HttpResponseStreamMeta): ResponseStream;
    };
    function streamifyResponse<E = unknown>(
      handler: (event: E, responseStream: ResponseStream, context: unknown) => Promise<void>,
    ): (event: E, responseStream: ResponseStream, context: unknown) => Promise<void>;
  }
}

export {};
```

- [ ] **Step 6: Run the test to verify it passes + typecheck**

```bash
pnpm vitest run src/poster-schema.test.ts
pnpm typecheck
```
Expected: schema tests PASS; typecheck clean.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): add poster request schema, deps, streaming types"
```

---

### Task 3: Web-scrape tool (stub) with a generated JPEG fixture

**Files:**
- Create: `lambda/mastra-handler/src/mastra/tools/stub-band-image.ts` (generated, see Step 1)
- Create: `lambda/mastra-handler/src/mastra/tools/web-scrape.tool.ts`
- Test: `lambda/mastra-handler/src/mastra/tools/web-scrape.tool.test.ts`

**Interfaces:**
- Produces: `BandImage` type + `BandImageSchema` (Zod) `{ imageBase64: string; contentType: string; width: number; height: number; sourceUrl?: string }`; `webScrapeTool` (Mastra tool) with `execute({ performer, refinement? }) → BandImage`. Consumed by Task 8a.

- [ ] **Step 1: Generate the stub band-image constant (valid 48×48 JPEG)**

Run from the lambda dir (macOS `sips`; converts the repo's favicon into a small valid JPEG and writes it as a base64 TS constant):
```bash
cd /Users/wmyers/data/heres-whats-happening/lambda/mastra-handler
sips -z 48 48 -s format jpeg -s formatOptions 60 ../../web/public/favicon.png --out /tmp/band.jpg >/dev/null
B64=$(base64 -i /tmp/band.jpg | tr -d '\n')
cat > src/mastra/tools/stub-band-image.ts <<EOF
// AUTO-GENERATED stub band photo: a 48x48 JPEG (from web/public/favicon.png via sips).
// Stands in for a real image-search/scrape result until that API is wired up.
export const STUB_BAND_IMAGE_BASE64 = "$B64";
export const STUB_BAND_IMAGE = {
  imageBase64: STUB_BAND_IMAGE_BASE64,
  contentType: "image/jpeg",
  width: 48,
  height: 48,
} as const;
EOF
```
Verify it is a non-trivial base64 string:
```bash
node -e "import('./src/mastra/tools/stub-band-image.ts').catch(()=>{}); const s=require('fs').readFileSync('src/mastra/tools/stub-band-image.ts','utf8'); const m=s.match(/BASE64 = \"([^\"]+)\"/); const b=Buffer.from(m[1],'base64'); console.log('bytes',b.length,'soi',b.subarray(0,2).toString('hex'),'eoi',b.subarray(-2).toString('hex'));"
```
Expected: `bytes 1221 soi ffd8 eoi ffd9` (sizes may vary slightly; SOI must be `ffd8`, EOI `ffd9`).

- [ ] **Step 2: Write the failing test**

Create `src/mastra/tools/web-scrape.tool.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import { scrapeBandImage } from "./web-scrape.tool.js";

describe("scrapeBandImage (stub)", () => {
  it("returns a decodable JPEG with positive dimensions", async () => {
    const out = await scrapeBandImage("Khruangbin");
    expect(out.contentType).toBe("image/jpeg");
    expect(out.width).toBeGreaterThan(0);
    expect(out.height).toBeGreaterThan(0);
    const bytes = Buffer.from(out.imageBase64, "base64");
    expect(bytes.subarray(0, 2).toString("hex")).toBe("ffd8"); // JPEG SOI
  });

  it("accepts an optional refinement hint without changing the contract", async () => {
    const out = await scrapeBandImage("Khruangbin", "live band photo, not album art");
    expect(out.imageBase64.length).toBeGreaterThan(0);
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/web-scrape.tool.test.ts`
Expected: FAIL — cannot resolve `./web-scrape.tool.js`.

- [ ] **Step 4: Write the tool**

Create `src/mastra/tools/web-scrape.tool.ts`:
```typescript
import { createTool } from "@mastra/core/tools";
import { z } from "zod";
import { STUB_BAND_IMAGE } from "./stub-band-image.js";

export const BandImageSchema = z.object({
  imageBase64: z.string(),
  contentType: z.string(),
  width: z.number(),
  height: z.number(),
  sourceUrl: z.string().optional(),
});
export type BandImage = z.infer<typeof BandImageSchema>;

// STUB: returns a canned band photo. Replace the body with a real image-search /
// scrape API call. `refinement` carries feedback from a prior rejected candidate so
// the real implementation can issue a better query.
//
// The plain function is the tested + workflow-consumed entrypoint. Calling a Mastra
// tool's `.execute()` directly does NOT typecheck (its type is optional, expects a
// context arg, and returns a `void | ValidationError | Output` union), so the step
// and tests call `scrapeBandImage`, and `webScrapeTool` is a thin wrapper for Studio.
export async function scrapeBandImage(performer: string, refinement?: string): Promise<BandImage> {
  // TODO: real image-search/scrape API keyed on `performer` (+ `refinement`).
  void refinement;
  return {
    ...STUB_BAND_IMAGE,
    sourceUrl: `stub://band-image/${encodeURIComponent(performer)}`,
  };
}

export const webScrapeTool = createTool({
  id: "web-scrape-band-image",
  description: "Find a candidate photo of the given performer for use on a concert poster.",
  inputSchema: z.object({
    performer: z.string(),
    refinement: z.string().optional(),
  }),
  outputSchema: BandImageSchema,
  execute: async ({ performer, refinement }) => scrapeBandImage(performer, refinement),
});
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/web-scrape.tool.test.ts`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): stub web-scrape tool returning a band image"
```

---

### Task 4: SVG parse/substitute tool

**Files:**
- Create: `lambda/mastra-handler/src/mastra/tools/svg-parse.tool.ts`
- Test: `lambda/mastra-handler/src/mastra/tools/svg-parse.tool.test.ts`

**Interfaces:**
- Consumes: `BandImage` (Task 3) at call sites (Task 8b builds the data URI).
- Produces: `substituteAndValidateSvg(svg: string, dataUri: string) → { ok: boolean; svg: string; error?: string }` and `svgParseTool` (Mastra tool, same shape). Consumed by Task 8b.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/tools/svg-parse.tool.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import { substituteAndValidateSvg } from "./svg-parse.tool.js";

const DATA_URI = "data:image/jpeg;base64,/9j/AA==";

describe("substituteAndValidateSvg", () => {
  it("substitutes the placeholder and accepts well-formed SVG", () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><image href="__BAND_IMAGE__"/></svg>`;
    const r = substituteAndValidateSvg(svg, DATA_URI);
    expect(r.ok).toBe(true);
    expect(r.svg).toContain(DATA_URI);
    expect(r.svg).not.toContain("__BAND_IMAGE__");
  });

  it("rejects malformed XML with a message", () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><rect></svg>`; // <rect> not closed
    const r = substituteAndValidateSvg(svg, DATA_URI);
    expect(r.ok).toBe(false);
    expect(r.error).toBeTruthy();
  });

  it("flags an unsubstituted placeholder (no <image> source)", () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`;
    const r = substituteAndValidateSvg(svg, DATA_URI);
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/placeholder/i);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/svg-parse.tool.test.ts`
Expected: FAIL — cannot resolve `./svg-parse.tool.js`.

- [ ] **Step 3: Write the tool**

Create `src/mastra/tools/svg-parse.tool.ts`:
```typescript
import { createTool } from "@mastra/core/tools";
import { XMLValidator } from "fast-xml-parser";
import { z } from "zod";

const PLACEHOLDER = "__BAND_IMAGE__";

/** Replace the band-image placeholder with the real data URI, then validate the
 * result is well-formed XML/SVG. Returns the substituted SVG and an ok flag. */
export function substituteAndValidateSvg(svg: string, dataUri: string): { ok: boolean; svg: string; error?: string } {
  if (!svg.includes(PLACEHOLDER)) {
    return { ok: false, svg, error: `SVG is missing the ${PLACEHOLDER} placeholder for the band image` };
  }
  const substituted = svg.split(PLACEHOLDER).join(dataUri);
  const result = XMLValidator.validate(substituted);
  if (result !== true) {
    return { ok: false, svg: substituted, error: `SVG is not well-formed: ${result.err.msg} (line ${result.err.line})` };
  }
  if (!/<svg[\s>]/i.test(substituted)) {
    return { ok: false, svg: substituted, error: "document has no <svg> root element" };
  }
  return { ok: true, svg: substituted };
}

export const svgParseTool = createTool({
  id: "svg-parse",
  description: "Inject the band image data URI into the SVG placeholder and validate the SVG is well-formed.",
  inputSchema: z.object({ svg: z.string(), dataUri: z.string() }),
  outputSchema: z.object({ ok: z.boolean(), svg: z.string(), error: z.string().optional() }),
  execute: async ({ svg, dataUri }) => substituteAndValidateSvg(svg, dataUri),
});
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/svg-parse.tool.test.ts`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): svg parse/substitute tool"
```

---

### Task 5: Rasterize tool (`@resvg/resvg-wasm`) — the embedded-JPEG risk check

**Files:**
- Create: `lambda/mastra-handler/src/mastra/tools/rasterize.tool.ts`
- Test: `lambda/mastra-handler/src/mastra/tools/rasterize.tool.test.ts`

**Interfaces:**
- Produces: `rasterizeSvg(svg: string) → Promise<{ ok: boolean; pngBase64?: string; width?: number; height?: number; error?: string }>` and `rasterizeTool` (Mastra tool, same shape). Consumed by Task 8b.

- [ ] **Step 1: Write the failing test (includes the embedded-JPEG case)**

Create `src/mastra/tools/rasterize.tool.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import { rasterizeSvg } from "./rasterize.tool.js";
import { STUB_BAND_IMAGE_BASE64 } from "./stub-band-image.js";

const PNG_MAGIC = "89504e47";

describe("rasterizeSvg", () => {
  it("renders a plain SVG to a PNG", async () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#0af"/></svg>`;
    const r = await rasterizeSvg(svg);
    expect(r.ok).toBe(true);
    const bytes = Buffer.from(r.pngBase64!, "base64");
    expect(bytes.subarray(0, 4).toString("hex")).toBe(PNG_MAGIC);
    expect(r.width).toBeGreaterThan(0);
  });

  it("renders an SVG with an embedded base64 JPEG <image>", async () => {
    const dataUri = `data:image/jpeg;base64,${STUB_BAND_IMAGE_BASE64}`;
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48"><image x="0" y="0" width="48" height="48" href="${dataUri}"/></svg>`;
    const r = await rasterizeSvg(svg);
    expect(r.ok).toBe(true);
    const bytes = Buffer.from(r.pngBase64!, "base64");
    expect(bytes.subarray(0, 4).toString("hex")).toBe(PNG_MAGIC);
  });

  it("returns ok:false with an error for unrenderable input", async () => {
    const r = await rasterizeSvg("not an svg at all");
    expect(r.ok).toBe(false);
    expect(r.error).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/rasterize.tool.test.ts`
Expected: FAIL — cannot resolve `./rasterize.tool.js`.

- [ ] **Step 3: Write the tool**

Create `src/mastra/tools/rasterize.tool.ts`:
```typescript
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { createTool } from "@mastra/core/tools";
import { Resvg, initWasm } from "@resvg/resvg-wasm";
import { z } from "zod";

// initWasm must run exactly once per process. The .wasm asset ships inside the
// package; resolve it from node_modules and feed the bytes to initWasm.
let ready: Promise<void> | undefined;
function ensureReady(): Promise<void> {
  if (!ready) {
    const require = createRequire(import.meta.url);
    const wasmPath = require.resolve("@resvg/resvg-wasm/index_bg.wasm");
    ready = readFile(wasmPath).then((bytes) => initWasm(bytes));
  }
  return ready;
}

export type RasterizeResult = { ok: boolean; pngBase64?: string; width?: number; height?: number; error?: string };

/** Render an SVG string to a PNG. Never throws — failures come back as { ok:false, error }. */
export async function rasterizeSvg(svg: string): Promise<RasterizeResult> {
  try {
    await ensureReady();
    const resvg = new Resvg(svg);
    const rendered = resvg.render();
    const png = rendered.asPng();
    return {
      ok: true,
      pngBase64: Buffer.from(png).toString("base64"),
      width: rendered.width,
      height: rendered.height,
    };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e) };
  }
}

export const rasterizeTool = createTool({
  id: "rasterize-svg",
  description: "Render an SVG string to a PNG image (returns base64).",
  inputSchema: z.object({ svg: z.string() }),
  outputSchema: z.object({
    ok: z.boolean(),
    pngBase64: z.string().optional(),
    width: z.number().optional(),
    height: z.number().optional(),
    error: z.string().optional(),
  }),
  execute: async ({ svg }) => rasterizeSvg(svg),
});
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/rasterize.tool.test.ts`
Expected: PASS (all three).

**If the embedded-JPEG test fails** (resvg-wasm cannot decode the embedded JPEG): swap the dependency to the native build — `pnpm remove @resvg/resvg-wasm && pnpm add @resvg/resvg-js` — and rewrite the tool to `import { Resvg } from "@resvg/resvg-js"` with no `initWasm`/`ensureReady` (constructor + `.render().asPng()` are identical otherwise). The native prebuilt binaries cover both the AL2023 Lambda image and macOS. Re-run the test; commit with a note in the message.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): resvg rasterize tool (svg -> png)"
```

---

### Task 6: Poster sink (S3 + stub)

**Files:**
- Create: `lambda/mastra-handler/src/poster-sink.ts`
- Test: `lambda/mastra-handler/src/poster-sink.test.ts`

**Interfaces:**
- Consumes: `PosterRequest` (Task 2).
- Produces:
  - `PosterSink` interface: `put(req: PosterRequest, svg: string, pngBase64: string) => Promise<{ svgUrl: string; pngUrl: string }>`
  - `posterKeyBase(req: PosterRequest) => string` (pure; e.g. `posters/khruangbin/the-fillmore-2026-08-15`)
  - `StubPosterSink` (records calls; returns canned URLs) — used by Tasks 9 & 10 tests
  - `S3PosterSink` (real) — used by Task 11

- [ ] **Step 1: Write the failing test**

Create `src/poster-sink.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import { posterKeyBase, StubPosterSink } from "./poster-sink.js";

const req = { performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15" };

describe("posterKeyBase", () => {
  it("builds a slugged, prefixed key", () => {
    expect(posterKeyBase(req)).toBe("posters/khruangbin/the-fillmore-2026-08-15");
  });
  it("slugs spaces and punctuation", () => {
    expect(posterKeyBase({ performer: "Sigur Rós!", venue: "9:30 Club", date: "2026-09-01" }))
      .toBe("posters/sigur-ros/9-30-club-2026-09-01");
  });
});

describe("StubPosterSink", () => {
  it("records the put and returns canned urls", async () => {
    const sink = new StubPosterSink();
    const urls = await sink.put(req, "<svg/>", "AAAA");
    expect(urls.svgUrl).toContain("posters/khruangbin");
    expect(sink.calls).toHaveLength(1);
    expect(sink.calls[0].svg).toBe("<svg/>");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm vitest run src/poster-sink.test.ts`
Expected: FAIL — cannot resolve `./poster-sink.js`.

- [ ] **Step 3: Write the sink**

Create `src/poster-sink.ts`:
```typescript
import { PutObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import { GetObjectCommand } from "@aws-sdk/client-s3";
import type { PosterRequest } from "./poster-schema.js";

export interface PosterSink {
  put(req: PosterRequest, svg: string, pngBase64: string): Promise<{ svgUrl: string; pngUrl: string }>;
}

function slug(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // strip diacritics
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** Deterministic S3 key prefix for a request (no extension). */
export function posterKeyBase(req: PosterRequest): string {
  return `posters/${slug(req.performer)}/${slug(req.venue)}-${slug(req.date)}`;
}

/** Writes svg + png to S3 and returns presigned GET URLs (1 hour). */
export class S3PosterSink implements PosterSink {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  async put(req: PosterRequest, svg: string, pngBase64: string): Promise<{ svgUrl: string; pngUrl: string }> {
    const base = posterKeyBase(req);
    const svgKey = `${base}.svg`;
    const pngKey = `${base}.png`;
    await this.s3.send(new PutObjectCommand({ Bucket: this.bucket, Key: svgKey, Body: svg, ContentType: "image/svg+xml" }));
    await this.s3.send(new PutObjectCommand({ Bucket: this.bucket, Key: pngKey, Body: Buffer.from(pngBase64, "base64"), ContentType: "image/png" }));
    const [svgUrl, pngUrl] = await Promise.all([
      getSignedUrl(this.s3, new GetObjectCommand({ Bucket: this.bucket, Key: svgKey }), { expiresIn: 3600 }),
      getSignedUrl(this.s3, new GetObjectCommand({ Bucket: this.bucket, Key: pngKey }), { expiresIn: 3600 }),
    ]);
    return { svgUrl, pngUrl };
  }
}

/** Test double: records puts, returns deterministic fake URLs. */
export class StubPosterSink implements PosterSink {
  public calls: Array<{ req: PosterRequest; svg: string; pngBase64: string }> = [];
  async put(req: PosterRequest, svg: string, pngBase64: string): Promise<{ svgUrl: string; pngUrl: string }> {
    this.calls.push({ req, svg, pngBase64 });
    const base = posterKeyBase(req);
    return { svgUrl: `https://stub.local/${base}.svg`, pngUrl: `https://stub.local/${base}.png` };
  }
}
```

- [ ] **Step 4: Run the test to verify it passes + typecheck**

```bash
pnpm vitest run src/poster-sink.test.ts
pnpm typecheck
```
Expected: tests PASS; typecheck clean.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): poster sink (S3 + stub) with deterministic keys"
```

---

### Task 7: The three agents

LLM agents — no unit tests (they need `ANTHROPIC_API_KEY`), per the `MastraExtractor` precedent. Verified by typecheck and (manually) in Studio.

**Files:**
- Create: `lambda/mastra-handler/src/mastra/agents/image-analysis.agent.ts`
- Create: `lambda/mastra-handler/src/mastra/agents/svg-author.agent.ts`
- Create: `lambda/mastra-handler/src/mastra/agents/poster-critique.agent.ts`

**Interfaces:**
- Produces (consumed by Task 8a/8b via direct import + `.generate()`, reading `res.object`):
  - `imageAnalysisAgent`, output schema `ImageAnalysisSchema = { acceptable: boolean; reason: string; dominantColors: string[] }`
  - `svgAuthorAgent`, output schema `SvgAuthorSchema = { svg: string }`
  - `posterCritiqueAgent`, output schema `PosterCritiqueSchema = { acceptable: boolean; critique: string }`

- [ ] **Step 1: Write the image-analysis agent**

Create `src/mastra/agents/image-analysis.agent.ts`:
```typescript
import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const ImageAnalysisSchema = z.object({
  acceptable: z.boolean().describe("True only if this is a real photo of the named performer, suitable for a poster."),
  reason: z.string().describe("If not acceptable, why — used to refine the next image search. If acceptable, a short note."),
  dominantColors: z.array(z.string()).describe("3-5 dominant colors of the photo as hex strings, e.g. '#1a2b3c'."),
});

export const imageAnalysisAgent = new Agent({
  id: "poster-image-analysis",
  name: "Poster Band-Image Analyst",
  instructions: `You are validating a candidate photo for a concert poster of a specific performer.
The user message contains the performer name and an image. Decide whether the image is genuinely a
usable photo of that performer/band (not album art, not a logo, not the wrong artist, not unusable).
Always extract 3-5 dominant hex colors from the image for downstream poster theming. Be strict about
"acceptable" — when in doubt, reject with a concrete reason that would improve the next search.`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(ImageAnalysisSchema) } },
});
```

- [ ] **Step 2: Write the SVG-author agent**

Create `src/mastra/agents/svg-author.agent.ts`:
```typescript
import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const SvgAuthorSchema = z.object({
  svg: z.string().describe("A complete, standalone SVG document string starting with '<svg' and ending with '</svg>'."),
});

export const svgAuthorAgent = new Agent({
  id: "poster-svg-author",
  name: "Concert Poster SVG Author",
  instructions: `You design eye-catching concert-poster SVGs.
The user message is a JSON object: { performer, venue, date, colors: string[], imageWidth, imageHeight, critique? }.
Produce ONE complete SVG document (default canvas 1080x1350, portrait) that includes:
- An <image> element for the band photo. Use the EXACT literal href "__BAND_IMAGE__" (a downstream step
  substitutes the real image data; never invent a URL or data URI). Size/position it tastefully using the
  given imageWidth/imageHeight aspect ratio.
- The performer name as the dominant headline, the venue, and the date — all legible.
- A snazzy background pattern (gradients, shapes, repetition) themed with the provided 'colors'.
If 'critique' is present, it explains what was wrong with your previous attempt — fix it.
Return only the SVG via the 'svg' field. Use xmlns="http://www.w3.org/2000/svg". Keep it well-formed.`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(SvgAuthorSchema) } },
});
```

- [ ] **Step 3: Write the poster-critique agent**

Create `src/mastra/agents/poster-critique.agent.ts`:
```typescript
import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const PosterCritiqueSchema = z.object({
  acceptable: z.boolean().describe("True only if this is a cool, legible concert poster showing the band photo, venue, and date."),
  critique: z.string().describe("If not acceptable, specific actionable fixes for the SVG author. If acceptable, a short note."),
});

export const posterCritiqueAgent = new Agent({
  id: "poster-critique",
  name: "Concert Poster Critic",
  instructions: `You judge a RENDERED concert poster image.
The user message contains the intended { performer, venue, date } and the rendered poster image.
Approve only if it is visually cool AND legible AND clearly shows the band photo, the performer name,
the venue, and the date. Otherwise reject with concrete, actionable critique the SVG author can apply
(e.g. "date is illegible against the background", "band photo is tiny/cropped", "colors clash").`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(PosterCritiqueSchema) } },
});
```

- [ ] **Step 4: Typecheck**

Run: `pnpm typecheck`
Expected: clean. (`toStandardSchema` + `defaultOptions.structuredOutput` mirror the existing `email-extractor.agent.ts`.)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): image-analysis, svg-author, poster-critique agents"
```

---

### Task 8a: Workflow schemas + acquire-band-image loop step

**Files:**
- Create: `lambda/mastra-handler/src/mastra/workflows/poster.schemas.ts`
- Create: `lambda/mastra-handler/src/mastra/workflows/acquire-band-image.step.ts`

**Interfaces:**
- Produces:
  - `ImageLoopStateSchema` / `ImageLoopState`, `PosterLoopStateSchema` / `PosterLoopState`, `PosterWorkflowOutputSchema` / `PosterWorkflowOutput` (Zod + types)
  - `acquireBandImageStep` (Mastra step; `inputSchema === outputSchema === ImageLoopStateSchema`)

- [ ] **Step 1: Write the workflow schemas**

Create `src/mastra/workflows/poster.schemas.ts`:
```typescript
import { z } from "zod";
import { BandImageSchema } from "../tools/web-scrape.tool.js";

// Loop-1 state: input and output of the acquire-band-image step are the SAME shape,
// so the step's output can feed straight back as the next iteration's input.
export const ImageLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  attempts: z.number(),
  accepted: z.boolean(),
  reason: z.string().optional(),
  image: BandImageSchema.optional(),
  colors: z.array(z.string()).default([]),
});
export type ImageLoopState = z.infer<typeof ImageLoopStateSchema>;

// Loop-2 state: input and output of the compose-poster step are the SAME shape.
export const PosterLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  imageOk: z.boolean(),
  imageReason: z.string().optional(),
  image: BandImageSchema.optional(),
  colors: z.array(z.string()).default([]),
  attempts: z.number(),
  accepted: z.boolean(),
  critique: z.string().optional(),
  svg: z.string().optional(),
  pngBase64: z.string().optional(),
});
export type PosterLoopState = z.infer<typeof PosterLoopStateSchema>;

// Final workflow output: a controlled result (ok or a typed failure stage+reason).
export const PosterWorkflowOutputSchema = z.object({
  ok: z.boolean(),
  svg: z.string().optional(),
  pngBase64: z.string().optional(),
  failureStage: z.enum(["image", "svg"]).optional(),
  reason: z.string().optional(),
});
export type PosterWorkflowOutput = z.infer<typeof PosterWorkflowOutputSchema>;

export const MAX_IMAGE_ATTEMPTS = Number(process.env.MAX_IMAGE_ATTEMPTS ?? 3);
export const MAX_SVG_ATTEMPTS = Number(process.env.MAX_SVG_ATTEMPTS ?? 3);
```

- [ ] **Step 2: Write the acquire-band-image step**

Create `src/mastra/workflows/acquire-band-image.step.ts`:
```typescript
import { createStep } from "@mastra/core/workflows";
import { imageAnalysisAgent } from "../agents/image-analysis.agent.js";
import { scrapeBandImage } from "../tools/web-scrape.tool.js";
import { ImageLoopStateSchema } from "./poster.schemas.js";

// One iteration: scrape a candidate, then a vision agent judges it. Output shape ==
// input shape so .dountil can loop, carrying `reason` forward to refine the next scrape.
export const acquireBandImageStep = createStep({
  id: "acquire-band-image",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData }) => {
    const attempts = inputData.attempts + 1;
    const image = await scrapeBandImage(inputData.performer, inputData.reason);

    const res = await imageAnalysisAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: Buffer.from(image.imageBase64, "base64"), mimeType: image.contentType },
          { type: "text", text: `Performer: ${inputData.performer}. Is this a usable photo of this performer for a concert poster?` },
        ],
      },
    ]);
    const analysis = res.object;
    if (!analysis) {
      return { ...inputData, attempts, accepted: false, reason: "image analysis returned no result", image };
    }
    return {
      ...inputData,
      attempts,
      accepted: analysis.acceptable,
      reason: analysis.reason,
      image,
      colors: analysis.dominantColors ?? [],
    };
  },
});
```

- [ ] **Step 3: Typecheck**

Run: `pnpm typecheck`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): workflow schemas + acquire-band-image step"
```

---

### Task 8b: Compose-poster loop step

**Files:**
- Create: `lambda/mastra-handler/src/mastra/workflows/compose-poster.step.ts`

**Interfaces:**
- Consumes: `PosterLoopStateSchema` (8a), `svgAuthorAgent`/`posterCritiqueAgent` (Task 7), `substituteAndValidateSvg` (Task 4), `rasterizeSvg` (Task 5).
- Produces: `composePosterStep` (Mastra step; `inputSchema === outputSchema === PosterLoopStateSchema`).

- [ ] **Step 1: Write the compose-poster step**

Create `src/mastra/workflows/compose-poster.step.ts`:
```typescript
import { createStep } from "@mastra/core/workflows";
import { posterCritiqueAgent } from "../agents/poster-critique.agent.js";
import { svgAuthorAgent } from "../agents/svg-author.agent.js";
import { rasterizeSvg } from "../tools/rasterize.tool.js";
import { substituteAndValidateSvg } from "../tools/svg-parse.tool.js";
import { PosterLoopStateSchema } from "./poster.schemas.js";

// One iteration: author SVG -> substitute+parse -> rasterize -> critique. Any failure
// sets accepted=false and records actionable feedback in `critique` for the next attempt.
export const composePosterStep = createStep({
  id: "compose-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterLoopStateSchema,
  execute: async ({ inputData }) => {
    // Cheap short-circuit: if image acquisition failed, do no LLM work.
    if (!inputData.imageOk || !inputData.image) {
      return { ...inputData, accepted: false };
    }
    const attempts = inputData.attempts + 1;
    const { image } = inputData;

    // 1) Author the SVG (placeholder href for the image).
    const authored = await svgAuthorAgent.generate([
      {
        role: "user",
        content: JSON.stringify({
          performer: inputData.performer,
          venue: inputData.venue,
          date: inputData.date,
          colors: inputData.colors,
          imageWidth: image.width,
          imageHeight: image.height,
          critique: inputData.critique,
        }),
      },
    ]);
    const rawSvg = authored.object?.svg;
    if (!rawSvg) {
      return { ...inputData, attempts, accepted: false, critique: "SVG author returned no svg" };
    }

    // 2) Substitute the real image + validate well-formedness.
    const dataUri = `data:${image.contentType};base64,${image.imageBase64}`;
    const parsed = substituteAndValidateSvg(rawSvg, dataUri);
    if (!parsed.ok) {
      return { ...inputData, attempts, accepted: false, svg: rawSvg, critique: `Fix the SVG so it is well-formed: ${parsed.error}` };
    }

    // 3) Rasterize to PNG.
    const raster = await rasterizeSvg(parsed.svg);
    if (!raster.ok || !raster.pngBase64) {
      return { ...inputData, attempts, accepted: false, svg: parsed.svg, critique: `The SVG did not render: ${raster.error}` };
    }

    // 4) Critique the rendered poster.
    const critique = await posterCritiqueAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: Buffer.from(raster.pngBase64, "base64"), mimeType: "image/png" },
          { type: "text", text: `Intended poster — performer: ${inputData.performer}, venue: ${inputData.venue}, date: ${inputData.date}. Is this a cool, legible poster?` },
        ],
      },
    ]);
    const verdict = critique.object;
    return {
      ...inputData,
      attempts,
      svg: parsed.svg,
      pngBase64: raster.pngBase64,
      accepted: verdict?.acceptable ?? false,
      critique: verdict?.critique ?? "critique returned no result",
    };
  },
});
```

- [ ] **Step 2: Typecheck**

Run: `pnpm typecheck`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): compose-poster step (author->parse->raster->critique)"
```

---

### Task 8c: Assemble the workflow + register it

**Files:**
- Create: `lambda/mastra-handler/src/mastra/workflows/poster.workflow.ts`
- Modify: `lambda/mastra-handler/src/mastra/index.ts`

**Interfaces:**
- Consumes: steps (8a/8b), schemas (8a), `PosterRequestSchema` (Task 2).
- Produces: `posterWorkflow` (committed Mastra workflow; input `PosterRequestSchema`, output `PosterWorkflowOutputSchema`). Consumed by Task 11.

- [ ] **Step 1: Write the workflow**

Create `src/mastra/workflows/poster.workflow.ts`:
```typescript
import { createStep, createWorkflow } from "@mastra/core/workflows";
import { PosterRequestSchema } from "../../poster-schema.js";
import { acquireBandImageStep } from "./acquire-band-image.step.js";
import { composePosterStep } from "./compose-poster.step.js";
import {
  MAX_IMAGE_ATTEMPTS,
  MAX_SVG_ATTEMPTS,
  PosterLoopStateSchema,
  PosterWorkflowOutputSchema,
} from "./poster.schemas.js";

// Terminal step: normalize the last loop state into the controlled workflow output.
// (Workflows must end on a step whose outputSchema matches the workflow outputSchema.)
const finalizeStep = createStep({
  id: "finalize-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterWorkflowOutputSchema,
  execute: async ({ inputData }) => {
    if (!inputData.imageOk) {
      return { ok: false, failureStage: "image" as const, reason: inputData.imageReason ?? "no acceptable band image found" };
    }
    if (inputData.accepted && inputData.svg && inputData.pngBase64) {
      return { ok: true, svg: inputData.svg, pngBase64: inputData.pngBase64 };
    }
    return { ok: false, failureStage: "svg" as const, reason: inputData.critique ?? "could not produce an acceptable poster" };
  },
});

export const posterWorkflow = createWorkflow({
  id: "poster-workflow",
  inputSchema: PosterRequestSchema,
  outputSchema: PosterWorkflowOutputSchema,
})
  // Seed loop-1 state from the request.
  .map(async ({ inputData }) => ({
    performer: inputData.performer,
    venue: inputData.venue,
    date: inputData.date,
    attempts: 0,
    accepted: false,
    colors: [] as string[],
  }))
  // Loop 1: acquire an acceptable band image (bounded).
  .dountil(
    acquireBandImageStep,
    async ({ inputData }) => inputData.accepted || inputData.attempts >= MAX_IMAGE_ATTEMPTS,
  )
  // Seed loop-2 state, carrying whether the image succeeded.
  .map(async ({ inputData }) => ({
    performer: inputData.performer,
    venue: inputData.venue,
    date: inputData.date,
    imageOk: inputData.accepted,
    imageReason: inputData.reason,
    image: inputData.image,
    colors: inputData.colors,
    attempts: 0,
    accepted: false,
  }))
  // Loop 2: compose + validate the poster (bounded). Condition is immediately true
  // when imageOk is false (the step short-circuits without LLM work).
  .dountil(
    composePosterStep,
    async ({ inputData }) =>
      inputData.accepted || !inputData.imageOk || inputData.attempts >= MAX_SVG_ATTEMPTS,
  )
  // Normalize either outcome to the controlled workflow output.
  .then(finalizeStep)
  .commit();
```

- [ ] **Step 2: Register the workflow + agents in the Mastra instance**

Replace `src/mastra/index.ts` with:
```typescript
import { Mastra } from "@mastra/core";
import { emailExtractorAgent } from "./agents/email-extractor.agent.js";
import { imageAnalysisAgent } from "./agents/image-analysis.agent.js";
import { posterCritiqueAgent } from "./agents/poster-critique.agent.js";
import { svgAuthorAgent } from "./agents/svg-author.agent.js";
import { posterWorkflow } from "./workflows/poster.workflow.js";

export const mastra = new Mastra({
  agents: {
    emailExtractor: emailExtractorAgent,
    imageAnalysis: imageAnalysisAgent,
    svgAuthor: svgAuthorAgent,
    posterCritique: posterCritiqueAgent,
  },
  workflows: { posterWorkflow },
});
```

- [ ] **Step 3: Typecheck**

Run: `pnpm typecheck`
Expected: clean. (If `.map`/`.dountil` generic inference complains about an optional field, confirm the seed objects include every required key of the next schema — `colors` defaults aside, all listed keys are present.)

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): assemble + register poster workflow"
```

---

### Task 9: Poster core — `processPosterRequest` + request parsing + HTTP mapping

**Files:**
- Create: `lambda/mastra-handler/src/poster.ts`
- Test: `lambda/mastra-handler/src/poster.test.ts`

**Interfaces:**
- Consumes: `PosterRequest`/`PosterResult` (Task 2), `PosterSink`/`StubPosterSink` (Task 6), `PosterWorkflowOutput` (Task 8a).
- Produces:
  - `PosterDeps` = `{ runWorkflow: (req: PosterRequest) => Promise<PosterWorkflowOutput>; sink: PosterSink }`
  - `processPosterRequest(req: PosterRequest, deps: PosterDeps) => Promise<PosterResult>`
  - `BadRequestError` (class), `parsePosterRequest(body: string | undefined, isBase64: boolean) => PosterRequest`
  - `posterHttpResponse(result: PosterResult) => { statusCode: number; headers: Record<string,string>; body: string }`
  - Consumed by Task 10.

- [ ] **Step 1: Write the failing tests**

Create `src/poster.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import { StubPosterSink } from "./poster-sink.js";
import { BadRequestError, parsePosterRequest, posterHttpResponse, processPosterRequest } from "./poster.js";

const req = { performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15" };

describe("processPosterRequest", () => {
  it("on success writes to the sink and returns urls + svg", async () => {
    const sink = new StubPosterSink();
    const res = await processPosterRequest(req, {
      sink,
      runWorkflow: async () => ({ ok: true, svg: "<svg/>", pngBase64: "AAAA" }),
    });
    expect(res.ok).toBe(true);
    if (res.ok) {
      expect(res.svg).toBe("<svg/>");
      expect(res.svgUrl).toContain("posters/khruangbin");
    }
    expect(sink.calls).toHaveLength(1);
  });

  it("on a controlled failure returns ok:false with stage + reason and does NOT write", async () => {
    const sink = new StubPosterSink();
    const res = await processPosterRequest(req, {
      sink,
      runWorkflow: async () => ({ ok: false, failureStage: "image", reason: "no good photo" }),
    });
    expect(res).toEqual({ ok: false, stage: "image", reason: "no good photo" });
    expect(sink.calls).toHaveLength(0);
  });
});

describe("parsePosterRequest", () => {
  it("parses a plain JSON body", () => {
    expect(parsePosterRequest(JSON.stringify(req), false)).toEqual(req);
  });
  it("decodes a base64 body", () => {
    const b64 = Buffer.from(JSON.stringify(req), "utf8").toString("base64");
    expect(parsePosterRequest(b64, true)).toEqual(req);
  });
  it("throws BadRequestError on invalid JSON", () => {
    expect(() => parsePosterRequest("{not json", false)).toThrow(BadRequestError);
  });
  it("throws BadRequestError on a missing field", () => {
    expect(() => parsePosterRequest(JSON.stringify({ performer: "X" }), false)).toThrow(BadRequestError);
  });
});

describe("posterHttpResponse", () => {
  it("maps ok -> 200 json", () => {
    const r = posterHttpResponse({ ok: true, svg: "<svg/>", svgUrl: "u1", pngUrl: "u2" });
    expect(r.statusCode).toBe(200);
    expect(JSON.parse(r.body).svg).toBe("<svg/>");
  });
  it("maps failure -> 422 with stage", () => {
    const r = posterHttpResponse({ ok: false, stage: "svg", reason: "ugly" });
    expect(r.statusCode).toBe(422);
    expect(JSON.parse(r.body)).toEqual({ error: "ugly", stage: "svg" });
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm vitest run src/poster.test.ts`
Expected: FAIL — cannot resolve `./poster.js`.

- [ ] **Step 3: Write the core**

Create `src/poster.ts`:
```typescript
import { PosterRequestSchema, type PosterRequest, type PosterResult } from "./poster-schema.js";
import type { PosterSink } from "./poster-sink.js";
import type { PosterWorkflowOutput } from "./mastra/workflows/poster.schemas.js";

export interface PosterDeps {
  runWorkflow: (req: PosterRequest) => Promise<PosterWorkflowOutput>;
  sink: PosterSink;
}

export class BadRequestError extends Error {}

/** Parse + validate a Function URL request body into a PosterRequest. */
export function parsePosterRequest(body: string | undefined, isBase64: boolean): PosterRequest {
  const raw = body ? (isBase64 ? Buffer.from(body, "base64").toString("utf8") : body) : "";
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    throw new BadRequestError("request body is not valid JSON");
  }
  const parsed = PosterRequestSchema.safeParse(json);
  if (!parsed.success) {
    throw new BadRequestError(parsed.error.issues.map((i) => i.message).join("; "));
  }
  return parsed.data;
}

/** Run the workflow; on success persist artifacts via the sink. Never persists on failure. */
export async function processPosterRequest(req: PosterRequest, deps: PosterDeps): Promise<PosterResult> {
  const out = await deps.runWorkflow(req);
  if (!out.ok || !out.svg || !out.pngBase64) {
    return { ok: false, stage: out.failureStage ?? "svg", reason: out.reason ?? "unknown failure" };
  }
  const { svgUrl, pngUrl } = await deps.sink.put(req, out.svg, out.pngBase64);
  return { ok: true, svg: out.svg, svgUrl, pngUrl };
}

const JSON_HEADERS = { "content-type": "application/json" };

export function posterHttpResponse(result: PosterResult): { statusCode: number; headers: Record<string, string>; body: string } {
  if (result.ok) {
    return { statusCode: 200, headers: JSON_HEADERS, body: JSON.stringify({ svg: result.svg, svgUrl: result.svgUrl, pngUrl: result.pngUrl }) };
  }
  // 422 (never 403/404 — see Global Constraints / spec §8).
  return { statusCode: 422, headers: JSON_HEADERS, body: JSON.stringify({ error: result.reason, stage: result.stage }) };
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm vitest run src/poster.test.ts`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): poster core (process, parse, http mapping)"
```

---

### Task 10: Handler bifurcation + streaming entrypoint

**Files:**
- Modify: `lambda/mastra-handler/src/handler.ts`
- Test: `lambda/mastra-handler/src/handler.poster.test.ts`

**Interfaces:**
- Consumes: `processPosterRequest`/`parsePosterRequest`/`posterHttpResponse`/`BadRequestError`/`PosterDeps` (Task 9), existing `processEmail`/`prodDeps`/`getObject`/`loadModelKey`/`AwsSecretReader` (handler).
- Produces: `isFunctionUrlEvent(event)`, `handlePosterHttp(event, deps)` (both testable, no streaming), `handleS3(event)`, and the streaming `handler` export. `runPosterWorkflow`/`prodPosterDeps` are added in Task 11.

- [ ] **Step 1: Write the failing tests**

Create `src/handler.poster.test.ts`:
```typescript
import { describe, expect, it } from "vitest";
import type { APIGatewayProxyEventV2, S3Event } from "aws-lambda";
import { StubPosterSink } from "./poster-sink.js";
import { handlePosterHttp, isFunctionUrlEvent } from "./handler.js";

function fnUrlEvent(body: unknown): APIGatewayProxyEventV2 {
  return {
    version: "2.0",
    routeKey: "$default",
    rawPath: "/api/poster",
    rawQueryString: "",
    headers: { "content-type": "application/json" },
    requestContext: { http: { method: "POST", path: "/api/poster" } },
    body: JSON.stringify(body),
    isBase64Encoded: false,
  } as unknown as APIGatewayProxyEventV2;
}

const s3Event = { Records: [{ eventSource: "aws:s3", s3: { bucket: { name: "b" }, object: { key: "raw/x" } } }] } as unknown as S3Event;

describe("isFunctionUrlEvent", () => {
  it("is true for a v2 Function URL event", () => {
    expect(isFunctionUrlEvent(fnUrlEvent({}))).toBe(true);
  });
  it("is false for an S3 event", () => {
    expect(isFunctionUrlEvent(s3Event)).toBe(false);
  });
});

describe("handlePosterHttp", () => {
  const deps = { sink: new StubPosterSink(), runWorkflow: async () => ({ ok: true, svg: "<svg/>", pngBase64: "AAAA" }) };

  it("returns 200 for a valid request", async () => {
    const res = await handlePosterHttp(fnUrlEvent({ performer: "K", venue: "F", date: "2026-08-15" }), deps);
    expect(res.statusCode).toBe(200);
    expect(JSON.parse(res.body).svg).toBe("<svg/>");
  });

  it("returns 400 for an invalid body", async () => {
    const res = await handlePosterHttp(fnUrlEvent({ performer: "K" }), deps);
    expect(res.statusCode).toBe(400);
    expect(JSON.parse(res.body).error).toBeTruthy();
  });

  it("returns 500 if the workflow throws", async () => {
    const res = await handlePosterHttp(
      fnUrlEvent({ performer: "K", venue: "F", date: "2026-08-15" }),
      { sink: new StubPosterSink(), runWorkflow: async () => { throw new Error("boom"); } },
    );
    expect(res.statusCode).toBe(500);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm vitest run src/handler.poster.test.ts`
Expected: FAIL — `isFunctionUrlEvent` / `handlePosterHttp` not exported.

- [ ] **Step 3: Refactor the handler**

Edit `src/handler.ts`. Keep the existing imports and `processEmail`/`ProcessDeps`/`requireEnv`/`prodDeps`/`getObject` exactly as they are. Then:

(a) Add imports at the top:
```typescript
import type { APIGatewayProxyEventV2 } from "aws-lambda";
import { BadRequestError, parsePosterRequest, posterHttpResponse, processPosterRequest, type PosterDeps } from "./poster.js";
```

(b) Replace the existing `handler` function (the `export async function handler(event: S3Event)` at the bottom) with the discriminator, the two branch handlers, and a streaming entrypoint:
```typescript
type HandlerEvent = S3Event | APIGatewayProxyEventV2;
interface HttpResponse {
  statusCode: number;
  headers: Record<string, string>;
  body: string;
}

/** True when the event is a Lambda Function URL (API GW v2 payload) request. */
export function isFunctionUrlEvent(event: HandlerEvent): event is APIGatewayProxyEventV2 {
  return (
    typeof (event as APIGatewayProxyEventV2).version === "string" &&
    (event as APIGatewayProxyEventV2).version === "2.0" &&
    !!(event as APIGatewayProxyEventV2).requestContext?.http
  );
}

/** Poster path: parse -> run -> map to HTTP. Returns 400/422/500 only — never throws. */
export async function handlePosterHttp(event: APIGatewayProxyEventV2, deps: PosterDeps): Promise<HttpResponse> {
  let req;
  try {
    req = parsePosterRequest(event.body, event.isBase64Encoded ?? false);
  } catch (e) {
    if (e instanceof BadRequestError) {
      return { statusCode: 400, headers: { "content-type": "application/json" }, body: JSON.stringify({ error: e.message }) };
    }
    throw e;
  }
  try {
    const result = await processPosterRequest(req, deps);
    return posterHttpResponse(result);
  } catch (e) {
    console.error(JSON.stringify({ msg: "poster-error", error: e instanceof Error ? e.message : String(e) }));
    return { statusCode: 500, headers: { "content-type": "application/json" }, body: JSON.stringify({ error: "internal error" }) };
  }
}

/** Existing S3 -> email path (unchanged behavior), extracted for the branch. */
export async function handleS3(event: S3Event): Promise<void> {
  const deps = prodDeps();
  const s3 = new S3Client({ region: process.env.AWS_REGION });
  for (const rec of event.Records) {
    const bucket = rec.s3.bucket.name;
    const key = decodeURIComponent(rec.s3.object.key.replace(/\+/g, " "));
    const raw = await getObject(s3, bucket, key);
    await processEmail(raw, deps);
  }
}

/** Single Lambda entrypoint. Streaming-wrapped (required for the Function URL path);
 * S3 async invokes run the same code and the response stream is ignored. */
export const handler = awslambda.streamifyResponse(
  async (event: HandlerEvent, responseStream, _context): Promise<void> => {
    const secretArn = process.env.LLM_API_KEY_SECRET;
    if (secretArn) await loadModelKey(new AwsSecretReader(process.env.AWS_REGION), secretArn);

    if (isFunctionUrlEvent(event)) {
      const res = await handlePosterHttp(event, prodPosterDeps());
      const stream = awslambda.HttpResponseStream.from(responseStream, { statusCode: res.statusCode, headers: res.headers });
      stream.write(res.body);
      stream.end();
      return;
    }

    await handleS3(event as S3Event);
    responseStream.end();
  },
);
```

Note: `prodPosterDeps()` is referenced here but defined in Task 11. To keep this task green, add a temporary stub at the bottom of the file for now:
```typescript
// TEMP — replaced with the real implementation in Task 11.
function prodPosterDeps(): PosterDeps {
  throw new Error("prodPosterDeps not yet wired (Task 11)");
}
```

- [ ] **Step 4: Run the tests to verify they pass + typecheck + existing suite**

```bash
pnpm vitest run src/handler.poster.test.ts src/handler.test.ts
pnpm typecheck
```
Expected: new poster tests PASS; existing `handler.test.ts` (tests `processEmail`) still PASS; typecheck clean.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): bifurcate handler (S3 + streaming Function URL)"
```

---

### Task 11: Wire production deps + local invoke harness

**Files:**
- Modify: `lambda/mastra-handler/src/handler.ts` (replace the temp `prodPosterDeps`)
- Create: `lambda/mastra-handler/scripts/invoke-poster-local.ts`

**Interfaces:**
- Consumes: `posterWorkflow` (8c), `S3PosterSink` (Task 6), `PosterDeps`/`PosterWorkflowOutput`.
- Produces: real `runPosterWorkflow` + `prodPosterDeps`.

- [ ] **Step 1: Replace the temp `prodPosterDeps` with the real wiring**

In `src/handler.ts`, add imports:
```typescript
import { S3PosterSink } from "./poster-sink.js";
import { posterWorkflow } from "./mastra/workflows/poster.workflow.js";
import type { PosterRequest } from "./poster-schema.js";
import type { PosterWorkflowOutput } from "./mastra/workflows/poster.schemas.js";
```
Then delete the temp `prodPosterDeps` stub and replace it with:
```typescript
/** Run the registered workflow to completion and return its controlled output. */
export async function runPosterWorkflow(req: PosterRequest): Promise<PosterWorkflowOutput> {
  const run = await posterWorkflow.createRun();
  const result = await run.start({ inputData: req });
  if (result.status !== "success") {
    const detail = result.status === "failed" ? result.error?.message : result.status;
    throw new Error(`poster workflow did not complete: ${detail}`);
  }
  return result.result as PosterWorkflowOutput;
}

function prodPosterDeps(): PosterDeps {
  const region = requireEnv("AWS_REGION");
  const bucket = requireEnv("POSTERS_BUCKET");
  return {
    runWorkflow: runPosterWorkflow,
    sink: new S3PosterSink(new S3Client({ region }), bucket),
  };
}
```

- [ ] **Step 2: Write the local invoke harness**

Create `scripts/invoke-poster-local.ts`:
```typescript
/* Run the poster workflow locally and write the SVG + PNG to disk.
 * Usage: ANTHROPIC_API_KEY=... pnpm tsx scripts/invoke-poster-local.ts "Khruangbin" "The Fillmore" "2026-08-15"
 */
import { writeFileSync } from "node:fs";
import { runPosterWorkflow } from "../src/handler.js";

const [performer, venue, date] = process.argv.slice(2);
if (!performer || !venue || !date) throw new Error('usage: invoke-poster-local "<performer>" "<venue>" "<date>"');

const out = await runPosterWorkflow({ performer, venue, date });
if (!out.ok || !out.svg || !out.pngBase64) {
  console.error(JSON.stringify({ ok: false, failureStage: out.failureStage, reason: out.reason }, null, 2));
  process.exit(1);
}
writeFileSync("poster.svg", out.svg);
writeFileSync("poster.png", Buffer.from(out.pngBase64, "base64"));
console.log("wrote poster.svg + poster.png");
```

Add a script to `package.json` `"scripts"`:
```json
    "invoke-poster-local": "tsx scripts/invoke-poster-local.ts",
```

- [ ] **Step 3: Typecheck + full unit suite**

```bash
pnpm typecheck
pnpm vitest run src/poster-schema.test.ts src/poster.test.ts src/poster-sink.test.ts src/handler.poster.test.ts src/handler.test.ts src/mastra/tools/web-scrape.tool.test.ts src/mastra/tools/svg-parse.tool.test.ts src/mastra/tools/rasterize.tool.test.ts
```
Expected: typecheck clean; all tests PASS.

- [ ] **Step 4: (Manual, optional) End-to-end smoke via Studio / invoke-local**

With a real key (not run in CI):
```bash
ANTHROPIC_API_KEY=sk-... pnpm invoke-poster-local "Khruangbin" "The Fillmore" "2026-08-15"
```
Expected: `poster.svg` + `poster.png` written. Also `pnpm dev` (Studio) → the `poster-workflow` appears under Workflows with an input form. (Documented for the operator; not a gating step.)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(mastra-handler): wire prod poster deps + local invoke harness"
```

---

### Task 12: Terraform — rename + new poster infrastructure

No `terraform plan`/`apply`/`init` here (see Global Constraints). Verify with `terraform fmt` only; the operator applies, following the ordering in spec §7.

**Files:**
- Rename: `terraform/prod/lambda_email.tf` → `terraform/prod/lambda_mastra_handler.tf`; inside, resource labels `email_parser` → `mastra_handler` and name strings `-email-parser` → `-mastra-handler`
- Modify: `terraform/bootstrap/ecr.tf`, `terraform/bootstrap/codebuild.tf`, `terraform/bootstrap/iam.tf` (resource label + name `email_parser`/`-email-parser` → `mastra_handler`/`-mastra-handler`)
- Modify: `terraform/prod/data.tf` (the `aws_ecr_repository.email_parser` data lookup → `mastra_handler`)
- Modify: `terraform/prod/outputs.tf` (references to `data.aws_ecr_repository.email_parser`, the `email_parser_ecr_repo` output, and the `-email-parser` strings in operator-steps text)
- Modify: `terraform/prod/secrets_email.tf` (secret name `hwh/email-llm-api-key` → `hwh/mastra-handler-llm-api-key`; description)
- Modify: `terraform/prod/frontend.tf` (second origin + lambda OAC + ordered behavior)
- Create: `terraform/prod/posters.tf` (posters bucket + Function URL + permissions)

- [ ] **Step 1: Rename the bootstrap ECR + CodeBuild + IAM references**

In `terraform/bootstrap/ecr.tf`: rename resource `aws_ecr_repository.email_parser` → `aws_ecr_repository.mastra_handler` and `aws_ecr_lifecycle_policy.email_parser` → `...mastra_handler`; change `name = "${var.app_name_prefix}-email-parser"` → `"${var.app_name_prefix}-mastra-handler"`; update the `repository = aws_ecr_repository.email_parser.name` reference in the lifecycle policy to `.mastra_handler.name`; update the leading comment ("email-parser Lambda's container image" → "mastra-handler Lambda's container image", and `data.aws_ecr_repository.email_parser` → `...mastra_handler`).

In `terraform/bootstrap/codebuild.tf` (lines ~222–257): update the comment, and the two env values:
```hcl
      value = aws_ecr_repository.mastra_handler.name
```
```hcl
      value = "${var.app_name_prefix}-mastra-handler"
```

In `terraform/bootstrap/iam.tf` (lines ~255–284): update the comment, `resources = [aws_ecr_repository.mastra_handler.arn]`, and the function ARN resource `...:function:${var.app_name_prefix}-mastra-handler`.

- [ ] **Step 2: Rename the prod data lookup + outputs + secret**

In `terraform/prod/data.tf`: rename the `data "aws_ecr_repository" "email_parser"` block to `"mastra_handler"` and its `name` to `"${var.app_name_prefix}-mastra-handler"`; update the comment.

In `terraform/prod/outputs.tf`: rename output `email_parser_ecr_repo` → `mastra_handler_ecr_repo`; update its `value = data.aws_ecr_repository.mastra_handler.repository_url`; and update the `-email-parser` occurrences in the `email_post_apply_steps` text to `-mastra-handler` (and the ECR repo url reference).

In `terraform/prod/secrets_email.tf`: change `name = "${var.app_name_prefix}/email-llm-api-key"` → `"${var.app_name_prefix}/mastra-handler-llm-api-key"`; update the description and the example `--secret-id` comment. (Leave resource labels `email_llm_key`/`email_llm_key_placeholder` as-is to minimize churn — only the AWS-visible `name` changes. Update the `aws secretsmanager put-secret-value` example in `outputs.tf` to the new secret id.)

- [ ] **Step 3: Rename the Lambda Terraform file + resources + bump runtime**

```bash
cd /Users/wmyers/data/heres-whats-happening
git mv terraform/prod/lambda_email.tf terraform/prod/lambda_mastra_handler.tf
```
In `terraform/prod/lambda_mastra_handler.tf`:
- Rename every resource/data label `email_parser` → `mastra_handler` (function, role, assume policy, policy doc, role policy, DLQ, invoke config, S3 permission, alarm) and every `${var.app_name_prefix}-email-parser` → `-mastra-handler`. Update the `var.email_parser_image_tag` variable → `var.mastra_handler_image_tag` (and `default = "bootstrap"`), and the `image_uri` reference to `data.aws_ecr_repository.mastra_handler.repository_url`.
- Change `timeout = 120` → `timeout = 300` and `memory_size = 1024` → `memory_size = 1536`.
- Add to the `environment.variables` block:
```hcl
      POSTERS_BUCKET     = aws_s3_bucket.posters.bucket
      MAX_IMAGE_ATTEMPTS = "3"
      MAX_SVG_ATTEMPTS   = "3"
```
- Add an S3 write statement to `data "aws_iam_policy_document" "mastra_handler"`:
```hcl
  statement {
    sid       = "WritePosters"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.posters.arn}/*"]
  }
```
- Keep the `aws_lambda_permission.allow_s3_invoke` (renamed label) and the S3 notification wiring in `s3_inbound.tf` pointing at `aws_lambda_function.mastra_handler.arn` (update that reference in `s3_inbound.tf`).

Also update `terraform/prod/s3_inbound.tf`: the `aws_s3_bucket_notification.inbound_email` `lambda_function_arn = aws_lambda_function.mastra_handler.arn` and `depends_on = [aws_lambda_permission.allow_s3_invoke]` (the permission resource is now under the `mastra_handler` label — adjust the reference if its address changed).

- [ ] **Step 4: Create the posters bucket + Function URL + CloudFront invoke permission**

Create `terraform/prod/posters.tf`:
```hcl
# Generated concert posters (svg + png). Private; served via presigned URLs.
resource "aws_s3_bucket" "posters" {
  bucket = "${var.app_name_prefix}-posters-${data.aws_caller_identity.current.account_id}"
  tags   = { App = var.app_name_prefix }
}

resource "aws_s3_bucket_public_access_block" "posters" {
  bucket                  = aws_s3_bucket.posters.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "posters" {
  bucket = aws_s3_bucket.posters.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Function URL for the poster path. AWS_IAM so only CloudFront (OAC, SigV4) can call it.
# RESPONSE_STREAM lets the workflow run past CloudFront's buffered-origin read timeout.
resource "aws_lambda_function_url" "mastra_handler" {
  function_name      = aws_lambda_function.mastra_handler.function_name
  authorization_type = "AWS_IAM"
  invoke_mode        = "RESPONSE_STREAM"
}

# Allow the CloudFront distribution (frontend, extended in frontend.tf) to invoke the URL.
resource "aws_lambda_permission" "allow_cloudfront_invoke_url" {
  statement_id          = "AllowCloudFrontInvokeFunctionUrl"
  action                = "lambda:InvokeFunctionUrl"
  function_name         = aws_lambda_function.mastra_handler.function_name
  principal             = "cloudfront.amazonaws.com"
  source_arn            = aws_cloudfront_distribution.frontend.arn
  function_url_auth_type = "AWS_IAM"
}
```

- [ ] **Step 5: Extend the frontend CloudFront distribution with the poster origin + behavior**

In `terraform/prod/frontend.tf`:

(a) Add a lambda OAC near the existing one:
```hcl
resource "aws_cloudfront_origin_access_control" "poster_fn" {
  name                              = "${var.app_name_prefix}-poster-fn-oac"
  origin_access_control_origin_type = "lambda"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}
```

(b) Inside `resource "aws_cloudfront_distribution" "frontend"`, add a second `origin` block (the Function URL host — strip the scheme/trailing slash):
```hcl
  origin {
    domain_name              = replace(replace(aws_lambda_function_url.mastra_handler.function_url, "https://", ""), "/", "")
    origin_id                = "lambda-poster"
    origin_access_control_id = aws_cloudfront_origin_access_control.poster_fn.id
    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }
```

(c) Add an `ordered_cache_behavior` (uses AWS-managed policy IDs: `CachingDisabled` = `4135ea2d-6df8-44a3-9df3-4b5a84be39ad`, `AllViewerExceptHostHeader` = `b689b0a8-53d0-40ab-baf2-68738e2966ac`):
```hcl
  ordered_cache_behavior {
    path_pattern             = "/api/poster*"
    target_origin_id         = "lambda-poster"
    viewer_protocol_policy   = "redirect-to-https"
    allowed_methods          = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad" # Managed-CachingDisabled
    origin_request_policy_id = "b689b0a8-53d0-40ab-baf2-68738e2966ac" # Managed-AllViewerExceptHostHeader
  }
```

(d) Add a comment above the existing distribution-wide `custom_error_response` blocks documenting the coupling (spec §8):
```hcl
  # NOTE (spec §8): these 403/404 -> index.html rewrites are distribution-wide and
  # also apply to /api/poster*. The poster handler therefore emits ONLY 400/422/500.
```

- [ ] **Step 6: Format-check the Terraform**

```bash
cd /Users/wmyers/data/heres-whats-happening/terraform
terraform fmt -recursive
git diff --stat
```
Expected: files reformatted in place, no syntax errors from `fmt`. **Do not run** `init`/`validate`/`plan`/`apply` — those are operator steps (see spec §7 ordering).

- [ ] **Step 7: Commit**

```bash
cd /Users/wmyers/data/heres-whats-happening
git add -A
git commit -m "infra: rename email-parser -> mastra-handler; add posters bucket, function url, cloudfront /api/poster"
```

---

### Task 13: CI test list + README wrap-up

**Files:**
- Modify: `ci/buildspec-lambda.yml` (add the new unit tests to the `vitest run` list)
- Modify: `README.md` (document the poster endpoint, briefly)

- [ ] **Step 1: Add the new tests to the CI vitest list**

In `ci/buildspec-lambda.yml`, extend the `pnpm vitest run ...` line to include the new deterministic + core tests (keep excluding `sqs.test.ts` and `handler.e2e.test.ts`):
```
      - pnpm vitest run src/schema.test.ts src/hash.test.ts src/map.test.ts src/email.test.ts src/extractor.test.ts src/handler.test.ts src/poster-schema.test.ts src/poster.test.ts src/poster-sink.test.ts src/handler.poster.test.ts src/mastra/tools/web-scrape.tool.test.ts src/mastra/tools/svg-parse.tool.test.ts src/mastra/tools/rasterize.tool.test.ts
```

- [ ] **Step 2: Document the endpoint in README**

Add a short subsection to `README.md` under the lambda/mastra-handler description: that the same Lambda serves a poster endpoint at `POST /api/poster` (via CloudFront → Lambda Function URL) taking `{ performer, venue, date }` and returning `{ svg, svgUrl, pngUrl }`, writing artifacts to the posters S3 bucket; web scraping is currently stubbed.

- [ ] **Step 3: Run the full local unit suite one final time**

```bash
cd lambda/mastra-handler
pnpm typecheck
pnpm vitest run src/poster-schema.test.ts src/poster.test.ts src/poster-sink.test.ts src/handler.poster.test.ts src/handler.test.ts src/mastra/tools/web-scrape.tool.test.ts src/mastra/tools/svg-parse.tool.test.ts src/mastra/tools/rasterize.tool.test.ts
```
Expected: typecheck clean; all PASS.

- [ ] **Step 4: Commit**

```bash
cd /Users/wmyers/data/heres-whats-happening
git add -A
git commit -m "ci+docs: add poster tests to lambda CI; document /api/poster endpoint"
```

---

## Operator follow-up (not part of implementation; from spec §7)

Apply order, run by the operator (not in this environment):
1. `terraform apply` the **bootstrap** stack → creates the new `hwh-mastra-handler` ECR repo.
2. Run the lambda CI lane (or manual build/push) → push an image to the new repo.
3. Re-seed the renamed secret: `aws secretsmanager put-secret-value --secret-id hwh/mastra-handler-llm-api-key --secret-string "<key>"`.
4. `terraform apply` the **prod** stack → recreates the Lambda/role/DLQ/alarm under the new name, re-wires the S3 notification, and creates the Function URL + CloudFront `/api/poster*` wiring.
5. Smoke test: `curl -X POST https://<domain>/api/poster -d '{"performer":"...","venue":"...","date":"..."}'`.
