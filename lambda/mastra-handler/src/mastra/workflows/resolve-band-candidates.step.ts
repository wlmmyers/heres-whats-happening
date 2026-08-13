import { createStep } from '@mastra/core/workflows';
import type { ArtistMatch } from '../tools/band-image.js';
import { searchArtists } from '../tools/musicbrainz.tool.js';
import { resolveImageCandidates } from '../tools/wikimedia.tool.js';
import { ImageLoopStateSchema, MAX_ARTIST_FALLTHROUGH } from './poster.schemas.js';

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function label(a: ArtistMatch): string {
  return a.disambiguation ? `${a.name} (${a.disambiguation})` : a.name;
}

// Runs ONCE, ahead of the loop: MB search -> MBID -> Wikidata -> Commons.
// Deterministic for a given performer, so doing it per-iteration would re-spend
// the 1 req/sec MusicBrainz budget recomputing an identical answer.
// Never throws; failures come back as state with a reason (rasterize.tool.ts:28).
export const resolveBandCandidatesStep = createStep({
  id: 'resolve-band-candidates',
  inputSchema: ImageLoopStateSchema,
  outputSchema: ImageLoopStateSchema,
  execute: async ({ inputData }) => {
    let matches: ArtistMatch[];
    try {
      matches = await searchArtists(inputData.performer, { limit: MAX_ARTIST_FALLTHROUGH });
    } catch (e) {
      return { ...inputData, candidates: [], reason: `musicbrainz search failed: ${message(e)}` };
    }

    if (matches.length === 0) {
      return {
        ...inputData,
        candidates: [],
        reason: `no MusicBrainz match for '${inputData.performer}'`,
      };
    }

    const tried: string[] = [];
    let lastError: string | undefined;

    for (const match of matches.slice(0, MAX_ARTIST_FALLTHROUGH)) {
      tried.push(label(match));
      try {
        const candidates = await resolveImageCandidates(match.mbid, { artistName: match.name });
        if (candidates.length > 0) {
          return { ...inputData, artist: match, candidates, candidateIndex: 0 };
        }
      } catch (e) {
        lastError = message(e);
      }
    }

    const detail = lastError ? `; last error: ${lastError}` : '';
    return {
      ...inputData,
      artist: matches[0], // report the best match even though it yielded nothing
      candidates: [],
      reason: `no Wikimedia image for '${inputData.performer}' (tried: ${tried.join(', ')})${detail}`,
    };
  },
});
