import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { z } from "zod";

export const SvgAuthorSchema = z.object({
  svg: z.string().describe("A complete, standalone SVG document string starting with '<svg' and ending with '</svg>'."),
});

export const svgAuthorAgent = new Agent({
  id: "poster-svg-author",
  name: "Concert Poster SVG Author",
  instructions: `You design eye-catching concert-poster SVGs.
The user message is a JSON object: { performer, venue, date, colors: string[], imageWidth, imageHeight, critique? }.
Produce ONE complete SVG document (default canvas 1080x1350, portrait) that includes:
- An <image> element for the band photo. Use the EXACT literal href "__BAND_IMAGE__" (a downstream step
  substitutes the real image data; never invent a URL or data URI). Size/position it tastefully using the
  given imageWidth/imageHeight aspect ratio.
- The performer name as the dominant headline, the venue, and the date — all legible.
- A snazzy background pattern (gradients, shapes, repetition) themed with the provided 'colors'.
If 'critique' is present, it explains what was wrong with your previous attempt — fix it.
Return only the SVG via the 'svg' field. Use xmlns="http://www.w3.org/2000/svg". Keep it well-formed.`,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: { structuredOutput: { schema: toStandardSchema(SvgAuthorSchema) } },
});
