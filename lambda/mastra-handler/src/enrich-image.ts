import type { ImageInfo } from './enrichment-schema.js';
import { toWireCredit } from './enrichment-schema.js';
import {
  imageAnalysisAgent,
  type ImageAnalysisSchema,
} from './mastra/agents/image-analysis.agent.js';
import type { z } from 'zod';
import type { ImageCandidate } from './mastra/tools/band-image.js';
import { fetchImageBytes, resolveImageCandidates } from './mastra/tools/wikimedia.tool.js';
import type { ArtistRef } from './enrich-bio.js';

type ImageAnalysis = z.infer<typeof ImageAnalysisSchema>;

export const MAX_IMAGE_ATTEMPTS = Number(process.env.MAX_IMAGE_ATTEMPTS ?? 3);

export interface ImageDeps {
  candidates(mbid: string, artistName: string): Promise<ImageCandidate[]>;
  bytes(candidate: ImageCandidate): Promise<Buffer>;
  judge(bytes: Buffer, contentType: string, who: string): Promise<ImageAnalysis | null>;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * Walks Commons candidates until the vision judge accepts one. Unlike the poster
 * path the BYTES are not kept — only the thumbnail URL and its credit are — so
 * nothing is written to disk or S3. NEVER throws.
 */
export async function enrichImage(deps: ImageDeps, artist: ArtistRef): Promise<ImageInfo> {
  if (!artist.mbid) {
    return { status: 'none', reason: 'no MusicBrainz match, so no Wikidata image to resolve' };
  }

  let candidates: ImageCandidate[];
  try {
    candidates = await deps.candidates(artist.mbid, artist.name);
  } catch (e) {
    return { status: 'error', reason: message(e) };
  }
  if (candidates.length === 0) {
    return { status: 'none', reason: `no Wikimedia image for '${artist.name}'` };
  }

  const who = artist.disambiguation ? `${artist.name} (${artist.disambiguation})` : artist.name;
  let lastReason = 'no candidate accepted';

  for (const candidate of candidates.slice(0, MAX_IMAGE_ATTEMPTS)) {
    let bytes: Buffer;
    try {
      bytes = await deps.bytes(candidate);
    } catch (e) {
      lastReason = `could not fetch ${candidate.file}: ${message(e)}`;
      continue;
    }

    let analysis: ImageAnalysis | null;
    try {
      analysis = await deps.judge(bytes, candidate.contentType, who);
    } catch (e) {
      // A provider outage is an error, not a verdict — stop rather than burning
      // the remaining candidates against a service that is down.
      return { status: 'error', reason: `image analysis failed: ${message(e)}` };
    }

    if (analysis?.acceptable) {
      return {
        status: 'ok',
        url: candidate.url,
        width: candidate.width,
        height: candidate.height,
        file: candidate.file,
        source: candidate.source,
        credit: toWireCredit(candidate.credit),
      };
    }
    lastReason = analysis?.reason ?? 'image analysis returned no result';
  }

  // Attempts exhausted with no acceptance is a real answer, not a failure.
  return { status: 'none', reason: lastReason };
}

/** Production deps. */
export function prodImageDeps(): ImageDeps {
  return {
    candidates: (mbid, artistName) => resolveImageCandidates(mbid, { artistName }),
    bytes: (candidate) => fetchImageBytes(candidate),
    judge: async (bytes, contentType, who) => {
      const res = await imageAnalysisAgent.generate([
        {
          role: 'user',
          content: [
            { type: 'image', image: bytes, mimeType: contentType },
            { type: 'text', text: `Performer: ${who}. Is this a usable photo of this performer?` },
          ],
        },
      ]);
      return (res.object as ImageAnalysis | undefined) ?? null;
    },
  };
}
