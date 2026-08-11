import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { ImageAnalysisSchema, imageAnalysisAgent } from "../agents/image-analysis.agent.js";
import { artifactStore } from "../tools/artifact-store.js";
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

// One iteration: fetch the indexed candidate's bytes, write them to the run's
// artifact directory, then a vision agent judges them. The BYTES never enter
// workflow state — only an ImageRef does. `candidateIndex` advances on every
// iteration regardless of verdict, so the next attempt sees a NEW photo.
export const judgeBandImageStep = createStep({
  id: "judge-band-image",
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData, runId }) => {
    const candidate = inputData.candidates[inputData.candidateIndex];
    // Cheap short-circuit: nothing to judge, so spend no attempt and no LLM call.
    if (!candidate) {
      return { ...inputData, accepted: false };
    }

    const attempts = inputData.attempts + 1;
    const candidateIndex = inputData.candidateIndex + 1;

    let bytes: Buffer;
    try {
      bytes = await fetchImageBytes(candidate);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `could not fetch ${candidate.file}: ${message(e)}`,
      };
    }

    let image;
    try {
      const store = artifactStore(runId);
      const ext = candidate.contentType === "image/png" ? "png" : "jpg";
      const ref = await store.write(`band-${attempts}.${ext}`, bytes, candidate.contentType);
      image = {
        ...ref,
        width: candidate.width,
        height: candidate.height,
        sourceUrl: candidate.credit.descriptionUrl || candidate.url,
        credit: candidate.credit,
      };
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `could not store ${candidate.file}: ${message(e)}`,
      };
    }

    const who = describeArtist(inputData.artist, inputData.performer);
    // A provider outage (429/5xx/529) must NOT escape the step: throwing here
    // would 500 the request AND strand the artifacts this step just wrote, since
    // the caller's cleanup keys off the workflow's returned output. Same shape as
    // every other failure path — spend the attempt, advance, say why.
    let res;
    try {
      res = await imageAnalysisAgent.generate([
        {
          role: "user",
          content: [
            { type: "image", image: bytes, mimeType: candidate.contentType },
            { type: "text", text: `Performer: ${who}. Is this a usable photo of this performer for a concert poster?` },
          ],
        },
      ]);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        candidateIndex,
        accepted: false,
        reason: `image analysis failed: ${message(e)}`,
        image,
      };
    }

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
