import { z } from "zod";
import type { ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";

export const PosterRequestSchema = z
  .object({
    performer: z.string().trim().min(1, "performer is required"),
    venue: z.string().trim().min(1, "venue is required"),
    date: z.string().trim().min(1, "date is required"),
  })
  .strict();
export type PosterRequest = z.infer<typeof PosterRequestSchema>;

/** Result of the poster pipeline, mapped to HTTP by the handler. Provenance
 * fields are additive and absent when unknown. */
export type PosterResult =
  | { ok: true; svg: string; svgUrl: string; pngUrl: string; artist?: ArtistMatch; credit?: ImageCredit }
  | { ok: false; stage: "image" | "svg"; reason: string; artist?: ArtistMatch };
