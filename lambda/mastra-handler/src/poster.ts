import { readFile } from "node:fs/promises";
import { PosterRequestSchema, type PosterRequest, type PosterResult } from "./poster-schema.js";
import type { PosterSink } from "./poster-sink.js";
import type { PosterWorkflowOutput } from "./mastra/workflows/poster.schemas.js";

export interface PosterDeps {
  runWorkflow: (req: PosterRequest) => Promise<PosterWorkflowOutput>;
  sink: PosterSink;
}

export class BadRequestError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "BadRequestError";
  }
}

/** Parse + validate a Function URL request body into a PosterRequest. */
export function parsePosterRequest(body: string | undefined, isBase64: boolean): PosterRequest {
  const raw = body ? (isBase64 ? Buffer.from(body, "base64").toString("utf8") : body) : "";
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    throw new BadRequestError("request body is not valid JSON");
  }
  const parsed = PosterRequestSchema.safeParse(json);
  if (!parsed.success) {
    throw new BadRequestError(parsed.error.issues.map((i) => i.message).join("; "));
  }
  return parsed.data;
}

/** Run the workflow; on success persist artifacts via the sink. Never persists on failure. */
export async function processPosterRequest(req: PosterRequest, deps: PosterDeps): Promise<PosterResult> {
  const out = await deps.runWorkflow(req);
  if (!out.ok || !out.render) {
    return {
      ok: false,
      stage: out.failureStage ?? "svg",
      reason: out.reason ?? "unknown failure",
      artist: out.artist,
    };
  }
  // The response still carries the raw svg text (URL-only response is a later
  // task's scope), so that's the one read left; the sink now takes the file
  // refs directly and streams them itself.
  const svg = await readFile(out.render.svg.path, "utf8");
  const artifacts = await deps.sink.put(req, out.render.svg, out.render.png, {
    artist: out.artist,
    credit: out.credit,
  });
  return { ok: true, svg, svgUrl: artifacts.svgUrl, pngUrl: artifacts.pngUrl, artist: artifacts.artist, credit: artifacts.credit };
}

const JSON_HEADERS = { "content-type": "application/json" };

export function posterHttpResponse(result: PosterResult): { statusCode: number; headers: Record<string, string>; body: string } {
  if (result.ok) {
    // JSON.stringify drops undefined keys, so provenance is simply absent when unknown.
    return {
      statusCode: 200,
      headers: JSON_HEADERS,
      body: JSON.stringify({
        svg: result.svg,
        svgUrl: result.svgUrl,
        pngUrl: result.pngUrl,
        artist: result.artist,
        credit: result.credit,
      }),
    };
  }
  // 422 (never 403/404 — see Global Constraints / spec §8).
  return {
    statusCode: 422,
    headers: JSON_HEADERS,
    body: JSON.stringify({ error: result.reason, stage: result.stage, artist: result.artist }),
  };
}
