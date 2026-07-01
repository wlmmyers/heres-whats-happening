import { createTool } from "@mastra/core/tools";
import { XMLValidator } from "fast-xml-parser";
import { z } from "zod";

const PLACEHOLDER = "__BAND_IMAGE__";

/** Replace the band-image placeholder with the real data URI, then validate the
 * result is well-formed XML/SVG. Returns the substituted SVG and an ok flag. */
export function substituteAndValidateSvg(svg: string, dataUri: string): { ok: boolean; svg: string; error?: string } {
  if (!svg.includes(PLACEHOLDER)) {
    return { ok: false, svg, error: `SVG is missing the ${PLACEHOLDER} placeholder for the band image` };
  }
  const substituted = svg.split(PLACEHOLDER).join(dataUri);
  const result = XMLValidator.validate(substituted);
  if (result !== true) {
    return { ok: false, svg: substituted, error: `SVG is not well-formed: ${result.err.msg} (line ${result.err.line})` };
  }
  if (!/<svg[\s>]/i.test(substituted)) {
    return { ok: false, svg: substituted, error: "document has no <svg> root element" };
  }
  return { ok: true, svg: substituted };
}

export const svgParseTool = createTool({
  id: "svg-parse",
  description: "Inject the band image data URI into the SVG placeholder and validate the SVG is well-formed.",
  inputSchema: z.object({ svg: z.string(), dataUri: z.string() }),
  outputSchema: z.object({ ok: z.boolean(), svg: z.string(), error: z.string().optional() }),
  execute: async ({ svg, dataUri }) => substituteAndValidateSvg(svg, dataUri),
});
