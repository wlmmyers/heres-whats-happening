import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { StubPosterSink } from "./poster-sink.js";
import { BadRequestError, parsePosterRequest, posterHttpResponse, processPosterRequest } from "./poster.js";

const req = { performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15", force: false };

/** processPosterRequest now reads render refs off disk, so tests need real files. */
async function renderFixture(svg: string, pngBase64 = "AAAA") {
  const dir = await mkdtemp(join(tmpdir(), "poster-test-"));
  const svgPath = join(dir, "poster.svg");
  const pngPath = join(dir, "poster.png");
  const png = Buffer.from(pngBase64, "base64");
  await writeFile(svgPath, svg, "utf8");
  await writeFile(pngPath, png);
  return {
    svg: { path: svgPath, contentType: "image/svg+xml", bytes: Buffer.byteLength(svg) },
    png: { path: pngPath, contentType: "image/png", bytes: png.byteLength },
  };
}

describe("processPosterRequest", () => {
  it("on a cache miss runs the workflow, writes to the sink, and returns keys", async () => {
    const sink = new StubPosterSink();
    const res = await processPosterRequest(req, {
      sink,
      runWorkflow: async () => ({ ok: true, render: await renderFixture("<svg/>") }),
    });
    expect(res.ok).toBe(true);
    if (res.ok) {
      expect(res.cached).toBe(false);
      expect(res.svgKey).toContain("posters/v1/khruangbin");
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
    const r = posterHttpResponse({ ok: true, svgKey: "k1", pngKey: "k2", cached: false });
    expect(r.statusCode).toBe(200);
    expect(JSON.parse(r.body)).toEqual({ svgKey: "k1", pngKey: "k2", cached: false });
  });
  it("maps failure -> 422 with stage", () => {
    const r = posterHttpResponse({ ok: false, stage: "svg", reason: "ugly" });
    expect(r.statusCode).toBe(422);
    expect(JSON.parse(r.body)).toEqual({ error: "ugly", stage: "svg" });
  });
});

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
      runWorkflow: async () => ({ ok: true, render: await renderFixture("<svg/>"), artist, credit }),
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
      svgKey: "posters/v1/x/s.svg",
      pngKey: "posters/v1/x/s.png",
      cached: false,
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

describe("Key-only response", () => {
  it("omits the svg body and includes cached", () => {
    const out = posterHttpResponse({
      ok: true,
      svgKey: "posters/v1/x/s.svg",
      pngKey: "posters/v1/x/s.png",
      cached: true,
    });
    const body = JSON.parse(out.body);
    expect(out.statusCode).toBe(200);
    expect("svg" in body).toBe(false);
    expect(body.svgKey).toBe("posters/v1/x/s.svg");
    expect(body.cached).toBe(true);
  });
});
