import { artistKey } from './artist-key.js';
import type { CacheEntry, CacheRecord, EnrichmentCache, WorkflowName } from './enrichment-cache.js';
import { isFresh } from './enrichment-cache.js';
import type {
  ArtistInfo,
  BioInfo,
  EnrichedMessage,
  ImageInfo,
  TourInfo,
} from './enrichment-schema.js';
import type { ArtistRef } from './enrich-bio.js';
import type { ArtistMatch } from './mastra/tools/band-image.js';
import type { EventMessage } from './schema.js';

/** Per-workflow slice of the Lambda's 300s timeout, so one hung fetch cannot
 * starve the other two. */
export const WORKFLOW_BUDGET_MS = Number(process.env.ENRICH_BUDGET_MS ?? 120_000);

/** How many MusicBrainz matches to consider. Mirrors MAX_ARTIST_FALLTHROUGH. */
const ARTIST_SEARCH_LIMIT = 3;

/**
 * Source segments whose events are never enriched.
 *
 * Every workflow here is built on MusicBrainz, so a ball game or a play has
 * nothing to gain from them — and something to lose. Roughly half of a
 * Ticketmaster city is non-music, and the performers that come with it are team
 * names and show titles. The ones MusicBrainz *does* match are the expensive
 * case, not the harmless one: "Hamilton" and "Sword" both resolve to real
 * artists, and the result is a musician's photo and LLM-written biography hung
 * off a theatre listing for the 90-day lifetime of the cache entry.
 *
 * Deliberately a deny-list rather than an allow-list of ['music']. Ticketmaster
 * files a great many small club shows under "Undefined" — live Seattle data had
 * The Sword, Red Fang and Diggy Graves there, all at music venues — and those
 * are exactly the artists this pipeline exists to enrich. Anything unrecognized
 * therefore enriches: the gate fails open, matching the event-level rule that
 * enrichment problems never cost us an event.
 */
const SKIP_SEGMENTS = new Set(['sports', 'arts-theatre', 'miscellaneous']);

export interface EnrichDeps {
  cache: EnrichmentCache;
  searchArtists(performer: string, opts: { limit: number }): Promise<ArtistMatch[]>;
  enrichImage(artist: ArtistRef): Promise<ImageInfo>;
  enrichBio(artist: ArtistRef): Promise<BioInfo>;
  enrichTour(artist: ArtistRef, event: { venue: string; date: string }): Promise<TourInfo>;
  now?(): number;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * Resolve ONE artist for all three workflows.
 *
 * Deliberately takes the best match rather than falling through candidates the
 * way resolveBandCandidatesStep does for posters. Falling through per-workflow
 * would let the bio describe "La Luz, US rock band" beside a photo of "La Luz,
 * Belgium based house group" — the disambiguation field exists because those
 * collide.
 */
export async function pickArtist(
  performer: string,
  deps: Pick<EnrichDeps, 'searchArtists'>,
): Promise<ArtistInfo> {
  const matches = await deps.searchArtists(performer, { limit: ARTIST_SEARCH_LIMIT });
  const best = matches[0];
  if (!best) {
    // A real answer, not a failure: it gets an artists row with status
    // not_found so the attempt is recorded and not retried every scrape.
    return { performer, display_name: performer, status: 'not_found' };
  }
  return {
    performer,
    display_name: best.name,
    mbid: best.mbid,
    disambiguation: best.disambiguation,
    type: best.type,
    country: best.country,
    begin_year: best.beginYear,
    status: 'ok',
  };
}

/** Run a workflow under its time budget. A timeout is an 'error', which the
 * cache retries in hours rather than suppressing for months. */
async function withBudget<T extends { status: string; reason?: string }>(
  name: string,
  run: () => Promise<T>,
  fallback: (reason: string) => T,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      run(),
      new Promise<T>((resolve) => {
        timer = setTimeout(
          () => resolve(fallback(`${name} exceeded ${WORKFLOW_BUDGET_MS}ms budget`)),
          WORKFLOW_BUDGET_MS,
        );
      }),
    ]);
  } catch (e) {
    // The workflows are documented never to throw, but a dependency injected by
    // a caller might. Belt and braces: never let this reach the invocation.
    return fallback(message(e));
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function cachedPayload<T>(
  entry: CacheEntry | null,
  name: WorkflowName,
  now: number,
): T | undefined {
  const rec = entry?.workflows?.[name];
  if (!rec || !isFresh(rec, now)) return undefined;
  return rec.payload as T | undefined;
}

/** Identity of the event a log line is about — enough to find the show in the
 * DB or the scrape it came from, without shipping description bodies and URLs
 * to CloudWatch on every miss. */
function eventRef(event: EventMessage) {
  return {
    source_id: event.source_id,
    source_event_id: event.source_event_id,
    title: event.title,
    venue: event.venue.name,
    starts_at: event.starts_at,
  };
}

function record(result: ImageInfo | BioInfo | TourInfo, at: string): CacheRecord {
  return {
    status: result.status,
    at,
    reason: result.reason,
    payload: result,
  };
}

/**
 * Enrich one normalized event. NEVER throws: the caller must republish the
 * event whatever happened, because enrichment failing is not a reason to stop
 * ingesting a perfectly good show.
 */
export async function enrichEvent(deps: EnrichDeps, event: EventMessage): Promise<EnrichedMessage> {
  const now = deps.now?.() ?? Date.now();
  const performer = event.performers?.[0];
  // Nothing to enrich against. Emit the event exactly as it arrived.
  if (!performer) return { ...event };

  // Ahead of the cache read on purpose: a skipped event should cost no S3 round
  // trip either, which is most of the point of skipping it.
  if (event.segment && SKIP_SEGMENTS.has(event.segment)) {
    console.log(
      JSON.stringify({ msg: 'enrichment-skipped-segment', performer, event: eventRef(event) }),
    );
    return { ...event };
  }

  // A cache failure must be loud but non-fatal: a misconfigured bucket should
  // show up in logs, not present as an ingest outage.
  let entry: CacheEntry | null = null;
  try {
    entry = await deps.cache.read(performer);
  } catch (e) {
    console.error(
      JSON.stringify({ msg: 'enrichment-cache-read-failed', performer, error: message(e) }),
    );
  }

  const cachedImage = cachedPayload<ImageInfo>(entry, 'image', now);
  const cachedBio = cachedPayload<BioInfo>(entry, 'bio', now);
  const cachedTour = cachedPayload<TourInfo>(entry, 'tour', now);

  // The event's own image always wins, so there is nothing to resolve when the
  // source supplied one.
  const needsImage = !event.image_url;
  const runImage = needsImage && cachedImage === undefined;
  const runBio = cachedBio === undefined;
  const runTour = cachedTour === undefined;

  // The gate is what keeps this Lambda's API and LLM spend bounded, so a miss
  // is the number worth watching: one line per event that had to pay for work,
  // naming what missed and which event paid for it. A cold artist logs a miss
  // legitimately; the same performer missing every scrape means the cache is
  // not working (see the AccessDenied warning in enrichment-cache.ts). The
  // image workflow is absent when the EVENT supplied a picture — that is a skip
  // the cache was never asked about, and counting it would inflate the rate.
  const missed: string[] = [];
  if (!entry?.artist) missed.push('artist');
  if (runImage) missed.push('image');
  if (runBio) missed.push('bio');
  if (runTour) missed.push('tour');
  if (missed.length > 0) {
    console.log(
      JSON.stringify({
        msg: 'enrichment-cache-miss',
        performer,
        entry_found: entry !== null,
        missed,
        event: eventRef(event),
      }),
    );
  }

  // Everything satisfied from cache AND an artist on file: skip the
  // MusicBrainz call entirely. This is the whole point of the gate.
  if (!runImage && !runBio && !runTour && entry?.artist) {
    return {
      ...event,
      enrichment: {
        artist: entry.artist,
        image: cachedImage,
        bio: cachedBio,
        tour: cachedTour,
        attempted_at: new Date(now).toISOString(),
      },
    };
  }

  let artist: ArtistInfo;
  try {
    artist = await pickArtist(performer, deps);
  } catch (e) {
    // The prelude failed, so nothing downstream can run. Emit the bare event;
    // no cache is written, so the next delivery retries immediately.
    console.error(JSON.stringify({ msg: 'artist-prelude-failed', performer, error: message(e) }));
    return { ...event };
  }

  const ref: ArtistRef = {
    mbid: artist.mbid ?? '',
    name: artist.display_name,
    disambiguation: artist.disambiguation,
  };
  const tourEvent = { venue: event.venue.name, date: event.starts_at.slice(0, 10) };

  const [image, bio, tour] = await Promise.all([
    runImage
      ? withBudget(
          'image',
          () => deps.enrichImage(ref),
          (reason) => ({
            status: 'error' as const,
            reason,
          }),
        )
      : Promise.resolve(cachedImage),
    runBio
      ? withBudget(
          'bio',
          () => deps.enrichBio(ref),
          (reason) => ({
            status: 'error' as const,
            reason,
          }),
        )
      : Promise.resolve(cachedBio),
    runTour
      ? withBudget(
          'tour',
          () => deps.enrichTour(ref, tourEvent),
          (reason) => ({
            status: 'error' as const,
            reason,
          }),
        )
      : Promise.resolve(cachedTour),
  ]);

  // ONE write, after the fan-out, so the three workflows never
  // read-modify-write the same object against each other.
  const at = new Date(now).toISOString();
  const workflows: CacheEntry['workflows'] = { ...(entry?.workflows ?? {}) };
  // Note the image branch: a skip caused by the EVENT having its own picture
  // records nothing, because that fact says nothing about the artist.
  if (runImage && image) workflows.image = record(image, at);
  if (runBio && bio) workflows.bio = record(bio, at);
  if (runTour && tour) workflows.tour = record(tour, at);

  try {
    await deps.cache.write(performer, {
      artist_key: artistKey(performer),
      performer,
      artist,
      workflows,
    });
  } catch (e) {
    console.error(
      JSON.stringify({ msg: 'enrichment-cache-write-failed', performer, error: message(e) }),
    );
  }

  return {
    ...event,
    enrichment: { artist, image, bio, tour, attempted_at: at },
  };
}
