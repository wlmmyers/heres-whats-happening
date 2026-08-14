import { describe, expect, it, vi } from 'vitest';
import { StubEnrichmentCache } from './enrichment-cache.js';
import { enrichEvent, type EnrichDeps } from './enrichment.js';
import type { EventMessage } from './schema.js';

const NOW = Date.parse('2026-08-12T00:00:00Z');

function baseEvent(overrides: Partial<EventMessage> = {}): EventMessage {
  return {
    source_id: 'ticketmaster',
    source_event_id: 'tm-aaa',
    title: 'La Luz at The Chapel',
    starts_at: '2026-09-02T20:00:00Z',
    venue: { name: 'The Chapel' },
    performers: ['La Luz', 'Opener'],
    ...overrides,
  };
}

function deps(over: Partial<EnrichDeps> = {}): EnrichDeps {
  return {
    cache: new StubEnrichmentCache(),
    searchArtists: async () => [
      {
        mbid: 'mbid-1',
        name: 'La Luz',
        score: 100,
        disambiguation: 'US rock band',
        type: 'Group',
        country: 'US',
        beginYear: '2012',
      },
    ],
    enrichImage: async () => ({ status: 'ok', url: 'https://img/a.jpg', width: 640, height: 427 }),
    enrichBio: async () => ({ status: 'ok', bio_md: 'Formed in Seattle.' }),
    enrichTour: async () => ({ status: 'ok', tour_name: 'T', songs: [{ name: 'S' }] }),
    now: () => NOW,
    ...over,
  };
}

describe('enrichEvent', () => {
  it('enriches the FIRST performer — the headliner', async () => {
    const searchArtists = vi.fn(async () => [{ mbid: 'mbid-1', name: 'La Luz', score: 100 }]);
    const out = await enrichEvent(deps({ searchArtists }), baseEvent());
    expect(searchArtists).toHaveBeenCalledWith('La Luz', expect.anything());
    expect(out.enrichment?.artist?.performer).toBe('La Luz');
    expect(out.enrichment?.artist?.display_name).toBe('La Luz');
    expect(out.enrichment?.artist?.status).toBe('ok');
  });

  it('passes the original event fields through untouched', async () => {
    const out = await enrichEvent(deps(), baseEvent());
    expect(out.title).toBe('La Luz at The Chapel');
    expect(out.venue.name).toBe('The Chapel');
    expect(out.performers).toEqual(['La Luz', 'Opener']);
  });

  it('emits no enrichment at all when the event has no performers', async () => {
    const out = await enrichEvent(deps(), baseEvent({ performers: [] }));
    expect(out.enrichment).toBeUndefined();
  });

  // An unresolvable performer still gets an artist section so the attempt is
  // recorded rather than retried on every scrape.
  it('records status not_found when MusicBrainz has no match', async () => {
    const out = await enrichEvent(deps({ searchArtists: async () => [] }), baseEvent());
    expect(out.enrichment?.artist?.status).toBe('not_found');
    expect(out.enrichment?.artist?.mbid).toBeUndefined();
    expect(out.enrichment?.artist?.display_name).toBe('La Luz');
  });

  it('still emits the event when every workflow fails', async () => {
    const out = await enrichEvent(
      deps({
        enrichImage: async () => ({ status: 'error', reason: 'a' }),
        enrichBio: async () => ({ status: 'error', reason: 'b' }),
        enrichTour: async () => ({ status: 'error', reason: 'c' }),
      }),
      baseEvent(),
    );
    expect(out.title).toBe('La Luz at The Chapel');
    expect(out.enrichment?.bio?.status).toBe('error');
  });

  it('still emits the event when the prelude itself throws', async () => {
    const out = await enrichEvent(
      deps({
        searchArtists: async () => {
          throw new Error('musicbrainz 503');
        },
      }),
      baseEvent(),
    );
    expect(out.title).toBe('La Luz at The Chapel');
    expect(out.enrichment?.artist).toBeUndefined();
  });

  it('skips the image workflow when the event already has an image', async () => {
    const enrichImage = vi.fn(async () => ({ status: 'ok' as const }));
    const out = await enrichEvent(
      deps({ enrichImage }),
      baseEvent({ image_url: 'https://source/provided.jpg' }),
    );
    expect(enrichImage).not.toHaveBeenCalled();
    expect(out.image_url).toBe('https://source/provided.jpg');
  });

  // Skipping because THIS event had an image is a property of the event, not
  // the artist. Recording it would suppress the image workflow for every later
  // event by that band that has no image at all.
  it('writes no image cache entry when it skipped for that reason', async () => {
    const cache = new StubEnrichmentCache();
    await enrichEvent(deps({ cache }), baseEvent({ image_url: 'https://source/provided.jpg' }));
    const entry = await cache.read('La Luz');
    expect(entry?.workflows.image).toBeUndefined();
    expect(entry?.workflows.bio).toBeDefined();
  });

  it('reuses fresh cached payloads instead of re-running', async () => {
    const cache = new StubEnrichmentCache();
    await cache.write('La Luz', {
      artist_key: 'la luz',
      performer: 'La Luz',
      artist: { performer: 'La Luz', display_name: 'La Luz', mbid: 'mbid-1', status: 'ok' },
      workflows: {
        image: {
          status: 'ok',
          at: new Date(NOW - 1000).toISOString(),
          payload: { status: 'ok', url: 'https://cached.jpg' },
        },
        bio: {
          status: 'ok',
          at: new Date(NOW - 1000).toISOString(),
          payload: { status: 'ok', bio_md: 'cached bio' },
        },
        tour: {
          status: 'ok',
          at: new Date(NOW - 1000).toISOString(),
          payload: { status: 'ok', tour_name: 'cached tour' },
        },
      },
    });

    const searchArtists = vi.fn(async () => []);
    const enrichBio = vi.fn(async () => ({ status: 'ok' as const, bio_md: 'fresh' }));
    const out = await enrichEvent(deps({ cache, searchArtists, enrichBio }), baseEvent());

    // All three fresh AND the artist is cached, so the MusicBrainz call is
    // skipped entirely — that is the whole point of the gate.
    expect(searchArtists).not.toHaveBeenCalled();
    expect(enrichBio).not.toHaveBeenCalled();
    expect(out.enrichment?.bio?.bio_md).toBe('cached bio');
    expect(out.enrichment?.artist?.mbid).toBe('mbid-1');
  });

  it('re-runs a workflow whose cached record has expired', async () => {
    const cache = new StubEnrichmentCache();
    await cache.write('La Luz', {
      artist_key: 'la luz',
      performer: 'La Luz',
      artist: { performer: 'La Luz', display_name: 'La Luz', mbid: 'mbid-1', status: 'ok' },
      workflows: {
        // error TTL is 6h; this is 7h old
        bio: { status: 'error', at: new Date(NOW - 7 * 3600_000).toISOString() },
      },
    });
    const enrichBio = vi.fn(async () => ({ status: 'ok' as const, bio_md: 'fresh' }));
    const out = await enrichEvent(deps({ cache, enrichBio }), baseEvent());
    expect(enrichBio).toHaveBeenCalled();
    expect(out.enrichment?.bio?.bio_md).toBe('fresh');
  });

  it('writes the merged result back to the cache once', async () => {
    const cache = new StubEnrichmentCache();
    const spy = vi.spyOn(cache, 'write');
    await enrichEvent(deps({ cache }), baseEvent());
    expect(spy).toHaveBeenCalledTimes(1);
    const entry = await cache.read('La Luz');
    expect(entry?.workflows.bio?.status).toBe('ok');
    expect(entry?.workflows.tour?.payload).toBeDefined();
  });

  it('does not fail the event when the cache write fails', async () => {
    const cache = new StubEnrichmentCache();
    cache.write = async () => {
      throw new Error('s3 down');
    };
    const out = await enrichEvent(deps({ cache }), baseEvent());
    expect(out.enrichment?.bio?.status).toBe('ok');
  });

  // AccessDenied on read must propagate, but the EVENT must still flow — a
  // misconfigured cache should be loud in logs, not a silent ingest outage.
  it('still emits the event when the cache read throws', async () => {
    const cache = new StubEnrichmentCache();
    cache.read = async () => {
      throw new Error('Access Denied');
    };
    const out = await enrichEvent(deps({ cache }), baseEvent());
    expect(out.title).toBe('La Luz at The Chapel');
  });
});
