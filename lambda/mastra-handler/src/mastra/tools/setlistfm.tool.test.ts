import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { stubFetch } from './stub-fetch.js';
import {
  MAX_SETLIST_AGE_DAYS,
  createSetlistFmClient,
  parseEventDate,
  pickRecentSetlist,
} from './setlistfm.tool.js';

const fixture = JSON.parse(
  readFileSync(
    new URL('../../__fixtures__/setlistfm-artist-setlists.json', import.meta.url),
    'utf8',
  ),
);
const NOW = Date.parse('2026-08-12T00:00:00Z');
const MBID = '9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a';

describe('parseEventDate', () => {
  // new Date("08-12-2026") silently parses as August 12th under US
  // interpretation, and new Date("23-08-1964") is Invalid Date. Both produce a
  // plausible-looking wrong answer, so the format is parsed explicitly.
  it('reads dd-MM-yyyy, not the US ordering', () => {
    expect(parseEventDate('08-12-2026')).toBe('2026-12-08');
    expect(parseEventDate('23-08-1964')).toBe('1964-08-23');
  });

  it('returns null for junk rather than a wrong date', () => {
    expect(parseEventDate('2026-08-12')).toBeNull();
    expect(parseEventDate('')).toBeNull();
  });
});

describe('pickRecentSetlist', () => {
  it('skips the songless recent entry and takes the newest one with songs', () => {
    const picked = pickRecentSetlist(fixture.setlist, NOW);
    expect(picked?.setlistId).toBe('aaaaaaa2');
    expect(picked?.observedDate).toBe('2026-07-14');
    expect(picked?.tourName).toBe('News of the Universe Tour');
    expect(picked?.observedVenue).toBe('The Greek');
    expect(picked?.observedCity).toBe('Berkeley');
  });

  it('flattens sets into an ordered song list carrying encore and cover', () => {
    const picked = pickRecentSetlist(fixture.setlist, NOW)!;
    expect(picked.songs).toEqual([
      { name: 'Sure As Spring' },
      { name: 'Cicada', info: 'acoustic' },
      { name: 'Strange World', encore: 1, cover_of: 'Cover Artist' },
    ]);
  });

  // The endpoint documentation does not state a sort order. It is newest-first
  // in practice, but building on an undocumented ordering produces a bug that
  // appears months later with no code change.
  it('sorts client-side rather than trusting response order', () => {
    const shuffled = [fixture.setlist[2], fixture.setlist[1], fixture.setlist[0]];
    expect(pickRecentSetlist(shuffled, NOW)?.setlistId).toBe('aaaaaaa2');
  });

  it('returns null when everything qualifying is too old', () => {
    const stale = Date.parse('2027-06-01T00:00:00Z');
    expect(pickRecentSetlist(fixture.setlist, stale)).toBeNull();
  });

  it('returns null when every entry is songless', () => {
    expect(pickRecentSetlist([fixture.setlist[0]], NOW)).toBeNull();
  });

  it('bounds staleness at 180 days', () => {
    expect(MAX_SETLIST_AGE_DAYS).toBe(180);
  });
});

describe('createSetlistFmClient', () => {
  it('sends the api key and asks for JSON', async () => {
    const fetchFn = stubFetch([{ match: /artist\/.*\/setlists/, json: fixture }]);
    const client = createSetlistFmClient({
      baseUrl: 'https://fake.setlist',
      apiKey: 'test-key',
      fetchFn,
      minIntervalMs: 0,
    });

    await client.recentSetlist(MBID);

    expect(fetchFn.calls).toHaveLength(1);
    expect(fetchFn.calls[0].url).toContain('/rest/1.0/artist/' + MBID + '/setlists?p=1');
    expect(fetchFn.calls[0].headers['x-api-key']).toBe('test-key');
    // The API defaults to XML; without this header the body is unparseable.
    expect(fetchFn.calls[0].headers['accept']).toBe('application/json');
  });

  it('treats 404 as no setlists rather than an error', async () => {
    const fetchFn = stubFetch([{ match: /setlists/, status: 404, json: {} }]);
    const client = createSetlistFmClient({
      baseUrl: 'https://fake.setlist',
      apiKey: 'k',
      fetchFn,
      minIntervalMs: 0,
    });
    expect(await client.recentSetlist(MBID)).toBeNull();
  });

  it('throws on 429 so the caller can record an error status', async () => {
    const fetchFn = stubFetch([{ match: /setlists/, status: 429, json: {} }]);
    const client = createSetlistFmClient({
      baseUrl: 'https://fake.setlist',
      apiKey: 'k',
      fetchFn,
      minIntervalMs: 0,
    });
    await expect(client.recentSetlist(MBID)).rejects.toThrow(/429/);
  });

  it('never paginates', async () => {
    const fetchFn = stubFetch([{ match: /setlists/, json: fixture }]);
    const client = createSetlistFmClient({
      baseUrl: 'https://fake.setlist',
      apiKey: 'k',
      fetchFn,
      minIntervalMs: 0,
    });
    await client.recentSetlist(MBID);
    expect(fetchFn.calls).toHaveLength(1);
  });
});
