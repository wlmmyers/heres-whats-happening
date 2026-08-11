import { createStep } from "@mastra/core/workflows";
import { type z } from "zod";
import { PosterCritiqueSchema, posterCritiqueAgent } from "../agents/poster-critique.agent.js";
import { SvgAuthorSchema, svgAuthorAgent } from "../agents/svg-author.agent.js";
import { artifactStore } from "../tools/artifact-store.js";
import { rasterizeSvg } from "../tools/rasterize.tool.js";
import { substituteAndValidateSvg } from "../tools/svg-parse.tool.js";
import { PosterLoopStateSchema } from "./poster.schemas.js";

type SvgAuthor = z.infer<typeof SvgAuthorSchema>;
type PosterCritique = z.infer<typeof PosterCritiqueSchema>;

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// One iteration: author SVG -> substitute+parse -> rasterize -> critique. Any failure
// sets accepted=false and records actionable feedback in `critique` for the next attempt.
export const composePosterStep = createStep({
  id: "compose-poster",
  inputSchema: PosterLoopStateSchema,
  outputSchema: PosterLoopStateSchema,
  execute: async ({ inputData, runId }) => {
    // Cheap short-circuit: if image acquisition failed, do no LLM work.
    // `attempts` still advances so the loop stays bounded on THIS path too. The
    // dountil condition is `accepted || !imageOk || attempts >= MAX_SVG_ATTEMPTS`;
    // returning without advancing attempts would spin forever for any input that
    // reaches here with imageOk true.
    if (!inputData.imageOk || !inputData.image) {
      return {
        ...inputData,
        attempts: inputData.attempts + 1,
        accepted: false,
        critique: inputData.critique ?? "no usable band image was available to compose",
      };
    }

    const attempts = inputData.attempts + 1;
    const { image } = inputData;
    const store = artifactStore(runId);

    // Read the photo BEFORE spending an LLM call on authoring.
    let photo: Buffer;
    try {
      photo = await store.read(image);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        accepted: false,
        critique: `could not read the band image at ${image.path}: ${message(e)}`,
      };
    }

    // 1) Author the SVG (placeholder href for the image). Small — kept in state.
    // A provider outage (429/5xx/529) must NOT escape the step: throwing would
    // 500 the request AND strand this run's artifacts, because the caller's
    // cleanup keys off the workflow's returned output.
    let authored;
    try {
      authored = await svgAuthorAgent.generate([
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
    } catch (e) {
      return { ...inputData, attempts, accepted: false, critique: `the SVG author failed: ${message(e)}` };
    }
    const rawSvg = (authored.object as SvgAuthor | undefined)?.svg;
    if (!rawSvg) {
      return { ...inputData, attempts, accepted: false, critique: "SVG author returned no svg" };
    }

    // 2) Substitute the real image + validate well-formedness. The substituted
    //    string is transient — only the file keeps it.
    const dataUri = `data:${image.contentType};base64,${photo.toString("base64")}`;
    const parsed = substituteAndValidateSvg(rawSvg, dataUri);
    if (!parsed.ok) {
      return {
        ...inputData,
        attempts,
        accepted: false,
        authoredSvg: rawSvg,
        critique: `Fix the SVG so it is well-formed: ${parsed.error}`,
      };
    }

    // 3) Rasterize to PNG.
    const raster = await rasterizeSvg(parsed.svg);
    if (!raster.ok || !raster.png) {
      return {
        ...inputData,
        attempts,
        accepted: false,
        authoredSvg: rawSvg,
        critique: `The SVG did not render: ${raster.error}`,
      };
    }

    // 4) Persist both artifacts; state carries refs only.
    let render;
    try {
      render = {
        svg: await store.write(`poster-${attempts}.svg`, Buffer.from(parsed.svg, "utf8"), "image/svg+xml"),
        png: await store.write(`poster-${attempts}.png`, raster.png, "image/png"),
      };
    } catch (e) {
      return { ...inputData, attempts, accepted: false, authoredSvg: rawSvg, critique: `could not store the render: ${message(e)}` };
    }

    // 5) Critique the rendered poster. Same rule as the author call: a provider
    //    failure becomes returned state, never a throw. `render` is carried so the
    //    files just written stay accounted for.
    let critiqueRes;
    try {
      critiqueRes = await posterCritiqueAgent.generate([
        {
          role: "user",
          content: [
            { type: "image", image: raster.png, mimeType: "image/png" },
            { type: "text", text: `Intended poster — performer: ${inputData.performer}, venue: ${inputData.venue}, date: ${inputData.date}. Is this a cool, legible poster?` },
          ],
        },
      ]);
    } catch (e) {
      return {
        ...inputData,
        attempts,
        authoredSvg: rawSvg,
        render,
        accepted: false,
        critique: `the poster critique failed: ${message(e)}`,
      };
    }
    const verdict = critiqueRes.object as PosterCritique | undefined;
    return {
      ...inputData,
      attempts,
      authoredSvg: rawSvg,
      render,
      accepted: verdict?.acceptable ?? false,
      critique: verdict?.critique ?? "critique returned no result",
    };
  },
});
