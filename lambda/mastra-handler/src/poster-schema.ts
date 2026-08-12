import { z } from "zod";
import type { ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";

// Length bounds. These MUST match the Go handler and the poster_jobs CHECK
// constraints — if Go accepts what this rejects, the caller gets a 202 and then
// a silently failed job instead of a clean 400.
export const MAX_PERFORMER = 200;
export const MAX_VENUE = 200;
export const MAX_DATE = 100;

/**
 * Length in Unicode CODE POINTS, which is what all three layers agree to count.
 * Not `.length`, which counts UTF-16 code units and so charges two per astral
 * character — a 200-emoji performer measures 400 here but 200 to Postgres
 * `char_length` and 200 to Go's utf8.RuneCountInString. Counting code units
 * would make this schema stricter than the Go bound in front of it, which is
 * the one direction that turns a clean 400 into a 202-then-failed job.
 */
const codePoints = (s: string) => [...s].length;

/** A required, trimmed string bounded by code-point count. */
const bounded = (field: string, max: number) =>
  z
    .string()
    .trim()
    .min(1, `${field} is required`)
    .refine((s) => codePoints(s) <= max, `${field} must be at most ${max} characters`);

export const PosterRequestSchema = z
  .object({
    // Scopes the S3 key, so one user's forced regeneration cannot overwrite
    // another's poster. Constrained to a UUID rather than any string because it
    // is interpolated straight into an object key: the format guarantees no "/"
    // and no ".." can enter the path. Supplied by the API service from the
    // authenticated session, never by the browser.
    userId: z.string().uuid("userId must be a UUID"),
    // .trim() runs before the bound, so it measures the trimmed value — which
    // is what JobID hashes.
    performer: bounded("performer", MAX_PERFORMER),
    venue: bounded("venue", MAX_VENUE),
    date: bounded("date", MAX_DATE),
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
