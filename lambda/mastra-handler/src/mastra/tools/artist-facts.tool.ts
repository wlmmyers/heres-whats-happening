import { USER_AGENT } from './band-image.js';
import type { FetchFn } from './musicbrainz.tool.js';

const WIKIDATA_BASE = 'https://www.wikidata.org';
const WIKIPEDIA_BASE = 'https://en.wikipedia.org';
const MUSICBRAINZ_BASE = 'https://musicbrainz.org';
const TIMEOUT_MS = 15_000;

/** Full plain-text extracts for major artists run to 100KB+. ~16KB is roughly
 * 4K tokens, which is ample for a 150-250 word bio. */
export const MAX_EXTRACT_CHARS = 16_000;

export interface WikipediaExtract {
  title: string;
  url: string;
  revisionId: number;
  text: string;
}

interface FactsOptions {
  fetchFn?: FetchFn;
  wikidataBaseUrl?: string;
  wikipediaBaseUrl?: string;
}

// Wikimedia's action API returns deeply-nested, endpoint-specific JSON that
// callers navigate with optional chaining. `unknown` would buy no safety here
// without hand-writing a schema per endpoint.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function getJson(url: string, doFetch: FetchFn): Promise<any> {
  const res = await doFetch(url, {
    headers: { 'User-Agent': USER_AGENT, Accept: 'application/json' },
    signal: AbortSignal.timeout(TIMEOUT_MS),
  });
  if (!res.ok) throw new Error(`${new URL(url).host} ${res.status}`);
  return res.json();
}

/** QID -> enwiki article -> plain-text extract + revision id.
 * Null means the artist has no English Wikipedia article, which is a real
 * answer ('none') rather than a failure. */
export async function fetchWikipediaExtract(
  qid: string,
  opts: FactsOptions = {},
): Promise<WikipediaExtract | null> {
  const doFetch = opts.fetchFn ?? globalThis.fetch;
  const wikidata = opts.wikidataBaseUrl ?? WIKIDATA_BASE;
  const wikipedia = opts.wikipediaBaseUrl ?? WIKIPEDIA_BASE;

  const entities = await getJson(
    `${wikidata}/w/api.php?${new URLSearchParams({
      action: 'wbgetentities',
      ids: qid,
      props: 'sitelinks',
      sitefilter: 'enwiki',
      format: 'json',
    })}`,
    doFetch,
  );
  const title: string | undefined = entities?.entities?.[qid]?.sitelinks?.enwiki?.title;
  if (!title) return null;

  // extracts + revisions in ONE call rather than two round trips.
  const page = await getJson(
    `${wikipedia}/w/api.php?${new URLSearchParams({
      action: 'query',
      prop: 'extracts|revisions',
      rvprop: 'ids',
      explaintext: '1',
      redirects: '1',
      titles: title,
      format: 'json',
    })}`,
    doFetch,
  );
  const pages = page?.query?.pages ?? {};
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const first: any = Object.values(pages)[0];
  if (!first || first.missing !== undefined || !first.extract) return null;

  return {
    title: first.title ?? title,
    url: `${wikipedia}/wiki/${encodeURIComponent((first.title ?? title).replace(/ /g, '_'))}`,
    revisionId: first.revisions?.[0]?.revid ?? 0,
    text: String(first.extract).slice(0, MAX_EXTRACT_CHARS),
  };
}

export interface ReleaseGroup {
  title: string;
  year: string;
}

/** Albums with first-release years, newest first. Singles, EPs and undated
 * entries are dropped: this exists to give the model FACTS it cannot get wrong,
 * so anything ambiguous is worse than absent. */
export async function fetchReleaseGroups(
  mbid: string,
  opts: { fetchFn?: FetchFn; baseUrl?: string; minIntervalMs?: number } = {},
): Promise<ReleaseGroup[]> {
  const doFetch = opts.fetchFn ?? globalThis.fetch;
  const baseUrl = opts.baseUrl ?? MUSICBRAINZ_BASE;
  const wait = opts.minIntervalMs ?? 1000; // MusicBrainz allows ~1 req/sec
  if (wait > 0) await new Promise((r) => setTimeout(r, wait));

  const payload = await getJson(
    `${baseUrl}/ws/2/artist/${mbid}?${new URLSearchParams({ inc: 'release-groups', fmt: 'json' })}`,
    doFetch,
  );
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const groups: any[] = payload?.['release-groups'] ?? [];
  return groups
    .filter((g) => g['primary-type'] === 'Album' && typeof g['first-release-date'] === 'string')
    .map((g) => ({ title: String(g.title), year: String(g['first-release-date']).slice(0, 4) }))
    .filter((g) => /^\d{4}$/.test(g.year))
    .sort((a, b) => b.year.localeCompare(a.year));
}
