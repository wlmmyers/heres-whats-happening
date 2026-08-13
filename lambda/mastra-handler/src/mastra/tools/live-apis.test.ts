import { describe, expect, it } from 'vitest';
import { createMusicBrainzClient } from './musicbrainz.tool.js';
import { createSetlistFmClient } from './setlistfm.tool.js';
import { createWikimediaClient } from './wikimedia.tool.js';

// Opt in with: LIVE_API_TESTS=1 pnpm vitest run src/mastra/tools/live-apis.test.ts
// Kept out of CI so the build never depends on external APIs or spends
// MusicBrainz rate-limit budget.
const live = process.env.LIVE_API_TESTS === '1' ? describe : describe.skip;

live('live MusicBrainz + Wikimedia', () => {
  it("resolves 'la luz' to the US rock band and its Commons photos", async () => {
    const mb = createMusicBrainzClient();
    const matches = await mb.searchArtists('la luz', { limit: 3 });
    expect(matches[0].mbid).toBe('9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a');
    expect(matches[0].disambiguation).toBe('US rock band');

    const wm = createWikimediaClient();
    const candidates = await wm.resolveImageCandidates(matches[0].mbid, {
      artistName: matches[0].name,
    });
    expect(candidates.length).toBeGreaterThanOrEqual(2);
    expect(candidates[0].source).toBe('p18');
    expect(candidates[0].width).toBe(1080);
    expect(candidates[0].credit.licenseShortName).toBeTruthy();

    const bytes = await wm.fetchImageBytes(candidates[0]);
    expect(bytes.subarray(0, 2).toString('hex')).toBe('ffd8'); // JPEG SOI
  }, 60_000);
});

live('live setlist.fm', () => {
  it('returns a recent setlist for a touring band', async () => {
    const apiKey = process.env.SETLISTFM_API_KEY;
    if (!apiKey) throw new Error('set SETLISTFM_API_KEY to run this');
    const client = createSetlistFmClient({ apiKey });
    const got = await client.recentSetlist('9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a');
    // May legitimately be null if the band has not toured in 180 days; assert
    // the SHAPE when present rather than requiring data we do not control.
    if (got) {
      expect(got.observedDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
      expect(got.songs.length).toBeGreaterThan(0);
    }
  }, 30_000);
});
