import { beforeEach, describe, expect, it, vi } from "vitest";
import type { RasterizeResult } from "../tools/rasterize.tool.js";

const authorGenerate = vi.fn();
const critiqueGenerate = vi.fn();
const rasterizeSvg = vi.fn<(svg: string) => Promise<RasterizeResult>>();

// vi.mock is hoisted, but the factory body runs lazily at import time — by which
// point the spies above are initialized. So they can be referenced directly.
// svg-parse.tool.js is deliberately NOT mocked: it is pure and already covered,
// so the real substitution/validation runs here.
vi.mock("../agents/svg-author.agent.js", () => ({
  svgAuthorAgent: { generate: authorGenerate },
  SvgAuthorSchema: {},
}));
vi.mock("../agents/poster-critique.agent.js", () => ({
  posterCritiqueAgent: { generate: critiqueGenerate },
  PosterCritiqueSchema: {},
}));
vi.mock("../tools/rasterize.tool.js", () => ({ rasterizeSvg }));

const { composePosterStep } = await import("./compose-poster.step.js");

const GOOD_SVG = '<svg xmlns="http://www.w3.org/2000/svg"><image href="__BAND_IMAGE__"/></svg>';

const base = {
  performer: "La Luz",
  venue: "Occidental Square",
  date: "Thursday, August 20",
  imageOk: true,
  colors: ["#111"],
  attempts: 0,
  accepted: false,
  image: { imageBase64: "AAAA", contentType: "image/jpeg", width: 1080, height: 810 },
};

const run = (data: Record<string, unknown>) => composePosterStep.execute({ inputData: data } as never) as Promise<any>;

beforeEach(() => {
  authorGenerate.mockReset();
  critiqueGenerate.mockReset();
  rasterizeSvg.mockReset();
});

describe("composePosterStep short-circuit", () => {
  it("does no LLM work when the image stage failed", async () => {
    const out = await run({ ...base, imageOk: false, image: undefined });

    expect(authorGenerate).not.toHaveBeenCalled();
    expect(critiqueGenerate).not.toHaveBeenCalled();
    expect(rasterizeSvg).not.toHaveBeenCalled();
    expect(out.accepted).toBe(false);
  });

  it("ADVANCES attempts on the short-circuit so the loop cannot spin", async () => {
    // Regression guard: the dountil exit is
    // `accepted || !imageOk || attempts >= MAX_SVG_ATTEMPTS`. If this branch ever
    // returns without advancing attempts, an imageOk/image mismatch loops forever.
    const out = await run({ ...base, imageOk: true, image: undefined });

    expect(out.attempts).toBe(1);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain("no usable band image");
    expect(authorGenerate).not.toHaveBeenCalled();
  });

  it("keeps an existing critique rather than overwriting it", async () => {
    const out = await run({ ...base, imageOk: false, image: undefined, critique: "earlier feedback" });
    expect(out.critique).toBe("earlier feedback");
  });
});

describe("composePosterStep authoring", () => {
  it("produces svg + png and accepts when the critique approves", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, pngBase64: "PNGDATA", width: 1080, height: 1350 });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: true, critique: "bold and legible" } });

    const out = await run(base);

    expect(out.attempts).toBe(1);
    expect(out.accepted).toBe(true);
    expect(out.pngBase64).toBe("PNGDATA");
    // The placeholder was replaced with the real data URI by the REAL svg-parse.
    expect(out.svg).toContain("data:image/jpeg;base64,AAAA");
    expect(out.svg).not.toContain("__BAND_IMAGE__");
  });

  it("rejects and records the critique when the poster is judged poor", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, pngBase64: "PNGDATA" });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: false, critique: "title is unreadable" } });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toBe("title is unreadable");
    expect(out.attempts).toBe(1);
  });

  it("feeds the prior critique back to the author", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, pngBase64: "PNGDATA" });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: true, critique: "ok" } });

    await run({ ...base, attempts: 1, critique: "the date was cropped" });

    const payload = JSON.parse((authorGenerate.mock.calls[0][0] as Array<{ content: string }>)[0].content);
    expect(payload.critique).toBe("the date was cropped");
    expect(payload.imageWidth).toBe(1080);
    expect(payload.imageHeight).toBe(810);
    expect(payload.colors).toEqual(["#111"]);
  });
});

describe("composePosterStep failure paths", () => {
  it("handles the author returning no svg", async () => {
    authorGenerate.mockResolvedValue({ object: undefined });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.critique).toContain("no svg");
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it("reports a missing image placeholder as actionable feedback", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: '<svg xmlns="http://www.w3.org/2000/svg"></svg>' } });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain("__BAND_IMAGE__");
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it("reports malformed svg without attempting to rasterize", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: '<svg><image href="__BAND_IMAGE__"></svg>' } });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain("well-formed");
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it("reports a rasterization failure with the renderer's error", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: false, error: "unsupported filter" });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain("did not render");
    expect(out.critique).toContain("unsupported filter");
    expect(critiqueGenerate).not.toHaveBeenCalled();
  });

  it("handles the critique agent returning no structured object", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, pngBase64: "PNGDATA" });
    critiqueGenerate.mockResolvedValue({ object: undefined });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toBe("critique returned no result");
  });
});
