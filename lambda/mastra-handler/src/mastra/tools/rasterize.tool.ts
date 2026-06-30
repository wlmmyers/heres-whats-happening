import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { createTool } from "@mastra/core/tools";
import { Resvg, initWasm } from "@resvg/resvg-wasm";
import { z } from "zod";

// initWasm must run exactly once per process. The .wasm asset ships inside the
// package; resolve it from node_modules and feed the bytes to initWasm.
let ready: Promise<void> | undefined;
function ensureReady(): Promise<void> {
  if (!ready) {
    const require = createRequire(import.meta.url);
    const wasmPath = require.resolve("@resvg/resvg-wasm/index_bg.wasm");
    ready = readFile(wasmPath).then((bytes) => initWasm(bytes));
  }
  return ready;
}

export type RasterizeResult = { ok: boolean; pngBase64?: string; width?: number; height?: number; error?: string };

/** Render an SVG string to a PNG. Never throws — failures come back as { ok:false, error }. */
export async function rasterizeSvg(svg: string): Promise<RasterizeResult> {
  try {
    await ensureReady();
    const resvg = new Resvg(svg);
    const rendered = resvg.render();
    const png = rendered.asPng();
    return {
      ok: true,
      pngBase64: Buffer.from(png).toString("base64"),
      width: rendered.width,
      height: rendered.height,
    };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e) };
  }
}

export const rasterizeTool = createTool({
  id: "rasterize-svg",
  description: "Render an SVG string to a PNG image (returns base64).",
  inputSchema: z.object({ svg: z.string() }),
  outputSchema: z.object({
    ok: z.boolean(),
    pngBase64: z.string().optional(),
    width: z.number().optional(),
    height: z.number().optional(),
    error: z.string().optional(),
  }),
  execute: async ({ svg }) => rasterizeSvg(svg),
});
