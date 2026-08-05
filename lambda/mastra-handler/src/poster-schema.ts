import { z } from "zod";

export const PosterRequestSchema = z
  .object({
    performer: z.string().trim().min(1, "performer is required"),
    venue: z.string().trim().min(1, "venue is required"),
    date: z.string().trim().min(1, "date is required"),
  })
  .strict();
export type PosterRequest = z.infer<typeof PosterRequestSchema>;

/** Result of the poster pipeline, mapped to HTTP by the handler. */
export type PosterResult =
  | { ok: true; svg: string; svgUrl: string; pngUrl: string }
  | { ok: false; stage: "image" | "svg"; reason: string };
