import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { ImageAnalysisSchema, imageAnalysisAgent } from "../agents/image-analysis.agent.js";
import type { ArtistMatch } from "../tools/band-image.js";
import { fetchImageBytes } from "../tools/wikimedia.tool.js";
import { ImageLoopStateSchema } from "./poster.schemas.js";

type ImageAnalysis = z.infer<typeof ImageAnalysisSchema>;

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** "La Luz, US rock band, Group, US, formed 2012" — far more judgeable than "la luz". */
function describeArtist(artist: ArtistMatch | undefined, fallback: string): string {
  if (!artist) return fallback;
  return [
    artist.name,
    artist.disambiguation,
    artist.type,
    artist.country,
    artist.beginYear ? `formed ${artist.beginYear}` : undefined,
  ]
    .filter(Boolean)
    .join(", ");
}

// One iteration: fetch the indexed candidate's bytes, then a vision agent judges
// it. Output shape == input shape so .dountil can loop. `candidateIndex` advances
// on every iteration regardless of verdict, so the next attempt sees a NEW photo.
export const judgeBandImageStep = createStep({
  id: "judge-band-image",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData }) => {
    const candidate = inputData.candidates[inputData.candidateIndex];
    // Cheap short-circuit: nothing to judge, so spend no attempt and no LLM call.
    if (!candidate) {
      return { ...inputData, accepted: false };
    }

    const attempts = inputData.attempts + 1;
    const candidateIndex = inputData.candidateIndex + 1;

    let image;
    try {
      image = await fetchImageBytes(candidate);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `could not fetch ${candidate.file}: ${message(e)}`,
      };
    }

    const who = describeArtist(inputData.artist, inputData.performer);
    const res = await imageAnalysisAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: Buffer.from(image.imageBase64, "base64"), mimeType: image.contentType },
          { type: "text", text: `Performer: ${who}. Is this a usable photo of this performer for a concert poster?` },
        ],
      },
    ]);

    const analysis = res.object as ImageAnalysis | undefined;
    if (!analysis) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: "image analysis returned no result",
        image,
      };
    }

    return {
      ...inputData,
      attempts,
      candidateIndex,
      accepted: analysis.acceptable,
      reason: analysis.reason,
      image,
      colors: analysis.dominantColors ?? [],
    };
  },
});
