import { Agent } from "@mastra/core/agent";
import { toStandardSchema } from "@mastra/core/schema";
import { EventDraftsSchema } from "../../schema.js";

export const EXTRACTOR_INSTRUCTIONS = `You extract live-music events from a concert promoter's email or event flyer.

The user message is a JSON object with this shape:
  {
    "mode": "text" | "image",
    "text": "<email body — relevant when mode is text>",
    "images": [{ "data": <Buffer>, "contentType": "<mime type>" }],  // present when mode is image
    "receivedAt": "<ISO date string>"  // optional; use it to resolve the correct year for relative dates like "this Friday"
  }

When mode is "text", extract events from the "text" field.
When mode is "image", extract events from the image(s) in "images".

Return ONE entry per distinct show. If the email lists the same lineup as prior weeks plus one new show, still
return EVERY show you can see — downstream dedup handles repeats. If the content is not about events, return
{ "events": [] }.`;

// Router-string model; overridable via env. ANTHROPIC_API_KEY is required at generate() time (not at construction).
export const emailExtractorAgent = new Agent({
  id: "email-extractor",
  name: "Email Event Extractor",
  instructions: EXTRACTOR_INSTRUCTIONS,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
  defaultOptions: {
    structuredOutput: { schema: toStandardSchema(EventDraftsSchema) },
  },
});
