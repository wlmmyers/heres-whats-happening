import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtistMatch, ImageCandidate } from "../tools/band-image.js";

const searchArtists = vi.fn<(p: string, o?: { limit?: number }) => Promise<ArtistMatch[]>>();
const resolveImageCandidates = vi.fn<(m: string, o?: { artistName?: string }) => Promise<ImageCandidate[]>>();
const fetchImageBytes = vi.fn<(c: ImageCandidate) => Promise<Buffer>>();
const analyze = vi.fn();
const compose = vi.fn();

// vi.mock is hoisted, but the factory body runs lazily at import time — by which
// point the spies above are initialized. So they can be referenced directly.
vi.mock("../tools/musicbrainz.tool.js", () => ({ searchArtists }));
vi.mock("../tools/wikimedia.tool.js", () => ({ resolveImageCandidates, fetchImageBytes }));
vi.mock("../agents/image-analysis.agent.js", () => ({
  imageAnalysisAgent: { generate: analyze },
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

// Raw thumbnail bytes as fetchImageBytes now returns them (a Buffer, not the
// old BandImage/base64 shape). Credit comes from the candidate, not this.
const imageBytes = Buffer.from("fake-band-photo-bytes");
const request = { performer: "la luz", venue: "Occidental Square", date: "Thursday, August 20", force: false };

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
  fetchImageBytes.mockResolvedValue(imageBytes);
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
    resolveImageCandidates.mockResolvedValue([
      candidate("File:A.jpg"),
      candidate("File:B.jpg"),
      candidate("File:C.jpg"),
    ]);
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
    resolveImageCandidates.mockResolvedValue([
      candidate("File:A.jpg"),
      candidate("File:B.jpg"),
      candidate("File:C.jpg"),
    ]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "no", dominantColors: [] } });

    await runWorkflow();

    expect(searchArtists).toHaveBeenCalledTimes(1);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(1);
  });

  it("stops the loop as soon as a candidate is accepted", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([
      candidate("File:A.jpg"),
      candidate("File:B.jpg"),
      candidate("File:C.jpg"),
    ]);
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
    fetchImageBytes.mockResolvedValue(imageBytes);
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
    const house: ArtistMatch = {
      mbid: "mb-house",
      name: "La Luz",
      score: 88,
      disambiguation: "Belgium based house group",
    };
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValueOnce([]).mockResolvedValueOnce([candidate("File:H.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "no", dominantColors: [] } });

    const result = await runWorkflow();
    expect((result as any).result.artist).toEqual(house);
  });

  it("returns png + provenance on full success", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    fetchImageBytes.mockResolvedValue(imageBytes);
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    compose.mockImplementation(async (s: Record<string, unknown>) => ({
      ...s,
      attempts: ((s.attempts as number) ?? 0) + 1,
      accepted: true,
      render: {
        png: { path: "/tmp/run/poster-1.png", contentType: "image/png", bytes: 20 },
      },
    }));

    const result = await runWorkflow();
    const out = (result as any).result;
    expect(out.ok).toBe(true);
    expect(out.render.png.path).toBeTruthy();
    expect(out.artist).toEqual(rock);
    expect(out.credit.artist).toBe("Shark2000br");
  });
});

describe("posterWorkflow attribution correctness", () => {
  it("does NOT credit a photo that was fetched and then rejected", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    // The image IS fetched (so its credit lands in state) and then rejected.
    fetchImageBytes.mockResolvedValue(imageBytes);
    analyze.mockResolvedValue({ object: { acceptable: false, reason: "not the band", dominantColors: [] } });

    const result = await runWorkflow();
    const out = (result as any).result;

    expect(out.ok).toBe(false);
    expect(out.failureStage).toBe("image");
    // artist is still reported — we know WHO was looked up.
    expect(out.artist).toEqual(rock);
    // ...but no attribution, because no photo was used.
    expect(out.credit).toBeUndefined();
  });

  it("still credits the photo on the svg-failure branch, where an image WAS used", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    fetchImageBytes.mockResolvedValue(imageBytes);
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    // compose never accepts -> svg-stage failure, but the image was genuinely used.
    const result = await runWorkflow();
    const out = (result as any).result;

    expect(out.ok).toBe(false);
    expect(out.failureStage).toBe("svg");
    expect(out.credit.artist).toBe("Shark2000br");
  });
});

describe("posterWorkflow keeps blobs OUT of state", () => {
  it("has no string field over 10 KB anywhere in the run record", async () => {
    searchArtists.mockResolvedValue([rock]);
    resolveImageCandidates.mockResolvedValue([candidate("File:A.jpg")]);
    analyze.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });
    compose.mockImplementation(async (s: Record<string, unknown>) => ({
      ...s,
      attempts: ((s.attempts as number) ?? 0) + 1,
      accepted: true,
      render: {
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
        png: { path: "/tmp/run/poster-1.png", contentType: "image/png", bytes: 20 },
      },
    }));

    const out: any = (await runWorkflow() as any).result;

    expect(out.ok).toBe(true);
    expect(out.render.png.path).toContain("poster-1.png");
    // finalizeStep only computes the path — artifactStore does not mkdir until a
    // write — so the real default root is fine here and nothing is created.
    expect(out.artifactDir).toContain("hwh-poster");
    expect("svg" in out).toBe(false);
    expect("pngBase64" in out).toBe(false);
  });

  it("emits artifactDir on a FAILURE too, so the caller can still clean up", async () => {
    searchArtists.mockResolvedValue([]);
    const out: any = (await runWorkflow() as any).result;
    expect(out.ok).toBe(false);
    expect(out.artifactDir).toBeTruthy();
  });
});
