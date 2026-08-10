import { z } from "zod";
import {
  ArtifactRefSchema,
  ArtistMatchSchema,
  ImageCandidateSchema,
  ImageCreditSchema,
  ImageRefSchema,
} from "../tools/band-image.js";

// Loop-1 state: input and output of the judge-band-image step are the SAME shape,
// so the step's output can feed straight back as the next iteration's input.
// `candidates` is resolved ONCE before the loop; `candidateIndex` walks it.
export const ImageLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  attempts: z.number(),
  accepted: z.boolean(),
  reason: z.string().optional(),
  image: ImageRefSchema.optional(),
  colors: z.array(z.string()).default([]),
  artist: ArtistMatchSchema.optional(),
  candidates: z.array(ImageCandidateSchema).default([]),
  candidateIndex: z.number().default(0),
});
export type ImageLoopState = z.infer<typeof ImageLoopStateSchema>;

// Loop-2 state: input and output of the compose-poster step are the SAME shape.
// `artist` and `credit` are carriers only — composePosterStep neither reads nor
// writes them; they exist so finalizeStep can report provenance.
export const PosterLoopStateSchema = z.object({
  performer: z.string(),
  venue: z.string(),
  date: z.string(),
  imageOk: z.boolean(),
  imageReason: z.string().optional(),
  image: ImageRefSchema.optional(),
  colors: z.array(z.string()).default([]),
  attempts: z.number(),
  accepted: z.boolean(),
  critique: z.string().optional(),
  // The SVG BEFORE substitution: still contains the literal __BAND_IMAGE__, so
  // it is ~2 KB and is the thing worth reading in Studio when a poster is wrong.
  authoredSvg: z.string().optional(),
  render: z.object({ svg: ArtifactRefSchema, png: ArtifactRefSchema }).optional(),
  artist: ArtistMatchSchema.optional(),
  credit: ImageCreditSchema.optional(),
});
export type PosterLoopState = z.infer<typeof PosterLoopStateSchema>;

// Final workflow output: a controlled result (ok or a typed failure stage+reason),
// plus provenance on BOTH branches — a failure is far more actionable when it
// names the artist that was resolved.
export const PosterWorkflowOutputSchema = z.object({
  ok: z.boolean(),
  render: z.object({ svg: ArtifactRefSchema, png: ArtifactRefSchema }).optional(),
  // The run's artifact directory, so the caller can delete it. Emitted on BOTH
  // branches: a failed run still has files worth cleaning up (and, from Studio,
  // worth inspecting).
  artifactDir: z.string().optional(),
  failureStage: z.enum(["image", "svg"]).optional(),
  reason: z.string().optional(),
  artist: ArtistMatchSchema.optional(),
  credit: ImageCreditSchema.optional(),
});
export type PosterWorkflowOutput = z.infer<typeof PosterWorkflowOutputSchema>;

export const MAX_IMAGE_ATTEMPTS = Number(process.env.MAX_IMAGE_ATTEMPTS ?? 3);
export const MAX_SVG_ATTEMPTS = Number(process.env.MAX_SVG_ATTEMPTS ?? 3);

/** How many MusicBrainz matches to probe before giving up on images. */
export const MAX_ARTIST_FALLTHROUGH = 3;
