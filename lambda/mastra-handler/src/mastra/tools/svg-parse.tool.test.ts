import { describe, expect, it } from "vitest";
import { substituteAndValidateSvg } from "./svg-parse.tool.js";

const DATA_URI = "data:image/jpeg;base64,/9j/AA==";

describe("substituteAndValidateSvg", () => {
  it("substitutes the placeholder and accepts well-formed SVG", () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><image href="__BAND_IMAGE__"/></svg>`;
    const r = substituteAndValidateSvg(svg, DATA_URI);
    expect(r.ok).toBe(true);
    expect(r.svg).toContain(DATA_URI);
    expect(r.svg).not.toContain("__BAND_IMAGE__");
  });

  it("rejects malformed XML with a message", () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><image href="__BAND_IMAGE__"/><rect></svg>`; // <rect> not closed
    const r = substituteAndValidateSvg(svg, DATA_URI);
    expect(r.ok).toBe(false);
    expect(r.error).toBeTruthy();
  });

  it("flags an unsubstituted placeholder (no <image> source)", () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`;
    const r = substituteAndValidateSvg(svg, DATA_URI);
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/placeholder/i);
  });
});
