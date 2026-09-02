import { createTool } from '@mastra/core/tools';
import { z } from 'zod';
import { artistKey } from '../../artist-key.js';
import { ArtistMatchSchema, USER_AGENT, type ArtistMatch } from './band-image.js';

const DEFAULT_BASE_URL = 'https://musicbrainz.org';
const MIN_INTERVAL_MS = 1000; // MusicBrainz allows ~1 req/sec
const TIMEOUT_MS = 15_000;
const DEFAULT_LIMIT = 3;
// How many hits to pull from MusicBrainz before re-ranking, regardless of what
// the caller asked for. The caller's limit caps the ANSWER; it must not cap the
// pool we rank, or the exact match can be truncated away before we ever see it:
// "Pond" has four exactly-named artists and the top 3 hold only two of them,
// and for "Girls" the exact match is outside MusicBrainz's top 3 entirely.
// Costs nothing extra — `limit` is a query parameter on the same one request.
const SEARCH_POOL = 25;
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

/**
 * Move artists actually NAMED `performer` ahead of the rest, keeping
 * MusicBrainz's order within each group.
 *
 * MusicBrainz's `score` is Lucene relevance, not name equality, and it reliably
 * ranks a longer name that merely CONTAINS the query at or above the exact one:
 * `artist:"Pond"` returns Bardo Pond (100) above Pond (99), `artist:"Bush"`
 * returns Kate Bush (100) above Bush (95). Callers that take the top hit
 * therefore enrich a show with the wrong band — which is how a Pond gig at The
 * Showbox ended up with a Bardo Pond biography.
 *
 * Comparison is artistKey()'s, the same normalization the DB keys artists on,
 * so 'POND' and 'Pond' are one name and 'Björk'/'Bjork' agree. Nothing beyond
 * equality: 'The Sword' and 'Sword' are two different real bands, so stripping
 * articles here would trade this bug for its mirror image.
 *
 * A stable partition, not a filter. When nothing matches exactly the order is
 * unchanged, so the fuzzy path — misspellings, "Beyoncé" for "Beyonce Knowles"
 * — still resolves on MusicBrainz's ranking exactly as before.
 */
export function preferExactName(performer: string, matches: ArtistMatch[]): ArtistMatch[] {
  const want = artistKey(performer);
  const exact: ArtistMatch[] = [];
  const rest: ArtistMatch[] = [];
  for (const m of matches) (artistKey(m.name) === want ? exact : rest).push(m);
  return [...exact, ...rest];
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
        limit: String(Math.max(limit, SEARCH_POOL)),
      });
      const payload = (await getJson(`/ws/2/artist?${q.toString()}`)) as { artists?: MbArtist[] };
      const matches = (payload.artists ?? []).map(toArtistMatch);
      return preferExactName(performer, matches).slice(0, limit);
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
