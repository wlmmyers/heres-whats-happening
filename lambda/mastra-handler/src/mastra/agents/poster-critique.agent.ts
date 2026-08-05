import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const PosterCritiqueSchema = z.object({
  acceptable: z.boolean().describe("True only if this is a cool, legible concert poster showing the band photo, venue, and date."),
  critique: z.string().describe("If not acceptable, specific actionable fixes for the SVG author. If acceptable, a short note."),
});

export const posterCritiqueAgent = new Agent({
  id: "poster-critique",
  name: "Concert Poster Critic",
  instructions: `You judge a RENDERED concert poster image.
The user message contains the intended { performer, venue, date } and the rendered poster image.
Approve only if it is visually cool AND legible AND clearly shows the band photo, the performer name,
the venue, and the date. Otherwise reject with concrete, actionable critique the SVG author can apply
(e.g. "date is illegible against the background", "band photo is tiny/cropped", "colors clash").`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(PosterCritiqueSchema) } },
});
