# File-Backed Poster Artifacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move every large binary out of Mastra workflow state into files on disk, return URLs instead of bytes from `POST /api/poster`, and skip the whole workflow when a poster already exists in S3.

**Architecture:** A new `artifact-store.ts` owns all filesystem access, writing run-scoped files under `{tmpdir}/hwh-poster/{runId}/`. Workflow state carries `ArtifactRef`/`ImageRef` (path + contentType + byte count) instead of base64. `S3PosterSink` streams those files to S3 and writes a provenance sidecar last, which doubles as the cache index for a repeat-request short-circuit.

**Tech Stack:** TypeScript ES2022, Zod 3, Mastra `@mastra/core` 1.51, Vitest 2, `@aws-sdk/client-s3` (no new dependencies).

**Spec:** `docs/superpowers/specs/2026-08-09-file-backed-poster-artifacts-design.md`

## Global Constraints

- **DO NOT COMMIT.** The user reviews and tests by hand. Every task ends in verification, never `git commit`. Leave the working tree dirty.
- Working directory for all commands: `lambda/mastra-handler`.
- Relative imports use the `.js` extension (`moduleResolution: bundler`).
- Never throw out of a workflow step — convert failures to state, per `rasterize.tool.ts:28`.
- Bytes may exist transiently in memory (the vision agent needs the image, resvg needs an SVG string). They must never be **stored** in workflow state, copied by a seed map, or persisted into a run snapshot.
- Presigned URL expiry stays **3600s**.
- No new npm dependencies. S3 streaming uses `createReadStream` + `ContentLength`.
- Tests never touch the network or real S3. `S3PosterSink` tests inject a fake client with a `send` method.
- No new env vars. `artifactStore` takes a `root` **parameter** so tests can point at a scratch dir.

## File Structure

**Create:**
| File | Responsibility |
|---|---|
| `src/mastra/tools/artifact-store.ts` | The only module that touches the filesystem. Write/read run-scoped files, sweep stale run dirs. |
| `src/mastra/tools/artifact-store.test.ts` | |
| `src/poster-sink.s3.test.ts` | Covers `S3PosterSink` with a fake S3 client — currently untested. |

**Modify:** `src/mastra/tools/band-image.ts`, `src/mastra/tools/wikimedia.tool.ts`, `src/mastra/tools/rasterize.tool.ts`, `src/mastra/tools/rasterize.tool.test.ts`, `src/mastra/tools/wikimedia.tool.test.ts`, `src/mastra/workflows/poster.schemas.ts`, `src/mastra/workflows/judge-band-image.step.ts` (+test), `src/mastra/workflows/compose-poster.step.ts` (+test), `src/mastra/workflows/poster.workflow.ts` (+test), `src/poster-schema.ts`, `src/poster-sink.ts`, `src/poster-sink.test.ts`, `src/poster.ts`, `src/poster.test.ts`, `src/handler.poster.test.ts`, `README.md`

**Deviation from the spec, deliberate:** the spec sketches `ArtifactRefSchema`/`ImageRefSchema` inside `artifact-store.ts`. This plan puts the **schemas** in `band-image.ts` (which is already the pure, I/O-free schema module) and only the **I/O** in `artifact-store.ts`. That preserves the existing invariant that `band-image.ts` has no side effects, and avoids workflow schemas importing a filesystem module.

---

### Task 1: Artifact schemas and store

**Files:**
- Modify: `src/mastra/tools/band-image.ts`
- Create: `src/mastra/tools/artifact-store.ts`, `src/mastra/tools/artifact-store.test.ts`

**Interfaces:**
- Consumes: `ImageCreditSchema` (existing, in `band-image.ts`).
- Produces:
  - `ArtifactRefSchema` / `ArtifactRef = { path: string; contentType: string; bytes: number }` — `bytes` is the **size**, not the content.
  - `ImageRefSchema` / `ImageRef = ArtifactRef & { width: number; height: number; sourceUrl?: string; credit?: ImageCredit }` — replaces `BandImageSchema`, which is deleted.
  - `artifactStore(runId: string, opts?: { root?: string }): ArtifactStore` where `ArtifactStore = { dir: string; write(name: string, data: Buffer, contentType: string): Promise<ArtifactRef>; read(ref: { path: string }): Promise<Buffer> }`
  - `defaultRoot(): string`

- [ ] **Step 1: Write the failing test**

Create `src/mastra/tools/artifact-store.test.ts`:

```ts
import { mkdir, mkdtemp, readFile, stat, utimes, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { artifactStore, defaultRoot } from "./artifact-store.js";

let root: string;
beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "artifact-store-test-"));
});

describe("artifactStore", () => {
  it("writes a file under a run-scoped directory and returns a ref", async () => {
    const store = artifactStore("run-abc", { root });
    const data = Buffer.from([0xff, 0xd8, 0xff, 0xe0]);

    const ref = await store.write("band-1.jpg", data, "image/jpeg");

    expect(ref.path).toBe(join(root, "run-abc", "band-1.jpg"));
    expect(ref.contentType).toBe("image/jpeg");
    expect(ref.bytes).toBe(4); // SIZE, not content
    expect(await readFile(ref.path)).toEqual(data);
  });

  it("round-trips through read", async () => {
    const store = artifactStore("run-abc", { root });
    const data = Buffer.from("<svg/>", "utf8");
    const ref = await store.write("poster-1.svg", data, "image/svg+xml");
    expect(await store.read(ref)).toEqual(data);
  });

  it("keeps different runs in different directories", async () => {
    const a = await artifactStore("run-a", { root }).write("x.png", Buffer.from("a"), "image/png");
    const b = await artifactStore("run-b", { root }).write("x.png", Buffer.from("b"), "image/png");

    expect(a.path).not.toBe(b.path);
    expect(await readFile(a.path, "utf8")).toBe("a");
    expect(await readFile(b.path, "utf8")).toBe("b");
  });

  it("exposes the run directory so callers can clean it up", () => {
    expect(artifactStore("run-abc", { root }).dir).toBe(join(root, "run-abc"));
  });

  it("sweeps run directories older than an hour, sparing fresh ones", async () => {
    const stale = join(root, "stale-run");
    const fresh = join(root, "fresh-run");
    await mkdir(stale, { recursive: true });
    await mkdir(fresh, { recursive: true });
    await writeFile(join(stale, "f"), "x");
    await writeFile(join(fresh, "f"), "x");
    const old = new Date(Date.now() - 2 * 60 * 60 * 1000);
    await utimes(stale, old, old);

    // The sweep runs lazily on first write.
    await artifactStore("new-run", { root }).write("a.png", Buffer.from("a"), "image/png");

    expect(existsSync(stale)).toBe(false);
    expect(existsSync(fresh)).toBe(true);
  });

  it("survives a root that does not exist yet", async () => {
    const missing = join(root, "nested", "deeper");
    const ref = await artifactStore("run-abc", { root: missing }).write("a.png", Buffer.from("a"), "image/png");
    expect((await stat(ref.path)).size).toBe(1);
  });
});

describe("defaultRoot", () => {
  it("lives under the OS temp dir", () => {
    expect(defaultRoot().startsWith(tmpdir())).toBe(true);
    expect(defaultRoot()).toContain("hwh-poster");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/artifact-store.test.ts`
Expected: FAIL — `Cannot find module './artifact-store.js'`

- [ ] **Step 3: Add the schemas**

In `src/mastra/tools/band-image.ts`, **delete** `BandImageSchema` and its `BandImage` type, and add in their place:

```ts
// A file on local disk. `bytes` is the SIZE, not the content — it feeds S3's
// ContentLength so artifacts can stream from disk without being buffered.
export const ArtifactRefSchema = z.object({
  path: z.string(),
  contentType: z.string(),
  bytes: z.number(),
});
export type ArtifactRef = z.infer<typeof ArtifactRefSchema>;

// The band photo, as a reference rather than 335 KB of base64. Replaces the
// former BandImageSchema; every field except imageBase64 -> path/bytes is the same.
export const ImageRefSchema = ArtifactRefSchema.extend({
  width: z.number(),
  height: z.number(),
  sourceUrl: z.string().optional(),
  credit: ImageCreditSchema.optional(),
});
export type ImageRef = z.infer<typeof ImageRefSchema>;
```

- [ ] **Step 4: Write the store**

Create `src/mastra/tools/artifact-store.ts`:

```ts
import { mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ArtifactRef } from "./band-image.js";

const ROOT_NAME = "hwh-poster";
const SWEEP_MAX_AGE_MS = 60 * 60 * 1000;

/** Roots already swept in this process — the sweep is a backstop, not per-call work. */
const sweptRoots = new Set<string>();

export interface ArtifactStore {
  /** The run-scoped directory. Callers delete this to clean up a whole run. */
  readonly dir: string;
  write(name: string, data: Buffer, contentType: string): Promise<ArtifactRef>;
  read(ref: { path: string }): Promise<Buffer>;
}

export function defaultRoot(): string {
  return join(tmpdir(), ROOT_NAME);
}

/**
 * Delete run directories older than an hour. Best-effort: a local dev loop
 * should not fill tmpdir, but a failed sweep must never fail a request.
 */
async function sweep(root: string): Promise<void> {
  if (sweptRoots.has(root)) return;
  sweptRoots.add(root);
  try {
    const entries = await readdir(root, { withFileTypes: true });
    const cutoff = Date.now() - SWEEP_MAX_AGE_MS;
    await Promise.all(
      entries
        .filter((e) => e.isDirectory())
        .map(async (e) => {
          const dir = join(root, e.name);
          try {
            const info = await stat(dir);
            if (info.mtimeMs < cutoff) await rm(dir, { recursive: true, force: true });
          } catch {
            // Raced with another sweep, or vanished. Nothing to do.
          }
        }),
    );
  } catch {
    // Root does not exist yet. Nothing to sweep.
  }
}

/**
 * Files for one workflow run. `runId` comes from the step's execute params, so
 * every artifact of a run lands together and Studio traces point at real paths.
 */
export function artifactStore(runId: string, opts: { root?: string } = {}): ArtifactStore {
  const root = opts.root ?? defaultRoot();
  const dir = join(root, runId);
  let ready: Promise<void> | undefined;

  const ensure = (): Promise<void> =>
    (ready ??= (async () => {
      await sweep(root);
      await mkdir(dir, { recursive: true });
    })());

  return {
    dir,
    async write(name, data, contentType) {
      await ensure();
      const path = join(dir, name);
      await writeFile(path, data);
      return { path, contentType, bytes: data.byteLength };
    },
    async read(ref) {
      return readFile(ref.path);
    },
  };
}
```

- [ ] **Step 5: Run tests**

Run: `pnpm vitest run src/mastra/tools/artifact-store.test.ts src/mastra/tools/band-image.test.ts`
Expected: artifact-store PASS (7 tests). `band-image.test.ts` FAILS on the deleted `BandImageSchema` import — fix it in the next step.

- [ ] **Step 6: Update the band-image test**

In `src/mastra/tools/band-image.test.ts`, replace the `BandImageSchema` import and its `describe` block with:

```ts
describe("ImageRefSchema", () => {
  it("carries a path and a byte count instead of base64", () => {
    const ref = ImageRefSchema.parse({
      path: "/tmp/hwh-poster/run-1/band-1.jpg",
      contentType: "image/jpeg",
      bytes: 257_432,
      width: 1080,
      height: 810,
    });
    expect(ref.path).toContain("band-1.jpg");
    expect(ref.bytes).toBe(257_432);
    expect("imageBase64" in ref).toBe(false);
    expect(ref.credit).toBeUndefined();
  });
});
```

Update the import line to `import { ArtistMatchSchema, ImageCreditSchema, ImageRefSchema, USER_AGENT } from "./band-image.js";`

- [ ] **Step 7: Verify, do not commit**

Run: `pnpm vitest run src/mastra/tools/artifact-store.test.ts src/mastra/tools/band-image.test.ts`
Expected: PASS. Typecheck will still fail elsewhere (consumers of `BandImageSchema`) — Tasks 2-5 fix those. `git status --short`; leave uncommitted.

---

### Task 2: rasterizeSvg returns bytes

**Files:**
- Modify: `src/mastra/tools/rasterize.tool.ts`, `src/mastra/tools/rasterize.tool.test.ts`

**Interfaces:**
- Consumes: nothing new.
- Produces: `RasterizeResult = { ok: boolean; png?: Buffer; width?: number; height?: number; error?: string }`. `pngBase64` is gone. `rasterizeTool` (the Studio wrapper) still emits base64.

Why: the sink used to immediately undo `rasterizeSvg`'s base64 with `Buffer.from(pngBase64, "base64")`. With a file destination that round-trip is pure waste.

- [ ] **Step 1: Update the test**

In `src/mastra/tools/rasterize.tool.test.ts`, change assertions from `pngBase64` to `png`. The PNG magic-number check becomes:

```ts
    expect(res.ok).toBe(true);
    expect(res.png).toBeInstanceOf(Buffer);
    // PNG signature: 89 50 4E 47
    expect(res.png!.subarray(0, 4).toString("hex")).toBe("89504e47");
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/rasterize.tool.test.ts`
Expected: FAIL — `res.png` is undefined.

- [ ] **Step 3: Change the return type**

In `src/mastra/tools/rasterize.tool.ts`:

```ts
export type RasterizeResult = { ok: boolean; png?: Buffer; width?: number; height?: number; error?: string };
```

In `rasterizeSvg`, replace the success return with:

```ts
    return {
      ok: true,
      png: Buffer.from(rendered.asPng()),
      width: rendered.width,
      height: rendered.height,
    };
```

In `rasterizeTool`, convert at the boundary so Studio still shows something useful:

```ts
  execute: async ({ svg }) => {
    const res = await rasterizeSvg(svg);
    // Studio cannot render a Buffer; base64 only at this boundary.
    return { ok: res.ok, pngBase64: res.png?.toString("base64"), width: res.width, height: res.height, error: res.error };
  },
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/rasterize.tool.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Verify, do not commit**

`git status --short`; leave uncommitted.

---

### Task 3: Loop state and the judge step

**Files:**
- Modify: `src/mastra/tools/wikimedia.tool.ts`, `src/mastra/tools/wikimedia.tool.test.ts`, `src/mastra/workflows/poster.schemas.ts`, `src/mastra/workflows/judge-band-image.step.ts`, `src/mastra/workflows/judge-band-image.step.test.ts`

**Interfaces:**
- Consumes: `ImageRef`, `ArtifactRef` (Task 1), `artifactStore` (Task 1).
- Produces:
  - `fetchImageBytes(candidate: ImageCandidate): Promise<Buffer>` — was returning a `BandImage`, now returns raw bytes; the step decides where they land.
  - `ImageLoopStateSchema.image` is now `ImageRefSchema.optional()`.

- [ ] **Step 1: Update the wikimedia test**

In `src/mastra/tools/wikimedia.tool.test.ts`, replace the `fetchImageBytes` describe block body with:

```ts
  it("returns the raw bytes so the caller decides where they land", async () => {
    const bytes = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]);
    const { c } = client([{ match: /upload\.wikimedia/, body: bytes }]);
    expect(await c.fetchImageBytes(candidate)).toEqual(bytes);
  });

  it("throws on a non-2xx so the caller can count it as a failed attempt", async () => {
    const { c } = client([{ match: /upload\.wikimedia/, status: 404, json: {} }]);
    await expect(c.fetchImageBytes(candidate)).rejects.toThrow(/404/);
  });
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/mastra/tools/wikimedia.tool.test.ts`
Expected: FAIL — the returned object is a `BandImage`, not a Buffer.

- [ ] **Step 3: Change `fetchImageBytes`**

In `src/mastra/tools/wikimedia.tool.ts`, change the interface member and the implementation:

```ts
export interface WikimediaClient {
  resolveImageCandidates(mbid: string, opts?: { artistName?: string }): Promise<ImageCandidate[]>;
  /** Raw bytes of the candidate's thumbnail. The caller decides where they land. */
  fetchImageBytes(candidate: ImageCandidate): Promise<Buffer>;
}
```

```ts
    async fetchImageBytes(candidate) {
      const res = await doFetch(candidate.url, {
        headers: { "User-Agent": userAgent },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      if (!res.ok) throw new Error(`commons ${res.status} fetching ${candidate.file}`);
      return Buffer.from(await res.arrayBuffer());
    },
```

Update the module-level convenience export and remove the now-unused `BandImage` import:

```ts
export function fetchImageBytes(candidate: ImageCandidate): Promise<Buffer> {
  return wikimediaClient.fetchImageBytes(candidate);
}
```

- [ ] **Step 4: Point loop state at `ImageRefSchema`**

In `src/mastra/workflows/poster.schemas.ts`, change the import and the two `image` fields:

```ts
import { ArtistMatchSchema, ImageCreditSchema, ImageRefSchema } from "../tools/band-image.js";
```

In `ImageLoopStateSchema` and `PosterLoopStateSchema`, replace `image: BandImageSchema.optional(),` with:

```ts
  image: ImageRefSchema.optional(),
```

- [ ] **Step 5: Write the failing judge-step test**

In `src/mastra/workflows/judge-band-image.step.test.ts`, replace the `image` fixture and add coverage. Change the top of the file to mock the store as well:

```ts
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { readFile } from "node:fs/promises";

const fetchImageBytes = vi.fn<(c: ImageCandidate) => Promise<Buffer>>();
const generate = vi.fn();
let root: string;

vi.mock("../tools/wikimedia.tool.js", () => ({ fetchImageBytes }));
vi.mock("../agents/image-analysis.agent.js", () => ({
  imageAnalysisAgent: { generate },
  ImageAnalysisSchema: {},
}));
```

Delete the old `const image: BandImage = ...` fixture. Replace the run helper so it supplies a `runId` and a scratch root:

```ts
const PHOTO = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]);

const run = (data: Record<string, unknown>) =>
  judgeBandImageStep.execute({ inputData: data, runId: "run-test", artifactRoot: root } as never) as Promise<any>;

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "judge-step-test-"));
  fetchImageBytes.mockReset();
  generate.mockReset();
  fetchImageBytes.mockResolvedValue(PHOTO);
});
```

Then replace the "accepts a good candidate" test and add two new ones:

```ts
  it("writes the photo to a file and stores a ref, not base64", async () => {
    generate.mockResolvedValue({ object: { acceptable: true, reason: "clear band photo", dominantColors: ["#111"] } });

    const out = await run(base);

    expect(out.accepted).toBe(true);
    expect(out.attempts).toBe(1);
    expect(out.image.path).toContain(join("run-test", "band-1.jpg"));
    expect(out.image.bytes).toBe(PHOTO.byteLength);
    expect(out.image.width).toBe(1080);
    expect("imageBase64" in out.image).toBe(false);
    expect(await readFile(out.image.path)).toEqual(PHOTO);
  });

  it("names the file after the attempt so a trace says which one it was", async () => {
    generate.mockResolvedValue({ object: { acceptable: false, reason: "no", dominantColors: [] } });
    const out = await run({ ...base, attempts: 2, candidateIndex: 1 });
    expect(out.image.path).toContain("band-3.jpg");
  });

  it("carries the candidate's credit onto the ref", async () => {
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    const out = await run(base);
    expect(out.image.credit.attributionRequired).toBe(true);
    expect(out.image.sourceUrl).toContain("commons.wikimedia.org");
  });

  it("counts a failed store write as a used attempt and never throws", async () => {
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    // A path that cannot be created: the root is an existing FILE, not a dir.
    const { mkdtemp, writeFile } = await import("node:fs/promises");
    const blocked = join(await mkdtemp(join(tmpdir(), "blocked-")), "not-a-dir");
    await writeFile(blocked, "x");

    const out = await (judgeBandImageStep.execute({
      inputData: base,
      runId: "run-test",
      artifactRoot: blocked,
    } as never) as Promise<any>);

    expect(out.attempts).toBe(1);
    expect(out.candidateIndex).toBe(1);
    expect(out.accepted).toBe(false);
    expect(out.reason).toContain("could not store");
    expect(generate).not.toHaveBeenCalled();
  });
```

Update the remaining tests that referenced the old `image` fixture: `expect(out.image).toEqual(image)` becomes `expect(out.image.path).toBeTruthy()`.

- [ ] **Step 6: Run to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/judge-band-image.step.test.ts`
Expected: FAIL — `out.image.path` is undefined.

- [ ] **Step 7: Rewrite the judge step**

Replace `src/mastra/workflows/judge-band-image.step.ts` execute body. Full file:

```ts
import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { ImageAnalysisSchema, imageAnalysisAgent } from "../agents/image-analysis.agent.js";
import { artifactStore } from "../tools/artifact-store.js";
import type { ArtistMatch } from "../tools/band-image.js";
import { fetchImageBytes } from "../tools/wikimedia.tool.js";
import { ImageLoopStateSchema } from "./poster.schemas.js";

type ImageAnalysis = z.infer<typeof ImageAnalysisSchema>;

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** "La Luz, US rock band, Group, US, formed 2012" — far more judgeable than "la luz". */
function describeArtist(artist: ArtistMatch | undefined, fallback: string): string {
  if (!artist) return fallback;
  return [
    artist.name,
    artist.disambiguation,
    artist.type,
    artist.country,
    artist.beginYear ? `formed ${artist.beginYear}` : undefined,
  ]
    .filter(Boolean)
    .join(", ");
}

// One iteration: fetch the indexed candidate's bytes, write them to the run's
// artifact directory, then a vision agent judges them. The BYTES never enter
// workflow state — only an ImageRef does. `candidateIndex` advances on every
// iteration regardless of verdict, so the next attempt sees a NEW photo.
export const judgeBandImageStep = createStep({
  id: "judge-band-image",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData, runId, artifactRoot }: any) => {
    const candidate = inputData.candidates[inputData.candidateIndex];
    // Cheap short-circuit: nothing to judge, so spend no attempt and no LLM call.
    if (!candidate) {
      return { ...inputData, accepted: false };
    }

    const attempts = inputData.attempts + 1;
    const candidateIndex = inputData.candidateIndex + 1;

    let bytes: Buffer;
    try {
      bytes = await fetchImageBytes(candidate);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `could not fetch ${candidate.file}: ${message(e)}`,
      };
    }

    let image;
    try {
      const store = artifactStore(runId, { root: artifactRoot });
      const ext = candidate.contentType === "image/png" ? "png" : "jpg";
      const ref = await store.write(`band-${attempts}.${ext}`, bytes, candidate.contentType);
      image = {
        ...ref,
        width: candidate.width,
        height: candidate.height,
        sourceUrl: candidate.credit.descriptionUrl || candidate.url,
        credit: candidate.credit,
      };
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `could not store ${candidate.file}: ${message(e)}`,
      };
    }

    const who = describeArtist(inputData.artist, inputData.performer);
    const res = await imageAnalysisAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: bytes, mimeType: candidate.contentType },
          { type: "text", text: `Performer: ${who}. Is this a usable photo of this performer for a concert poster?` },
        ],
      },
    ]);

    const analysis = res.object as ImageAnalysis | undefined;
    if (!analysis) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: "image analysis returned no result",
        image,
      };
    }

    return {
      ...inputData,
      attempts,
      candidateIndex,
      accepted: analysis.acceptable,
      reason: analysis.reason,
      image,
      colors: analysis.dominantColors ?? [],
    };
  },
});
```

Note the `: any` on the destructure: Mastra's execute params include `runId` but not a custom `artifactRoot`. `artifactRoot` is a test-only injection passed through the same object; production never sets it, so `artifactStore` falls back to `defaultRoot()`.

- [ ] **Step 8: Run tests**

Run: `pnpm vitest run src/mastra/workflows/judge-band-image.step.test.ts src/mastra/tools/wikimedia.tool.test.ts`
Expected: PASS.

- [ ] **Step 9: Verify, do not commit**

`git status --short`; leave uncommitted.

---

### Task 4: Compose step writes SVG and PNG to files

**Files:**
- Modify: `src/mastra/workflows/poster.schemas.ts`, `src/mastra/workflows/compose-poster.step.ts`, `src/mastra/workflows/compose-poster.step.test.ts`

**Interfaces:**
- Consumes: `artifactStore`, `ArtifactRef` (Task 1); `rasterizeSvg` returning `png?: Buffer` (Task 2); `ImageRef` state (Task 3).
- Produces: `PosterLoopStateSchema` with `authoredSvg?: string` and `render?: { svg: ArtifactRef; png: ArtifactRef }`; `svg` and `pngBase64` removed.

- [ ] **Step 1: Update `PosterLoopStateSchema`**

In `src/mastra/workflows/poster.schemas.ts`, inside `PosterLoopStateSchema`, delete `svg` and `pngBase64` and add:

```ts
  // The SVG BEFORE substitution: still contains the literal __BAND_IMAGE__, so
  // it is ~2 KB and is the thing worth reading in Studio when a poster is wrong.
  authoredSvg: z.string().optional(),
  render: z.object({ svg: ArtifactRefSchema, png: ArtifactRefSchema }).optional(),
```

Add `ArtifactRefSchema` to the `band-image.js` import.

- [ ] **Step 2: Write the failing test**

In `src/mastra/workflows/compose-poster.step.test.ts`, add the store plumbing at the top:

```ts
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

let root: string;
```

Change the `base` fixture's `image` and the `run` helper:

```ts
const base = {
  performer: "La Luz",
  venue: "Occidental Square",
  date: "Thursday, August 20",
  imageOk: true,
  colors: ["#111"],
  attempts: 0,
  accepted: false,
  image: { path: "", contentType: "image/jpeg", bytes: 4, width: 1080, height: 810 },
};

const PHOTO = Buffer.from([0xff, 0xd8, 0xff, 0xe0]);
const PNG = Buffer.from([0x89, 0x50, 0x4e, 0x47]);

const run = (data: Record<string, unknown>) =>
  composePosterStep.execute({ inputData: data, runId: "run-test", artifactRoot: root } as never) as Promise<any>;

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "compose-step-test-"));
  authorGenerate.mockReset();
  critiqueGenerate.mockReset();
  rasterizeSvg.mockReset();
  // Give the step a real band-photo file to read.
  const { artifactStore } = await import("../tools/artifact-store.js");
  const ref = await artifactStore("run-test", { root }).write("band-1.jpg", PHOTO, "image/jpeg");
  base.image = { ...ref, width: 1080, height: 810 };
});
```

Replace the "produces svg + png" test and add file-backed assertions:

```ts
  it("writes svg and png to files and stores refs, not blobs", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG, width: 1080, height: 1350 });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: true, critique: "bold and legible" } });

    const out = await run(base);

    expect(out.accepted).toBe(true);
    expect(out.render.svg.path).toContain(join("run-test", "poster-1.svg"));
    expect(out.render.png.path).toContain(join("run-test", "poster-1.png"));
    expect(out.render.png.bytes).toBe(PNG.byteLength);
    expect(out.render.svg.contentType).toBe("image/svg+xml");
    expect(await readFile(out.render.png.path)).toEqual(PNG);
    expect("pngBase64" in out).toBe(false);
  });

  it("keeps the SMALL authored svg in state, with the placeholder intact", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: true, critique: "ok" } });

    const out = await run(base);

    expect(out.authoredSvg).toBe(GOOD_SVG);
    expect(out.authoredSvg).toContain("__BAND_IMAGE__");
    // The substituted version, which inlines the photo, is on disk only.
    const written = await readFile(out.render.svg.path, "utf8");
    expect(written).toContain("data:image/jpeg;base64,");
    expect(written).not.toContain("__BAND_IMAGE__");
  });

  it("produces a critique when the band photo file cannot be read", async () => {
    const out = await run({ ...base, image: { ...base.image, path: join(root, "gone.jpg") } });

    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.critique).toContain("could not read the band image");
    expect(authorGenerate).not.toHaveBeenCalled();
  });
```

Update the remaining failure-path tests: they assert on `out.critique`, which is unchanged, but the `rasterizeSvg` mocks must return `{ ok: true, png: PNG }` rather than `pngBase64`.

- [ ] **Step 3: Run to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/compose-poster.step.test.ts`
Expected: FAIL — `out.render` is undefined.

- [ ] **Step 4: Rewrite the compose step**

Replace the `execute` in `src/mastra/workflows/compose-poster.step.ts`:

```ts
  execute: async ({ inputData, runId, artifactRoot }: any) => {
    // Cheap short-circuit: if image acquisition failed, do no LLM work.
    // `attempts` still advances so the loop stays bounded on THIS path too. The
    // dountil condition is `accepted || !imageOk || attempts >= MAX_SVG_ATTEMPTS`;
    // returning without advancing attempts would spin forever for any input that
    // reaches here with imageOk true.
    if (!inputData.imageOk || !inputData.image) {
      return {
        ...inputData,
        attempts: inputData.attempts + 1,
        accepted: false,
        critique: inputData.critique ?? "no usable band image was available to compose",
      };
    }

    const attempts = inputData.attempts + 1;
    const { image } = inputData;
    const store = artifactStore(runId, { root: artifactRoot });

    // Read the photo BEFORE spending an LLM call on authoring.
    let photo: Buffer;
    try {
      photo = await store.read(image);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        accepted: false,
        critique: `could not read the band image at ${image.path}: ${message(e)}`,
      };
    }

    // 1) Author the SVG (placeholder href for the image). Small — kept in state.
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
    const rawSvg = (authored.object as SvgAuthor | undefined)?.svg;
    if (!rawSvg) {
      return { ...inputData, attempts, accepted: false, critique: "SVG author returned no svg" };
    }

    // 2) Substitute the real image + validate well-formedness. The substituted
    //    string is transient — only the file keeps it.
    const dataUri = `data:${image.contentType};base64,${photo.toString("base64")}`;
    const parsed = substituteAndValidateSvg(rawSvg, dataUri);
    if (!parsed.ok) {
      return {
        ...inputData,
        attempts,
        accepted: false,
        authoredSvg: rawSvg,
        critique: `Fix the SVG so it is well-formed: ${parsed.error}`,
      };
    }

    // 3) Rasterize to PNG.
    const raster = await rasterizeSvg(parsed.svg);
    if (!raster.ok || !raster.png) {
      return {
        ...inputData,
        attempts,
        accepted: false,
        authoredSvg: rawSvg,
        critique: `The SVG did not render: ${raster.error}`,
      };
    }

    // 4) Persist both artifacts; state carries refs only.
    let render;
    try {
      render = {
        svg: await store.write(`poster-${attempts}.svg`, Buffer.from(parsed.svg, "utf8"), "image/svg+xml"),
        png: await store.write(`poster-${attempts}.png`, raster.png, "image/png"),
      };
    } catch (e) {
      return { ...inputData, attempts, accepted: false, authoredSvg: rawSvg, critique: `could not store the render: ${message(e)}` };
    }

    // 5) Critique the rendered poster.
    const critiqueRes = await posterCritiqueAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: raster.png, mimeType: "image/png" },
          { type: "text", text: `Intended poster — performer: ${inputData.performer}, venue: ${inputData.venue}, date: ${inputData.date}. Is this a cool, legible poster?` },
        ],
      },
    ]);
    const verdict = critiqueRes.object as PosterCritique | undefined;
    return {
      ...inputData,
      attempts,
      authoredSvg: rawSvg,
      render,
      accepted: verdict?.acceptable ?? false,
      critique: verdict?.critique ?? "critique returned no result",
    };
  },
```

Add these imports at the top of the file:

```ts
import { artifactStore } from "../tools/artifact-store.js";
```

and this helper beside the existing ones:

```ts
function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
```

- [ ] **Step 5: Run tests**

Run: `pnpm vitest run src/mastra/workflows/compose-poster.step.test.ts`
Expected: PASS (13 tests).

- [ ] **Step 6: Verify, do not commit**

`git status --short`; leave uncommitted.

---

### Task 5: Workflow wiring, output schema, and the size guard

**Files:**
- Modify: `src/mastra/workflows/poster.schemas.ts`, `src/mastra/workflows/poster.workflow.ts`, `src/mastra/workflows/poster.workflow.test.ts`

**Interfaces:**
- Consumes: everything from Tasks 1-4.
- Produces: `PosterWorkflowOutputSchema` with `render?: { svg: ArtifactRef; png: ArtifactRef }` and `artifactDir?: string`; `svg` and `pngBase64` removed.

- [ ] **Step 1: Update `PosterWorkflowOutputSchema`**

In `src/mastra/workflows/poster.schemas.ts`, replace `PosterWorkflowOutputSchema` with:

```ts
export const PosterWorkflowOutputSchema = z.object({
  ok: z.boolean(),
  render: z.object({ svg: ArtifactRefSchema, png: ArtifactRefSchema }).optional(),
  // The run's artifact directory, so the caller can delete it. Emitted on BOTH
  // branches: a failed run still has files worth cleaning up (and, from Studio,
  // worth inspecting).
  artifactDir: z.string().optional(),
  failureStage: z.enum(["image", "svg"]).optional(),
  reason: z.string().optional(),
  artist: ArtistMatchSchema.optional(),
  credit: ImageCreditSchema.optional(),
});
```

- [ ] **Step 2: Write the failing guard test**

Append to `src/mastra/workflows/poster.workflow.test.ts`:

```ts
describe("posterWorkflow keeps blobs OUT of state", () => {
  it("has no string field over 10 KB anywhere in the run record", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    compose.mockImplementation(async (s: Record<string, unknown>) => ({
      ...s,
      attempts: ((s.attempts as number) ?? 0) + 1,
      accepted: true,
      authoredSvg: "<svg>__BAND_IMAGE__</svg>",
      render: {
        svg: { path: "/tmp/run/poster-1.svg", contentType: "image/svg+xml", bytes: 340_000 },
        png: { path: "/tmp/run/poster-1.png", contentType: "image/png", bytes: 900_000 },
      },
    }));

    const result: any = await runWorkflow();

    // Walk the entire serialized run record looking for a smuggled blob.
    const offenders: Array<[string, number]> = [];
    const walk = (node: unknown, path: string) => {
      if (typeof node === "string") {
        if (node.length > 10_000) offenders.push([path, node.length]);
      } else if (Array.isArray(node)) {
        node.forEach((v, i) => walk(v, `${path}[${i}]`));
      } else if (node && typeof node === "object") {
        for (const [k, v] of Object.entries(node)) walk(v, `${path}.${k}`);
      }
    };
    walk(result.steps, "steps");
    walk(result.result, "result");

    expect(offenders).toEqual([]);
  });

  it("emits render refs and an artifactDir instead of svg/pngBase64", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    compose.mockImplementation(async (s: Record<string, unknown>) => ({
      ...s,
      attempts: ((s.attempts as number) ?? 0) + 1,
      accepted: true,
      render: {
        svg: { path: "/tmp/run/poster-1.svg", contentType: "image/svg+xml", bytes: 10 },
        png: { path: "/tmp/run/poster-1.png", contentType: "image/png", bytes: 20 },
      },
    }));

    const out: any = (await runWorkflow()).result;

    expect(out.ok).toBe(true);
    expect(out.render.png.path).toContain("poster-1.png");
    expect(out.artifactDir).toContain("run");
    expect("svg" in out).toBe(false);
    expect("pngBase64" in out).toBe(false);
  });

  it("emits artifactDir on a FAILURE too, so the caller can still clean up", async () => {
    searchArtists.mockResolvedValue([]);
    const out: any = (await runWorkflow()).result;
    expect(out.ok).toBe(false);
    expect(out.artifactDir).toBeTruthy();
  });
});
```

Also update the existing "returns svg + png + provenance on full success" test: replace the `svg`/`pngBase64` assertions with `expect(out.render.svg.path).toBeTruthy()`.

- [ ] **Step 3: Run to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/poster.workflow.test.ts`
Expected: FAIL — `out.render` and `out.artifactDir` are undefined.

- [ ] **Step 4: Update the workflow**

In `src/mastra/workflows/poster.workflow.ts`, replace `finalizeStep` with:

```ts
const finalizeStep = createStep({
  id: "finalize-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterWorkflowOutputSchema,
  execute: async ({ inputData, runId, artifactRoot }: any) => {
    const provenance = { artist: inputData.artist, credit: inputData.credit };
    // Emitted on every branch so the caller can always clean up the run dir.
    const artifactDir = artifactStore(runId, { root: artifactRoot }).dir;

    if (!inputData.imageOk) {
      return {
        ok: false,
        failureStage: "image" as const,
        reason: inputData.imageReason ?? "no acceptable band image found",
        artifactDir,
        ...provenance,
      };
    }
    if (inputData.accepted && inputData.render) {
      return { ok: true, render: inputData.render, artifactDir, ...provenance };
    }
    return {
      ok: false,
      failureStage: "svg" as const,
      reason: inputData.critique ?? "could not produce an acceptable poster",
      artifactDir,
      ...provenance,
    };
  },
});
```

Add the import:

```ts
import { artifactStore } from "../tools/artifact-store.js";
```

In the seed-2 `.map()`, the `image` line is unchanged (it now carries an `ImageRef`), and `credit` keeps its accepted-only gate.

- [ ] **Step 5: Run the whole mastra suite**

Run: `pnpm vitest run src/mastra/`
Expected: PASS. This is the first point where all the workflow pieces line up.

- [ ] **Step 6: Verify, do not commit**

`git status --short`; leave uncommitted.

---

### Task 6: Versioned keys, streaming puts, provenance sidecar, and find

**Files:**
- Modify: `src/poster-sink.ts`, `src/poster-sink.test.ts`
- Create: `src/poster-sink.s3.test.ts`

**Interfaces:**
- Consumes: `ArtifactRef` (Task 1), `ArtistMatch` / `ImageCredit` (existing).
- Produces:
  - `POSTER_SCHEMA_VERSION = 1`
  - `posterKeyBase(req)` → `posters/v1/{performer}/{venue}-{date}`
  - `PosterProvenance = { artist?: ArtistMatch; credit?: ImageCredit }`
  - `PosterArtifacts = { svgUrl: string; pngUrl: string; artist?: ArtistMatch; credit?: ImageCredit }`
  - `PosterSink = { find(req): Promise<PosterArtifacts | null>; put(req, svg: ArtifactRef, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts> }`

- [ ] **Step 1: Write the failing tests**

Update `src/poster-sink.test.ts`'s key assertions to include the version, and rewrite the `StubPosterSink` block:

```ts
describe("posterKeyBase", () => {
  it("builds a slugged, versioned, prefixed key", () => {
    expect(posterKeyBase(req)).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15");
  });
  it("slugs spaces and punctuation", () => {
    expect(posterKeyBase({ performer: "Sigur Rós!", venue: "9:30 Club", date: "2026-09-01" }))
      .toBe("posters/v1/sigur-ros/9-30-club-2026-09-01");
  });
});

describe("StubPosterSink", () => {
  const svgRef = { path: "/tmp/p.svg", contentType: "image/svg+xml", bytes: 10 };
  const pngRef = { path: "/tmp/p.png", contentType: "image/png", bytes: 20 };
  const provenance = { artist: { mbid: "m", name: "K", score: 100 } };

  it("records the put and returns canned urls plus provenance", async () => {
    const sink = new StubPosterSink();
    const out = await sink.put(req, svgRef, pngRef, provenance);
    expect(out.svgUrl).toContain("posters/v1/khruangbin");
    expect(out.artist).toEqual(provenance.artist);
    expect(sink.calls).toHaveLength(1);
    expect(sink.calls[0].svg).toEqual(svgRef);
  });

  it("find misses until something has been put", async () => {
    const sink = new StubPosterSink();
    expect(await sink.find(req)).toBeNull();
    await sink.put(req, svgRef, pngRef, provenance);
    expect((await sink.find(req))?.artist).toEqual(provenance.artist);
  });
});
```

Create `src/poster-sink.s3.test.ts`:

```ts
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { S3PosterSink } from "./poster-sink.js";

vi.mock("@aws-sdk/s3-request-presigner", () => ({
  getSignedUrl: vi.fn(async (_c: unknown, cmd: any) => `https://signed.test/${cmd.input.Key}`),
}));

const req = { performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15" };
const provenance = {
  artist: { mbid: "mb", name: "Khruangbin", score: 100 },
  credit: { file: "File:K.jpg", descriptionUrl: "https://commons/K", attributionRequired: true },
};

/** Records every command; GetObject serves whatever `objects` holds. */
function fakeS3(objects: Record<string, string> = {}) {
  const sent: any[] = [];
  const client = {
    sent,
    async send(cmd: any) {
      sent.push(cmd);
      const name = cmd.constructor.name;
      if (name === "GetObjectCommand") {
        const body = objects[cmd.input.Key];
        if (body === undefined) {
          const err: any = new Error("NoSuchKey");
          err.name = "NoSuchKey";
          throw err;
        }
        return { Body: { transformToString: async () => body } };
      }
      return {};
    },
  };
  return client as any;
}

let root: string;
beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "sink-test-"));
});

async function refs() {
  const svgPath = join(root, "p.svg");
  const pngPath = join(root, "p.png");
  await writeFile(svgPath, "<svg/>");
  await writeFile(pngPath, Buffer.from([0x89, 0x50, 0x4e, 0x47]));
  return {
    svg: { path: svgPath, contentType: "image/svg+xml", bytes: 6 },
    png: { path: pngPath, contentType: "image/png", bytes: 4 },
  };
}

describe("S3PosterSink.put", () => {
  it("streams both artifacts with ContentLength rather than buffering them", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);

    const puts = s3.sent.filter((c: any) => c.constructor.name === "PutObjectCommand");
    const svgPut = puts.find((c: any) => c.input.Key.endsWith(".svg"))!;
    expect(svgPut.input.ContentLength).toBe(6);
    expect(svgPut.input.ContentType).toBe("image/svg+xml");
    expect(typeof svgPut.input.Body.pipe).toBe("function"); // a stream, not a Buffer/string
  });

  it("writes the provenance sidecar LAST, so it is a commit marker", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);

    const keys = s3.sent
      .filter((c: any) => c.constructor.name === "PutObjectCommand")
      .map((c: any) => c.input.Key as string);
    expect(keys).toEqual([
      "posters/v1/khruangbin/the-fillmore-2026-08-15.svg",
      "posters/v1/khruangbin/the-fillmore-2026-08-15.png",
      "posters/v1/khruangbin/the-fillmore-2026-08-15.json",
    ]);
  });

  it("returns signed urls plus the provenance it stored", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    const out = await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);
    expect(out.svgUrl).toContain(".svg");
    expect(out.pngUrl).toContain(".png");
    expect(out.credit).toEqual(provenance.credit);
  });
});

describe("S3PosterSink.find", () => {
  const key = "posters/v1/khruangbin/the-fillmore-2026-08-15.json";

  it("returns urls and provenance when the sidecar exists", async () => {
    const s3 = fakeS3({ [key]: JSON.stringify(provenance) });
    const hit = await new S3PosterSink(s3, "bkt").find(req);

    expect(hit).not.toBeNull();
    expect(hit!.artist).toEqual(provenance.artist);
    expect(hit!.credit!.attributionRequired).toBe(true);
    expect(hit!.pngUrl).toContain(".png");
  });

  it("returns null when the sidecar is absent — a half-written poster is not a hit", async () => {
    // svg and png notionally exist; only the commit marker is missing.
    const s3 = fakeS3({});
    expect(await new S3PosterSink(s3, "bkt").find(req)).toBeNull();
  });

  it("rethrows a non-404 so the caller can decide", async () => {
    const s3 = {
      async send() {
        const err: any = new Error("AccessDenied");
        err.name = "AccessDenied";
        throw err;
      },
    } as any;
    await expect(new S3PosterSink(s3, "bkt").find(req)).rejects.toThrow(/AccessDenied/);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/poster-sink.test.ts src/poster-sink.s3.test.ts`
Expected: FAIL — `find` does not exist and the key has no version segment.

- [ ] **Step 3: Rewrite the sink**

Replace `src/poster-sink.ts`:

```ts
import { createReadStream } from "node:fs";
import { GetObjectCommand, PutObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import type { ArtifactRef, ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";
import type { PosterRequest } from "./poster-schema.js";

/**
 * Bump when the pipeline changes in a way that should invalidate stored posters
 * (a new author prompt, a different canvas, a changed candidate strategy).
 * Without this the cache would freeze output quality permanently, since the key
 * otherwise encodes only performer/venue/date.
 */
export const POSTER_SCHEMA_VERSION = 1;

const SIGNED_URL_TTL_SECONDS = 3600;

export interface PosterProvenance {
  artist?: ArtistMatch;
  credit?: ImageCredit;
}

export interface PosterArtifacts extends PosterProvenance {
  svgUrl: string;
  pngUrl: string;
}

export interface PosterSink {
  /** Signed urls + provenance when a COMPLETE poster already exists, else null. */
  find(req: PosterRequest): Promise<PosterArtifacts | null>;
  put(req: PosterRequest, svg: ArtifactRef, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts>;
}

function slug(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // strip diacritics
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** Deterministic, versioned S3 key prefix for a request (no extension). */
export function posterKeyBase(req: PosterRequest): string {
  return `posters/v${POSTER_SCHEMA_VERSION}/${slug(req.performer)}/${slug(req.venue)}-${slug(req.date)}`;
}

export class S3PosterSink implements PosterSink {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  private sign(base: string): Promise<[string, string]> {
    return Promise.all([
      getSignedUrl(this.s3, new GetObjectCommand({ Bucket: this.bucket, Key: `${base}.svg` }), {
        expiresIn: SIGNED_URL_TTL_SECONDS,
      }),
      getSignedUrl(this.s3, new GetObjectCommand({ Bucket: this.bucket, Key: `${base}.png` }), {
        expiresIn: SIGNED_URL_TTL_SECONDS,
      }),
    ]) as Promise<[string, string]>;
  }

  /** Stream from disk. ContentLength is what lets a stream body avoid buffering. */
  private async putFile(key: string, ref: ArtifactRef): Promise<void> {
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: key,
        Body: createReadStream(ref.path),
        ContentLength: ref.bytes,
        ContentType: ref.contentType,
      }),
    );
  }

  async find(req: PosterRequest): Promise<PosterArtifacts | null> {
    const base = posterKeyBase(req);
    let provenance: PosterProvenance;
    try {
      const res = await this.s3.send(new GetObjectCommand({ Bucket: this.bucket, Key: `${base}.json` }));
      const body = await res.Body?.transformToString();
      if (!body) return null;
      provenance = JSON.parse(body) as PosterProvenance;
    } catch (e) {
      // Absent sidecar == no complete poster. Anything else is a real problem
      // the caller should see (it decides whether to degrade to a miss).
      const name = (e as { name?: string })?.name;
      if (name === "NoSuchKey" || name === "NotFound") return null;
      throw e;
    }
    const [svgUrl, pngUrl] = await this.sign(base);
    return { svgUrl, pngUrl, ...provenance };
  }

  async put(
    req: PosterRequest,
    svg: ArtifactRef,
    png: ArtifactRef,
    provenance: PosterProvenance,
  ): Promise<PosterArtifacts> {
    const base = posterKeyBase(req);
    await this.putFile(`${base}.svg`, svg);
    await this.putFile(`${base}.png`, png);
    // The sidecar goes LAST. `find` keys off it, so its presence proves the
    // other two objects are complete and a half-written poster is never served.
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: `${base}.json`,
        Body: JSON.stringify(provenance),
        ContentType: "application/json",
      }),
    );
    const [svgUrl, pngUrl] = await this.sign(base);
    return { svgUrl, pngUrl, ...provenance };
  }
}

/** Test double: records puts, serves them back from find, fake urls. */
export class StubPosterSink implements PosterSink {
  public calls: Array<{ req: PosterRequest; svg: ArtifactRef; png: ArtifactRef; provenance: PosterProvenance }> = [];
  private stored = new Map<string, PosterProvenance>();

  async find(req: PosterRequest): Promise<PosterArtifacts | null> {
    const base = posterKeyBase(req);
    const provenance = this.stored.get(base);
    if (!provenance) return null;
    return { svgUrl: `https://stub.local/${base}.svg`, pngUrl: `https://stub.local/${base}.png`, ...provenance };
  }

  async put(
    req: PosterRequest,
    svg: ArtifactRef,
    png: ArtifactRef,
    provenance: PosterProvenance,
  ): Promise<PosterArtifacts> {
    this.calls.push({ req, svg, png, provenance });
    const base = posterKeyBase(req);
    this.stored.set(base, provenance);
    return { svgUrl: `https://stub.local/${base}.svg`, pngUrl: `https://stub.local/${base}.png`, ...provenance };
  }
}
```

- [ ] **Step 4: Run tests**

Run: `pnpm vitest run src/poster-sink.test.ts src/poster-sink.s3.test.ts`
Expected: PASS (10 tests).

- [ ] **Step 5: Verify, do not commit**

`git status --short`; leave uncommitted.

---

### Task 7: Cache short-circuit, force flag, cleanup, and the URL-only response

**Files:**
- Modify: `src/poster-schema.ts`, `src/poster.ts`, `src/poster.test.ts`, `src/handler.poster.test.ts`

**Interfaces:**
- Consumes: `PosterSink.find` / `PosterSink.put` / `PosterArtifacts` (Task 6); `PosterWorkflowOutput.render` / `.artifactDir` (Task 5).
- Produces: `PosterRequest` with `force?: boolean`; `PosterResult` ok branch `{ ok: true; svgUrl; pngUrl; cached: boolean; artist?; credit? }`; 200 body `{ svgUrl, pngUrl, cached, artist?, credit? }`.

- [ ] **Step 1: Write the failing tests**

In `src/poster.test.ts`, replace the two original `processPosterRequest` tests and the provenance block's HTTP tests, then append:

```ts
const artifactRefs = {
  render: {
    svg: { path: "/tmp/run/p.svg", contentType: "image/svg+xml", bytes: 10 },
    png: { path: "/tmp/run/p.png", contentType: "image/png", bytes: 20 },
  },
  artifactDir: "/tmp/run",
};

describe("repeat requests", () => {
  const artist = { mbid: "mb", name: "Khruangbin", score: 100 };
  const credit = { file: "File:K.jpg", descriptionUrl: "https://commons/K", attributionRequired: true };

  it("serves an existing poster WITHOUT running the workflow", async () => {
    const sink = new StubPosterSink();
    await sink.put(req, artifactRefs.render.svg, artifactRefs.render.png, { artist, credit });
    const runWorkflow = vi.fn();

    const res = await processPosterRequest(req, { sink, runWorkflow });

    expect(runWorkflow).not.toHaveBeenCalled();
    expect(res.ok).toBe(true);
    if (res.ok) {
      expect(res.cached).toBe(true);
      // A hit must be indistinguishable from a fresh run — attribution included.
      expect(res.artist).toEqual(artist);
      expect(res.credit).toEqual(credit);
    }
  });

  it("marks a fresh generation as not cached", async () => {
    const sink = new StubPosterSink();
    const res = await processPosterRequest(req, {
      sink,
      runWorkflow: async () => ({ ok: true, ...artifactRefs, artist, credit }),
    });
    expect(res.ok && res.cached).toBe(false);
  });

  it("force: true bypasses the cache and reruns the workflow", async () => {
    const sink = new StubPosterSink();
    await sink.put(req, artifactRefs.render.svg, artifactRefs.render.png, { artist, credit });
    const runWorkflow = vi.fn(async () => ({ ok: true, ...artifactRefs, artist, credit }));

    const res = await processPosterRequest({ ...req, force: true }, { sink, runWorkflow });

    expect(runWorkflow).toHaveBeenCalledTimes(1);
    expect(res.ok && res.cached).toBe(false);
  });

  it("treats a failing find as a miss rather than failing the request", async () => {
    const sink = new StubPosterSink();
    sink.find = async () => {
      throw new Error("S3 unreachable");
    };
    const res = await processPosterRequest(req, {
      sink,
      runWorkflow: async () => ({ ok: true, ...artifactRefs, artist, credit }),
    });
    expect(res.ok).toBe(true);
  });
});

describe("artifact cleanup", () => {
  it("deletes the run directory after a successful put", async () => {
    const { mkdtemp, writeFile } = await import("node:fs/promises");
    const { existsSync } = await import("node:fs");
    const { tmpdir } = await import("node:os");
    const { join } = await import("node:path");

    const dir = await mkdtemp(join(tmpdir(), "cleanup-test-"));
    await writeFile(join(dir, "poster-1.png"), "x");

    await processPosterRequest(req, {
      sink: new StubPosterSink(),
      runWorkflow: async () => ({
        ok: true,
        render: artifactRefs.render,
        artifactDir: dir,
      }),
    });

    expect(existsSync(dir)).toBe(false);
  });

  it("deletes the run directory even when the sink throws", async () => {
    const { mkdtemp } = await import("node:fs/promises");
    const { existsSync } = await import("node:fs");
    const { tmpdir } = await import("node:os");
    const { join } = await import("node:path");

    const dir = await mkdtemp(join(tmpdir(), "cleanup-fail-test-"));
    const sink = new StubPosterSink();
    sink.put = async () => {
      throw new Error("s3 down");
    };

    await expect(
      processPosterRequest(req, {
        sink,
        runWorkflow: async () => ({ ok: true, render: artifactRefs.render, artifactDir: dir }),
      }),
    ).rejects.toThrow(/s3 down/);

    expect(existsSync(dir)).toBe(false);
  });
});

describe("URL-only response", () => {
  it("omits the svg body and includes cached", () => {
    const out = posterHttpResponse({
      ok: true,
      svgUrl: "https://x/s.svg",
      pngUrl: "https://x/p.png",
      cached: true,
    });
    const body = JSON.parse(out.body);
    expect(out.statusCode).toBe(200);
    expect("svg" in body).toBe(false);
    expect(body.svgUrl).toBe("https://x/s.svg");
    expect(body.cached).toBe(true);
  });
});
```

Add `vi` to the vitest import at the top of `poster.test.ts`.

In `src/handler.poster.test.ts:36`, replace `expect(JSON.parse(res.body).svg).toBe("<svg/>")` with:

```ts
    expect(JSON.parse(res.body).svgUrl).toBeTruthy();
    expect("svg" in JSON.parse(res.body)).toBe(false);
```

and update that test's workflow stub to return `{ ok: true, render: { svg: {...}, png: {...} }, artifactDir: "/tmp/x" }`.

- [ ] **Step 2: Run to verify it fails**

Run: `pnpm vitest run src/poster.test.ts src/handler.poster.test.ts`
Expected: FAIL — `find` is not consulted and `cached` is undefined.

- [ ] **Step 3: Add the force flag**

In `src/poster-schema.ts`, add `force` to the request schema and rewrite `PosterResult`:

```ts
export const PosterRequestSchema = z
  .object({
    performer: z.string().trim().min(1, "performer is required"),
    venue: z.string().trim().min(1, "venue is required"),
    date: z.string().trim().min(1, "date is required"),
    // Poster generation is LLM-driven and nondeterministic, so a user who
    // dislikes a result needs a re-roll. NOT part of posterKeyBase: a forced run
    // overwrites the same keys rather than creating a parallel copy.
    force: z.boolean().optional().default(false),
  })
  .strict();
export type PosterRequest = z.infer<typeof PosterRequestSchema>;

/** Result of the poster pipeline, mapped to HTTP by the handler. */
export type PosterResult =
  | { ok: true; svgUrl: string; pngUrl: string; cached: boolean; artist?: ArtistMatch; credit?: ImageCredit }
  | { ok: false; stage: "image" | "svg"; reason: string; artist?: ArtistMatch };
```

- [ ] **Step 4: Rewrite `processPosterRequest` and `posterHttpResponse`**

In `src/poster.ts`, add the import and replace both functions:

```ts
import { rm } from "node:fs/promises";
```

```ts
/**
 * Serve an existing poster when there is one; otherwise run the workflow, upload,
 * and clean up the run's scratch directory. Never persists on failure.
 */
export async function processPosterRequest(req: PosterRequest, deps: PosterDeps): Promise<PosterResult> {
  if (!req.force) {
    try {
      const hit = await deps.sink.find(req);
      if (hit) return { ok: true, cached: true, ...hit };
    } catch {
      // A cache must never fail a request that would otherwise succeed.
    }
  }

  let out: PosterWorkflowOutput | undefined;
  try {
    out = await deps.runWorkflow(req);
    if (!out.ok || !out.render) {
      return {
        ok: false,
        stage: out.failureStage ?? "svg",
        reason: out.reason ?? "unknown failure",
        artist: out.artist,
      };
    }
    const artifacts = await deps.sink.put(req, out.render.svg, out.render.png, {
      artist: out.artist,
      credit: out.credit,
    });
    return { ok: true, cached: false, ...artifacts };
  } finally {
    // Lambda's /tmp persists across warm invocations, so this is not optional.
    // Studio never calls this function, which is exactly why its runs keep their
    // artifacts for inspection.
    if (out?.artifactDir) {
      await rm(out.artifactDir, { recursive: true, force: true }).catch(() => {});
    }
  }
}

const JSON_HEADERS = { "content-type": "application/json" };

export function posterHttpResponse(result: PosterResult): { statusCode: number; headers: Record<string, string>; body: string } {
  if (result.ok) {
    // URLs, not bytes. JSON.stringify drops undefined keys, so provenance is
    // simply absent when unknown.
    return {
      statusCode: 200,
      headers: JSON_HEADERS,
      body: JSON.stringify({
        svgUrl: result.svgUrl,
        pngUrl: result.pngUrl,
        cached: result.cached,
        artist: result.artist,
        credit: result.credit,
      }),
    };
  }
  // 422 (never 403/404 — see Global Constraints / spec §8).
  return {
    statusCode: 422,
    headers: JSON_HEADERS,
    body: JSON.stringify({ error: result.reason, stage: result.stage, artist: result.artist }),
  };
}
```

- [ ] **Step 5: Run tests and typecheck**

Run: `pnpm vitest run src/poster.test.ts src/handler.poster.test.ts src/poster-schema.test.ts && pnpm typecheck`
Expected: PASS, typecheck clean.

- [ ] **Step 6: Verify, do not commit**

`git status --short`; leave uncommitted.

---

### Task 8: Docs and full verification

**Files:**
- Modify: `README.md:296`

- [ ] **Step 1: Update the documented contract**

In `README.md`, replace the poster-endpoint paragraph with:

```markdown
The same Lambda also serves a **`POST /api/poster`** endpoint (via CloudFront → Lambda Function URL). It takes a JSON body with `{ performer, venue, date, force? }` and returns `{ svgUrl, pngUrl, cached, artist?, credit? }`. `svgUrl` and `pngUrl` are presigned S3 URLs (1 hour) to artifacts in the posters bucket; `cached` reports whether the poster was served from S3 without rerunning the pipeline; `artist` and `credit` carry MusicBrainz and Wikimedia Commons provenance, including licence and required attribution. Pass `force: true` to regenerate a poster you don't like. The endpoint resolves the performer via MusicBrainz, sources imagery from Wikimedia Commons, and renders SVG to PNG via WebAssembly.
```

- [ ] **Step 2: Run the full suite and typecheck**

Run: `pnpm test && pnpm typecheck`
Expected: all PASS, typecheck exit 0. (`pnpm test` excludes the ElasticMQ integration specs by config; run `pnpm test:integration` separately with docker up.)

- [ ] **Step 3: Run the integration lane**

Run: `docker compose up -d elasticmq` (from the repo root) then `pnpm test:integration`
Expected: 2 files, 2 tests PASS.

- [ ] **Step 4: Confirm no stale references**

Run: `grep -rn "pngBase64\|BandImageSchema\|imageBase64" src/ | grep -v "rasterize.tool.ts" | grep -v "stub-band-image"`
Expected: no matches. `rasterize.tool.ts` legitimately keeps `pngBase64` in the Studio tool wrapper, and `stub-band-image.ts` is a rasterization test fixture.

- [ ] **Step 5: Manual check in Studio**

Run: `pnpm dev`, open `http://localhost:4111`, run `poster-workflow` with `{ performer: "la luz", venue: "Occidental Square", date: "Thursday, August 20" }`.

Expected: every step card is readable — `judge-band-image` shows `image: { path: "…/band-1.jpg", bytes: 257432, … }` rather than a wall of base64, and `compose-poster` shows `authoredSvg` with `__BAND_IMAGE__` intact plus `render` paths. The files exist on disk at the shown paths and can be opened (Studio runs do **not** clean up).

- [ ] **Step 6: Hand off for review, do not commit**

Run: `git status --short`. Report the suite results. **Do not commit.**

---

## Notes for the implementer

**On `artifactRoot`.** Steps destructure `{ inputData, runId, artifactRoot }: any`. `runId` is genuinely provided by Mastra (verified). `artifactRoot` is not — it is a test-only injection riding on the same params object so tests can point at a scratch directory. Production never sets it, so `artifactStore` falls back to `defaultRoot()`.

**On what "no blobs in state" means.** Bytes still exist transiently: the vision agent receives a Buffer, resvg receives an SVG string. That is unavoidable and fine. The invariant is that they are never *stored* in state, copied by a seed map, or persisted into a run snapshot. The guard test in Task 5 is what enforces it.

**On the sidecar ordering.** `put` writes svg, then png, then json. Do not reorder or parallelise these. `find` keys off the json, so its presence is the proof that the other two are complete.

**On cache honesty.** The endpoint has no callers today, so the value of Task 6-7 rests on the presigned-expiry case: URLs die after an hour, and without the cache a returning user pays a full regeneration just for a fresh URL. If usage turns out to be one-shot per poster, the check costs ~15ms per request and nothing worse.

**Attribution is captured, not rendered.** None of this draws a credit line onto the poster. Every Commons candidate observed so far requires attribution and most are CC BY-SA, which is copyleft. Carrying `credit` through to the response — including on cache hits — is a precondition for handling that, not compliance with it.
