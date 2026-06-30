import { describe, expect, it } from "vitest";
import { rasterizeSvg } from "./rasterize.tool.js";
import { STUB_BAND_IMAGE_BASE64 } from "./stub-band-image.js";

const PNG_MAGIC = "89504e47";

describe("rasterizeSvg", () => {
  it("renders a plain SVG to a PNG", async () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#0af"/></svg>`;
    const r = await rasterizeSvg(svg);
    expect(r.ok).toBe(true);
    const bytes = Buffer.from(r.pngBase64!, "base64");
    expect(bytes.subarray(0, 4).toString("hex")).toBe(PNG_MAGIC);
    expect(r.width).toBeGreaterThan(0);
  });

  it("renders an SVG with an embedded base64 JPEG <image>", async () => {
    const dataUri = `data:image/jpeg;base64,${STUB_BAND_IMAGE_BASE64}`;
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48"><image x="0" y="0" width="48" height="48" href="${dataUri}"/></svg>`;
    const r = await rasterizeSvg(svg);
    expect(r.ok).toBe(true);
    const bytes = Buffer.from(r.pngBase64!, "base64");
    expect(bytes.subarray(0, 4).toString("hex")).toBe(PNG_MAGIC);
  });

  it("returns ok:false with an error for unrenderable input", async () => {
    const r = await rasterizeSvg("not an svg at all");
    expect(r.ok).toBe(false);
    expect(r.error).toBeTruthy();
  });
});
