import { createTool } from "@mastra/core/tools";
import { z } from "zod";
import { STUB_BAND_IMAGE } from "./stub-band-image.js";

export const BandImageSchema = z.object({
  imageBase64: z.string(),
  contentType: z.string(),
  width: z.number(),
  height: z.number(),
  sourceUrl: z.string().optional(),
});
export type BandImage = z.infer<typeof BandImageSchema>;

// STUB: returns a canned band photo. Replace the body with a real image-search /
// scrape API call. `refinement` carries feedback from a prior rejected candidate so
// the real implementation can issue a better query.
//
// The plain function is the tested + workflow-consumed entrypoint. Calling a Mastra
// tool's `.execute()` directly does NOT typecheck, so callers use `scrapeBandImage`
// and `webScrapeTool` is a thin wrapper for Studio.
export async function scrapeBandImage(performer: string, refinement?: string): Promise<BandImage> {
  // TODO: real image-search/scrape API keyed on `performer` (+ `refinement`).
  void refinement;
  return {
    ...STUB_BAND_IMAGE,
    sourceUrl: `stub://band-image/${encodeURIComponent(performer)}`,
  };
}

export const webScrapeTool = createTool({
  id: "web-scrape-band-image",
  description: "Find a candidate photo of the given performer for use on a concert poster.",
  inputSchema: z.object({
    performer: z.string(),
    refinement: z.string().optional(),
  }),
  outputSchema: BandImageSchema,
  execute: async ({ performer, refinement }) => scrapeBandImage(performer, refinement),
});
