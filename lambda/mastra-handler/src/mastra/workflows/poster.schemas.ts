import { z } from "zod";
import { BandImageSchema } from "../tools/web-scrape.tool.js";

// Loop-1 state: input and output of the acquire-band-image step are the SAME shape,
// so the step's output can feed straight back as the next iteration's input.
export const ImageLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  attempts: z.number(),
  accepted: z.boolean(),
  reason: z.string().optional(),
  image: BandImageSchema.optional(),
  colors: z.array(z.string()).default([]),
});
export type ImageLoopState = z.infer<typeof ImageLoopStateSchema>;

// Loop-2 state: input and output of the compose-poster step are the SAME shape.
export const PosterLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  imageOk: z.boolean(),
  imageReason: z.string().optional(),
  image: BandImageSchema.optional(),
  colors: z.array(z.string()).default([]),
  attempts: z.number(),
  accepted: z.boolean(),
  critique: z.string().optional(),
  svg: z.string().optional(),
  pngBase64: z.string().optional(),
});
export type PosterLoopState = z.infer<typeof PosterLoopStateSchema>;

// Final workflow output: a controlled result (ok or a typed failure stage+reason).
export const PosterWorkflowOutputSchema = z.object({
  ok: z.boolean(),
  svg: z.string().optional(),
  pngBase64: z.string().optional(),
  failureStage: z.enum(["image", "svg"]).optional(),
  reason: z.string().optional(),
});
export type PosterWorkflowOutput = z.infer<typeof PosterWorkflowOutputSchema>;

export const MAX_IMAGE_ATTEMPTS = Number(process.env.MAX_IMAGE_ATTEMPTS ?? 3);
export const MAX_SVG_ATTEMPTS = Number(process.env.MAX_SVG_ATTEMPTS ?? 3);
