import type { S3Client } from '@aws-sdk/client-s3';
import { describe, expect, it } from 'vitest';
import {
  CACHE_TTL_MS,
  S3EnrichmentCache,
  StubEnrichmentCache,
  cacheObjectKey,
  isFresh,
  type CacheEntry,
} from './enrichment-cache.js';

const NOW = Date.parse('2026-08-12T00:00:00Z');

describe('isFresh', () => {
  it('keeps an ok record for 90 days', () => {
    const at = new Date(NOW - 89 * 24 * 3600_000).toISOString();
    expect(isFresh({ status: 'ok', at }, NOW)).toBe(true);
  });

  it('expires an ok record after 90 days', () => {
    const at = new Date(NOW - 91 * 24 * 3600_000).toISOString();
    expect(isFresh({ status: 'ok', at }, NOW)).toBe(false);
  });

  it('retries an error record after 6 hours', () => {
    expect(isFresh({ status: 'error', at: new Date(NOW - 5 * 3600_000).toISOString() }, NOW)).toBe(
      true,
    );
    expect(isFresh({ status: 'error', at: new Date(NOW - 7 * 3600_000).toISOString() }, NOW)).toBe(
      false,
    );
  });

  it('retries a none record after 14 days', () => {
    expect(
      isFresh({ status: 'none', at: new Date(NOW - 13 * 24 * 3600_000).toISOString() }, NOW),
    ).toBe(true);
    expect(
      isFresh({ status: 'none', at: new Date(NOW - 15 * 24 * 3600_000).toISOString() }, NOW),
    ).toBe(false);
  });

  it('treats an unparseable timestamp as stale rather than fresh-forever', () => {
    expect(isFresh({ status: 'ok', at: 'not-a-date' }, NOW)).toBe(false);
  });

  it('is stale exactly at the TTL boundary (strict comparison)', () => {
    const at = new Date(NOW - CACHE_TTL_MS.ok).toISOString();
    expect(isFresh({ status: 'ok', at }, NOW)).toBe(false);
  });

  it('is fresh one millisecond inside the TTL boundary', () => {
    const at = new Date(NOW - CACHE_TTL_MS.ok + 1).toISOString();
    expect(isFresh({ status: 'ok', at }, NOW)).toBe(true);
  });

  it('has the TTLs the spec fixed', () => {
    expect(CACHE_TTL_MS.ok).toBe(90 * 24 * 3600_000);
    expect(CACHE_TTL_MS.none).toBe(14 * 24 * 3600_000);
    expect(CACHE_TTL_MS.error).toBe(6 * 3600_000);
  });
});

describe('cacheObjectKey', () => {
  it('hashes the artist key so punctuation never reaches the path', () => {
    const key = cacheObjectKey('AC/DC');
    expect(key).toMatch(/^enrichment\/v1\/[0-9a-f]{64}\.json$/);
    expect(key).not.toContain('ac/dc');
  });

  it('is stable and case-insensitive via artistKey', () => {
    expect(cacheObjectKey('La Luz')).toBe(cacheObjectKey('  la luz  '));
  });
});

describe('StubEnrichmentCache', () => {
  it('round-trips an entry', async () => {
    const cache = new StubEnrichmentCache();
    await cache.write('La Luz', {
      artist_key: 'la luz',
      performer: 'La Luz',
      workflows: { bio: { status: 'none', at: new Date(NOW).toISOString() } },
    });
    const got = await cache.read('La Luz');
    expect(got?.workflows.bio?.status).toBe('none');
  });

  it('returns null on a miss', async () => {
    expect(await new StubEnrichmentCache().read('Nobody')).toBeNull();
  });
});

describe('S3EnrichmentCache', () => {
  it('throws on AccessDenied rather than reporting a miss', async () => {
    const s3 = {
      send: async () => {
        const e = new Error('Access Denied') as Error & {
          name: string;
          $metadata: { httpStatusCode: number };
        };
        e.name = 'AccessDenied';
        e.$metadata = { httpStatusCode: 403 };
        throw e;
      },
    } as unknown as S3Client;

    await expect(new S3EnrichmentCache(s3, 'b').read('La Luz')).rejects.toThrow(/Access Denied/);
  });

  it('reports NoSuchKey as a miss', async () => {
    const s3 = {
      send: async () => {
        const e = new Error('no key') as Error & { name: string };
        e.name = 'NoSuchKey';
        throw e;
      },
    } as unknown as S3Client;

    expect(await new S3EnrichmentCache(s3, 'b').read('La Luz')).toBeNull();
  });

  it('throws on NoSuchBucket rather than reporting a miss, even though it is also a 404', async () => {
    const s3 = {
      send: async () => {
        const e = new Error('The specified bucket does not exist') as Error & {
          name: string;
          $metadata: { httpStatusCode: number };
        };
        e.name = 'NoSuchBucket';
        e.$metadata = { httpStatusCode: 404 };
        throw e;
      },
    } as unknown as S3Client;

    await expect(new S3EnrichmentCache(s3, 'b').read('La Luz')).rejects.toThrow(
      /specified bucket does not exist/,
    );
  });

  // A malformed cached object must be treated as a miss rather than trusted:
  // this self-heals a poisoned entry on the next successful write, instead of
  // crashing the workflow that reads it (or feeding it downstream half-typed).
  function s3ReturningBody(body: string): S3Client {
    return {
      send: async () => ({
        Body: { transformToString: async () => body },
      }),
    } as unknown as S3Client;
  }

  it('reports a miss when the cached object is not valid JSON', async () => {
    const s3 = s3ReturningBody('{not json');
    expect(await new S3EnrichmentCache(s3, 'b').read('La Luz')).toBeNull();
  });

  it('reports a miss when the cached object does not match the schema', async () => {
    const s3 = s3ReturningBody(JSON.stringify({ artist_key: 'la luz' })); // missing performer/workflows
    expect(await new S3EnrichmentCache(s3, 'b').read('La Luz')).toBeNull();
  });

  it('reports a miss when a workflow record has a wrong-typed field', async () => {
    const s3 = s3ReturningBody(
      JSON.stringify({
        artist_key: 'la luz',
        performer: 'La Luz',
        workflows: { bio: { status: 'ok', at: 123 } }, // at must be a string
      }),
    );
    expect(await new S3EnrichmentCache(s3, 'b').read('La Luz')).toBeNull();
  });

  it('accepts a well-formed cached entry', async () => {
    const entry = {
      artist_key: 'la luz',
      performer: 'La Luz',
      artist: { performer: 'La Luz', display_name: 'La Luz', status: 'ok' as const },
      workflows: {
        bio: {
          status: 'ok' as const,
          at: '2026-08-12T00:00:00Z',
          payload: { status: 'ok' as const, bio_md: 'x' },
        },
      },
    };
    const s3 = s3ReturningBody(JSON.stringify(entry));
    const got = await new S3EnrichmentCache(s3, 'b').read('La Luz');
    expect(got?.workflows.bio?.payload).toEqual({ status: 'ok', bio_md: 'x' });
  });

  // write() must validate too, not just read(). Otherwise a deterministically
  // invalid payload (e.g. a non-integer width cast unchecked from Wikimedia's
  // thumbwidth) round-trips forever: write() persists it, read()'s validation
  // rejects it as a miss every time, and the next run just rewrites the same
  // bad object — the cache gate silently stops working for that artist.
  function spyingS3(): { s3: S3Client; calls: unknown[] } {
    const calls: unknown[] = [];
    const s3 = {
      send: async (cmd: unknown) => {
        calls.push(cmd);
        return {};
      },
    } as unknown as S3Client;
    return { s3, calls };
  }

  it('skips the write and logs when the entry fails schema validation', async () => {
    const { s3, calls } = spyingS3();
    const bad = {
      artist_key: 'la luz',
      performer: 'La Luz',
      workflows: {
        image: {
          status: 'ok',
          at: '2026-08-12T00:00:00Z',
          payload: { status: 'ok', width: 'not-a-number' },
        },
      },
    } as unknown as CacheEntry;

    await new S3EnrichmentCache(s3, 'b').write('La Luz', bad);

    expect(calls).toHaveLength(0); // no PutObjectCommand was ever sent
  });

  it('writes normally when the entry is well-formed', async () => {
    const { s3, calls } = spyingS3();
    const good = {
      artist_key: 'la luz',
      performer: 'La Luz',
      workflows: {
        bio: {
          status: 'ok' as const,
          at: '2026-08-12T00:00:00Z',
          payload: { status: 'ok' as const, bio_md: 'x' },
        },
      },
    };

    await new S3EnrichmentCache(s3, 'b').write('La Luz', good);

    expect(calls).toHaveLength(1);
  });
});
