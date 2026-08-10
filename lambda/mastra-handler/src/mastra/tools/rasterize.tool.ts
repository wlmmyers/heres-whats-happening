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
    ready = readFile(wasmPath)
      .then((bytes) => initWasm(bytes))
      .catch((e) => {
        ready = undefined; // allow retry on a later call instead of caching the rejection
        throw e;
      });
  }
  return ready;
}

export type RasterizeResult = { ok: boolean; png?: Buffer; width?: number; height?: number; error?: string };

/** Render an SVG string to a PNG. Never throws — failures come back as { ok:false, error }. */
export async function rasterizeSvg(svg: string): Promise<RasterizeResult> {
  try {
    await ensureReady();
    const resvg = new Resvg(svg);
    const rendered = resvg.render();
    return {
      ok: true,
      png: Buffer.from(rendered.asPng()),
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
  execute: async ({ svg }) => {
    const res = await rasterizeSvg(svg);
    // Studio cannot render a Buffer; base64 only at this boundary.
    return { ok: res.ok, pngBase64: res.png?.toString("base64"), width: res.width, height: res.height, error: res.error };
  },
});
