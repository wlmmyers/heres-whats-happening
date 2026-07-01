import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const ImageAnalysisSchema = z.object({
  acceptable: z.boolean().describe("True only if this is a real photo of the named performer, suitable for a poster."),
  reason: z.string().describe("If not acceptable, why — used to refine the next image search. If acceptable, a short note."),
  dominantColors: z.array(z.string()).describe("3-5 dominant colors of the photo as hex strings, e.g. '#1a2b3c'."),
});

export const imageAnalysisAgent = new Agent({
  id: "poster-image-analysis",
  name: "Poster Band-Image Analyst",
  instructions: `You are validating a candidate photo for a concert poster of a specific performer.
The user message contains the performer name and an image. Decide whether the image is genuinely a
usable photo of that performer/band (not album art, not a logo, not the wrong artist, not unusable).
Always extract 3-5 dominant hex colors from the image for downstream poster theming. Be strict about
"acceptable" — when in doubt, reject with a concrete reason that would improve the next search.`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(ImageAnalysisSchema) } },
});
