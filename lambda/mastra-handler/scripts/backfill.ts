/* Pure logic for the enrichment backfill: turn one row of
 * backfill-enrichment.sql into a canonical wire message.
 *
 * Lives in scripts/ rather than src/ so none of it ships in the Lambda image.
 * It is imported by backfill-enrichment.ts (the runner) and exercised directly
 * by backfill.test.ts.
 */
import { EventMessageSchema, type EventMessage } from '../src/schema.js';

/** One row of backfill-enrichment.sql, already JSON-decoded. Column names mirror
 * the query's aliases; nullable columns arrive as null, never undefined. */
export interface EventRow {
  /** event_sources.name — NOT events.source_id. The consumer resolves the source
   * with GetEventSourceByName, so the wire carries the name. */
  source_name: string;
  source_event_id: string;
  title: string;
  description: string | null;
  /** RFC3339 with offset; the query pins the session to UTC so this is +00:00. */
  starts_at: string;
  ends_at: string | null;
  time_tbd: boolean;
  image_url: string | null;
  url: string | null;
  /** The event_performers.performer_name behind events.headline_artist_id, or
   * null when the event was never enriched. */
  headline_performer: string | null;
  venue_name: string;
  venue_address: string | null;
  venue_lat: number | null;
  venue_lng: number | null;
  venue_website_url: string | null;
  /** Every performer on the bill, in physical (ctid) row order. */
  performers: string[];
  genres: string[];
}

/** Which signal decided the headliner. Surfaced in the dry run so the title
 * guesses can be eyeballed before anything is published. */
export type HeadlinerSignal = 'headline_artist' | 'title' | 'ctid' | 'none';

export interface HeadlinerPick {
  headliner: string | null;
  signal: HeadlinerSignal;
}

/** Escape a performer name for literal use inside a RegExp. */
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Index of `performer` within `title`, or -1.
 *
 * Boundary-anchored on both sides: performers whose names contain one another
 * ("Low" inside "Lowlands") otherwise both match at the same offset and the
 * shorter, wrong one wins. The boundary is "not a letter or digit" rather than
 * \b because \b treats "!!!" and "Sleater-Kinney" as starting/ending mid-word.
 */
function titleIndex(title: string, performer: string): number {
  const name = performer.trim();
  if (name === '') return -1;
  const re = new RegExp(`(?<![\\p{L}\\p{N}])${escapeRegExp(name)}(?![\\p{L}\\p{N}])`, 'iu');
  return title.search(re);
}

/**
 * Decide which performer the Lambda should enrich.
 *
 * event_performers is a set — PK (event_id, normalized_name), no position
 * column — so the headliner-first ordering the extractor produced is gone by
 * the time we read it back. Three signals, best first:
 *
 *   1. headline_artist — the artist the event is already linked to. Authoritative.
 *   2. title           — titles bill the headliner first ("X with Y").
 *   3. ctid            — ingest bulk-deletes then reinserts performers in array
 *                        order, so physical order usually survives. A guess.
 */
export function pickHeadliner(row: EventRow): HeadlinerPick {
  if (row.performers.length === 0) return { headliner: null, signal: 'none' };

  // A stale link (performers re-scraped since the enrichment) must not put a
  // name on the wire that the event no longer lists.
  if (row.headline_performer && row.performers.includes(row.headline_performer)) {
    return { headliner: row.headline_performer, signal: 'headline_artist' };
  }

  let best: string | null = null;
  let bestAt = Infinity;
  for (const p of row.performers) {
    const at = titleIndex(row.title, p);
    if (at === -1) continue;
    // Equal offsets mean one name is a prefix of the other; the longer one is
    // the more specific match and the one the title actually names.
    if (at < bestAt || (at === bestAt && best !== null && p.length > best.length)) {
      best = p;
      bestAt = at;
    }
  }
  if (best !== null) return { headliner: best, signal: 'title' };

  return { headliner: row.performers[0]!, signal: 'ctid' };
}

/** Postgres NOT NULL DEFAULT '' columns arrive as '' and mean absent; the wire
 * optionals reject both '' semantics and null, so collapse each to undefined. */
function text(v: string | null): string | undefined {
  return v ? v : undefined;
}

/**
 * Build the canonical wire message for one event.
 *
 * This message round-trips through the full pipeline — enrichment Lambda, then
 * the Go ingest consumer's UpsertEvent — and that consumer REPLACES the event's
 * performers and genres from what it receives. So the message must reproduce
 * the row faithfully; only the performer ORDER changes, to put the headliner at
 * index 0 where enrichEvent looks for it.
 */
export function buildMessage(row: EventRow): EventMessage {
  const { headliner } = pickHeadliner(row);
  if (headliner === null) {
    // enrichEvent returns early with no enrichment when performers[0] is
    // undefined, so such a message would rewrite the event row for nothing.
    // The query already filters these out; this guards a hand-edited JSONL.
    throw new Error(`event ${row.source_name}/${row.source_event_id} has no performers`);
  }

  const msg: EventMessage = {
    source_id: row.source_name,
    source_event_id: row.source_event_id,
    title: row.title,
    description: text(row.description),
    starts_at: row.starts_at,
    ends_at: text(row.ends_at),
    time_tbd: row.time_tbd,
    venue: {
      name: row.venue_name,
      address: text(row.venue_address),
      // ?? not ||: a venue genuinely at latitude 0 must keep its coordinate.
      lat: row.venue_lat ?? undefined,
      lng: row.venue_lng ?? undefined,
      website_url: text(row.venue_website_url),
    },
    performers: [headliner, ...row.performers.filter((p) => p !== headliner)],
    genres: row.genres.length ? row.genres : undefined,
    image_url: text(row.image_url),
    url: text(row.url),
  };

  // Defensive, mirroring toMessage: nothing reaches the queue unvalidated.
  return EventMessageSchema.parse(msg);
}
