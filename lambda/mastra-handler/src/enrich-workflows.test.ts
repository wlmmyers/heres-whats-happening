import { describe, expect, it } from 'vitest';
import { enrichBio } from './enrich-bio.js';
import { enrichTour } from './enrich-tour.js';
import { enrichImage } from './enrich-image.js';
import type { ImageCandidate } from './mastra/tools/band-image.js';

const artist = { mbid: 'mbid-1', name: 'La Luz', disambiguation: 'US rock band' };

describe('enrichBio', () => {
  it('returns ok with sources when everything resolves', async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => 'Q123',
        fetchExtract: async () => ({
          title: 'La Luz (band)',
          url: 'https://en.wikipedia.org/wiki/La_Luz_(band)',
          revisionId: 987,
          text: 'La Luz formed in Seattle in 2012.',
        }),
        fetchAlbums: async () => [{ title: 'Floating Features', year: '2018' }],
        writeBio: async () => ({ bio: 'La Luz formed in Seattle in 2012.', usable: true }),
        model: 'test-model',
      },
      artist,
    );

    expect(got.status).toBe('ok');
    expect(got.bio_md).toContain('Seattle');
    expect(got.sources).toEqual([
      {
        kind: 'wikipedia',
        title: 'La Luz (band)',
        url: 'https://en.wikipedia.org/wiki/La_Luz_(band)',
        revision_id: 987,
      },
      { kind: 'musicbrainz', mbid: 'mbid-1' },
    ]);
  });

  it("is 'none', not 'error', when the artist has no Wikipedia article", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => 'Q123',
        fetchExtract: async () => null,
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: '', usable: false }),
        model: 'test-model',
      },
      artist,
    );
    expect(got.status).toBe('none');
    expect(got.reason).toMatch(/no English Wikipedia article/);
  });

  it("is 'none' when the model judges the source too thin", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => 'Q1',
        fetchExtract: async () => ({ title: 'T', url: 'u', revisionId: 1, text: 'x' }),
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: '', usable: false }),
        model: 'test-model',
      },
      artist,
    );
    expect(got.status).toBe('none');
  });

  // A provider outage must not escape: the orchestrator emits the message
  // regardless, and a throw here would take the whole event down with it.
  it("returns 'error' instead of throwing when a fetch fails", async () => {
    const got = await enrichBio(
      {
        resolveQid: async () => {
          throw new Error('wikidata 503');
        },
        fetchExtract: async () => null,
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: '', usable: false }),
        model: 'test-model',
      },
      artist,
    );
    expect(got.status).toBe('error');
    expect(got.reason).toMatch(/wikidata 503/);
  });

  it("is 'none' with no MBID and spends no calls", async () => {
    let called = false;
    const got = await enrichBio(
      {
        resolveQid: async () => {
          called = true;
          return null;
        },
        fetchExtract: async () => null,
        fetchAlbums: async () => [],
        writeBio: async () => ({ bio: '', usable: false }),
        model: 'test-model',
      },
      { ...artist, mbid: '' },
    );
    expect(got.status).toBe('none');
    expect(called).toBe(false);
  });
});

describe('enrichTour', () => {
  const evt = { venue: 'The Chapel', date: '2026-09-02' };
  const setlist = {
    setlistId: 's1',
    setlistUrl: 'https://setlist.fm/x',
    tourName: 'News of the Universe Tour',
    songs: [{ name: 'Sure As Spring' }, { name: 'Cicada' }, { name: 'Strange World', encore: 1 }],
    observedDate: '2026-07-14',
    observedVenue: 'The Greek',
    observedCity: 'Berkeley',
  };

  it('returns ok with the setlist and blurb', async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => setlist,
        writeBlurb: async () => ({ blurb: 'Out on the News of the Universe Tour.', usable: true }),
        model: 'test-model',
      },
      artist,
      evt,
    );
    expect(got.status).toBe('ok');
    expect(got.tour_name).toBe('News of the Universe Tour');
    expect(got.songs).toHaveLength(3);
    expect(got.observed_date).toBe('2026-07-14');
    expect(got.blurb).toContain('News of the Universe');
  });

  // The setlist landed and is worth serving on its own.
  it('stays ok with a null blurb when the blurb call fails', async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => setlist,
        writeBlurb: async () => {
          throw new Error('529 overloaded');
        },
        model: 'test-model',
      },
      artist,
      evt,
    );
    expect(got.status).toBe('ok');
    expect(got.songs).toHaveLength(3);
    expect(got.blurb).toBeUndefined();
  });

  it("is 'none' when there is no qualifying setlist", async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => null,
        writeBlurb: async () => ({ blurb: '', usable: false }),
        model: 'm',
      },
      artist,
      evt,
    );
    expect(got.status).toBe('none');
  });

  it("is 'error' when setlist.fm rate-limits", async () => {
    const got = await enrichTour(
      {
        recentSetlist: async () => {
          throw new Error('setlistfm 429: rate limited');
        },
        writeBlurb: async () => ({ blurb: '', usable: false }),
        model: 'm',
      },
      artist,
      evt,
    );
    expect(got.status).toBe('error');
    expect(got.reason).toMatch(/429/);
  });

  it("is 'none' with no MBID and spends no request", async () => {
    let called = false;
    const got = await enrichTour(
      {
        recentSetlist: async () => {
          called = true;
          return null;
        },
        writeBlurb: async () => ({ blurb: '', usable: false }),
        model: 'm',
      },
      { ...artist, mbid: '' },
      evt,
    );
    expect(got.status).toBe('none');
    expect(called).toBe(false);
  });
});

function candidate(file: string): ImageCandidate {
  return {
    file,
    url: `https://upload.wikimedia.org/${file}`,
    width: 640,
    height: 427,
    contentType: 'image/jpeg',
    source: 'p18',
    credit: { file, descriptionUrl: 'https://commons/desc', attributionRequired: true },
  };
}

describe('enrichImage', () => {
  it('returns ok with a snake_case credit block', async () => {
    const got = await enrichImage(
      {
        candidates: async () => [candidate('a.jpg')],
        bytes: async () => Buffer.from('x'),
        judge: async () => ({ acceptable: true, reason: 'good', dominantColors: [] }),
      },
      artist,
    );
    expect(got.status).toBe('ok');
    expect(got.url).toBe('https://upload.wikimedia.org/a.jpg');
    expect(got.credit?.description_url).toBe('https://commons/desc');
    expect(got.credit?.attribution_required).toBe(true);
  });

  it('advances to the next candidate after a rejection', async () => {
    let judged = 0;
    const got = await enrichImage(
      {
        candidates: async () => [candidate('a.jpg'), candidate('b.jpg')],
        bytes: async () => Buffer.from('x'),
        judge: async () => {
          judged += 1;
          return { acceptable: judged === 2, reason: 'r', dominantColors: [] };
        },
      },
      artist,
    );
    expect(judged).toBe(2);
    expect(got.url).toContain('b.jpg');
  });

  it("is 'none' when every candidate is rejected", async () => {
    const got = await enrichImage(
      {
        candidates: async () => [candidate('a.jpg')],
        bytes: async () => Buffer.from('x'),
        judge: async () => ({ acceptable: false, reason: 'album art', dominantColors: [] }),
      },
      artist,
    );
    expect(got.status).toBe('none');
    expect(got.reason).toBe('album art');
  });

  it("is 'error' when the vision provider is down", async () => {
    const got = await enrichImage(
      {
        candidates: async () => [candidate('a.jpg')],
        bytes: async () => Buffer.from('x'),
        judge: async () => {
          throw new Error('529 overloaded');
        },
      },
      artist,
    );
    expect(got.status).toBe('error');
  });
});
