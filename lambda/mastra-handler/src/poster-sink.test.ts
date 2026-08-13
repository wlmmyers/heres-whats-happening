import { describe, expect, it } from 'vitest';
import { posterKeyBase, StubPosterSink } from './poster-sink.js';

const UID = '550e8400-e29b-41d4-a716-446655440000';
const OTHER_UID = '6ba7b810-9dad-11d1-80b4-00c04fd430c8';
const req = {
  userId: UID,
  performer: 'Khruangbin',
  venue: 'The Fillmore',
  date: '2026-08-15',
  force: false,
};

describe('posterKeyBase', () => {
  it('builds a user-scoped, slugged, versioned, prefixed key', () => {
    // The trailing digest carries identity; the slugs are there to be readable.
    expect(posterKeyBase(req)).toMatch(
      new RegExp(`^posters/v2/u-${UID}/khruangbin/the-fillmore-2026-08-15-[0-9a-f]{10}$`),
    );
  });

  it('slugs spaces and punctuation', () => {
    expect(
      posterKeyBase({ ...req, performer: 'Sigur Rós!', venue: '9:30 Club', date: '2026-09-01' }),
    ).toMatch(/\/sigur-ros\/9-30-club-2026-09-01-[0-9a-f]{10}$/);
  });

  it('is deterministic, so the cache can still hit', () => {
    expect(posterKeyBase(req)).toBe(posterKeyBase({ ...req }));
  });

  it('ignores force, so a re-roll overwrites rather than forking a copy', () => {
    expect(posterKeyBase({ ...req, force: true })).toBe(posterKeyBase({ ...req, force: false }));
  });
});

/**
 * The DB row is scoped per user, but the S3 object it points at was not. Since
 * `force: true` skips the cache read and always re-puts, a shared key let any
 * confirmed user overwrite any other user's poster — and then the victim's own
 * poll would presign that key and serve the attacker's image.
 */
describe('posterKeyBase user scoping', () => {
  it('gives two users DIFFERENT keys for the same show', () => {
    expect(posterKeyBase(req)).not.toBe(posterKeyBase({ ...req, userId: OTHER_UID }));
  });

  it("puts the user segment above the performer, so one user's tree is one prefix", () => {
    expect(posterKeyBase(req).startsWith(`posters/v2/u-${UID}/`)).toBe(true);
  });
});

/**
 * `[^a-z0-9]+` deletes every non-Latin character, so the readable slugs alone are
 * many-to-one: "La Luz", "La Luz Мумий" and "La Luz 椎名林檎" all slug to "la-luz".
 * `find` READS these keys, so a collision hands one act another act's poster,
 * photo, and the wrong photographer's CC BY-SA attribution.
 */
describe('posterKeyBase collision safety', () => {
  const atNeumos = (performer: string) =>
    posterKeyBase({ ...req, performer, venue: 'Neumos', date: '2026-08-15' });

  it('distinguishes names that differ ONLY in non-Latin text', () => {
    const keys = ['La Luz', 'La Luz Мумий', 'La Luz 椎名林檎', 'La Luz!!!'].map(atNeumos);
    expect(new Set(keys).size).toBe(4);
    // ...while all four still slug to the same readable segment, which is the
    // reason the digest has to exist.
    for (const k of keys) expect(k).toContain('/la-luz/');
  });

  it('gives distinct all-non-ASCII performers distinct keys', () => {
    expect(new Set(['椎名林檎', 'Мумий Тролль', '!!!'].map(atNeumos)).size).toBe(3);
  });

  it('never emits an empty path component', () => {
    for (const key of ['椎名林檎', 'Мумий Тролль', '!!!'].map(atNeumos)) {
      expect(key).not.toContain('//');
      expect(key.startsWith('posters/v2/')).toBe(true);
    }
  });

  it('distinguishes the venue too, not just the performer', () => {
    const one = posterKeyBase({ ...req, performer: 'Khruangbin', venue: '東京ドーム' });
    const two = posterKeyBase({ ...req, performer: 'Khruangbin', venue: '武道館' });
    expect(one).not.toBe(two);
    expect(one).toContain('/khruangbin/');
    // A blank venue component would leave the key starting at the date separator.
    expect(one).not.toContain('khruangbin/-2026');
  });

  // Length-prefixed hashing. A plain join would let these two hash alike, which
  // is the same ambiguity poster.JobID had to fix on the Go side.
  it('does not confuse field boundaries', () => {
    const a = posterKeyBase({ ...req, performer: 'ab', venue: 'c', date: '2026-08-15' });
    const b = posterKeyBase({ ...req, performer: 'a', venue: 'bc', date: '2026-08-15' });
    expect(a).not.toBe(b);
  });

  it('still folds Unicode composition differences onto ONE key, so the cache hits', () => {
    // "Sigur Rós" with a precomposed ó vs. o + combining acute.
    expect(atNeumos('Sigur Rós')).toBe(atNeumos('Sigur Rós'));
  });
});

describe('StubPosterSink', () => {
  const pngRef = { path: '/tmp/p.png', contentType: 'image/png', bytes: 20 };
  const provenance = { artist: { mbid: 'm', name: 'K', score: 100 } };

  it('records the put and returns canned keys plus provenance', async () => {
    const sink = new StubPosterSink();
    const out = await sink.put(req, pngRef, provenance);
    expect(out.pngKey).toBe(`${posterKeyBase(req)}.png`);
    expect('svgKey' in out).toBe(false);
    expect(out.artist).toEqual(provenance.artist);
    expect(sink.calls).toHaveLength(1);
    expect(sink.calls[0].png).toEqual(pngRef);
  });

  it('find misses until something has been put', async () => {
    const sink = new StubPosterSink();
    expect(await sink.find(req)).toBeNull();
    await sink.put(req, pngRef, provenance);
    const hit = await sink.find(req);
    expect(hit?.pngKey).toBe(`${posterKeyBase(req)}.png`);
    expect('svgKey' in hit!).toBe(false);
    expect(hit?.artist).toEqual(provenance.artist);
  });

  it("does not serve one user's poster to another", async () => {
    const sink = new StubPosterSink();
    await sink.put(req, pngRef, provenance);
    expect(await sink.find({ ...req, userId: OTHER_UID })).toBeNull();
  });
});
