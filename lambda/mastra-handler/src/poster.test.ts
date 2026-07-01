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
    expect(JSON.parse(r.body)).toEqual({ svg: "<svg/>", svgUrl: "u1", pngUrl: "u2" });
  });
  it("maps failure -> 422 with stage", () => {
    const r = posterHttpResponse({ ok: false, stage: "svg", reason: "ugly" });
    expect(r.statusCode).toBe(422);
    expect(JSON.parse(r.body)).toEqual({ error: "ugly", stage: "svg" });
  });
});
