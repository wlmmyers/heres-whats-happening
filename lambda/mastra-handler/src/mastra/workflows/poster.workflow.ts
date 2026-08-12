import { createStep, createWorkflow } from "@mastra/core/workflows";
import { PosterRequestSchema } from "../../poster-schema.js";
import { artifactStore } from "../tools/artifact-store.js";
import { composePosterStep } from "./compose-poster.step.js";
import { judgeBandImageStep } from "./judge-band-image.step.js";
import {
  MAX_IMAGE_ATTEMPTS,
  MAX_SVG_ATTEMPTS,
  PosterLoopStateSchema,
  PosterWorkflowOutputSchema,
} from "./poster.schemas.js";
import { resolveBandCandidatesStep } from "./resolve-band-candidates.step.js";

// Terminal step: normalize the last loop state into the controlled workflow output.
// (Workflows must end on a step whose outputSchema matches the workflow outputSchema.)
// Provenance is emitted on BOTH branches — a failure that names the resolved
// artist is far more actionable than a bare "no acceptable band image found".
const finalizeStep = createStep({
  id: "finalize-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterWorkflowOutputSchema,
  execute: async ({ inputData, runId }) => {
    const provenance = { artist: inputData.artist, credit: inputData.credit };
    // Emitted on every branch so the caller can always clean up the run dir.
    const artifactDir = artifactStore(runId).dir;

    if (!inputData.imageOk) {
      return {
        ok: false,
        failureStage: "image" as const,
        reason: inputData.imageReason ?? "no acceptable band image found",
        artifactDir,
        ...provenance,
      };
    }
    if (inputData.accepted && inputData.render) {
      return { ok: true, render: inputData.render, artifactDir, ...provenance };
    }
    return {
      ok: false,
      failureStage: "svg" as const,
      reason: inputData.critique ?? "could not produce an acceptable poster",
      artifactDir,
      ...provenance,
    };
  },
});

export const posterWorkflow = createWorkflow({
  id: "poster-workflow",
  inputSchema: PosterRequestSchema,
  outputSchema: PosterWorkflowOutputSchema,
})
  // Seed loop-1 state from the request.
  .map(async ({ inputData }) => ({
    performer: inputData.performer,
    venue: inputData.venue,
    date: inputData.date,
    attempts: 0,
    accepted: false,
    colors: [] as string[],
    candidates: [],
    candidateIndex: 0,
  }))
  // Resolve the candidate pool ONCE. Deterministic per performer, so keeping it
  // out of the loop avoids re-spending the 1 req/sec MusicBrainz budget.
  .then(resolveBandCandidatesStep)
  // Loop 1: judge one candidate per attempt (bounded by attempts AND by pool size).
  .dountil(
    judgeBandImageStep,
    async ({ inputData }) =>
      inputData.accepted ||
      inputData.attempts >= MAX_IMAGE_ATTEMPTS ||
      inputData.candidateIndex >= inputData.candidates.length,
  )
  // Seed loop-2 state, carrying whether the image succeeded plus its provenance.
  .map(async ({ inputData }) => ({
    performer: inputData.performer,
    venue: inputData.venue,
    date: inputData.date,
    imageOk: inputData.accepted,
    imageReason: inputData.reason,
    image: inputData.image,
    colors: inputData.colors,
    artist: inputData.artist,
    // Only credit a photo that was actually ACCEPTED. `image` holds the last
    // candidate judged, rejected ones included, so crediting it unconditionally
    // would attribute a photographer whose work never made it into a poster.
    credit: inputData.accepted ? inputData.image?.credit : undefined,
    attempts: 0,
    accepted: false,
  }))
  // Loop 2: compose + validate the poster (bounded). Condition is immediately true
  // when imageOk is false (the step short-circuits without LLM work).
  .dountil(
    composePosterStep,
    async ({ inputData }) =>
      inputData.accepted || !inputData.imageOk || inputData.attempts >= MAX_SVG_ATTEMPTS,
  )
  // Normalize either outcome to the controlled workflow output.
  .then(finalizeStep)
  .commit();
