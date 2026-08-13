import { rm } from 'node:fs/promises';
import { PosterRequestSchema, type PosterRequest, type PosterResult } from './poster-schema.js';
import type { PosterSink } from './poster-sink.js';
import type { PosterWorkflowOutput } from './mastra/workflows/poster.schemas.js';

export interface PosterDeps {
  runWorkflow: (req: PosterRequest) => Promise<PosterWorkflowOutput>;
  sink: PosterSink;
}

export class BadRequestError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'BadRequestError';
  }
}

/** Parse + validate a Function URL request body into a PosterRequest. */
export function parsePosterRequest(body: string | undefined, isBase64: boolean): PosterRequest {
  const raw = body ? (isBase64 ? Buffer.from(body, 'base64').toString('utf8') : body) : '';
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    throw new BadRequestError('request body is not valid JSON');
  }
  const parsed = PosterRequestSchema.safeParse(json);
  if (!parsed.success) {
    throw new BadRequestError(parsed.error.issues.map((i) => i.message).join('; '));
  }
  return parsed.data;
}

/**
 * Serve an existing poster when there is one; otherwise run the workflow, upload,
 * and clean up the run's scratch directory. Never persists on failure.
 */
export async function processPosterRequest(
  req: PosterRequest,
  deps: PosterDeps,
): Promise<PosterResult> {
  if (!req.force) {
    try {
      const hit = await deps.sink.find(req);
      if (hit) return { ok: true, cached: true, ...hit };
    } catch {
      // A cache must never fail a request that would otherwise succeed.
    }
  }

  let out: PosterWorkflowOutput | undefined;
  try {
    out = await deps.runWorkflow(req);
    if (!out.ok || !out.render) {
      return {
        ok: false,
        stage: out.failureStage ?? 'svg',
        reason: out.reason ?? 'unknown failure',
        artist: out.artist,
      };
    }
    const artifacts = await deps.sink.put(req, out.render.png, {
      artist: out.artist,
      credit: out.credit,
    });
    return { ok: true, cached: false, ...artifacts };
  } finally {
    // Lambda's /tmp persists across warm invocations, so this is not optional.
    // Studio never calls this function, which is exactly why its runs keep their
    // artifacts for inspection.
    if (out?.artifactDir) {
      await rm(out.artifactDir, { recursive: true, force: true }).catch(() => {});
    }
  }
}

const JSON_HEADERS = { 'content-type': 'application/json' };

export function posterHttpResponse(result: PosterResult): {
  statusCode: number;
  headers: Record<string, string>;
  body: string;
} {
  if (result.ok) {
    // S3 object keys, not signed URLs — the API service presigns at read time.
    // JSON.stringify drops undefined keys, so provenance is simply absent when
    // unknown.
    return {
      statusCode: 200,
      headers: JSON_HEADERS,
      body: JSON.stringify({
        pngKey: result.pngKey,
        cached: result.cached,
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
