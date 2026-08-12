import { Mastra } from "@mastra/core";
import { emailExtractorAgent } from "./agents/email-extractor.agent.js";
import { imageAnalysisAgent } from "./agents/image-analysis.agent.js";
import { posterCritiqueAgent } from "./agents/poster-critique.agent.js";
import { svgAuthorAgent } from "./agents/svg-author.agent.js";
import { musicBrainzArtistTool } from "./tools/musicbrainz.tool.js";
import { wikimediaImagesTool } from "./tools/wikimedia.tool.js";
import { posterWorkflow } from "./workflows/poster.workflow.js";

export const mastra = new Mastra({
  agents: {
    emailExtractor: emailExtractorAgent,
    imageAnalysis: imageAnalysisAgent,
    svgAuthor: svgAuthorAgent,
    posterCritique: posterCritiqueAgent,
  },
  workflows: { posterWorkflow },
  tools: { musicBrainzArtist: musicBrainzArtistTool, wikimediaImages: wikimediaImagesTool },
});
