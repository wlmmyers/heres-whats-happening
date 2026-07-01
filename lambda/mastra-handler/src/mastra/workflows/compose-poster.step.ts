import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { PosterCritiqueSchema, posterCritiqueAgent } from "../agents/poster-critique.agent.js";
import { SvgAuthorSchema, svgAuthorAgent } from "../agents/svg-author.agent.js";
import { rasterizeSvg } from "../tools/rasterize.tool.js";
import { substituteAndValidateSvg } from "../tools/svg-parse.tool.js";
import { PosterLoopStateSchema } from "./poster.schemas.js";

type SvgAuthor = z.infer<typeof SvgAuthorSchema>;
type PosterCritique = z.infer<typeof PosterCritiqueSchema>;

// One iteration: author SVG -> substitute+parse -> rasterize -> critique. Any failure
// sets accepted=false and records actionable feedback in `critique` for the next attempt.
export const composePosterStep = createStep({
  id: "compose-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterLoopStateSchema,
  execute: async ({ inputData }) => {
    // Cheap short-circuit: if image acquisition failed, do no LLM work.
    if (!inputData.imageOk || !inputData.image) {
      return { ...inputData, accepted: false };
    }
    const attempts = inputData.attempts + 1;
    const { image } = inputData;

    // 1) Author the SVG (placeholder href for the image).
    const authored = await svgAuthorAgent.generate([
      {
        role: "user",
        content: JSON.stringify({
          performer: inputData.performer,
          venue: inputData.venue,
          date: inputData.date,
          colors: inputData.colors,
          imageWidth: image.width,
          imageHeight: image.height,
          critique: inputData.critique,
        }),
      },
    ]);
    const rawSvg = (authored.object as SvgAuthor | undefined)?.svg;
    if (!rawSvg) {
      return { ...inputData, attempts, accepted: false, critique: "SVG author returned no svg" };
    }

    // 2) Substitute the real image + validate well-formedness.
    const dataUri = `data:${image.contentType};base64,${image.imageBase64}`;
    const parsed = substituteAndValidateSvg(rawSvg, dataUri);
    if (!parsed.ok) {
      return { ...inputData, attempts, accepted: false, svg: rawSvg, critique: `Fix the SVG so it is well-formed: ${parsed.error}` };
    }

    // 3) Rasterize to PNG.
    const raster = await rasterizeSvg(parsed.svg);
    if (!raster.ok || !raster.pngBase64) {
      return { ...inputData, attempts, accepted: false, svg: parsed.svg, critique: `The SVG did not render: ${raster.error}` };
    }

    // 4) Critique the rendered poster.
    const critiqueRes = await posterCritiqueAgent.generate([
      {
        role: "user",
        content: [
          { type: "image", image: Buffer.from(raster.pngBase64, "base64"), mimeType: "image/png" },
          { type: "text", text: `Intended poster — performer: ${inputData.performer}, venue: ${inputData.venue}, date: ${inputData.date}. Is this a cool, legible poster?` },
        ],
      },
    ]);
    const verdict = critiqueRes.object as PosterCritique | undefined;
    return {
      ...inputData,
      attempts,
      svg: parsed.svg,
      pngBase64: raster.pngBase64,
      accepted: verdict?.acceptable ?? false,
      critique: verdict?.critique ?? "critique returned no result",
    };
  },
});
