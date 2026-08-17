import { describe, expect, it } from 'vitest';
import { EventMessageSchema } from '../src/schema.js';
import { buildMessage, pickHeadliner, type EventRow } from './backfill.js';

/** A minimal well-formed row; individual tests override what they exercise. */
function row(over: Partial<EventRow> = {}): EventRow {
  return {
    source_name: 'email_newsletter',
    source_event_id: 'abc123',
    title: 'Khruangbin',
    description: null,
    starts_at: '2026-09-15T20:00:00+00:00',
    ends_at: null,
    time_tbd: false,
    image_url: null,
    url: null,
    headline_performer: null,
    venue_name: 'The Fillmore',
    venue_address: null,
    venue_lat: null,
    venue_lng: null,
    venue_website_url: null,
    performers: ['Khruangbin'],
    genres: [],
    ...over,
  };
}

describe('pickHeadliner', () => {
  it('prefers headline_performer when the event was already enriched', () => {
    // Title order says Mdou Moctar; the recorded artist link outranks it.
    const r = row({
      title: 'Mdou Moctar with Khruangbin',
      performers: ['Khruangbin', 'Mdou Moctar'],
      headline_performer: 'Khruangbin',
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Khruangbin', signal: 'headline_artist' });
  });

  it('ignores a headline_performer that is not on the bill', () => {
    // Stale link (performers were re-scraped since). Fall through, do not emit
    // an artist the event no longer lists.
    const r = row({
      title: 'Khruangbin with Mdou Moctar',
      performers: ['Khruangbin', 'Mdou Moctar'],
      headline_performer: 'Someone Else',
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Khruangbin', signal: 'title' });
  });

  it('picks the performer named earliest in the title', () => {
    const r = row({
      title: 'Mdou Moctar w/ Khruangbin',
      performers: ['Khruangbin', 'Mdou Moctar'],
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Mdou Moctar', signal: 'title' });
  });

  it('matches on word boundaries, not bare substrings', () => {
    // "Low" occurs at index 0 of "Lowlands" — a naive indexOf would headline the
    // opener on every title whose headliner name contains it.
    const r = row({
      title: 'Lowlands with Low',
      performers: ['Low', 'Lowlands'],
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Lowlands', signal: 'title' });
  });

  it('prefers the longer name when two performers match at the same index', () => {
    // Both match at index 0: the space after "The Beths" satisfies the trailing
    // boundary inside "The Beths Quartet". The longer name is what the title
    // actually bills.
    const r = row({
      title: 'The Beths Quartet with The Beths',
      performers: ['The Beths', 'The Beths Quartet'],
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'The Beths Quartet', signal: 'title' });
  });

  it('breaks that tie the same way regardless of ctid order', () => {
    const r = row({
      title: 'The Beths Quartet with The Beths',
      performers: ['The Beths Quartet', 'The Beths'],
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'The Beths Quartet', signal: 'title' });
  });

  it('is case- and punctuation-insensitive about the title', () => {
    const r = row({
      title: 'AN EVENING WITH SLEATER-KINNEY',
      performers: ['Sleater-Kinney'],
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Sleater-Kinney', signal: 'title' });
  });

  it('falls back to ctid order when the title names no performer', () => {
    const r = row({
      title: 'Friday Night Live',
      performers: ['Khruangbin', 'Mdou Moctar'],
    });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Khruangbin', signal: 'ctid' });
  });

  it('reports the single performer as a ctid pick when the title omits it', () => {
    const r = row({ title: 'Sold Out Show', performers: ['Khruangbin'] });
    expect(pickHeadliner(r)).toEqual({ headliner: 'Khruangbin', signal: 'ctid' });
  });

  it('returns none for an event with no performers', () => {
    const r = row({ performers: [] });
    expect(pickHeadliner(r)).toEqual({ headliner: null, signal: 'none' });
  });

  it('does not let regex metacharacters in a name throw', () => {
    const r = row({ title: '!!! (chk chk chk)', performers: ['!!!'] });
    expect(() => pickHeadliner(r)).not.toThrow();
  });
});

describe('buildMessage', () => {
  it('rotates the headliner to index 0 and keeps every other performer', () => {
    const msg = buildMessage(
      row({
        title: 'Mdou Moctar with Khruangbin and Altin Gun',
        performers: ['Khruangbin', 'Mdou Moctar', 'Altin Gun'],
      }),
    );
    // Index 0 is the only thing enrichEvent reads; the rest must survive because
    // ingest deletes and reinserts event_performers from this array.
    expect(msg.performers?.[0]).toBe('Mdou Moctar');
    expect(msg.performers).toHaveLength(3);
    expect(new Set(msg.performers)).toEqual(new Set(['Khruangbin', 'Mdou Moctar', 'Altin Gun']));
  });

  it('emits source_id as the source NAME, which is what the consumer looks up', () => {
    // internal/ingest/events.go calls GetEventSourceByName(ctx, m.SourceID).
    // A UUID here would fail the lookup and DLQ every message.
    const msg = buildMessage(row({ source_name: 'ticketmaster' }));
    expect(msg.source_id).toBe('ticketmaster');
  });

  it('carries genres through so ingest does not wipe them', () => {
    // handleMessage does DeleteEventGenresByEvent then reinserts from m.Genres.
    const msg = buildMessage(row({ genres: ['rock', 'psychedelic'] }));
    expect(msg.genres).toEqual(['rock', 'psychedelic']);
  });

  it('omits absent optionals rather than sending null', () => {
    // The schema is .strict() and its optionals reject null outright.
    const msg = buildMessage(row());
    expect(JSON.parse(JSON.stringify(msg))).not.toHaveProperty('description');
    expect(JSON.parse(JSON.stringify(msg))).not.toHaveProperty('ends_at');
    expect(JSON.parse(JSON.stringify(msg))).not.toHaveProperty('image_url');
    expect(JSON.parse(JSON.stringify(msg))).not.toHaveProperty('url');
    expect(JSON.parse(JSON.stringify(msg)).venue).not.toHaveProperty('address');
  });

  it('treats an empty-string column as absent', () => {
    // description is NOT NULL DEFAULT '' in Postgres, so '' is the common case.
    const msg = buildMessage(row({ description: '', url: '' }));
    expect(JSON.parse(JSON.stringify(msg))).not.toHaveProperty('description');
    expect(JSON.parse(JSON.stringify(msg))).not.toHaveProperty('url');
  });

  it('preserves every field the event upsert would otherwise overwrite', () => {
    const msg = buildMessage(
      row({
        description: 'An evening of psychedelic soul',
        ends_at: '2026-09-15T23:30:00+00:00',
        time_tbd: true,
        image_url: 'https://example.com/poster.jpg',
        url: 'https://example.com/tickets',
        venue_address: '1805 Geary Blvd, San Francisco, CA',
        venue_lat: 37.784,
        venue_lng: -122.433,
        venue_website_url: 'https://thefillmore.com',
      }),
    );
    expect(msg.description).toBe('An evening of psychedelic soul');
    expect(msg.ends_at).toBe('2026-09-15T23:30:00+00:00');
    expect(msg.time_tbd).toBe(true);
    expect(msg.image_url).toBe('https://example.com/poster.jpg');
    expect(msg.url).toBe('https://example.com/tickets');
    expect(msg.venue).toEqual({
      name: 'The Fillmore',
      address: '1805 Geary Blvd, San Francisco, CA',
      lat: 37.784,
      lng: -122.433,
      website_url: 'https://thefillmore.com',
    });
  });

  it('keeps lat/lng of 0 rather than dropping them as falsy', () => {
    const msg = buildMessage(row({ venue_lat: 0, venue_lng: 0 }));
    expect(msg.venue.lat).toBe(0);
    expect(msg.venue.lng).toBe(0);
  });

  it('produces something the real wire schema accepts', () => {
    const msg = buildMessage(row({ performers: ['Khruangbin', 'Mdou Moctar'] }));
    expect(EventMessageSchema.safeParse(JSON.parse(JSON.stringify(msg))).success).toBe(true);
  });

  it('rejects a row whose timestamp carries no offset', () => {
    // Postgres must render timestamptz with a zone; a naive local timestamp here
    // would silently shift every event's start time.
    expect(() => buildMessage(row({ starts_at: '2026-09-15T20:00:00' }))).toThrow();
  });

  it('throws on an event with no performers instead of emitting a no-op message', () => {
    // Such a message would rewrite the event row and enrich nothing, because
    // enrichEvent returns early when performers[0] is undefined.
    expect(() => buildMessage(row({ performers: [] }))).toThrow(/no performers/i);
  });
});
