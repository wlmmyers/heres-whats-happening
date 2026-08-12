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

  // The bound counts CODE POINTS, matching Postgres char_length and Go's
  // utf8.RuneCountInString. zod's own .max() counts UTF-16 code units, which
  // charges two per astral character — so a 200-emoji performer would measure
  // 400 here and be rejected after Go had already accepted it and returned 202.
  it("counts code points, not UTF-16 code units", () => {
    const emoji = "🎸".repeat(200); // 200 code points, 400 UTF-16 units
    expect(emoji.length).toBe(400); // the trap this guards against
    expect(PosterRequestSchema.safeParse({ ...ok, performer: emoji }).success).toBe(true);
    expect(PosterRequestSchema.safeParse({ ...ok, performer: "🎸".repeat(201) }).success).toBe(false);
  });

  it("accepts 200 CJK characters, which are 600 bytes", () => {
    expect(PosterRequestSchema.safeParse({ ...ok, performer: "林".repeat(200) }).success).toBe(true);
  });
});
