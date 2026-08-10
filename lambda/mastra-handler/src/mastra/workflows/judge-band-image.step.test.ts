import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BandImage, ImageCandidate } from "../tools/band-image.js";

const fetchImageBytes = vi.fn<(c: ImageCandidate) => Promise<BandImage>>();
const generate = vi.fn();

// vi.mock is hoisted, but the factory body runs lazily at import time — by which
// point the spies above are initialized. So they can be referenced directly.
vi.mock("../tools/wikimedia.tool.js", () => ({ fetchImageBytes }));
vi.mock("../agents/image-analysis.agent.js", () => ({
  imageAnalysisAgent: { generate },
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
