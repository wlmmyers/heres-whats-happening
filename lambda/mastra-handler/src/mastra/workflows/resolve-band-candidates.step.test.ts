import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ArtistMatch, ImageCandidate } from '../tools/band-image.js';

const searchArtists = vi.fn<(p: string, o?: { limit?: number }) => Promise<ArtistMatch[]>>();
const resolveImageCandidates =
  vi.fn<(m: string, o?: { artistName?: string }) => Promise<ImageCandidate[]>>();

// vi.mock is hoisted, but the factory body runs lazily at import time — by which
// point the spies above are initialized. So they can be referenced directly.
vi.mock('../tools/musicbrainz.tool.js', () => ({ searchArtists }));
vi.mock('../tools/wikimedia.tool.js', () => ({ resolveImageCandidates }));

const { resolveBandCandidatesStep } = await import('./resolve-band-candidates.step.js');

const input = {
  performer: 'la luz',
  venue: 'Occidental Square',
  date: 'Thursday, August 20',
  attempts: 0,
  accepted: false,
  colors: [],
  candidates: [],
  candidateIndex: 0,
};

const rock: ArtistMatch = {
  mbid: 'mb-rock',
  name: 'La Luz',
  score: 100,
  disambiguation: 'US rock band',
};
const house: ArtistMatch = {
  mbid: 'mb-house',
  name: 'La Luz',
  score: 88,
  disambiguation: 'Belgium based house group',
};
const chill: ArtistMatch = {
  mbid: 'mb-chill',
  name: 'La Luz',
  score: 88,
  disambiguation: 'chillout music',
};

function candidate(file: string): ImageCandidate {
  return {
    file,
    url: `https://upload.wikimedia.org/${file}`,
    width: 1080,
    height: 810,
    contentType: 'image/jpeg',
    source: 'p18',
    credit: {
      file,
      descriptionUrl: `https://commons.wikimedia.org/wiki/${file}`,
      attributionRequired: false,
    },
  };
}

// The step is invoked exactly as the workflow engine invokes it.
const run = (data: typeof input) =>
  resolveBandCandidatesStep.execute({ inputData: data } as never) as Promise<any>;

beforeEach(() => {
  searchArtists.mockReset();
  resolveImageCandidates.mockReset();
});

describe('resolveBandCandidatesStep', () => {
  it('stores the top match and its candidates', async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValue([candidate('File:A.jpg')]);

    const out = await run(input);
    expect(out.artist).toEqual(rock);
    expect(out.candidates).toHaveLength(1);
    expect(out.candidateIndex).toBe(0);
    expect(resolveImageCandidates).toHaveBeenCalledWith('mb-rock', { artistName: 'La Luz' });
  });

  it('falls through to the next match when the top one yields nothing', async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([candidate('File:B.jpg')]);

    const out = await run(input);
    expect(out.artist).toEqual(house);
    expect(out.candidates).toHaveLength(1);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(2);
  });

  it('stops probing at the first match that yields candidates', async () => {
    searchArtists.mockResolvedValue([rock, house, chill]);
    resolveImageCandidates.mockResolvedValue([candidate('File:A.jpg')]);

    await run(input);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(1);
  });

  it('probes at most three matches', async () => {
    searchArtists.mockResolvedValue([rock, house, chill, { ...chill, mbid: 'mb-4' }]);
    resolveImageCandidates.mockResolvedValue([]);

    const out = await run(input);
    expect(resolveImageCandidates).toHaveBeenCalledTimes(3);
    expect(out.candidates).toEqual([]);
  });

  it('names who was tried when nothing yields an image', async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates.mockResolvedValue([]);

    const out = await run(input);
    expect(out.reason).toContain('La Luz (US rock band)');
    expect(out.reason).toContain('La Luz (Belgium based house group)');
    // The best match is still reported so the caller knows who was looked up.
    expect(out.artist).toEqual(rock);
  });

  it('reports no MusicBrainz match without throwing', async () => {
    searchArtists.mockResolvedValue([]);
    const out = await run(input);
    expect(out.candidates).toEqual([]);
    expect(out.reason).toContain('no MusicBrainz match');
    expect(out.artist).toBeUndefined();
  });

  it('converts a MusicBrainz failure into state, never throwing', async () => {
    searchArtists.mockRejectedValue(new Error('ECONNRESET'));
    const out = await run(input);
    expect(out.candidates).toEqual([]);
    expect(out.reason).toContain('musicbrainz search failed');
    expect(out.reason).toContain('ECONNRESET');
  });

  it('keeps falling through when a Wikimedia call errors, and reports the last error', async () => {
    searchArtists.mockResolvedValue([rock, house]);
    resolveImageCandidates
      .mockRejectedValueOnce(new Error('wikimedia 500'))
      .mockResolvedValueOnce([]);

    const out = await run(input);
    expect(out.candidates).toEqual([]);
    expect(out.reason).toContain('wikimedia 500');
  });
});
