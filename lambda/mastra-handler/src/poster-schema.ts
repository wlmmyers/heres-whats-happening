import { z } from "zod";
import type { ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";

// Length bounds. These MUST match the Go handler and the poster_jobs CHECK
// constraints — if Go accepts what this rejects, the caller gets a 202 and then
// a silently failed job instead of a clean 400.
export const MAX_PERFORMER = 200;
export const MAX_VENUE = 200;
export const MAX_DATE = 100;

export const PosterRequestSchema = z
  .object({
    // .trim() runs before .max(), so the bound measures the trimmed value —
    // which is what JobID hashes.
    performer: z
      .string()
      .trim()
      .min(1, "performer is required")
      .max(MAX_PERFORMER, `performer must be at most ${MAX_PERFORMER} characters`),
    venue: z
      .string()
      .trim()
      .min(1, "venue is required")
      .max(MAX_VENUE, `venue must be at most ${MAX_VENUE} characters`),
    date: z
      .string()
      .trim()
      .min(1, "date is required")
      .max(MAX_DATE, `date must be at most ${MAX_DATE} characters`),
    // Poster generation is LLM-driven and nondeterministic, so a user who
    // dislikes a result needs a re-roll. NOT part of posterKeyBase: a forced run
    // overwrites the same keys rather than creating a parallel copy.
    force: z.boolean().optional().default(false),
  })
  .strict();
export type PosterRequest = z.infer<typeof PosterRequestSchema>;

/** Result of the poster pipeline, mapped to HTTP by the handler. */
export type PosterResult =
  | { ok: true; pngKey: string; cached: boolean; artist?: ArtistMatch; credit?: ImageCredit }
  | { ok: false; stage: "image" | "svg"; reason: string; artist?: ArtistMatch };
