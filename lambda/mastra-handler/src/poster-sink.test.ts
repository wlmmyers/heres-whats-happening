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
