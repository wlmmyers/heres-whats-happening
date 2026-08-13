import type { S3Client } from '@aws-sdk/client-s3';
import { describe, expect, it } from 'vitest';
import {
  CACHE_TTL_MS,
  S3EnrichmentCache,
  StubEnrichmentCache,
  cacheObjectKey,
  isFresh,
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
});
