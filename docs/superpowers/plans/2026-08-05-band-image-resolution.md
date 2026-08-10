# Band Image Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the stub band-image scraper with real MusicBrainz artist resolution and Wikimedia image sourcing, so the poster workflow's `.dountil` retry loop judges a genuinely different candidate on each attempt.

**Architecture:** Two new tool modules split on network boundary — `musicbrainz.tool.ts` owns all MusicBrainz traffic, `wikimedia.tool.ts` owns all Wikidata/Commons traffic. Candidate resolution moves into a step that runs **once** before the loop (it is deterministic per performer); the loop step shrinks to fetch-one-candidate + judge. Provenance (which artist, which image, what licence) threads additively out to the HTTP response.

**Tech Stack:** TypeScript ES2022 modules, Zod 3, Mastra `@mastra/core` 1.51, Vitest 2, `html-to-text` (already a dependency).

**Spec:** `docs/superpowers/specs/2026-08-05-band-image-resolution-design.md`

## Global Constraints

- **DO NOT COMMIT.** The user is reviewing and testing all changes by hand. Every task ends with a verification step, never a `git commit`. Leave the working tree dirty.
- Working directory for all commands: `lambda/mastra-handler`.
- All relative imports use the `.js` extension (ES2022 + `moduleResolution: bundler`). `import { x } from "./foo.js"` even though the file is `foo.ts`.
- User-Agent for **every** outbound MusicBrainz and Wikimedia request, exactly: `heres-whats-happening/1.0 ( wlmmyers@gmail.com )`. Both services require an app identifier plus a contact; MusicBrainz rejects requests without one.
- MusicBrainz is rate limited to ~1 request/second. Wikidata and Commons are not.
- Thumbnail width is `1080`, matching the poster canvas in `svg-author.agent.ts` (1080x1350).
- Artist fall-through bound: examine at most **3** MusicBrainz matches.
- Candidate pool cap: **12** total, P18 inclusive.
- Never throw out of a workflow step. Convert failures into state, following the `rasterizeSvg` convention at `src/mastra/tools/rasterize.tool.ts:28`.
- No new env vars. Base URLs and `fetch` are constructor-injected; caps and widths are module constants. `MAX_IMAGE_ATTEMPTS` / `MAX_SVG_ATTEMPTS` stay as they are.
- Tests never touch the network. Every test injects a stub `fetch`.

## File Structure

**Create:**
| File | Responsibility |
|---|---|
| `src/mastra/tools/band-image.ts` | Shared Zod schemas + the User-Agent constant. No I/O. |
| `src/mastra/tools/stub-fetch.ts` | Test-only routed `fetch` stub. Mirrors how `StubPosterSink` lives in `poster-sink.ts`. |
| `src/mastra/tools/musicbrainz.tool.ts` | All MusicBrainz I/O. `searchArtists`. |
| `src/mastra/tools/wikimedia.tool.ts` | All Wikidata/Commons I/O. `resolveImageCandidates`, `fetchImageBytes`. |
| `src/mastra/workflows/resolve-band-candidates.step.ts` | Runs once before the loop. Fall-through over artist matches. |
| `src/mastra/workflows/judge-band-image.step.ts` | Runs per iteration. Fetch one candidate, judge it. |
| `src/mastra/tools/musicbrainz.tool.test.ts` | |
| `src/mastra/tools/wikimedia.tool.test.ts` | |
| `src/mastra/workflows/resolve-band-candidates.step.test.ts` | |
| `src/mastra/workflows/judge-band-image.step.test.ts` | |
| `src/mastra/workflows/poster.workflow.test.ts` | |

**Modify:** `src/mastra/workflows/poster.schemas.ts`, `src/mastra/workflows/poster.workflow.ts`, `src/poster-schema.ts`, `src/poster.ts`, `src/poster.test.ts`, `ci/buildspec-lambda.yml`

**Delete:** `src/mastra/tools/web-scrape.tool.ts`, `src/mastra/tools/web-scrape.tool.test.ts`, `src/mastra/workflows/acquire-band-image.step.ts`

**Keep:** `src/mastra/tools/stub-band-image.ts` — demoted to a test fixture. `rasterize.tool.test.ts:3` imports `STUB_BAND_IMAGE_BASE64` as an embedded-image fixture for SVG rasterization, which is unrelated to scraping. Do **not** delete it.

---

### Task 1: Shared schemas

**Files:**
- Create: `src/mastra/tools/band-image.ts`
- Modify: `src/mastra/workflows/poster.schemas.ts:2`
- Test: `src/mastra/tools/band-image.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `BandImageSchema`/`BandImage`, `ImageCreditSchema`/`ImageCredit`, `ImageCandidateSchema`/`ImageCandidate`, `ArtistMatchSchema`/`ArtistMatch`, `USER_AGENT`. Every later task imports from here.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/tools/band-image.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { ArtistMatchSchema, BandImageSchema, ImageCreditSchema, USER_AGENT } from "./band-image.js";

describe("ImageCreditSchema", () => {
  it("defaults attributionRequired to false when absent (public-domain files omit it)", () => {
    const c = ImageCreditSchema.parse({
      file: "File:Example.jpg",
      descriptionUrl: "https://commons.wikimedia.org/wiki/File:Example.jpg",
    });
    expect(c.attributionRequired).toBe(false);
    expect(c.artist).toBeUndefined();
    expect(c.license).toBeUndefined();
  });

  it("round-trips a fully populated credit", () => {
    const c = ImageCreditSchema.parse({
      file: "File:La Luz.jpg",
      descriptionUrl: "https://commons.wikimedia.org/wiki/File:La_Luz.jpg",
      artist: "Shark2000br",
      credit: "Own work",
      license: "cc-by-sa-4.0",
      licenseShortName: "CC BY-SA 4.0",
      licenseUrl: "https://creativecommons.org/licenses/by-sa/4.0",
      usageTerms: "Creative Commons Attribution-Share Alike 4.0",
      attributionRequired: true,
    });
    expect(c.licenseShortName).toBe("CC BY-SA 4.0");
    expect(c.attributionRequired).toBe(true);
  });
});

describe("BandImageSchema", () => {
  it("keeps credit optional so the shape stays backward compatible", () => {
    const img = BandImageSchema.parse({
      imageBase64: "AAAA",
      contentType: "image/jpeg",
      width: 1080,
      height: 810,
    });
    expect(img.credit).toBeUndefined();
  });
});

describe("ArtistMatchSchema", () => {
  it("accepts a match with only the required fields", () => {
    const a = ArtistMatchSchema.parse({ mbid: "abc", name: "La Luz", score: 100 });
    expect(a.disambiguation).toBeUndefined();
  });
});

describe("USER_AGENT", () => {
  it("identifies the app and a contact, as both services require", () => {
    expect(USER_AGENT).toBe("heres-whats-happening/1.0 ( wlmmyers@gmail.com )");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/band-image.test.ts`
Expected: FAIL — `Cannot find module './band-image.js'`

- [ ] **Step 3: Write the implementation**

Create `src/mastra/tools/band-image.ts`:

```ts
import { z } from "zod";

// Identifies the app + a contact. MusicBrainz rejects requests without a
// User-Agent and Wikimedia's UA policy requires the same. Kept identical to the
// string the Go side registers (internal/scraper/spotify/genres.go).
export const USER_AGENT = "heres-whats-happening/1.0 ( wlmmyers@gmail.com )";

// Attribution data for one Wikimedia Commons file. Every field except the
// identifiers is optional — public-domain files legitimately carry no author or
// licence, and dropping such a candidate would be worse than crediting nothing.
export const ImageCreditSchema = z.object({
  file: z.string(),
  descriptionUrl: z.string(),
  artist: z.string().optional(),
  credit: z.string().optional(),
  license: z.string().optional(),
  licenseShortName: z.string().optional(),
  licenseUrl: z.string().optional(),
  usageTerms: z.string().optional(),
  attributionRequired: z.boolean().default(false),
});
export type ImageCredit = z.infer<typeof ImageCreditSchema>;

// The bytes actually embedded in the poster SVG. Shape is unchanged from the
// former web-scrape.tool.ts except for the added optional `credit`.
export const BandImageSchema = z.object({
  imageBase64: z.string(),
  contentType: z.string(),
  width: z.number(),
  height: z.number(),
  sourceUrl: z.string().optional(),
  credit: ImageCreditSchema.optional(),
});
export type BandImage = z.infer<typeof BandImageSchema>;

// A resolved image the loop MAY judge. Metadata only — no bytes. width/height
// are the THUMBNAIL dimensions, since the thumbnail URL is what gets fetched.
export const ImageCandidateSchema = z.object({
  file: z.string(),
  url: z.string(),
  width: z.number(),
  height: z.number(),
  contentType: z.string(),
  source: z.enum(["p18", "category"]),
  credit: ImageCreditSchema,
});
export type ImageCandidate = z.infer<typeof ImageCandidateSchema>;

// One MusicBrainz artist search hit, carrying the disambiguation fields that
// distinguish "La Luz, US rock band" from "La Luz, Belgium based house group".
export const ArtistMatchSchema = z.object({
  mbid: z.string(),
  name: z.string(),
  score: z.number(),
  disambiguation: z.string().optional(),
  type: z.string().optional(),
  country: z.string().optional(),
  beginYear: z.string().optional(),
});
export type ArtistMatch = z.infer<typeof ArtistMatchSchema>;
```

- [ ] **Step 4: Repoint `poster.schemas.ts` at the new module**

In `src/mastra/workflows/poster.schemas.ts`, change line 2 from:

```ts
import { BandImageSchema } from "../tools/web-scrape.tool.js";
```

to:

```ts
import { BandImageSchema } from "../tools/band-image.js";
```

Leave the rest of the file alone; Task 8 revisits it.

- [ ] **Step 5: Run tests and typecheck**

Run: `pnpm vitest run src/mastra/tools/band-image.test.ts && pnpm typecheck`
Expected: PASS, and typecheck clean.

- [ ] **Step 6: Verify, do not commit**

Run: `git status --short`
Expected: `band-image.ts`, `band-image.test.ts` untracked; `poster.schemas.ts` modified. **Leave uncommitted.**

---

### Task 2: Routed fetch stub for tests

**Files:**
- Create: `src/mastra/tools/stub-fetch.ts`
- Test: `src/mastra/tools/stub-fetch.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `stubFetch(routes: StubRoute[]): StubFetch`, where `StubFetch` is `typeof globalThis.fetch & { calls: StubCall[] }` and `StubCall` is `{ url: string; headers: Record<string, string> }`. Tasks 3-7 use this for all network stubbing.

Why a shared helper: five test files need URL-routed responses and header assertions. Writing that inline five times invites five subtly different versions.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/tools/stub-fetch.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { stubFetch } from "./stub-fetch.js";

describe("stubFetch", () => {
  it("routes by regex and returns JSON", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: { artists: [{ id: "x" }] } }]);
    const res = await f("https://musicbrainz.org/ws/2/artist?query=a");
    expect(await res.json()).toEqual({ artists: [{ id: "x" }] });
  });

  it("records url and headers for assertions", async () => {
    const f = stubFetch([{ match: /./, json: {} }]);
    await f("https://example.test/a", { headers: { "User-Agent": "ua/1.0" } });
    expect(f.calls).toHaveLength(1);
    expect(f.calls[0].url).toBe("https://example.test/a");
    expect(f.calls[0].headers["user-agent"]).toBe("ua/1.0");
  });

  it("serves binary bodies for image fetches", async () => {
    const bytes = Buffer.from([0xff, 0xd8, 0xff, 0xe0]);
    const f = stubFetch([{ match: /upload\./, body: bytes }]);
    const res = await f("https://upload.wikimedia.org/x.jpg");
    expect(Buffer.from(await res.arrayBuffer())).toEqual(bytes);
  });

  it("returns the given status and supports a per-call queue", async () => {
    const f = stubFetch([{ match: /./, statuses: [503, 200], json: { ok: true } }]);
    expect((await f("https://x.test/")).status).toBe(503);
    expect((await f("https://x.test/")).status).toBe(200);
  });

  it("throws on an unrouted url so typos surface loudly", async () => {
    const f = stubFetch([{ match: /nope/, json: {} }]);
    await expect(f("https://example.test/other")).rejects.toThrow(/no stub route/);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/stub-fetch.test.ts`
Expected: FAIL — `Cannot find module './stub-fetch.js'`

- [ ] **Step 3: Write the implementation**

Create `src/mastra/tools/stub-fetch.ts`:

```ts
// Test-only routed `fetch`. Lives beside production code for the same reason
// StubPosterSink lives in poster-sink.ts: it is part of the module's contract
// surface, and colocating keeps it in sync with the client that consumes it.

export interface StubRoute {
  match: RegExp;
  /** JSON body. Ignored when `body` is set. */
  json?: unknown;
  /** Raw bytes, for image fetches. */
  body?: Buffer;
  /** Single status for every match. Defaults to 200. */
  status?: number;
  /** Status per successive call, for retry tests. Overrides `status`. */
  statuses?: number[];
  contentType?: string;
}

export interface StubCall {
  url: string;
  /** Header names lower-cased. */
  headers: Record<string, string>;
}

export type StubFetch = typeof globalThis.fetch & { calls: StubCall[] };

function headersOf(init?: RequestInit): Record<string, string> {
  const out: Record<string, string> = {};
  const h = init?.headers;
  if (!h) return out;
  if (h instanceof Headers) {
    h.forEach((v, k) => (out[k.toLowerCase()] = v));
  } else if (Array.isArray(h)) {
    for (const [k, v] of h) out[k.toLowerCase()] = v;
  } else {
    for (const [k, v] of Object.entries(h)) out[k.toLowerCase()] = String(v);
  }
  return out;
}

export function stubFetch(routes: StubRoute[]): StubFetch {
  const calls: StubCall[] = [];
  const counts = new Map<StubRoute, number>();

  const fn = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    calls.push({ url, headers: headersOf(init) });

    const route = routes.find((r) => r.match.test(url));
    if (!route) throw new Error(`no stub route for ${url}`);

    const n = counts.get(route) ?? 0;
    counts.set(route, n + 1);
    const status = route.statuses ? (route.statuses[Math.min(n, route.statuses.length - 1)] ?? 200) : (route.status ?? 200);

    if (route.body) {
      return new Response(new Uint8Array(route.body), {
        status,
        headers: { "content-type": route.contentType ?? "image/jpeg" },
      });
    }
    return new Response(JSON.stringify(route.json ?? {}), {
      status,
      headers: { "content-type": route.contentType ?? "application/json" },
    });
  };

  const typed = fn as unknown as StubFetch;
  typed.calls = calls;
  return typed;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/stub-fetch.test.ts && pnpm typecheck`
Expected: PASS, typecheck clean.

- [ ] **Step 5: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 3: MusicBrainz client

**Files:**
- Create: `src/mastra/tools/musicbrainz.tool.ts`
- Test: `src/mastra/tools/musicbrainz.tool.test.ts`

**Interfaces:**
- Consumes: `ArtistMatch`, `USER_AGENT` (Task 1); `stubFetch` (Task 2).
- Produces:
  - `type FetchFn = typeof globalThis.fetch`
  - `createMusicBrainzClient(options?: MusicBrainzOptions): MusicBrainzClient`
  - `MusicBrainzOptions = { baseUrl?: string; userAgent?: string; fetchFn?: FetchFn; minIntervalMs?: number }`
  - `MusicBrainzClient = { searchArtists(performer: string, opts?: { limit?: number }): Promise<ArtistMatch[]> }`
  - `searchArtists` — module-level convenience bound to the default production client. Task 6 imports this one.

Mirrors `internal/musicbrainz/client.go`: injectable base URL, required User-Agent, ~1 req/sec limiter, 15s timeout, Lucene quote escaping.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/tools/musicbrainz.tool.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { createMusicBrainzClient } from "./musicbrainz.tool.js";
import { stubFetch } from "./stub-fetch.js";

// Trimmed from a real response for artist:"la luz" (verified 2026-08-05).
const LA_LUZ = {
  count: 21,
  artists: [
    {
      id: "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a",
      name: "La Luz",
      score: 100,
      disambiguation: "US rock band",
      type: "Group",
      country: "US",
      "life-span": { begin: "2012", ended: null },
    },
    {
      id: "1d75fdf0-fe29-48aa-9e3e-b70ca92119e7",
      name: "La Luz",
      score: 88,
      disambiguation: "Belgium based house group",
      type: "Group",
      "life-span": { ended: null },
    },
  ],
};

function client(f: ReturnType<typeof stubFetch>) {
  return createMusicBrainzClient({ baseUrl: "https://mb.test", fetchFn: f, minIntervalMs: 0 });
}

describe("searchArtists", () => {
  it("maps hits including the disambiguation fields", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    const out = await client(f).searchArtists("la luz");

    expect(out).toHaveLength(2);
    expect(out[0]).toEqual({
      mbid: "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a",
      name: "La Luz",
      score: 100,
      disambiguation: "US rock band",
      type: "Group",
      country: "US",
      beginYear: "2012",
    });
    // Absent fields become undefined rather than null or "".
    expect(out[1].country).toBeUndefined();
    expect(out[1].beginYear).toBeUndefined();
  });

  it("sends the required User-Agent and Accept headers", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    await client(f).searchArtists("la luz");
    expect(f.calls[0].headers["user-agent"]).toBe("heres-whats-happening/1.0 ( wlmmyers@gmail.com )");
    expect(f.calls[0].headers["accept"]).toBe("application/json");
  });

  it("requests a bounded, quoted Lucene query", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    await client(f).searchArtists("la luz", { limit: 3 });
    const url = new URL(f.calls[0].url);
    expect(url.searchParams.get("query")).toBe('artist:"la luz"');
    expect(url.searchParams.get("fmt")).toBe("json");
    expect(url.searchParams.get("limit")).toBe("3");
  });

  it("escapes quotes and backslashes so the query cannot be broken", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: { artists: [] } }]);
    await client(f).searchArtists('AC\\DC "live"');
    const url = new URL(f.calls[0].url);
    expect(url.searchParams.get("query")).toBe('artist:"AC\\\\DC \\"live\\""');
  });

  it("returns [] when there are no matches", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: { count: 0, artists: [] } }]);
    expect(await client(f).searchArtists("zzzz")).toEqual([]);
  });

  it("surfaces a non-2xx as an error", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, status: 500, json: { error: "boom" } }]);
    await expect(client(f).searchArtists("la luz")).rejects.toThrow(/musicbrainz 500/);
  });

  it("retries once on 503, their rate-limit response", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, statuses: [503, 200], json: LA_LUZ }]);
    const out = await client(f).searchArtists("la luz");
    expect(f.calls).toHaveLength(2);
    expect(out).toHaveLength(2);
  });

  it("gives up after a second 503", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, statuses: [503, 503], json: {} }]);
    await expect(client(f).searchArtists("la luz")).rejects.toThrow(/503/);
    expect(f.calls).toHaveLength(2);
  });

  it("spaces successive requests by the configured interval", async () => {
    vi.useFakeTimers();
    try {
      const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
      const c = createMusicBrainzClient({ baseUrl: "https://mb.test", fetchFn: f, minIntervalMs: 1000 });

      const first = c.searchArtists("a");
      await vi.advanceTimersByTimeAsync(0);
      await first;
      expect(f.calls).toHaveLength(1);

      const second = c.searchArtists("b");
      await vi.advanceTimersByTimeAsync(0);
      expect(f.calls).toHaveLength(1); // still gated

      await vi.advanceTimersByTimeAsync(1000);
      await second;
      expect(f.calls).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/musicbrainz.tool.test.ts`
Expected: FAIL — `Cannot find module './musicbrainz.tool.js'`

- [ ] **Step 3: Write the implementation**

Create `src/mastra/tools/musicbrainz.tool.ts`:

```ts
import { createTool } from "@mastra/core/tools";
import { z } from "zod";
import { ArtistMatchSchema, USER_AGENT, type ArtistMatch } from "./band-image.js";

const DEFAULT_BASE_URL = "https://musicbrainz.org";
const MIN_INTERVAL_MS = 1000; // MusicBrainz allows ~1 req/sec
const TIMEOUT_MS = 15_000;
const DEFAULT_LIMIT = 3;

export type FetchFn = typeof globalThis.fetch;

export interface MusicBrainzOptions {
  /** Defaults to the production host. Tests pass a fake origin. */
  baseUrl?: string;
  userAgent?: string;
  fetchFn?: FetchFn;
  /** Set to 0 in tests to disable throttling. */
  minIntervalMs?: number;
}

export interface MusicBrainzClient {
  searchArtists(performer: string, opts?: { limit?: number }): Promise<ArtistMatch[]>;
}

interface MbArtist {
  id: string;
  name: string;
  score?: number;
  disambiguation?: string;
  type?: string;
  country?: string;
  "life-span"?: { begin?: string | null };
}

function toArtistMatch(a: MbArtist): ArtistMatch {
  return {
    mbid: a.id,
    name: a.name,
    score: a.score ?? 0,
    disambiguation: a.disambiguation || undefined,
    type: a.type || undefined,
    country: a.country || undefined,
    beginYear: a["life-span"]?.begin?.slice(0, 4) || undefined,
  };
}

export function createMusicBrainzClient(options: MusicBrainzOptions = {}): MusicBrainzClient {
  const baseUrl = options.baseUrl ?? DEFAULT_BASE_URL;
  const userAgent = options.userAgent ?? USER_AGENT;
  const doFetch = options.fetchFn ?? globalThis.fetch;
  const minIntervalMs = options.minIntervalMs ?? MIN_INTERVAL_MS;

  // Slot-reservation limiter: each caller claims the next free instant and waits
  // for it, so concurrent callers queue instead of all firing at once. This is
  // per-process — see the plan's note on Lambda container scope.
  let nextSlot = 0;
  async function throttle(): Promise<void> {
    if (minIntervalMs <= 0) return;
    const now = Date.now();
    const at = Math.max(now, nextSlot);
    nextSlot = at + minIntervalMs;
    if (at > now) await new Promise((resolve) => setTimeout(resolve, at - now));
  }

  async function getJson(path: string): Promise<unknown> {
    let lastStatus = 0;
    // Two passes: a 503 is MusicBrainz's rate-limit signal, and `throttle()`
    // already spaces the retry by minIntervalMs.
    for (let attempt = 0; attempt < 2; attempt++) {
      await throttle();
      const res = await doFetch(`${baseUrl}${path}`, {
        headers: { "User-Agent": userAgent, Accept: "application/json" },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      if (res.status === 503) {
        lastStatus = 503;
        continue;
      }
      if (!res.ok) throw new Error(`musicbrainz ${res.status}: ${await res.text()}`);
      return res.json();
    }
    throw new Error(`musicbrainz ${lastStatus}: rate limited after retry`);
  }

  return {
    async searchArtists(performer, opts) {
      const limit = opts?.limit ?? DEFAULT_LIMIT;
      // Escape the Lucene string exactly as internal/musicbrainz/client.go does.
      const esc = performer.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
      const q = new URLSearchParams({ query: `artist:"${esc}"`, fmt: "json", limit: String(limit) });
      const payload = (await getJson(`/ws/2/artist?${q.toString()}`)) as { artists?: MbArtist[] };
      return (payload.artists ?? []).map(toArtistMatch);
    },
  };
}

/** Production client. */
export const musicBrainzClient = createMusicBrainzClient();

export function searchArtists(performer: string, opts?: { limit?: number }): Promise<ArtistMatch[]> {
  return musicBrainzClient.searchArtists(performer, opts);
}

/** Thin wrapper so the lookup is inspectable in Mastra Studio. */
export const musicBrainzArtistTool = createTool({
  id: "musicbrainz-search-artist",
  description: "Resolve a fuzzy band name to MusicBrainz artist matches with disambiguation hints.",
  inputSchema: z.object({ performer: z.string(), limit: z.number().optional() }),
  outputSchema: z.object({ matches: z.array(ArtistMatchSchema) }),
  execute: async ({ performer, limit }) => ({ matches: await searchArtists(performer, { limit }) }),
});
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/musicbrainz.tool.test.ts && pnpm typecheck`
Expected: PASS (9 tests), typecheck clean.

- [ ] **Step 5: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 4: Wikimedia candidate resolution

**Files:**
- Create: `src/mastra/tools/wikimedia.tool.ts`
- Test: `src/mastra/tools/wikimedia.tool.test.ts`

**Interfaces:**
- Consumes: `ImageCandidate`, `ImageCredit`, `BandImage`, `USER_AGENT` (Task 1); `FetchFn` (Task 3); `stubFetch` (Task 2).
- Produces:
  - `createWikimediaClient(options?: WikimediaOptions): WikimediaClient`
  - `WikimediaOptions = { wikidataBaseUrl?: string; commonsBaseUrl?: string; userAgent?: string; fetchFn?: FetchFn }`
  - `WikimediaClient = { resolveImageCandidates(mbid, opts?: { artistName?: string }): Promise<ImageCandidate[]>; fetchImageBytes(candidate: ImageCandidate): Promise<BandImage> }`
  - `resolveImageCandidates` and `fetchImageBytes` — module-level convenience bound to the default production client. Tasks 6 and 7 import these.

Pipeline, all verified against the live APIs on 2026-08-05:
`mbid --[haswbstatement:P434]--> QID --[wbgetclaims P18/P373]--> file + category --[categorymembers]--> files --[imageinfo batched]--> candidates`.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/tools/wikimedia.tool.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { stubFetch, type StubRoute } from "./stub-fetch.js";
import { createWikimediaClient } from "./wikimedia.tool.js";

const MBID = "9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a";
const QID = "Q21485176";
const P18_FILE = "La Luz performing at Siberia in New Orleans, LA 5 March 2015.jpg";

const searchHit = { query: { search: [{ title: QID }] } };
const searchEmpty = { query: { search: [] } };
const claimsP18 = { claims: { P18: [{ mainsnak: { datavalue: { value: P18_FILE } } }] } };
const claimsP373 = { claims: { P373: [{ mainsnak: { datavalue: { value: "La Luz (band)" } } }] } };
const claimsNone = { claims: {} };

function categoryOf(...titles: string[]) {
  return { query: { categorymembers: titles.map((t, i) => ({ pageid: 100 + i, title: `File:${t}` })) } };
}

function imageinfoPage(title: string, extra: Record<string, unknown> = {}, meta: Record<string, string> = {}) {
  return {
    title: `File:${title}`,
    imageinfo: [
      {
        mime: "image/jpeg",
        width: 1600,
        height: 1200,
        thumbwidth: 1080,
        thumbheight: 810,
        thumburl: `https://upload.wikimedia.org/thumb/${encodeURIComponent(title)}`,
        descriptionurl: `https://commons.wikimedia.org/wiki/File:${title.replace(/ /g, "_")}`,
        extmetadata: Object.fromEntries(Object.entries(meta).map(([k, v]) => [k, { value: v }])),
        ...extra,
      },
    ],
  };
}

/**
 * `pages` is keyed by pageid. JS iterates integer-like keys in ascending
 * numeric order, so iteration follows ARGUMENT order here. Callers that want to
 * prove title-joining (rather than positional joining) pass pages in a
 * different order from the titles they requested.
 */
function imageinfoOf(...pages: ReturnType<typeof imageinfoPage>[]) {
  return { query: { pages: Object.fromEntries(pages.map((p, i) => [String(900 + i), p])) } };
}

const ROUTE_SEARCH: StubRoute = { match: /haswbstatement/, json: searchHit };

function client(routes: StubRoute[]) {
  const f = stubFetch(routes);
  const c = createWikimediaClient({
    wikidataBaseUrl: "https://wd.test",
    commonsBaseUrl: "https://commons.test",
    fetchFn: f,
  });
  return { c, f };
}

describe("resolveImageCandidates", () => {
  it("returns the P18 image first, tagged as p18", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    const out = await c.resolveImageCandidates(MBID);
    expect(out).toHaveLength(1);
    expect(out[0].source).toBe("p18");
    expect(out[0].file).toBe(`File:${P18_FILE}`);
  });

  it("uses THUMBNAIL dimensions, not the original", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.width).toBe(1080);
    expect(only.height).toBe(810);
    expect(only.url).toContain("upload.wikimedia.org/thumb");
  });

  it("requests thumbnails at the poster canvas width", async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    await c.resolveImageCandidates(MBID);
    const call = f.calls.find((x) => x.url.includes("prop=imageinfo"))!;
    expect(new URL(call.url).searchParams.get("iiurlwidth")).toBe("1080");
  });

  it("returns [] when the MBID has no Wikidata entity", async () => {
    const { c } = client([{ match: /haswbstatement/, json: searchEmpty }]);
    expect(await c.resolveImageCandidates(MBID)).toEqual([]);
  });

  it("returns [] when the entity has neither P18 nor P373", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsNone },
    ]);
    expect(await c.resolveImageCandidates(MBID)).toEqual([]);
  });

  it("dedupes the P18 file out of the category listing", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsP373 },
      // The P18 file really does appear in the category verbatim.
      { match: /list=categorymembers/, json: categoryOf(P18_FILE, "Shana Cleveland.jpg") },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(imageinfoPage(P18_FILE), imageinfoPage("Shana Cleveland.jpg")),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID);
    expect(out.map((x) => x.file)).toEqual([`File:${P18_FILE}`, "File:Shana Cleveland.jpg"]);
  });

  it("orders category files containing the artist name ahead of the rest", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      {
        match: /list=categorymembers/,
        json: categoryOf(
          "Jenn Ghetto, Lena Simon & Shana Cleveland - Pop Conference 2015 - 01.jpg",
          "La Luz performing at Siberia in New Orleans, LA 16 August 2015.jpg",
          "Shana Cleveland.jpg",
        ),
      },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage("Jenn Ghetto, Lena Simon & Shana Cleveland - Pop Conference 2015 - 01.jpg"),
          imageinfoPage("La Luz performing at Siberia in New Orleans, LA 16 August 2015.jpg"),
          imageinfoPage("Shana Cleveland.jpg"),
        ),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID, { artistName: "La Luz" });
    // Alphabetically "Jenn Ghetto..." would win; the artist-name rule promotes the band photo.
    expect(out[0].file).toContain("La Luz performing");
  });

  it("joins imageinfo by title even when pages come back shuffled", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf("A.jpg", "B.jpg") },
      {
        // Titles were requested A,B — pages come back B,A. Positional joining
        // would swap the dimensions; joining by title must not.
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage("B.jpg", { thumbwidth: 800, thumbheight: 600 }),
          imageinfoPage("A.jpg", { thumbwidth: 640, thumbheight: 480 }),
        ),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID, { artistName: "Nobody" });
    const a = out.find((x) => x.file === "File:A.jpg")!;
    const b = out.find((x) => x.file === "File:B.jpg")!;
    expect(a.width).toBe(640);
    expect(b.width).toBe(800);
  });

  it("drops non-image files that Commons categories carry", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf("Photo.jpg", "Doc.pdf", "Clip.ogv") },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage("Photo.jpg"),
          imageinfoPage("Doc.pdf", { mime: "application/pdf" }),
          imageinfoPage("Clip.ogv", { mime: "video/ogg" }),
        ),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID, { artistName: "x" });
    expect(out.map((x) => x.file)).toEqual(["File:Photo.jpg"]);
  });

  it("caps the pool at 12 candidates", async () => {
    const titles = Array.from({ length: 20 }, (_, i) => `Pic ${String(i).padStart(2, "0")}.jpg`);
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf(...titles) },
      { match: /prop=imageinfo/, json: imageinfoOf(...titles.map((t) => imageinfoPage(t))) },
    ]);
    expect(await c.resolveImageCandidates(MBID, { artistName: "x" })).toHaveLength(12);
  });

  it("sends the User-Agent on every Wikimedia call", async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    await c.resolveImageCandidates(MBID);
    expect(f.calls.length).toBeGreaterThan(0);
    for (const call of f.calls) {
      expect(call.headers["user-agent"]).toBe("heres-whats-happening/1.0 ( wlmmyers@gmail.com )");
    }
  });
});

describe("licensing capture", () => {
  const meta = {
    License: "cc-by-sa-4.0",
    LicenseShortName: "CC BY-SA 4.0",
    LicenseUrl: "https://creativecommons.org/licenses/by-sa/4.0",
    UsageTerms: "Creative Commons Attribution-Share Alike 4.0",
    Artist: "Shark2000br",
    Credit: "Own work",
    AttributionRequired: "true",
  };

  it("maps the extmetadata fields onto the credit", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE, {}, meta)) },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.credit.license).toBe("cc-by-sa-4.0");
    expect(only.credit.licenseShortName).toBe("CC BY-SA 4.0");
    expect(only.credit.artist).toBe("Shark2000br");
    expect(only.credit.attributionRequired).toBe(true);
    expect(only.credit.descriptionUrl).toContain("commons.wikimedia.org/wiki/File:");
  });

  it("requests extmetadata so licensing costs no extra call", async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE, {}, meta)) },
    ]);
    await c.resolveImageCandidates(MBID);
    const call = f.calls.find((x) => x.url.includes("prop=imageinfo"))!;
    const iiprop = new URL(call.url).searchParams.get("iiprop")!;
    expect(iiprop).toContain("extmetadata");
    expect(iiprop).toContain("url");
  });

  it("strips the HTML Commons wraps around Credit", async () => {
    const html =
      '<a rel="nofollow" class="external free" href="https://www.flickr.com/photos/jmabel/16609507803/">https://www.flickr.com/photos/jmabel/16609507803/</a>';
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(imageinfoPage(P18_FILE, {}, { Credit: html, Artist: "<span>Joe Mabel</span>" })),
      },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.credit.credit).toBe("https://www.flickr.com/photos/jmabel/16609507803/");
    expect(only.credit.artist).toBe("Joe Mabel");
  });

  it("keeps a public-domain file that has no Artist or License", async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE, {}, {})) },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.credit.artist).toBeUndefined();
    expect(only.credit.license).toBeUndefined();
    expect(only.credit.attributionRequired).toBe(false);
  });
});

describe("fetchImageBytes", () => {
  const candidate = {
    file: `File:${P18_FILE}`,
    url: "https://upload.wikimedia.org/thumb/la-luz.jpg",
    width: 1080,
    height: 810,
    contentType: "image/jpeg",
    source: "p18" as const,
    credit: {
      file: `File:${P18_FILE}`,
      descriptionUrl: "https://commons.wikimedia.org/wiki/File:La_Luz.jpg",
      artist: "Shark2000br",
      attributionRequired: true,
    },
  };

  it("returns base64 bytes with the candidate's dimensions and credit", async () => {
    const bytes = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]);
    const { c } = client([{ match: /upload\.wikimedia/, body: bytes }]);
    const img = await c.fetchImageBytes(candidate);

    expect(Buffer.from(img.imageBase64, "base64")).toEqual(bytes);
    expect(img.contentType).toBe("image/jpeg");
    expect(img.width).toBe(1080);
    expect(img.height).toBe(810);
    // sourceUrl is the durable Commons file page, not the thumbnail URL.
    expect(img.sourceUrl).toBe("https://commons.wikimedia.org/wiki/File:La_Luz.jpg");
    expect(img.credit?.artist).toBe("Shark2000br");
  });

  it("throws on a non-2xx so the caller can count it as a failed attempt", async () => {
    const { c } = client([{ match: /upload\.wikimedia/, status: 404, json: {} }]);
    await expect(c.fetchImageBytes(candidate)).rejects.toThrow(/404/);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/tools/wikimedia.tool.test.ts`
Expected: FAIL — `Cannot find module './wikimedia.tool.js'`

- [ ] **Step 3: Write the implementation**

Create `src/mastra/tools/wikimedia.tool.ts`:

```ts
import { createTool } from "@mastra/core/tools";
import { convert as htmlToText } from "html-to-text";
import { z } from "zod";
import {
  ImageCandidateSchema,
  USER_AGENT,
  type BandImage,
  type ImageCandidate,
  type ImageCredit,
} from "./band-image.js";
import type { FetchFn } from "./musicbrainz.tool.js";

const DEFAULT_WIKIDATA_BASE_URL = "https://www.wikidata.org";
const DEFAULT_COMMONS_BASE_URL = "https://commons.wikimedia.org";
const TIMEOUT_MS = 15_000;

// Matches the poster canvas width in svg-author.agent.ts (1080x1350). The image
// is embedded as a base64 data URI, so anything wider is carried through
// workflow state and into resvg for nothing.
const THUMB_WIDTH = 1080;
const MAX_CANDIDATES = 12;
const CATEGORY_LIMIT = 50;
const IMAGEINFO_BATCH = 50; // MediaWiki's cap for `titles`
const ALLOWED_MIME = new Set(["image/jpeg", "image/png"]);

const EXTMETA_FIELDS = [
  "License",
  "LicenseShortName",
  "LicenseUrl",
  "UsageTerms",
  "Artist",
  "Credit",
  "AttributionRequired",
  "Restrictions",
].join("|");

export interface WikimediaOptions {
  wikidataBaseUrl?: string;
  commonsBaseUrl?: string;
  userAgent?: string;
  fetchFn?: FetchFn;
}

export interface WikimediaClient {
  resolveImageCandidates(mbid: string, opts?: { artistName?: string }): Promise<ImageCandidate[]>;
  fetchImageBytes(candidate: ImageCandidate): Promise<BandImage>;
}

interface RawImageInfo {
  mime?: string;
  thumbwidth?: number;
  thumbheight?: number;
  thumburl?: string;
  descriptionurl?: string;
  extmetadata?: Record<string, { value?: unknown }>;
}

/** Commons stores file titles with spaces or underscores interchangeably. */
function normalizeTitle(title: string): string {
  return title.replace(/^File:/i, "").replace(/_/g, " ").trim().toLowerCase();
}

/** extmetadata values may contain anchors and spans; we want the bare text. */
function plainText(value: unknown): string | undefined {
  if (value === undefined || value === null) return undefined;
  const text = htmlToText(String(value), {
    wordwrap: false,
    selectors: [{ selector: "a", options: { ignoreHref: true } }],
  }).trim();
  return text.length > 0 ? text : undefined;
}

function toCredit(title: string, info: RawImageInfo): ImageCredit {
  const meta = info.extmetadata ?? {};
  const field = (key: string) => plainText(meta[key]?.value);
  return {
    file: title,
    descriptionUrl: info.descriptionurl ?? "",
    artist: field("Artist"),
    credit: field("Credit"),
    license: field("License"),
    licenseShortName: field("LicenseShortName"),
    licenseUrl: field("LicenseUrl"),
    usageTerms: field("UsageTerms"),
    attributionRequired: String(meta.AttributionRequired?.value ?? "").toLowerCase() === "true",
  };
}

export function createWikimediaClient(options: WikimediaOptions = {}): WikimediaClient {
  const wikidataBaseUrl = options.wikidataBaseUrl ?? DEFAULT_WIKIDATA_BASE_URL;
  const commonsBaseUrl = options.commonsBaseUrl ?? DEFAULT_COMMONS_BASE_URL;
  const userAgent = options.userAgent ?? USER_AGENT;
  const doFetch = options.fetchFn ?? globalThis.fetch;

  async function getJson(base: string, params: Record<string, string>): Promise<any> {
    const url = `${base}/w/api.php?${new URLSearchParams({ ...params, format: "json" }).toString()}`;
    const res = await doFetch(url, {
      headers: { "User-Agent": userAgent, Accept: "application/json" },
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`wikimedia ${res.status} for ${base}`);
    return res.json();
  }

  /** MBID -> QID via Wikidata's reverse P434 index. Avoids a rate-limited MB call. */
  async function resolveQid(mbid: string): Promise<string | null> {
    const payload = await getJson(wikidataBaseUrl, {
      action: "query",
      list: "search",
      srsearch: `haswbstatement:P434=${mbid}`,
    });
    return payload?.query?.search?.[0]?.title ?? null;
  }

  async function claimValue(qid: string, property: string): Promise<string | null> {
    const payload = await getJson(wikidataBaseUrl, { action: "wbgetclaims", entity: qid, property });
    const value = payload?.claims?.[property]?.[0]?.mainsnak?.datavalue?.value;
    return typeof value === "string" && value.length > 0 ? value : null;
  }

  async function categoryFiles(category: string): Promise<string[]> {
    const payload = await getJson(commonsBaseUrl, {
      action: "query",
      list: "categorymembers",
      cmtitle: `Category:${category}`,
      cmtype: "file",
      cmlimit: String(CATEGORY_LIMIT),
    });
    const members = payload?.query?.categorymembers ?? [];
    return members.map((m: { title: string }) => m.title).filter(Boolean);
  }

  /**
   * Batched imageinfo. `pages` comes back keyed by pageid in ARBITRARY order,
   * so results are joined by normalized title, never by position.
   */
  async function imageInfo(titles: string[]): Promise<Map<string, RawImageInfo>> {
    const out = new Map<string, RawImageInfo>();
    for (let i = 0; i < titles.length; i += IMAGEINFO_BATCH) {
      const batch = titles.slice(i, i + IMAGEINFO_BATCH);
      const payload = await getJson(commonsBaseUrl, {
        action: "query",
        prop: "imageinfo",
        titles: batch.join("|"),
        iiprop: "url|size|mime|extmetadata",
        iiextmetadatafilter: EXTMETA_FIELDS,
        iiurlwidth: String(THUMB_WIDTH),
      });
      const pages = payload?.query?.pages ?? {};
      for (const page of Object.values<any>(pages)) {
        const info = page?.imageinfo?.[0];
        if (page?.title && info) out.set(normalizeTitle(page.title), info as RawImageInfo);
      }
    }
    return out;
  }

  function toCandidate(title: string, info: RawImageInfo, source: "p18" | "category"): ImageCandidate | null {
    const mime = info.mime ?? "";
    if (!ALLOWED_MIME.has(mime)) return null;
    if (!info.thumburl || !info.thumbwidth || !info.thumbheight) return null;
    return {
      file: title,
      url: info.thumburl,
      width: info.thumbwidth,
      height: info.thumbheight,
      contentType: mime,
      source,
      credit: toCredit(title, info),
    };
  }

  return {
    async resolveImageCandidates(mbid, opts) {
      const qid = await resolveQid(mbid);
      if (!qid) return [];

      const [p18, category] = await Promise.all([claimValue(qid, "P18"), claimValue(qid, "P373")]);
      if (!p18 && !category) return [];

      const p18Title = p18 ? `File:${p18}` : null;
      const categoryTitles = category ? await categoryFiles(category) : [];

      const wanted = [...(p18Title ? [p18Title] : []), ...categoryTitles];
      if (wanted.length === 0) return [];
      const info = await imageInfo(wanted);

      const seen = new Set<string>();
      const ordered: ImageCandidate[] = [];

      if (p18Title) {
        const raw = info.get(normalizeTitle(p18Title));
        const candidate = raw ? toCandidate(p18Title, raw, "p18") : null;
        if (candidate) {
          ordered.push(candidate);
          seen.add(normalizeTitle(p18Title));
        }
      }

      // Commons category order is alphabetical, which buries the band photos
      // under solo shots and panel photos. Promote titles naming the artist.
      const needle = opts?.artistName?.trim().toLowerCase();
      const rest = categoryTitles
        .filter((t) => !seen.has(normalizeTitle(t)))
        .map((t) => {
          const raw = info.get(normalizeTitle(t));
          return raw ? toCandidate(t, raw, "category") : null;
        })
        .filter((c): c is ImageCandidate => c !== null)
        .sort((a, b) => {
          const aHit = needle && a.file.toLowerCase().includes(needle) ? 0 : 1;
          const bHit = needle && b.file.toLowerCase().includes(needle) ? 0 : 1;
          if (aHit !== bHit) return aHit - bHit;
          return a.file.localeCompare(b.file);
        });

      return [...ordered, ...rest].slice(0, MAX_CANDIDATES);
    },

    async fetchImageBytes(candidate) {
      const res = await doFetch(candidate.url, {
        headers: { "User-Agent": userAgent },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      if (!res.ok) throw new Error(`commons ${res.status} fetching ${candidate.file}`);
      const bytes = Buffer.from(await res.arrayBuffer());
      return {
        imageBase64: bytes.toString("base64"),
        contentType: candidate.contentType,
        width: candidate.width,
        height: candidate.height,
        sourceUrl: candidate.credit.descriptionUrl || candidate.url,
        credit: candidate.credit,
      };
    },
  };
}

/** Production client. */
export const wikimediaClient = createWikimediaClient();

export function resolveImageCandidates(mbid: string, opts?: { artistName?: string }): Promise<ImageCandidate[]> {
  return wikimediaClient.resolveImageCandidates(mbid, opts);
}

export function fetchImageBytes(candidate: ImageCandidate): Promise<BandImage> {
  return wikimediaClient.fetchImageBytes(candidate);
}

/** Thin wrapper so the lookup is inspectable in Mastra Studio. */
export const wikimediaImagesTool = createTool({
  id: "wikimedia-artist-images",
  description: "Resolve a MusicBrainz artist MBID to Wikimedia Commons image candidates with licensing.",
  inputSchema: z.object({ mbid: z.string(), artistName: z.string().optional() }),
  outputSchema: z.object({ candidates: z.array(ImageCandidateSchema) }),
  execute: async ({ mbid, artistName }) => ({ candidates: await resolveImageCandidates(mbid, { artistName }) }),
});
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/tools/wikimedia.tool.test.ts && pnpm typecheck`
Expected: PASS (17 tests), typecheck clean.

- [ ] **Step 5: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 5: Loop state schema

**Files:**
- Modify: `src/mastra/workflows/poster.schemas.ts`
- Test: `src/mastra/workflows/poster.schemas.test.ts`

**Interfaces:**
- Consumes: `ArtistMatchSchema`, `ImageCandidateSchema`, `ImageCreditSchema`, `BandImageSchema` (Task 1).
- Produces: `ImageLoopStateSchema` with `artist?`, `candidates`, `candidateIndex`; `PosterLoopStateSchema` with `artist?`, `credit?`; `PosterWorkflowOutputSchema` with `artist?`, `credit?`; `MAX_ARTIST_FALLTHROUGH = 3`.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/workflows/poster.schemas.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import {
  ImageLoopStateSchema,
  MAX_ARTIST_FALLTHROUGH,
  PosterLoopStateSchema,
  PosterWorkflowOutputSchema,
} from "./poster.schemas.js";

const base = { performer: "La Luz", venue: "Occidental Square", date: "Thursday, August 20" };

describe("ImageLoopStateSchema", () => {
  it("defaults candidates to empty and the index to 0", () => {
    const s = ImageLoopStateSchema.parse({ ...base, attempts: 0, accepted: false });
    expect(s.candidates).toEqual([]);
    expect(s.candidateIndex).toBe(0);
    expect(s.artist).toBeUndefined();
  });

  it("carries a resolved artist", () => {
    const s = ImageLoopStateSchema.parse({
      ...base,
      attempts: 0,
      accepted: false,
      artist: { mbid: "abc", name: "La Luz", score: 100, disambiguation: "US rock band" },
    });
    expect(s.artist?.disambiguation).toBe("US rock band");
  });
});

describe("PosterLoopStateSchema", () => {
  it("carries artist and credit through the second loop", () => {
    const s = PosterLoopStateSchema.parse({
      ...base,
      imageOk: true,
      attempts: 0,
      accepted: false,
      artist: { mbid: "abc", name: "La Luz", score: 100 },
      credit: { file: "File:x.jpg", descriptionUrl: "https://commons.wikimedia.org/wiki/File:x.jpg" },
    });
    expect(s.artist?.mbid).toBe("abc");
    expect(s.credit?.attributionRequired).toBe(false);
  });
});

describe("PosterWorkflowOutputSchema", () => {
  it("accepts provenance on a failure result", () => {
    const o = PosterWorkflowOutputSchema.parse({
      ok: false,
      failureStage: "image",
      reason: "no acceptable band image found",
      artist: { mbid: "abc", name: "La Luz", score: 100 },
    });
    expect(o.artist?.name).toBe("La Luz");
  });
});

describe("MAX_ARTIST_FALLTHROUGH", () => {
  it("bounds how many MusicBrainz matches get probed", () => {
    expect(MAX_ARTIST_FALLTHROUGH).toBe(3);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/poster.schemas.test.ts`
Expected: FAIL — `MAX_ARTIST_FALLTHROUGH` is not exported and `candidates` is undefined.

- [ ] **Step 3: Write the implementation**

Replace `src/mastra/workflows/poster.schemas.ts` with:

```ts
import { z } from "zod";
import { ArtistMatchSchema, BandImageSchema, ImageCandidateSchema, ImageCreditSchema } from "../tools/band-image.js";

// Loop-1 state: input and output of the judge-band-image step are the SAME shape,
// so the step's output can feed straight back as the next iteration's input.
// `candidates` is resolved ONCE before the loop; `candidateIndex` walks it.
export const ImageLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  attempts: z.number(),
  accepted: z.boolean(),
  reason: z.string().optional(),
  image: BandImageSchema.optional(),
  colors: z.array(z.string()).default([]),
  artist: ArtistMatchSchema.optional(),
  candidates: z.array(ImageCandidateSchema).default([]),
  candidateIndex: z.number().default(0),
});
export type ImageLoopState = z.infer<typeof ImageLoopStateSchema>;

// Loop-2 state: input and output of the compose-poster step are the SAME shape.
// `artist` and `credit` are carriers only — composePosterStep neither reads nor
// writes them; they exist so finalizeStep can report provenance.
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
  artist: ArtistMatchSchema.optional(),
  credit: ImageCreditSchema.optional(),
});
export type PosterLoopState = z.infer<typeof PosterLoopStateSchema>;

// Final workflow output: a controlled result (ok or a typed failure stage+reason),
// plus provenance on BOTH branches — a failure is far more actionable when it
// names the artist that was resolved.
export const PosterWorkflowOutputSchema = z.object({
  ok: z.boolean(),
  svg: z.string().optional(),
  pngBase64: z.string().optional(),
  failureStage: z.enum(["image", "svg"]).optional(),
  reason: z.string().optional(),
  artist: ArtistMatchSchema.optional(),
  credit: ImageCreditSchema.optional(),
});
export type PosterWorkflowOutput = z.infer<typeof PosterWorkflowOutputSchema>;

export const MAX_IMAGE_ATTEMPTS = Number(process.env.MAX_IMAGE_ATTEMPTS ?? 3);
export const MAX_SVG_ATTEMPTS = Number(process.env.MAX_SVG_ATTEMPTS ?? 3);

/** How many MusicBrainz matches to probe before giving up on images. */
export const MAX_ARTIST_FALLTHROUGH = 3;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/workflows/poster.schemas.test.ts && pnpm typecheck`
Expected: tests PASS. Typecheck is expected to be clean — the legacy `acquire-band-image.step.ts` still compiles at this point, because the added state fields all carry `.default()` and its `{ ...inputData }` spread satisfies them. If it *does* error, that file is deleted in Task 8; note the error and continue rather than patching a file you are about to remove.

- [ ] **Step 5: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 6: Resolve-candidates step

**Files:**
- Create: `src/mastra/workflows/resolve-band-candidates.step.ts`
- Test: `src/mastra/workflows/resolve-band-candidates.step.test.ts`

**Interfaces:**
- Consumes: `searchArtists` (Task 3), `resolveImageCandidates` (Task 4), `ImageLoopStateSchema` + `MAX_ARTIST_FALLTHROUGH` (Task 5).
- Produces: `resolveBandCandidatesStep` — a Mastra step with id `"resolve-band-candidates"`, `ImageLoopStateSchema` in and out. Task 8 wires it into the workflow.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/workflows/resolve-band-candidates.step.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtistMatch, ImageCandidate } from "../tools/band-image.js";

const searchArtists = vi.fn<(p: string, o?: { limit?: number }) => Promise<ArtistMatch[]>>();
const resolveImageCandidates = vi.fn<(m: string, o?: { artistName?: string }) => Promise<ImageCandidate[]>>();

vi.mock("../tools/musicbrainz.tool.js", () => ({ searchArtists: (...a: never[]) => searchArtists(...(a as never)) }));
vi.mock("../tools/wikimedia.tool.js", () => ({
  resolveImageCandidates: (...a: never[]) => resolveImageCandidates(...(a as never)),
}));

const { resolveBandCandidatesStep } = await import("./resolve-band-candidates.step.js");

const input = {
  performer: "la luz",
  venue: "Occidental Square",
  date: "Thursday, August 20",
  attempts: 0,
  accepted: false,
  colors: [],
  candidates: [],
  candidateIndex: 0,
};

const rock: ArtistMatch = { mbid: "mb-rock", name: "La Luz", score: 100, disambiguation: "US rock band" };
const house: ArtistMatch = { mbid: "mb-house", name: "La Luz", score: 88, disambiguation: "Belgium based house group" };
const chill: ArtistMatch = { mbid: "mb-chill", name: "La Luz", score: 88, disambiguation: "chillout music" };

function candidate(file: string): ImageCandidate {
  return {
    file,
    url: `https://upload.wikimedia.org/${file}`,
    width: 1080,
    height: 810,
    contentType: "image/jpeg",
    source: "p18",
    credit: { file, descriptionUrl: `https://commons.wikimedia.org/wiki/${file}`, attributionRequired: false },
  };
}

// The step is invoked exactly as the workflow engine invokes it.
const run = (data: typeof input) => resolveBandCandidatesStep.execute({ inputData: data } as never) as Promise<any>;

beforeEach(() => {
  searchArtists.mockReset();
  resolveImageCandidates.mockReset();
});

describe("resolveBandCandidatesStep", () => {
  it("stores the top match and its candidates", async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);

    const out = await run(input);
    expect(out.artist).toEqual(rock);
    expect(out.candidates).toHaveLength(1);
    expect(out.candidateIndex).toBe(0);
    expect(resolveImageCandidates).toHaveBeenCalledWith("mb-rock", { artistName: "La Luz" });
  });

  it("falls through to the next match when the top one yields nothing", async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValueOnce([]).mockResolvedValueOnce([candidate("File:B.jpg")]);

    const out = await run(input);
    expect(out.artist).toEqual(house);
    expect(out.candidates).toHaveLength(1);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(2);
  });

  it("stops probing at the first match that yields candidates", async () => {
    searchArtists.mockResolvedValue([rock, house, chill]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);

    await run(input);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(1);
  });

  it("probes at most three matches", async () => {
    searchArtists.mockResolvedValue([rock, house, chill, { ...chill, mbid: "mb-4" }]);
    resolveImageCandidates.mockResolvedValue([]);

    const out = await run(input);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(3);
    expect(out.candidates).toEqual([]);
  });

  it("names who was tried when nothing yields an image", async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValue([]);

    const out = await run(input);
    expect(out.reason).toContain("La Luz (US rock band)");
    expect(out.reason).toContain("La Luz (Belgium based house group)");
    // The best match is still reported so the caller knows who was looked up.
    expect(out.artist).toEqual(rock);
  });

  it("reports no MusicBrainz match without throwing", async () => {
    searchArtists.mockResolvedValue([]);
    const out = await run(input);
    expect(out.candidates).toEqual([]);
    expect(out.reason).toContain("no MusicBrainz match");
    expect(out.artist).toBeUndefined();
  });

  it("converts a MusicBrainz failure into state, never throwing", async () => {
    searchArtists.mockRejectedValue(new Error("ECONNRESET"));
    const out = await run(input);
    expect(out.candidates).toEqual([]);
    expect(out.reason).toContain("musicbrainz search failed");
    expect(out.reason).toContain("ECONNRESET");
  });

  it("keeps falling through when a Wikimedia call errors, and reports the last error", async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockRejectedValueOnce(new Error("wikimedia 500")).mockResolvedValueOnce([]);

    const out = await run(input);
    expect(out.candidates).toEqual([]);
    expect(out.reason).toContain("wikimedia 500");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/resolve-band-candidates.step.test.ts`
Expected: FAIL — `Cannot find module './resolve-band-candidates.step.js'`

- [ ] **Step 3: Write the implementation**

Create `src/mastra/workflows/resolve-band-candidates.step.ts`:

```ts
import { createStep } from "@mastra/core/workflows";
import type { ArtistMatch } from "../tools/band-image.js";
import { searchArtists } from "../tools/musicbrainz.tool.js";
import { resolveImageCandidates } from "../tools/wikimedia.tool.js";
import { ImageLoopStateSchema, MAX_ARTIST_FALLTHROUGH } from "./poster.schemas.js";

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function label(a: ArtistMatch): string {
  return a.disambiguation ? `${a.name} (${a.disambiguation})` : a.name;
}

// Runs ONCE, ahead of the loop: MB search -> MBID -> Wikidata -> Commons.
// Deterministic for a given performer, so doing it per-iteration would re-spend
// the 1 req/sec MusicBrainz budget recomputing an identical answer.
// Never throws; failures come back as state with a reason (rasterize.tool.ts:28).
export const resolveBandCandidatesStep = createStep({
  id: "resolve-band-candidates",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData }) => {
    let matches: ArtistMatch[];
    try {
      matches = await searchArtists(inputData.performer, { limit: MAX_ARTIST_FALLTHROUGH });
    } catch (e) {
      return { ...inputData, candidates: [], reason: `musicbrainz search failed: ${message(e)}` };
    }

    if (matches.length === 0) {
      return { ...inputData, candidates: [], reason: `no MusicBrainz match for '${inputData.performer}'` };
    }

    const tried: string[] = [];
    let lastError: string | undefined;

    for (const match of matches.slice(0, MAX_ARTIST_FALLTHROUGH)) {
      tried.push(label(match));
      try {
        const candidates = await resolveImageCandidates(match.mbid, { artistName: match.name });
        if (candidates.length > 0) {
          return { ...inputData, artist: match, candidates, candidateIndex: 0 };
        }
      } catch (e) {
        lastError = message(e);
      }
    }

    const detail = lastError ? `; last error: ${lastError}` : "";
    return {
      ...inputData,
      artist: matches[0], // report the best match even though it yielded nothing
      candidates: [],
      reason: `no Wikimedia image for '${inputData.performer}' (tried: ${tried.join(", ")})${detail}`,
    };
  },
});
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/workflows/resolve-band-candidates.step.test.ts`
Expected: PASS (8 tests).

- [ ] **Step 5: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 7: Judge-image step

**Files:**
- Create: `src/mastra/workflows/judge-band-image.step.ts`
- Test: `src/mastra/workflows/judge-band-image.step.test.ts`

**Interfaces:**
- Consumes: `fetchImageBytes` (Task 4), `ImageLoopStateSchema` (Task 5), `imageAnalysisAgent` + `ImageAnalysisSchema` (existing, `../agents/image-analysis.agent.js`).
- Produces: `judgeBandImageStep` — a Mastra step with id `"judge-band-image"`, `ImageLoopStateSchema` in and out. Task 8 wires it into the loop.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/workflows/judge-band-image.step.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BandImage, ImageCandidate } from "../tools/band-image.js";

const fetchImageBytes = vi.fn<(c: ImageCandidate) => Promise<BandImage>>();
const generate = vi.fn();

vi.mock("../tools/wikimedia.tool.js", () => ({ fetchImageBytes: (...a: never[]) => fetchImageBytes(...(a as never)) }));
vi.mock("../agents/image-analysis.agent.js", () => ({
  imageAnalysisAgent: { generate: (...a: never[]) => generate(...(a as never)) },
  ImageAnalysisSchema: {},
}));

const { judgeBandImageStep } = await import("./judge-band-image.step.js");

function candidate(file: string): ImageCandidate {
  return {
    file,
    url: `https://upload.wikimedia.org/${file}`,
    width: 1080,
    height: 810,
    contentType: "image/jpeg",
    source: "category",
    credit: { file, descriptionUrl: `https://commons.wikimedia.org/wiki/${file}`, attributionRequired: true },
  };
}

const image: BandImage = { imageBase64: "AAAA", contentType: "image/jpeg", width: 1080, height: 810 };

const base = {
  performer: "la luz",
  venue: "Occidental Square",
  date: "Thursday, August 20",
  attempts: 0,
  accepted: false,
  colors: [],
  candidates: [candidate("File:A.jpg"), candidate("File:B.jpg")],
  candidateIndex: 0,
  artist: {
    mbid: "mb-rock",
    name: "La Luz",
    score: 100,
    disambiguation: "US rock band",
    type: "Group",
    country: "US",
    beginYear: "2012",
  },
};

const run = (data: Record<string, unknown>) => judgeBandImageStep.execute({ inputData: data } as never) as Promise<any>;

beforeEach(() => {
  fetchImageBytes.mockReset();
  generate.mockReset();
});

describe("judgeBandImageStep", () => {
  it("accepts a good candidate and records the colors", async () => {
    fetchImageBytes.mockResolvedValue(image);
    generate.mockResolvedValue({
      object: { acceptable: true, reason: "clear band photo", dominantColors: ["#111", "#222"] },
    });

    const out = await run(base);
    expect(out.accepted).toBe(true);
    expect(out.attempts).toBe(1);
    expect(out.candidateIndex).toBe(1);
    expect(out.colors).toEqual(["#111", "#222"]);
    expect(out.image).toEqual(image);
  });

  it("advances the index on rejection so the next attempt sees a new candidate", async () => {
    fetchImageBytes.mockResolvedValue(image);
    generate.mockResolvedValue({ object: { acceptable: false, reason: "album art", dominantColors: [] } });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.candidateIndex).toBe(1);
    expect(fetchImageBytes).toHaveBeenCalledWith(base.candidates[0]);
  });

  it("judges the indexed candidate, not always the first", async () => {
    fetchImageBytes.mockResolvedValue(image);
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });

    await run({ ...base, candidateIndex: 1, attempts: 1 });
    expect(fetchImageBytes).toHaveBeenCalledWith(base.candidates[1]);
  });

  it("short-circuits without spending an attempt when the pool is empty", async () => {
    const out = await run({ ...base, candidates: [], candidateIndex: 0 });
    expect(out.attempts).toBe(0);
    expect(out.accepted).toBe(false);
    expect(fetchImageBytes).not.toHaveBeenCalled();
    expect(generate).not.toHaveBeenCalled();
  });

  it("short-circuits when the index has run past the pool", async () => {
    const out = await run({ ...base, candidateIndex: 2 });
    expect(out.attempts).toBe(0);
    expect(generate).not.toHaveBeenCalled();
  });

  it("counts a byte-fetch failure as a used attempt and moves on", async () => {
    fetchImageBytes.mockRejectedValue(new Error("commons 404"));

    const out = await run(base);
    expect(out.attempts).toBe(1);
    expect(out.candidateIndex).toBe(1);
    expect(out.accepted).toBe(false);
    expect(out.reason).toContain("could not fetch File:A.jpg");
    expect(out.reason).toContain("commons 404");
    expect(generate).not.toHaveBeenCalled();
  });

  it("handles the agent returning no structured object", async () => {
    fetchImageBytes.mockResolvedValue(image);
    generate.mockResolvedValue({ object: undefined });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.reason).toBe("image analysis returned no result");
    expect(out.candidateIndex).toBe(1);
  });

  it("puts the artist disambiguation into the prompt", async () => {
    fetchImageBytes.mockResolvedValue(image);
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });

    await run(base);
    const messages = generate.mock.calls[0][0] as Array<{ content: Array<{ type: string; text?: string }> }>;
    const text = messages[0].content.find((c) => c.type === "text")!.text!;
    expect(text).toContain("La Luz");
    expect(text).toContain("US rock band");
    expect(text).toContain("2012");
  });

  it("falls back to the raw performer name when no artist was resolved", async () => {
    fetchImageBytes.mockResolvedValue(image);
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });

    await run({ ...base, artist: undefined });
    const messages = generate.mock.calls[0][0] as Array<{ content: Array<{ type: string; text?: string }> }>;
    expect(messages[0].content.find((c) => c.type === "text")!.text).toContain("la luz");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/judge-band-image.step.test.ts`
Expected: FAIL — `Cannot find module './judge-band-image.step.js'`

- [ ] **Step 3: Write the implementation**

Create `src/mastra/workflows/judge-band-image.step.ts`:

```ts
import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { ImageAnalysisSchema, imageAnalysisAgent } from "../agents/image-analysis.agent.js";
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
  return [artist.name, artist.disambiguation, artist.type, artist.country, artist.beginYear ? `formed ${artist.beginYear}` : undefined]
    .filter(Boolean)
    .join(", ");
}

// One iteration: fetch the indexed candidate's bytes, then a vision agent judges
// it. Output shape == input shape so .dountil can loop. `candidateIndex` advances
// on every iteration regardless of verdict, so the next attempt sees a NEW photo.
export const judgeBandImageStep = createStep({
  id: "judge-band-image",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData }) => {
    const candidate = inputData.candidates[inputData.candidateIndex];
    // Cheap short-circuit: nothing to judge, so spend no attempt and no LLM call.
    if (!candidate) {
      return { ...inputData, accepted: false };
    }

    const attempts = inputData.attempts + 1;
    const candidateIndex = inputData.candidateIndex + 1;

    let image;
    try {
      image = await fetchImageBytes(candidate);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `could not fetch ${candidate.file}: ${message(e)}`,
      };
    }

    const who = describeArtist(inputData.artist, inputData.performer);
    const res = await imageAnalysisAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: Buffer.from(image.imageBase64, "base64"), mimeType: image.contentType },
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

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm vitest run src/mastra/workflows/judge-band-image.step.test.ts`
Expected: PASS (9 tests).

- [ ] **Step 5: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 8: Workflow wiring and deletions

**Files:**
- Modify: `src/mastra/workflows/poster.workflow.ts`
- Delete: `src/mastra/workflows/acquire-band-image.step.ts`, `src/mastra/tools/web-scrape.tool.ts`, `src/mastra/tools/web-scrape.tool.test.ts`
- Test: `src/mastra/workflows/poster.workflow.test.ts`

**Interfaces:**
- Consumes: `resolveBandCandidatesStep` (Task 6), `judgeBandImageStep` (Task 7), schemas (Task 5).
- Produces: `posterWorkflow` with the new shape. This is the regression test for the originally reported symptom.

- [ ] **Step 1: Write the failing test**

Create `src/mastra/workflows/poster.workflow.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtistMatch, BandImage, ImageCandidate } from "../tools/band-image.js";

const searchArtists = vi.fn<(p: string, o?: { limit?: number }) => Promise<ArtistMatch[]>>();
const resolveImageCandidates = vi.fn<(m: string, o?: { artistName?: string }) => Promise<ImageCandidate[]>>();
const fetchImageBytes = vi.fn<(c: ImageCandidate) => Promise<BandImage>>();
const analyze = vi.fn();
const compose = vi.fn();

vi.mock("../tools/musicbrainz.tool.js", () => ({ searchArtists: (...a: never[]) => searchArtists(...(a as never)) }));
vi.mock("../tools/wikimedia.tool.js", () => ({
  resolveImageCandidates: (...a: never[]) => resolveImageCandidates(...(a as never)),
  fetchImageBytes: (...a: never[]) => fetchImageBytes(...(a as never)),
}));
vi.mock("../agents/image-analysis.agent.js", () => ({
  imageAnalysisAgent: { generate: (...a: never[]) => analyze(...(a as never)) },
  ImageAnalysisSchema: {},
}));
// Keep the compose loop out of this test; it has its own coverage.
vi.mock("./compose-poster.step.js", async () => {
  const { createStep } = await import("@mastra/core/workflows");
  const { PosterLoopStateSchema } = await import("./poster.schemas.js");
  return {
    composePosterStep: createStep({
      id: "compose-poster",
      inputSchema: PosterLoopStateSchema,
      outputSchema: PosterLoopStateSchema,
      execute: async ({ inputData }: { inputData: Record<string, unknown> }) => compose(inputData),
    }),
  };
});

const { posterWorkflow } = await import("./poster.workflow.js");

const rock: ArtistMatch = { mbid: "mb-rock", name: "La Luz", score: 100, disambiguation: "US rock band" };

function candidate(file: string): ImageCandidate {
  return {
    file,
    url: `https://upload.wikimedia.org/${file}`,
    width: 1080,
    height: 810,
    contentType: "image/jpeg",
    source: "category",
    credit: {
      file,
      descriptionUrl: `https://commons.wikimedia.org/wiki/${file}`,
      artist: "Shark2000br",
      licenseShortName: "CC BY-SA 4.0",
      attributionRequired: true,
    },
  };
}

const image: BandImage = { imageBase64: "AAAA", contentType: "image/jpeg", width: 1080, height: 810 };
const request = { performer: "la luz", venue: "Occidental Square", date: "Thursday, August 20" };

async function runWorkflow() {
  const run = await posterWorkflow.createRun();
  return run.start({ inputData: request });
}

beforeEach(() => {
  searchArtists.mockReset();
  resolveImageCandidates.mockReset();
  fetchImageBytes.mockReset();
  analyze.mockReset();
  compose.mockReset();
  fetchImageBytes.mockResolvedValue(image);
  // MUST increment `attempts`. The compose loop's exit condition is
  // `accepted || !imageOk || attempts >= MAX_SVG_ATTEMPTS` — a stub that returns
  // accepted:false without advancing attempts loops forever whenever imageOk is true.
  compose.mockImplementation(async (s: Record<string, unknown>) => ({
    ...s,
    attempts: ((s.attempts as number) ?? 0) + 1,
    accepted: false,
  }));
});

describe("posterWorkflow image loop", () => {
  it("judges three DISTINCT candidates across three rejections", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg"), candidate("File:B.jpg"), candidate("File:C.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "not the band", dominantColors: [] } });

    const result = await runWorkflow();

    expect(fetchImageBytes).toHaveBeenCalledTimes(3);
    const judged = fetchImageBytes.mock.calls.map((c) => c[0].file);
    expect(new Set(judged).size).toBe(3);
    expect(judged).toEqual(["File:A.jpg", "File:B.jpg", "File:C.jpg"]);

    const steps = (result as any).steps;
    expect(steps["judge-band-image"].metadata.iterationCount).toBe(3);
    expect((result as any).result.ok).toBe(false);
    expect((result as any).result.failureStage).toBe("image");
  });

  it("resolves candidates exactly once, not per iteration", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg"), candidate("File:B.jpg"), candidate("File:C.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "no", dominantColors: [] } });

    await runWorkflow();

    expect(searchArtists).toHaveBeenCalledTimes(1);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(1);
  });

  it("stops the loop as soon as a candidate is accepted", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg"), candidate("File:B.jpg"), candidate("File:C.jpg")]);
    analyze
      .mockResolvedValueOnce({ object: { acceptable: false, reason: "album art", dominantColors: [] } })
      .mockResolvedValueOnce({ object: { acceptable: true, reason: "great shot", dominantColors: ["#abc"] } });

    await runWorkflow();

    expect(fetchImageBytes).toHaveBeenCalledTimes(2);
    // The accepted image reached the compose loop.
    expect(compose).toHaveBeenCalled();
    expect(compose.mock.calls[0][0].imageOk).toBe(true);
    expect(compose.mock.calls[0][0].colors).toEqual(["#abc"]);
  });

  it("exits when candidates run out before MAX_IMAGE_ATTEMPTS", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:only.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "no", dominantColors: [] } });

    const result = await runWorkflow();

    expect(fetchImageBytes).toHaveBeenCalledTimes(1);
    expect((result as any).result.failureStage).toBe("image");
  });

  it("spends no vision call when nothing resolves", async () => {
    searchArtists.mockResolvedValue([]);
    const result = await runWorkflow();

    expect(analyze).not.toHaveBeenCalled();
    expect(fetchImageBytes).not.toHaveBeenCalled();
    expect((result as any).result.failureStage).toBe("image");
    expect((result as any).result.reason).toContain("no MusicBrainz match");
  });
});

describe("posterWorkflow provenance", () => {
  it("carries artist and credit through seed2 into the compose loop", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    fetchImageBytes.mockResolvedValue({ ...image, credit: candidate("File:A.jpg").credit });
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });

    await runWorkflow();

    const seeded = compose.mock.calls[0][0];
    expect(seeded.artist).toEqual(rock);
    expect(seeded.credit.licenseShortName).toBe("CC BY-SA 4.0");
  });

  it("reports the artist on an image-stage FAILURE, not only on success", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "wrong band", dominantColors: [] } });

    const result = await runWorkflow();
    expect((result as any).result.ok).toBe(false);
    expect((result as any).result.artist).toEqual(rock);
  });

  it("reports the SUBSTITUTED artist when fall-through picks match two", async () => {
    const house: ArtistMatch = { mbid: "mb-house", name: "La Luz", score: 88, disambiguation: "Belgium based house group" };
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValueOnce([]).mockResolvedValueOnce([candidate("File:H.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "no", dominantColors: [] } });

    const result = await runWorkflow();
    expect((result as any).result.artist).toEqual(house);
  });

  it("returns svg + png + provenance on full success", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    fetchImageBytes.mockResolvedValue({ ...image, credit: candidate("File:A.jpg").credit });
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    compose.mockImplementation(async (s: Record<string, unknown>) => ({
      ...s,
      attempts: ((s.attempts as number) ?? 0) + 1,
      accepted: true,
      svg: "<svg/>",
      pngBase64: "PNGPNG",
    }));

    const result = await runWorkflow();
    const out = (result as any).result;
    expect(out.ok).toBe(true);
    expect(out.svg).toBe("<svg/>");
    expect(out.pngBase64).toBe("PNGPNG");
    expect(out.artist).toEqual(rock);
    expect(out.credit.artist).toBe("Shark2000br");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/mastra/workflows/poster.workflow.test.ts`
Expected: FAIL — the workflow still references `acquireBandImageStep` and has no `judge-band-image` step.

- [ ] **Step 3: Rewrite the workflow**

Replace `src/mastra/workflows/poster.workflow.ts` with:

```ts
import { createStep, createWorkflow } from "@mastra/core/workflows";
import { PosterRequestSchema } from "../../poster-schema.js";
import { composePosterStep } from "./compose-poster.step.js";
import { judgeBandImageStep } from "./judge-band-image.step.js";
import {
  MAX_IMAGE_ATTEMPTS,
  MAX_SVG_ATTEMPTS,
  PosterLoopStateSchema,
  PosterWorkflowOutputSchema,
} from "./poster.schemas.js";
import { resolveBandCandidatesStep } from "./resolve-band-candidates.step.js";

// Terminal step: normalize the last loop state into the controlled workflow output.
// (Workflows must end on a step whose outputSchema matches the workflow outputSchema.)
// Provenance is emitted on BOTH branches — a failure that names the resolved
// artist is far more actionable than a bare "no acceptable band image found".
const finalizeStep = createStep({
  id: "finalize-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterWorkflowOutputSchema,
  execute: async ({ inputData }) => {
    const provenance = { artist: inputData.artist, credit: inputData.credit };
    if (!inputData.imageOk) {
      return {
        ok: false,
        failureStage: "image" as const,
        reason: inputData.imageReason ?? "no acceptable band image found",
        ...provenance,
      };
    }
    if (inputData.accepted && inputData.svg && inputData.pngBase64) {
      return { ok: true, svg: inputData.svg, pngBase64: inputData.pngBase64, ...provenance };
    }
    return {
      ok: false,
      failureStage: "svg" as const,
      reason: inputData.critique ?? "could not produce an acceptable poster",
      ...provenance,
    };
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
    candidates: [],
    candidateIndex: 0,
  }))
  // Resolve the candidate pool ONCE. Deterministic per performer, so keeping it
  // out of the loop avoids re-spending the 1 req/sec MusicBrainz budget.
  .then(resolveBandCandidatesStep)
  // Loop 1: judge one candidate per attempt (bounded by attempts AND by pool size).
  .dountil(
    judgeBandImageStep,
    async ({ inputData }) =>
      inputData.accepted ||
      inputData.attempts >= MAX_IMAGE_ATTEMPTS ||
      inputData.candidateIndex >= inputData.candidates.length,
  )
  // Seed loop-2 state, carrying whether the image succeeded plus its provenance.
  .map(async ({ inputData }) => ({
    performer: inputData.performer,
    venue: inputData.venue,
    date: inputData.date,
    imageOk: inputData.accepted,
    imageReason: inputData.reason,
    image: inputData.image,
    colors: inputData.colors,
    artist: inputData.artist,
    credit: inputData.image?.credit,
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

- [ ] **Step 4: Delete the replaced files**

```bash
rm src/mastra/workflows/acquire-band-image.step.ts
rm src/mastra/tools/web-scrape.tool.ts
rm src/mastra/tools/web-scrape.tool.test.ts
```

Do **not** delete `src/mastra/tools/stub-band-image.ts` — `rasterize.tool.test.ts:3` still imports `STUB_BAND_IMAGE_BASE64` as a rasterization fixture.

- [ ] **Step 5: Run tests and typecheck**

Run: `pnpm vitest run src/mastra/ && pnpm typecheck`
Expected: all `src/mastra` suites PASS, typecheck clean (the `acquire-band-image` error from Task 5 is now gone).

- [ ] **Step 6: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 9: Provenance in the HTTP response

**Files:**
- Modify: `src/poster-schema.ts:13-15`, `src/poster.ts:34-50`
- Test: `src/poster.test.ts` (extend)

**Interfaces:**
- Consumes: `ArtistMatch`, `ImageCredit` (Task 1); `PosterWorkflowOutput` (Task 5).
- Produces: `PosterResult` carrying `artist?`/`credit?` on success and `artist?` on failure; `posterHttpResponse` emitting them.

- [ ] **Step 1: Write the failing test**

Append to `src/poster.test.ts`:

```ts
describe("provenance passthrough", () => {
  const artist = { mbid: "mb-rock", name: "La Luz", score: 100, disambiguation: "US rock band" };
  const credit = {
    file: "File:La Luz.jpg",
    descriptionUrl: "https://commons.wikimedia.org/wiki/File:La_Luz.jpg",
    artist: "Shark2000br",
    licenseShortName: "CC BY-SA 4.0",
    attributionRequired: true,
  };

  it("carries artist and credit onto a successful result", async () => {
    const res = await processPosterRequest(req, {
      sink: new StubPosterSink(),
      runWorkflow: async () => ({ ok: true, svg: "<svg/>", pngBase64: "AAAA", artist, credit }),
    });
    expect(res.ok).toBe(true);
    if (res.ok) {
      expect(res.artist).toEqual(artist);
      expect(res.credit).toEqual(credit);
    }
  });

  it("carries the artist onto a failure result", async () => {
    const res = await processPosterRequest(req, {
      sink: new StubPosterSink(),
      runWorkflow: async () => ({ ok: false, failureStage: "image", reason: "no good photo", artist }),
    });
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.artist).toEqual(artist);
  });

  it("includes artist and credit in the 200 body", () => {
    const out = posterHttpResponse({
      ok: true,
      svg: "<svg/>",
      svgUrl: "https://x/s.svg",
      pngUrl: "https://x/p.png",
      artist,
      credit,
    });
    const body = JSON.parse(out.body);
    expect(out.statusCode).toBe(200);
    expect(body.artist.mbid).toBe("mb-rock");
    expect(body.credit.licenseShortName).toBe("CC BY-SA 4.0");
  });

  it("includes the artist in the 422 body", () => {
    const out = posterHttpResponse({ ok: false, stage: "image", reason: "no good photo", artist });
    const body = JSON.parse(out.body);
    expect(out.statusCode).toBe(422);
    expect(body.error).toBe("no good photo");
    expect(body.artist.name).toBe("La Luz");
  });

  it("omits the keys entirely when provenance is unknown", () => {
    const out = posterHttpResponse({ ok: false, stage: "svg", reason: "bad svg" });
    expect(Object.keys(JSON.parse(out.body))).toEqual(["error", "stage"]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm vitest run src/poster.test.ts`
Expected: FAIL — `artist` is not a property of `PosterResult`.

- [ ] **Step 3: Widen `PosterResult`**

In `src/poster-schema.ts`, add the import and replace the `PosterResult` type:

```ts
import type { ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";
```

```ts
/** Result of the poster pipeline, mapped to HTTP by the handler. Provenance
 * fields are additive and absent when unknown. */
export type PosterResult =
  | { ok: true; svg: string; svgUrl: string; pngUrl: string; artist?: ArtistMatch; credit?: ImageCredit }
  | { ok: false; stage: "image" | "svg"; reason: string; artist?: ArtistMatch };
```

- [ ] **Step 4: Thread it through `poster.ts`**

In `src/poster.ts`, replace `processPosterRequest` and `posterHttpResponse` with:

```ts
/** Run the workflow; on success persist artifacts via the sink. Never persists on failure. */
export async function processPosterRequest(req: PosterRequest, deps: PosterDeps): Promise<PosterResult> {
  const out = await deps.runWorkflow(req);
  if (!out.ok || !out.svg || !out.pngBase64) {
    return {
      ok: false,
      stage: out.failureStage ?? "svg",
      reason: out.reason ?? "unknown failure",
      artist: out.artist,
    };
  }
  const { svgUrl, pngUrl } = await deps.sink.put(req, out.svg, out.pngBase64);
  return { ok: true, svg: out.svg, svgUrl, pngUrl, artist: out.artist, credit: out.credit };
}

const JSON_HEADERS = { "content-type": "application/json" };

export function posterHttpResponse(result: PosterResult): { statusCode: number; headers: Record<string, string>; body: string } {
  if (result.ok) {
    // JSON.stringify drops undefined keys, so provenance is simply absent when unknown.
    return {
      statusCode: 200,
      headers: JSON_HEADERS,
      body: JSON.stringify({
        svg: result.svg,
        svgUrl: result.svgUrl,
        pngUrl: result.pngUrl,
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

Run: `pnpm vitest run src/poster.test.ts src/handler.poster.test.ts && pnpm typecheck`
Expected: PASS, typecheck clean.

- [ ] **Step 6: Verify, do not commit**

Run: `git status --short` — leave uncommitted.

---

### Task 10: Register tools, update CI, full verification

**Files:**
- Modify: `src/mastra/index.ts`, `ci/buildspec-lambda.yml`
- Create: `src/mastra/tools/live-apis.test.ts`

**Interfaces:**
- Consumes: everything above.
- Produces: a green suite and a CI list that matches the files on disk.

- [ ] **Step 1: Add the opt-in live test**

Create `src/mastra/tools/live-apis.test.ts`. It is skipped unless `LIVE_API_TESTS=1`, so CI never depends on external APIs or spends MusicBrainz budget:

```ts
import { describe, expect, it } from "vitest";
import { createMusicBrainzClient } from "./musicbrainz.tool.js";
import { createWikimediaClient } from "./wikimedia.tool.js";

// Opt in with: LIVE_API_TESTS=1 pnpm vitest run src/mastra/tools/live-apis.test.ts
const live = process.env.LIVE_API_TESTS === "1" ? describe : describe.skip;

live("live MusicBrainz + Wikimedia", () => {
  it("resolves 'la luz' to the US rock band and its Commons photos", async () => {
    const mb = createMusicBrainzClient();
    const matches = await mb.searchArtists("la luz", { limit: 3 });
    expect(matches[0].mbid).toBe("9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a");
    expect(matches[0].disambiguation).toBe("US rock band");

    const wm = createWikimediaClient();
    const candidates = await wm.resolveImageCandidates(matches[0].mbid, { artistName: matches[0].name });
    expect(candidates.length).toBeGreaterThanOrEqual(2);
    expect(candidates[0].source).toBe("p18");
    expect(candidates[0].width).toBe(1080);
    expect(candidates[0].credit.licenseShortName).toBeTruthy();

    const image = await wm.fetchImageBytes(candidates[0]);
    expect(Buffer.from(image.imageBase64, "base64").subarray(0, 2).toString("hex")).toBe("ffd8"); // JPEG SOI
  }, 60_000);
});
```

- [ ] **Step 2: Register the new tools for Studio**

In `src/mastra/index.ts`, add the imports and a `tools` entry so both lookups are inspectable in Studio:

```ts
import { musicBrainzArtistTool } from "./tools/musicbrainz.tool.js";
import { wikimediaImagesTool } from "./tools/wikimedia.tool.js";
```

Add to the `new Mastra({ ... })` config, alongside `agents` and `workflows`:

```ts
  tools: { musicBrainzArtist: musicBrainzArtistTool, wikimediaImages: wikimediaImagesTool },
```

- [ ] **Step 3: Update the CI test list**

In `ci/buildspec-lambda.yml`, the `pnpm vitest run ...` line lists test files explicitly — a file not on the list silently never runs. Remove `src/mastra/tools/web-scrape.tool.test.ts` (deleted in Task 8) and add the new specs. The line becomes:

```yaml
      - pnpm vitest run src/schema.test.ts src/hash.test.ts src/map.test.ts src/email.test.ts src/extractor.test.ts src/handler.test.ts src/poster-schema.test.ts src/poster.test.ts src/poster-sink.test.ts src/handler.poster.test.ts src/mastra/tools/band-image.test.ts src/mastra/tools/stub-fetch.test.ts src/mastra/tools/musicbrainz.tool.test.ts src/mastra/tools/wikimedia.tool.test.ts src/mastra/tools/svg-parse.tool.test.ts src/mastra/tools/rasterize.tool.test.ts src/mastra/workflows/poster.schemas.test.ts src/mastra/workflows/resolve-band-candidates.step.test.ts src/mastra/workflows/judge-band-image.step.test.ts src/mastra/workflows/poster.workflow.test.ts
```

`live-apis.test.ts` is deliberately absent — it self-skips, but leaving it off the list makes the intent explicit.

- [ ] **Step 4: Run the whole suite and typecheck**

Run: `pnpm vitest run && pnpm typecheck`
Expected: every suite PASS (the two ElasticMQ-dependent specs, `sqs.test.ts` and `handler.e2e.test.ts`, need `docker compose up` locally — if they fail with a connection error, that is pre-existing and unrelated). Typecheck clean.

- [ ] **Step 5: Confirm no stale references remain**

Run: `grep -rn "web-scrape\|scrapeBandImage\|acquireBandImage" src/ ci/ ; echo "exit=$?"`
Expected: no matches (`exit=1`). Any hit means a dangling import.

Run: `grep -rn "STUB_BAND_IMAGE" src/`
Expected: exactly two hits, both in `stub-band-image.ts` and `rasterize.tool.test.ts` — the fixture is still wired.

- [ ] **Step 6: Optional manual check in Studio**

Run: `pnpm dev`, open `http://localhost:4111`, run `poster-workflow` with `{ performer: "la luz", venue: "Occidental Square", date: "Thursday, August 20" }`.

Expected: a `resolve-band-candidates` card showing `artist.disambiguation: "US rock band"` and a populated `candidates` array, then a `judge-band-image` card whose `metadata.iterationCount` reflects the real number of iterations. Remember Studio renders only the **final** iteration of a looped step — `iterationCount` is how you read the true count.

- [ ] **Step 7: Hand off for review, do not commit**

Run: `git status --short`

Expected — all changes present and **uncommitted**:

```
 M ci/buildspec-lambda.yml
 M src/mastra/index.ts
 M src/mastra/workflows/poster.schemas.ts
 M src/mastra/workflows/poster.workflow.ts
 M src/poster-schema.ts
 M src/poster.ts
 M src/poster.test.ts
 D src/mastra/tools/web-scrape.tool.ts
 D src/mastra/tools/web-scrape.tool.test.ts
 D src/mastra/workflows/acquire-band-image.step.ts
?? src/mastra/tools/band-image.ts
?? src/mastra/tools/band-image.test.ts
?? src/mastra/tools/stub-fetch.ts
?? src/mastra/tools/stub-fetch.test.ts
?? src/mastra/tools/musicbrainz.tool.ts
?? src/mastra/tools/musicbrainz.tool.test.ts
?? src/mastra/tools/wikimedia.tool.ts
?? src/mastra/tools/wikimedia.tool.test.ts
?? src/mastra/tools/live-apis.test.ts
?? src/mastra/workflows/poster.schemas.test.ts
?? src/mastra/workflows/resolve-band-candidates.step.ts
?? src/mastra/workflows/resolve-band-candidates.step.test.ts
?? src/mastra/workflows/judge-band-image.step.ts
?? src/mastra/workflows/judge-band-image.step.test.ts
?? src/mastra/workflows/poster.workflow.test.ts
```

Report the suite results to the user and stop. **Do not commit.**

---

## Notes for the implementer

**On the original bug.** The `.dountil` loop was never broken. It was verified to execute its step three times; Mastra's run record keys `steps` by step id, so each iteration overwrites the last and only the final one is visible — `steps[id].metadata.iterationCount` reports the true count. The defect was that the stub scraper returned identical bytes every call, so there was nothing new to judge. Everything in this plan exists to give the loop real variation, not to repair the loop.

**On rate limiting.** The MusicBrainz limiter is per **process**, meaning per Lambda container. Concurrent invocations behind a shared NAT address could exceed 1 req/sec. The resolve-once structure keeps exposure small: one MB call per poster run, plus at most two more on the fall-through path. Do not present it as a global guarantee.

**On licensing.** This plan **captures** attribution; it does not act on it. No credit line is rendered into the SVG, candidate ordering is licence-blind, and nothing is written to the sink. Every La Luz candidate carries `AttributionRequired: true` and two of three are CC BY-**SA**, which is copyleft — a generated poster embedding such a photo is a derivative work. Capture is a precondition for compliance, not compliance itself. See component (h) of the spec.
