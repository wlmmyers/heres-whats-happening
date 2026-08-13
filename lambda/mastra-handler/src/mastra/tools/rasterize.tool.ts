import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import { createTool } from '@mastra/core/tools';
import { Resvg, initWasm } from '@resvg/resvg-wasm';
import { z } from 'zod';

/**
 * The ONLY font family the renderer can draw with. The wasm build ships no font
 * database and cannot read system fonts (`fontFiles`/`fontDirs` are no-ops here),
 * so text renders only for families covered by `fontBuffers` below — anything
 * else silently draws NOTHING. The SVG author agent is instructed to emit exactly
 * this family; keep the two in sync.
 */
export const POSTER_FONT_FAMILY = 'Inter';

// Bundled with the Lambda (a runtime `dependencies` entry, so `pnpm install --prod`
// ships it). Regular/Bold/Black cover the weights a poster actually uses.
const FONT_MODULE_PATHS = [
  '@expo-google-fonts/inter/400Regular/Inter_400Regular.ttf',
  '@expo-google-fonts/inter/700Bold/Inter_700Bold.ttf',
  '@expo-google-fonts/inter/900Black/Inter_900Black.ttf',
];

// initWasm must run exactly once per process, and the ~1 MB of font bytes must be
// read once per process too — not once per render. Both hang off this one promise.
// The .wasm asset and the .ttf files ship inside their packages; resolve them from
// node_modules and feed the bytes in.
let ready: Promise<Uint8Array[]> | undefined;
function ensureReady(): Promise<Uint8Array[]> {
  if (!ready) {
    const require = createRequire(import.meta.url);
    const wasmPath = require.resolve('@resvg/resvg-wasm/index_bg.wasm');
    const fontPaths = FONT_MODULE_PATHS.map((p) => require.resolve(p));
    ready = (async () => {
      const [wasmBytes, ...fonts] = await Promise.all(
        [wasmPath, ...fontPaths].map((p) => readFile(p)),
      );
      await initWasm(wasmBytes);
      return fonts as Uint8Array[];
    })().catch((e) => {
      ready = undefined; // allow retry on a later call instead of caching the rejection
      throw e;
    });
  }
  return ready;
}

export type RasterizeResult = {
  ok: boolean;
  png?: Buffer;
  width?: number;
  height?: number;
  error?: string;
};

/** Render an SVG string to a PNG. Never throws — failures come back as { ok:false, error }. */
export async function rasterizeSvg(svg: string): Promise<RasterizeResult> {
  try {
    const fontBuffers = await ensureReady();
    // Without fontBuffers every <text> element renders as nothing at all: the wasm
    // build has no font database and loadSystemFonts is unavailable to it.
    const resvg = new Resvg(svg, {
      font: { fontBuffers, defaultFontFamily: POSTER_FONT_FAMILY, loadSystemFonts: false },
    });
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
  id: 'rasterize-svg',
  description: 'Render an SVG string to a PNG image (returns base64).',
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
    return {
      ok: res.ok,
      pngBase64: res.png?.toString('base64'),
      width: res.width,
      height: res.height,
      error: res.error,
    };
  },
});
