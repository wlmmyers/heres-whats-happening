import { describe, expect, it } from "vitest";
import { PosterRequestSchema } from "./poster-schema.js";

describe("PosterRequestSchema", () => {
  it("accepts a complete request", () => {
    const r = PosterRequestSchema.safeParse({ performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15" });
    expect(r.success).toBe(true);
  });

  it("rejects missing performer", () => {
    const r = PosterRequestSchema.safeParse({ venue: "The Fillmore", date: "2026-08-15" });
    expect(r.success).toBe(false);
  });

  it("rejects an empty venue", () => {
    const r = PosterRequestSchema.safeParse({ performer: "X", venue: "  ", date: "2026-08-15" });
    expect(r.success).toBe(false);
  });
});

describe("PosterRequestSchema length bounds", () => {
  const ok = { venue: "V", date: "D" };

  it("accepts a performer at the limit and rejects one over it", () => {
    expect(PosterRequestSchema.safeParse({ ...ok, performer: "a".repeat(200) }).success).toBe(true);
    expect(PosterRequestSchema.safeParse({ ...ok, performer: "a".repeat(201) }).success).toBe(false);
  });

  it("measures the TRIMMED value, so whitespace cannot consume the budget", () => {
    // JobID normalizes by trimming, so these two are the SAME job — they must
    // therefore agree on whether they are acceptable.
    const padded = `  ${"a".repeat(200)}  `;
    expect(PosterRequestSchema.safeParse({ ...ok, performer: padded }).success).toBe(true);
  });

  it("bounds venue at 200 and date at 100", () => {
    expect(PosterRequestSchema.safeParse({ performer: "P", date: "D", venue: "a".repeat(201) }).success).toBe(false);
    expect(PosterRequestSchema.safeParse({ performer: "P", venue: "V", date: "a".repeat(101) }).success).toBe(false);
  });
});
