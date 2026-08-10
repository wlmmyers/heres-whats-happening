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
