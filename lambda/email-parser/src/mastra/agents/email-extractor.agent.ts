import { Agent } from "@mastra/core/agent";

export const EXTRACTOR_INSTRUCTIONS = `You extract live-music events from a concert promoter's email or event flyer.
Return ONE entry per distinct show. For each show provide: title; an ISO 8601 startsAt WITH timezone offset
(or Z); the venue name (and address if shown); and performers ordered HEADLINER FIRST. Include genres and a
ticket/event url when present. If the email injects a "received on <date>" line, use it to resolve the correct
year for relative dates like "this Friday". If the email lists the same lineup as prior weeks plus one new show,
still return EVERY show you can see — downstream dedup handles repeats. If the content is not about events,
return an empty list.`;

// Router-string model; overridable via env. ANTHROPIC_API_KEY is required at generate() time (not at construction).
export const emailExtractorAgent = new Agent({
  id: "email-extractor",
  name: "Email Event Extractor",
  instructions: EXTRACTOR_INSTRUCTIONS,
  model: process.env.LLM_MODEL || "anthropic/claude-sonnet-4-5",
});
