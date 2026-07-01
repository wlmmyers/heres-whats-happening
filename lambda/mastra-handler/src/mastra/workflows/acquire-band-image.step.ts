import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { ImageAnalysisSchema, imageAnalysisAgent } from "../agents/image-analysis.agent.js";
import { scrapeBandImage } from "../tools/web-scrape.tool.js";
import { ImageLoopStateSchema } from "./poster.schemas.js";

type ImageAnalysis = z.infer<typeof ImageAnalysisSchema>;

// One iteration: scrape a candidate, then a vision agent judges it. Output shape ==
// input shape so .dountil can loop, carrying `reason` forward to refine the next scrape.
export const acquireBandImageStep = createStep({
  id: "acquire-band-image",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData }) => {
    const attempts = inputData.attempts + 1;
    const image = await scrapeBandImage(inputData.performer, inputData.reason);

    const res = await imageAnalysisAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: Buffer.from(image.imageBase64, "base64"), mimeType: image.contentType },
          { type: "text", text: `Performer: ${inputData.performer}. Is this a usable photo of this performer for a concert poster?` },
        ],
      },
    ]);
    const analysis = res.object as ImageAnalysis | undefined;
    if (!analysis) {
      return { ...inputData, attempts, accepted: false, reason: "image analysis returned no result", image };
    }
    return {
      ...inputData,
      attempts,
      accepted: analysis.acceptable,
      reason: analysis.reason,
      image,
      colors: analysis.dominantColors ?? [],
    };
  },
});
