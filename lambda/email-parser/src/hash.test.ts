import { describe, expect, it } from "vitest";
import { contentHash, eventDateYMD, normalize } from "./hash.js";

describe("normalize", () => {
  it("lowercases, trims, collapses whitespace, strips punctuation/diacritics", () => {
    expect(normalize("  Phoebe   Bridgers!! ")).toBe("phoebe bridgers");
    expect(normalize("Café Tacvba")).toBe("cafe tacvba");
  });
});

describe("eventDateYMD", () => {
  it("returns UTC YYYYMMDD (day granularity, time ignored)", () => {
    expect(eventDateYMD("2026-06-15T20:00:00Z")).toBe("20260615");
    expect(eventDateYMD("2026-06-15T23:59:00Z")).toBe(eventDateYMD("2026-06-15T08:00:00Z"));
  });
  it("honours timezone offsets by converting to the UTC calendar day", () => {
    // 11pm US-Central on Jun 15 is Jun 16 04:00 UTC -> UTC day is used.
    expect(eventDateYMD("2026-06-15T23:00:00-05:00")).toBe("20260616");
  });
});

describe("contentHash", () => {
  it("is deterministic and order-/case-/punctuation-insensitive on inputs", () => {
    const a = contentHash("Phoebe Bridgers", "The Bowl", "20260615");
    const b = contentHash("phoebe   bridgers", "the bowl!", "20260615");
    expect(a).toBe(b);
    expect(a).toMatch(/^[0-9a-f]{64}$/);
  });
  it("differs when the date differs (the new show at the bottom)", () => {
    expect(contentHash("X", "Y", "20260615")).not.toBe(contentHash("X", "Y", "20260616"));
  });
});
