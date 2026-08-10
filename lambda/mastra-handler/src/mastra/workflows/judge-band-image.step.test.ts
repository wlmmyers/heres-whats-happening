import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ImageCandidate } from "../tools/band-image.js";

const fetchImageBytes = vi.fn<(c: ImageCandidate) => Promise<Buffer>>();
const generate = vi.fn();

/** Assigned in beforeEach; the mock below reads it at CALL time, not import time. */
let root: string;

vi.mock("../tools/wikimedia.tool.js", () => ({ fetchImageBytes }));
vi.mock("../agents/image-analysis.agent.js", () => ({
  imageAnalysisAgent: { generate },
  ImageAnalysisSchema: {},
}));
// The step calls artifactStore(runId) with the default root. Redirect it at the
// module seam — the same mechanism already used for the two mocks above — so
// production needs no test-only parameter.
vi.mock("../tools/artifact-store.js", async () => {
  const actual = await vi.importActual<typeof import("../tools/artifact-store.js")>(
    "../tools/artifact-store.js",
  );
  return { ...actual, artifactStore: (runId: string) => actual.artifactStore(runId, { root }) };
});

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

const PHOTO = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]);

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

const run = (data: Record<string, unknown>) =>
  judgeBandImageStep.execute({ inputData: data, runId: "run-test" } as never) as Promise<any>;

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "judge-step-test-"));
  fetchImageBytes.mockReset();
  generate.mockReset();
  fetchImageBytes.mockResolvedValue(PHOTO);
});

describe("judgeBandImageStep", () => {
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
    // Point the store at a root that cannot hold a directory: an existing FILE.
    // `root` is read by the module mock at call time, so reassigning it here works.
    const blocked = join(await mkdtemp(join(tmpdir(), "blocked-")), "not-a-dir");
    await writeFile(blocked, "x");
    root = blocked;

    const out = await run(base);

    expect(out.attempts).toBe(1);
    expect(out.candidateIndex).toBe(1);
    expect(out.accepted).toBe(false);
    expect(out.reason).toContain("could not store");
    expect(generate).not.toHaveBeenCalled();
  });

  it("advances the index on rejection so the next attempt sees a new candidate", async () => {
    generate.mockResolvedValue({ object: { acceptable: false, reason: "album art", dominantColors: [] } });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.candidateIndex).toBe(1);
    expect(fetchImageBytes).toHaveBeenCalledWith(base.candidates[0]);
  });

  it("judges the indexed candidate, not always the first", async () => {
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
    generate.mockResolvedValue({ object: undefined });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.reason).toBe("image analysis returned no result");
    expect(out.candidateIndex).toBe(1);
    expect(out.image.path).toBeTruthy();
  });

  it("puts the artist disambiguation into the prompt", async () => {
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });

    await run(base);
    const messages = generate.mock.calls[0][0] as Array<{ content: Array<{ type: string; text?: string }> }>;
    const text = messages[0].content.find((c) => c.type === "text")!.text!;
    expect(text).toContain("La Luz");
    expect(text).toContain("US rock band");
    expect(text).toContain("2012");
  });

  it("falls back to the raw performer name when no artist was resolved", async () => {
    generate.mockResolvedValue({ object: { acceptable: true, reason: "ok", dominantColors: [] } });

    await run({ ...base, artist: undefined });
    const messages = generate.mock.calls[0][0] as Array<{ content: Array<{ type: string; text?: string }> }>;
    expect(messages[0].content.find((c) => c.type === "text")!.text).toContain("la luz");
  });
});
