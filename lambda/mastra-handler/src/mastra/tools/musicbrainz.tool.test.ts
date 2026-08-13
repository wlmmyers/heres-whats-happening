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
    expect(url.searchParams.get('limit')).toBe('3');
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
