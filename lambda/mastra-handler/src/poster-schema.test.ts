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
