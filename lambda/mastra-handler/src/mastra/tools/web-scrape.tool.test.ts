import { describe, expect, it } from "vitest";
import { webScrapeTool } from "./web-scrape.tool.js";

describe("webScrapeTool (stub)", () => {
  it("returns a decodable JPEG with positive dimensions", async () => {
    const out = await webScrapeTool.execute({ performer: "Khruangbin" });
    expect(out.contentType).toBe("image/jpeg");
    expect(out.width).toBeGreaterThan(0);
    expect(out.height).toBeGreaterThan(0);
    const bytes = Buffer.from(out.imageBase64, "base64");
    expect(bytes.subarray(0, 2).toString("hex")).toBe("ffd8"); // JPEG SOI
  });

  it("accepts an optional refinement hint without changing the contract", async () => {
    const out = await webScrapeTool.execute({ performer: "Khruangbin", refinement: "live band photo, not album art" });
    expect(out.imageBase64.length).toBeGreaterThan(0);
  });
});
