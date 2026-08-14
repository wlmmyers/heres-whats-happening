import { describe, expect, it, vi } from 'vitest';
import { enrichTour, type TourDeps } from './enrich-tour.js';
import type { ArtistRef } from './enrich-bio.js';
import type { RecentSetlist } from './mastra/tools/setlistfm.tool.js';

const artist: ArtistRef = { mbid: 'mbid-1', name: 'La Luz' };
const event = { venue: 'The Chapel', date: '2026-09-02' };

const setlist: RecentSetlist = {
  setlistId: 's1',
  setlistUrl: 'https://www.setlist.fm/setlist/s1.html',
  songs: [{ name: 'Sure As Spring' }],
  observedDate: '2026-07-14',
};

function deps(over: Partial<TourDeps> = {}): TourDeps {
  return {
    recentSetlist: async () => setlist,
    writeBlurb: async () => ({ blurb: '', usable: false }),
    model: 'anthropic/claude-sonnet-4-5',
    ...over,
  };
}

describe('enrichTour', () => {
  it('spends no request when the artist has no MBID', async () => {
    const recentSetlist = vi.fn(async () => setlist);
    const out = await enrichTour(deps({ recentSetlist }), { ...artist, mbid: '' }, event);
    expect(recentSetlist).not.toHaveBeenCalled();
    expect(out.status).toBe('none');
  });

  // The guard this fix adds: an unseeded setlist.fm key must not burn a
  // request on a guaranteed 403. createSetlistFmClient accepts an empty
  // apiKey without complaint, so this is enforced here, before any network
  // call, not left to the client.
  it('spends no request and reports none when the setlist.fm key is not configured', async () => {
    const recentSetlist = vi.fn(async () => setlist);
    const out = await enrichTour(deps({ recentSetlist, apiKeyConfigured: false }), artist, event);
    expect(recentSetlist).not.toHaveBeenCalled();
    expect(out).toEqual({ status: 'none', reason: 'setlist.fm key not configured' });
  });

  it('proceeds normally when apiKeyConfigured is omitted (defaults to configured)', async () => {
    const recentSetlist = vi.fn(async () => setlist);
    const out = await enrichTour(deps({ recentSetlist }), artist, event);
    expect(recentSetlist).toHaveBeenCalledWith('mbid-1');
    expect(out.status).toBe('ok');
  });

  it('proceeds normally when apiKeyConfigured is explicitly true', async () => {
    const recentSetlist = vi.fn(async () => setlist);
    const out = await enrichTour(deps({ recentSetlist, apiKeyConfigured: true }), artist, event);
    expect(recentSetlist).toHaveBeenCalledWith('mbid-1');
    expect(out.status).toBe('ok');
  });

  it('reports none when there is no qualifying setlist', async () => {
    const out = await enrichTour(deps({ recentSetlist: async () => null }), artist, event);
    expect(out.status).toBe('none');
  });

  it('reports error when recentSetlist throws', async () => {
    const out = await enrichTour(
      deps({
        recentSetlist: async () => {
          throw new Error('setlistfm 503');
        },
      }),
      artist,
      event,
    );
    expect(out).toEqual({ status: 'error', reason: 'setlistfm 503' });
  });

  it('attaches a usable blurb', async () => {
    const out = await enrichTour(
      deps({ writeBlurb: async () => ({ blurb: ' Great show. ', usable: true }) }),
      artist,
      event,
    );
    expect(out.blurb).toBe('Great show.');
    expect(out.blurb_model).toBe('anthropic/claude-sonnet-4-5');
  });

  it('keeps status ok with no blurb when the blurb call throws', async () => {
    const out = await enrichTour(
      deps({
        writeBlurb: async () => {
          throw new Error('llm down');
        },
      }),
      artist,
      event,
    );
    expect(out.status).toBe('ok');
    expect(out.blurb).toBeUndefined();
  });
});
