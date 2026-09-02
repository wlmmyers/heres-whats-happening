import { describe, expect, it, vi } from 'vitest';
import { createMusicBrainzClient } from './musicbrainz.tool.js';
import { stubFetch } from './stub-fetch.js';

// Trimmed from a real response for artist:"la luz" (verified 2026-08-05).
const LA_LUZ = {
  count: 21,
  artists: [
    {
      id: '9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a',
      name: 'La Luz',
      score: 100,
      disambiguation: 'US rock band',
      type: 'Group',
      country: 'US',
      'life-span': { begin: '2012', ended: null },
    },
    {
      id: '1d75fdf0-fe29-48aa-9e3e-b70ca92119e7',
      name: 'La Luz',
      score: 88,
      disambiguation: 'Belgium based house group',
      type: 'Group',
      'life-span': { ended: null },
    },
  ],
};

// Verbatim from the real response for artist:"Pond" (verified 2026-09-01), the
// search behind the prod bug where a Pond show got a Bardo Pond biography.
// MusicBrainz ranks the SUPERSET name above the exact one: 'Bardo Pond' 100,
// 'Pond' 99. Four of these six are exactly "Pond".
const POND = {
  count: 74,
  artists: [
    {
      id: '2ad8bce1-8e55-48db-82ed-d98b52a3a13f',
      name: 'Bardo Pond',
      score: 100,
      disambiguation: 'psychedelic rock group',
      type: 'Group',
      country: 'US',
      'life-span': { begin: '1991' },
    },
    {
      id: '69ac44f7-c80d-47b4-9bc8-fcc758d209e6',
      name: 'Pond',
      score: 99,
      disambiguation: 'rock band from Perth, Australia',
      type: 'Group',
      country: 'AU',
      'life-span': { begin: '2008' },
    },
    {
      id: 'd4a9be59-13e5-481b-8c68-833c5c1fd458',
      name: 'matt pond PA',
      score: 92,
      type: 'Group',
      country: 'US',
      'life-span': { begin: '1998' },
    },
    {
      id: '8154377a-42a4-4fe2-a367-689a86ff07f7',
      name: 'POND',
      score: 92,
      disambiguation: 'German band specializing in electronic music',
      type: 'Group',
      country: 'DE',
      'life-span': { begin: '1978' },
    },
    {
      id: '1ef504e7-2f2d-41a5-9536-64f161f54174',
      name: 'Pond',
      score: 90,
      disambiguation: '90s Portland alternative rock band',
      type: 'Group',
      country: 'US',
      'life-span': { begin: '1991' },
    },
    {
      id: '11e37ff3-6e53-48d7-8c67-1c9f64cd48fb',
      name: 'Pond',
      score: 84,
      disambiguation: 'instrumental hip-hop artist',
      type: 'Person',
    },
  ],
};

function client(f: ReturnType<typeof stubFetch>) {
  return createMusicBrainzClient({ baseUrl: 'https://mb.test', fetchFn: f, minIntervalMs: 0 });
}

describe('searchArtists', () => {
  it('maps hits including the disambiguation fields', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    const out = await client(f).searchArtists('la luz');

    expect(out).toHaveLength(2);
    expect(out[0]).toEqual({
      mbid: '9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a',
      name: 'La Luz',
      score: 100,
      disambiguation: 'US rock band',
      type: 'Group',
      country: 'US',
      beginYear: '2012',
    });
    // Absent fields become undefined rather than null or "".
    expect(out[1].country).toBeUndefined();
    expect(out[1].beginYear).toBeUndefined();
  });

  it('sends the required User-Agent and Accept headers', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    await client(f).searchArtists('la luz');
    expect(f.calls[0].headers['user-agent']).toBe(
      'heres-whats-happening/1.0 ( wlmmyers@gmail.com )',
    );
    expect(f.calls[0].headers['accept']).toBe('application/json');
  });

  it('requests a bounded, quoted Lucene query', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    await client(f).searchArtists('la luz', { limit: 3 });
    const url = new URL(f.calls[0].url);
    expect(url.searchParams.get('query')).toBe('artist:"la luz"');
    expect(url.searchParams.get('fmt')).toBe('json');
    // The wire limit is the ranking pool, not the caller's 3 — see 'search pool'.
    expect(url.searchParams.get('limit')).toBe('25');
  });

  it('honours a caller limit larger than the pool', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
    await client(f).searchArtists('la luz', { limit: 50 });
    expect(new URL(f.calls[0].url).searchParams.get('limit')).toBe('50');
  });

  it('escapes quotes and backslashes so the query cannot be broken', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: { artists: [] } }]);
    await client(f).searchArtists('AC\\DC "live"');
    const url = new URL(f.calls[0].url);
    expect(url.searchParams.get('query')).toBe('artist:"AC\\\\DC \\"live\\""');
  });

  it('returns [] when there are no matches', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: { count: 0, artists: [] } }]);
    expect(await client(f).searchArtists('zzzz')).toEqual([]);
  });

  it('surfaces a non-2xx as an error', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, status: 500, json: { error: 'boom' } }]);
    await expect(client(f).searchArtists('la luz')).rejects.toThrow(/musicbrainz 500/);
  });

  it('retries once on 503, their rate-limit response', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, statuses: [503, 200], json: LA_LUZ }]);
    const out = await client(f).searchArtists('la luz');
    expect(f.calls).toHaveLength(2);
    expect(out).toHaveLength(2);
  });

  it('gives up after a second 503', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, statuses: [503, 503], json: {} }]);
    await expect(client(f).searchArtists('la luz')).rejects.toThrow(/503/);
    expect(f.calls).toHaveLength(2);
  });

  it('spaces successive requests by the configured interval', async () => {
    vi.useFakeTimers();
    try {
      const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: LA_LUZ }]);
      const c = createMusicBrainzClient({
        baseUrl: 'https://mb.test',
        fetchFn: f,
        minIntervalMs: 1000,
      });

      const first = c.searchArtists('a');
      await vi.advanceTimersByTimeAsync(0);
      await first;
      expect(f.calls).toHaveLength(1);

      const second = c.searchArtists('b');
      await vi.advanceTimersByTimeAsync(0);
      expect(f.calls).toHaveLength(1); // still gated

      await vi.advanceTimersByTimeAsync(1000);
      await second;
      expect(f.calls).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

// Regression: prod enriched a "Pond" show at The Showbox with a Bardo Pond
// biography. MusicBrainz's score ranks names that merely CONTAIN the performer
// at or above the exact name, so the top hit is not the best answer.
describe('exact-name preference', () => {
  it('puts the exact name first even when MusicBrainz scores a superset higher', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    const out = await client(f).searchArtists('Pond', { limit: 3 });

    expect(out[0].name).toBe('Pond');
    expect(out[0].mbid).toBe('69ac44f7-c80d-47b4-9bc8-fcc758d209e6');
    expect(out[0].disambiguation).toBe('rock band from Perth, Australia');
  });

  it('orders exact matches among themselves by MusicBrainz score', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    const out = await client(f).searchArtists('Pond', { limit: 4 });

    // All four exact "Pond" artists outrank the two superset names, and keep
    // MusicBrainz's relative order (99, 92, 90, 84) inside that group.
    expect(out.map((a) => a.score)).toEqual([99, 92, 90, 84]);
    expect(out.map((a) => a.name)).toEqual(['Pond', 'POND', 'Pond', 'Pond']);
  });

  it('demotes superset names below every exact match', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    const out = await client(f).searchArtists('Pond', { limit: 6 });
    expect(out.slice(4).map((a) => a.name)).toEqual(['Bardo Pond', 'matt pond PA']);
  });

  it('matches names case- and accent-insensitively, as artistKey does', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    const out = await client(f).searchArtists('POND', { limit: 1 });
    expect(out[0].name).toBe('Pond');
    expect(out[0].country).toBe('AU');
  });

  // The fuzzy path is untouched: with nothing to prefer, MusicBrainz's own
  // ranking still decides, so misspellings and abbreviations resolve as before.
  it('leaves MusicBrainz order alone when no candidate matches exactly', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    const out = await client(f).searchArtists('Ponnd', { limit: 3 });
    expect(out.map((a) => a.name)).toEqual(['Bardo Pond', 'Pond', 'matt pond PA']);
  });
});

// The caller's limit is a cap on the ANSWER, not on the pool we rank. Searching
// only the caller's 3 would have hidden two of the four exact "Pond" artists,
// and for "Girls" the exact match is not in MusicBrainz's top 3 at all. A wider
// pool is the same single request — `limit` is just a query parameter.
describe('search pool', () => {
  it('asks MusicBrainz for a deeper pool than the caller requested', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    await client(f).searchArtists('Pond', { limit: 3 });
    const url = new URL(f.calls[0].url);
    expect(Number(url.searchParams.get('limit'))).toBeGreaterThanOrEqual(25);
  });

  it('still returns no more than the caller asked for', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    expect(await client(f).searchArtists('Pond', { limit: 2 })).toHaveLength(2);
  });

  it('takes one request to do it', async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: POND }]);
    await client(f).searchArtists('Pond', { limit: 3 });
    expect(f.calls).toHaveLength(1);
  });
});

describe('error text handling', () => {
  it('truncates a huge upstream error body before it can reach a client response', async () => {
    const huge = '<html>' + 'x'.repeat(5000) + '</html>';
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, status: 500, json: huge }]);
    await expect(client(f).searchArtists('la luz')).rejects.toThrow(/musicbrainz 500/);

    const err = await client(stubFetch([{ match: /\/ws\/2\/artist\?/, status: 500, json: huge }]))
      .searchArtists('la luz')
      .catch((e: Error) => e);
    expect((err as Error).message.length).toBeLessThan(400);
    expect((err as Error).message).toContain('…');
  });
});
