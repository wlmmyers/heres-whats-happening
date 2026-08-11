import { z } from "zod";
import type { ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";

export const PosterRequestSchema = z
  .object({
    performer: z.string().trim().min(1, "performer is required"),
    venue: z.string().trim().min(1, "venue is required"),
    date: z.string().trim().min(1, "date is required"),
    // Poster generation is LLM-driven and nondeterministic, so a user who
    // dislikes a result needs a re-roll. NOT part of posterKeyBase: a forced run
    // overwrites the same keys rather than creating a parallel copy.
    force: z.boolean().optional().default(false),
  })
  .strict();
export type PosterRequest = z.infer<typeof PosterRequestSchema>;

/** Result of the poster pipeline, mapped to HTTP by the handler. */
export type PosterResult =
  | { ok: true; svgKey: string; pngKey: string; cached: boolean; artist?: ArtistMatch; credit?: ImageCredit }
  | { ok: false; stage: "image" | "svg"; reason: string; artist?: ArtistMatch };
