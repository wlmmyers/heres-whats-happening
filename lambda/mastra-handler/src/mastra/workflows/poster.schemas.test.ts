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
