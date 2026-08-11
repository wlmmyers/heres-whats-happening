import { describe, expect, it } from "vitest";
import { posterKeyBase, StubPosterSink } from "./poster-sink.js";

const req = { performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15", force: false };

describe("posterKeyBase", () => {
  it("builds a slugged, versioned, prefixed key", () => {
    expect(posterKeyBase(req)).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15");
  });
  it("slugs spaces and punctuation", () => {
    expect(posterKeyBase({ performer: "Sigur Rós!", venue: "9:30 Club", date: "2026-09-01", force: false }))
      .toBe("posters/v1/sigur-ros/9-30-club-2026-09-01");
  });
});

/**
 * A performer with no ASCII alphanumerics used to slug to "", so 椎名林檎, Мумий
 * Тролль and !!! all shared one key. `find` READS these keys now, so a collision
 * hands a band another band's poster — wrong photo, wrong name, and the wrong
 * photographer's CC BY-SA attribution in the response body.
 */
describe("posterKeyBase collision safety", () => {
  const NON_ASCII = ["椎名林檎", "Мумий Тролль", "!!!"];
  const atNeumos = (performer: string) =>
    posterKeyBase({ performer, venue: "Neumos", date: "2026-08-15", force: false });

  it("gives distinct non-ASCII performers DISTINCT keys at the same venue and date", () => {
    const keys = NON_ASCII.map(atNeumos);
    expect(new Set(keys).size).toBe(3);
  });

  it("never emits an empty path component", () => {
    for (const key of NON_ASCII.map(atNeumos)) {
      expect(key).not.toContain("//");
      expect(key.startsWith("posters/v1/")).toBe(true);
    }
  });

  it("is deterministic, so the cache can still hit", () => {
    expect(atNeumos("椎名林檎")).toBe(atNeumos("椎名林檎"));
  });

  it("applies the fallback per COMPONENT, not just to the performer", () => {
    const one = posterKeyBase({ performer: "Khruangbin", venue: "東京ドーム", date: "2026-08-15", force: false });
    const two = posterKeyBase({ performer: "Khruangbin", venue: "武道館", date: "2026-08-15", force: false });
    expect(one).not.toBe(two);
    expect(one).toContain("posters/v1/khruangbin/");
    // A blank venue component would leave the key starting at the date separator.
    expect(one).not.toContain("khruangbin/-2026-08-15");
  });

  it("leaves EXISTING ascii keys untouched", () => {
    expect(posterKeyBase(req)).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15");
    expect(posterKeyBase({ performer: "Sigur Rós!", venue: "9:30 Club", date: "2026-09-01", force: false }))
      .toBe("posters/v1/sigur-ros/9-30-club-2026-09-01");
  });
});

describe("StubPosterSink", () => {
  const pngRef = { path: "/tmp/p.png", contentType: "image/png", bytes: 20 };
  const provenance = { artist: { mbid: "m", name: "K", score: 100 } };

  it("records the put and returns canned keys plus provenance", async () => {
    const sink = new StubPosterSink();
    const out = await sink.put(req, pngRef, provenance);
    expect(out.pngKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.png");
    expect("svgKey" in out).toBe(false);
    expect(out.artist).toEqual(provenance.artist);
    expect(sink.calls).toHaveLength(1);
    expect(sink.calls[0].png).toEqual(pngRef);
  });

  it("find misses until something has been put", async () => {
    const sink = new StubPosterSink();
    expect(await sink.find(req)).toBeNull();
    await sink.put(req, pngRef, provenance);
    const hit = await sink.find(req);
    expect(hit?.pngKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.png");
    expect("svgKey" in hit!).toBe(false);
    expect(hit?.artist).toEqual(provenance.artist);
  });
});
