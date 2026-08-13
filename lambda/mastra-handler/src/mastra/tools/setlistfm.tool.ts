import type { FetchFn } from './musicbrainz.tool.js';

const DEFAULT_BASE_URL = 'https://api.setlist.fm';
// setlist.fm allows 2 req/sec, but this limiter is per-process and the SQS
// event source mapping runs up to 2 concurrent containers. At 500ms those two
// would put ~4 req/sec against a 2 req/sec limit; at 1000ms the worst case
// lands on their limit instead of double it. Throughput is not the binding
// constraint here — the 1,440 req/day cap is.
const MIN_INTERVAL_MS = 1000;
const TIMEOUT_MS = 15_000;
const MAX_ERROR_BODY = 200;

/** A setlist older than this is not "what they have been playing". */
export const MAX_SETLIST_AGE_DAYS = 180;

function truncate(text: string, limit = MAX_ERROR_BODY): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length <= limit ? flat : `${flat.slice(0, limit)}…`;
}

export interface SetlistSong {
  name: string;
  encore?: number;
  cover_of?: string;
  tape?: boolean;
  info?: string;
}

export interface RecentSetlist {
  setlistId: string;
  setlistUrl: string;
  tourName?: string;
  songs: SetlistSong[];
  observedDate: string; // YYYY-MM-DD
  observedVenue?: string;
  observedCity?: string;
}

interface RawSong {
  name?: string;
  info?: string;
  tape?: boolean;
  cover?: { name?: string };
}
interface RawSet {
  name?: string;
  encore?: number;
  song?: RawSong[];
}
interface RawSetlist {
  id?: string;
  eventDate?: string;
  url?: string;
  tour?: { name?: string };
  venue?: { name?: string; city?: { name?: string } };
  // XML-derived JSON: the sets live under `sets.set`, not a bare `set`.
  sets?: { set?: RawSet[] };
}

/** setlist.fm reports dd-MM-yyyy. Returns YYYY-MM-DD, or null for anything
 * else. Never hand this to `new Date()`: "08-12-2026" silently parses as
 * August 12th under US interpretation and "23-08-1964" is Invalid Date. */
export function parseEventDate(s: string): string | null {
  const m = /^(\d{2})-(\d{2})-(\d{4})$/.exec(s ?? '');
  if (!m) return null;
  const [, dd, mm, yyyy] = m;
  const month = Number(mm);
  const day = Number(dd);
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  return `${yyyy}-${mm}-${dd}`;
}

function flattenSongs(raw: RawSetlist): SetlistSong[] {
  const out: SetlistSong[] = [];
  for (const set of raw.sets?.set ?? []) {
    for (const song of set.song ?? []) {
      if (!song.name) continue;
      const entry: SetlistSong = { name: song.name };
      if (set.encore) entry.encore = set.encore;
      if (song.cover?.name) entry.cover_of = song.cover.name;
      if (song.tape) entry.tape = true;
      if (song.info) entry.info = song.info;
      out.push(entry);
    }
  }
  return out;
}

/** Newest setlist that actually has songs and is within MAX_SETLIST_AGE_DAYS.
 * Sorts client-side: the endpoint documentation does not state a sort order. */
export function pickRecentSetlist(raw: RawSetlist[], now = Date.now()): RecentSetlist | null {
  const cutoff = now - MAX_SETLIST_AGE_DAYS * 24 * 3600_000;

  const candidates = raw
    .map((r) => ({ raw: r, date: parseEventDate(r.eventDate ?? '') }))
    .filter((c): c is { raw: RawSetlist; date: string } => c.date !== null)
    .sort((a, b) => b.date.localeCompare(a.date)); // ISO dates sort lexically

  for (const c of candidates) {
    if (Date.parse(`${c.date}T00:00:00Z`) < cutoff) break; // sorted, so all later are older
    const songs = flattenSongs(c.raw);
    // Entries logged for attendance without a setlist are common. A songless
    // entry is an absent setlist, not a present empty one.
    if (songs.length === 0) continue;
    return {
      setlistId: c.raw.id ?? '',
      setlistUrl: c.raw.url ?? '',
      tourName: c.raw.tour?.name,
      songs,
      observedDate: c.date,
      observedVenue: c.raw.venue?.name,
      observedCity: c.raw.venue?.city?.name,
    };
  }
  return null;
}

export interface SetlistFmOptions {
  baseUrl?: string;
  apiKey: string;
  fetchFn?: FetchFn;
  /** Set to 0 in tests to disable throttling. */
  minIntervalMs?: number;
}

export interface SetlistFmClient {
  /** Null means the artist genuinely has no usable recent setlist. Throws only
   * on transport/limit failures, which the caller records as status 'error'. */
  recentSetlist(mbid: string, now?: number): Promise<RecentSetlist | null>;
}

export function createSetlistFmClient(options: SetlistFmOptions): SetlistFmClient {
  const baseUrl = options.baseUrl ?? DEFAULT_BASE_URL;
  const doFetch = options.fetchFn ?? globalThis.fetch;
  const minIntervalMs = options.minIntervalMs ?? MIN_INTERVAL_MS;

  // Slot-reservation limiter, same shape as musicbrainz.tool.ts:65-72: each
  // caller claims the next free instant so concurrent callers queue rather than
  // all firing at once. Per-process, i.e. per Lambda container — NOT global.
  let nextSlot = 0;
  async function throttle(): Promise<void> {
    if (minIntervalMs <= 0) return;
    const now = Date.now();
    const at = Math.max(now, nextSlot);
    nextSlot = at + minIntervalMs;
    if (at > now) await new Promise((resolve) => setTimeout(resolve, at - now));
  }

  return {
    async recentSetlist(mbid, now = Date.now()) {
      await throttle();
      // One page only: 20 items is ample recent history, and paging spends
      // daily budget fetching setlists the 180-day bound would reject.
      const res = await doFetch(`${baseUrl}/rest/1.0/artist/${mbid}/setlists?p=1`, {
        headers: {
          'x-api-key': options.apiKey,
          Accept: 'application/json', // the API defaults to XML
        },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      // 404 is "this artist has no setlists" — a real answer, not a failure.
      if (res.status === 404) return null;
      if (!res.ok) throw new Error(`setlistfm ${res.status}: ${truncate(await res.text())}`);
      const payload = (await res.json()) as { setlist?: RawSetlist[] };
      return pickRecentSetlist(payload.setlist ?? [], now);
    },
  };
}
