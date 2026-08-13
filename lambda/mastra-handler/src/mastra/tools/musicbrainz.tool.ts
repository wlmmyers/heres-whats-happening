import { createTool } from '@mastra/core/tools';
import { z } from 'zod';
import { ArtistMatchSchema, USER_AGENT, type ArtistMatch } from './band-image.js';

const DEFAULT_BASE_URL = 'https://musicbrainz.org';
const MIN_INTERVAL_MS = 1000; // MusicBrainz allows ~1 req/sec
const TIMEOUT_MS = 15_000;
const DEFAULT_LIMIT = 3;
// Upstream error bodies reach the client: they flow into the step's `reason`,
// through the workflow output, and out as the 422 body's `error`. An unbounded
// HTML error page has no business in an API response.
const MAX_ERROR_BODY = 200;

function truncate(text: string, limit = MAX_ERROR_BODY): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length <= limit ? flat : `${flat.slice(0, limit)}…`;
}

export type FetchFn = typeof globalThis.fetch;

export interface MusicBrainzOptions {
  /** Defaults to the production host. Tests pass a fake origin. */
  baseUrl?: string;
  userAgent?: string;
  fetchFn?: FetchFn;
  /** Set to 0 in tests to disable throttling. */
  minIntervalMs?: number;
}

export interface MusicBrainzClient {
  searchArtists(performer: string, opts?: { limit?: number }): Promise<ArtistMatch[]>;
}

interface MbArtist {
  id: string;
  name: string;
  score?: number;
  disambiguation?: string;
  type?: string;
  country?: string;
  'life-span'?: { begin?: string | null };
}

function toArtistMatch(a: MbArtist): ArtistMatch {
  return {
    mbid: a.id,
    name: a.name,
    score: a.score ?? 0,
    disambiguation: a.disambiguation || undefined,
    type: a.type || undefined,
    country: a.country || undefined,
    beginYear: a['life-span']?.begin?.slice(0, 4) || undefined,
  };
}

export function createMusicBrainzClient(options: MusicBrainzOptions = {}): MusicBrainzClient {
  const baseUrl = options.baseUrl ?? DEFAULT_BASE_URL;
  const userAgent = options.userAgent ?? USER_AGENT;
  const doFetch = options.fetchFn ?? globalThis.fetch;
  const minIntervalMs = options.minIntervalMs ?? MIN_INTERVAL_MS;

  // Slot-reservation limiter: each caller claims the next free instant and waits
  // for it, so concurrent callers queue instead of all firing at once. This is
  // per-process, i.e. per Lambda container — NOT a global guarantee.
  let nextSlot = 0;
  async function throttle(): Promise<void> {
    if (minIntervalMs <= 0) return;
    const now = Date.now();
    const at = Math.max(now, nextSlot);
    nextSlot = at + minIntervalMs;
    if (at > now) await new Promise((resolve) => setTimeout(resolve, at - now));
  }

  async function getJson(path: string): Promise<unknown> {
    let lastStatus = 0;
    // Two passes: a 503 is MusicBrainz's rate-limit signal, and `throttle()`
    // already spaces the retry by minIntervalMs.
    for (let attempt = 0; attempt < 2; attempt++) {
      await throttle();
      const res = await doFetch(`${baseUrl}${path}`, {
        headers: { 'User-Agent': userAgent, Accept: 'application/json' },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      if (res.status === 503) {
        lastStatus = 503;
        continue;
      }
      if (!res.ok) throw new Error(`musicbrainz ${res.status}: ${truncate(await res.text())}`);
      return res.json();
    }
    throw new Error(`musicbrainz ${lastStatus}: rate limited after retry`);
  }

  return {
    async searchArtists(performer, opts) {
      const limit = opts?.limit ?? DEFAULT_LIMIT;
      // Escape the Lucene string exactly as internal/musicbrainz/client.go does.
      const esc = performer.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
      const q = new URLSearchParams({
        query: `artist:"${esc}"`,
        fmt: 'json',
        limit: String(limit),
      });
      const payload = (await getJson(`/ws/2/artist?${q.toString()}`)) as { artists?: MbArtist[] };
      return (payload.artists ?? []).map(toArtistMatch);
    },
  };
}

/** Production client. */
export const musicBrainzClient = createMusicBrainzClient();

export function searchArtists(
  performer: string,
  opts?: { limit?: number },
): Promise<ArtistMatch[]> {
  return musicBrainzClient.searchArtists(performer, opts);
}

/** Thin wrapper so the lookup is inspectable in Mastra Studio. */
export const musicBrainzArtistTool = createTool({
  id: 'musicbrainz-search-artist',
  description: 'Resolve a fuzzy band name to MusicBrainz artist matches with disambiguation hints.',
  inputSchema: z.object({ performer: z.string(), limit: z.number().optional() }),
  outputSchema: z.object({ matches: z.array(ArtistMatchSchema) }),
  execute: async ({ performer, limit }) => ({ matches: await searchArtists(performer, { limit }) }),
});
