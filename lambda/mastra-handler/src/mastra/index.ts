import { Mastra } from "@mastra/core";
import { emailExtractorAgent } from "./agents/email-extractor.agent.js";

export const mastra = new Mastra({
  agents: { emailExtractor: emailExtractorAgent },
});
