import { createStep, createWorkflow } from "@mastra/core/workflows";
import { PosterRequestSchema } from "../../poster-schema.js";
import { acquireBandImageStep } from "./acquire-band-image.step.js";
import { composePosterStep } from "./compose-poster.step.js";
import {
  MAX_IMAGE_ATTEMPTS,
  MAX_SVG_ATTEMPTS,
  PosterLoopStateSchema,
  PosterWorkflowOutputSchema,
} from "./poster.schemas.js";

// Terminal step: normalize the last loop state into the controlled workflow output.
// (Workflows must end on a step whose outputSchema matches the workflow outputSchema.)
const finalizeStep = createStep({
  id: "finalize-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterWorkflowOutputSchema,
  execute: async ({ inputData }) => {
    if (!inputData.imageOk) {
      return { ok: false, failureStage: "image" as const, reason: inputData.imageReason ?? "no acceptable band image found" };
    }
    if (inputData.accepted && inputData.svg && inputData.pngBase64) {
      return { ok: true, svg: inputData.svg, pngBase64: inputData.pngBase64 };
    }
    return { ok: false, failureStage: "svg" as const, reason: inputData.critique ?? "could not produce an acceptable poster" };
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
  }))
  // Loop 1: acquire an acceptable band image (bounded).
  .dountil(
    acquireBandImageStep,
    async ({ inputData }) => inputData.accepted || inputData.attempts >= MAX_IMAGE_ATTEMPTS,
  )
  // Seed loop-2 state, carrying whether the image succeeded.
  .map(async ({ inputData }) => ({
    performer: inputData.performer,
    venue: inputData.venue,
    date: inputData.date,
    imageOk: inputData.accepted,
    imageReason: inputData.reason,
    image: inputData.image,
    colors: inputData.colors,
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
