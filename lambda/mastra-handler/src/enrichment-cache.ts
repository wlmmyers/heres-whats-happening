import { GetObjectCommand, PutObjectCommand, type S3Client } from '@aws-sdk/client-s3';
import { createHash } from 'node:crypto';
import { artistKey } from './artist-key.js';
import type { ArtistInfo, BioInfo, ImageInfo, TourInfo } from './enrichment-schema.js';

export type WorkflowName = 'image' | 'bio' | 'tour';
export type CacheStatus = 'ok' | 'none' | 'error';

/** How long an attempt with a given outcome suppresses a retry. Mirrors the
 * shape of internal/scraper/spotify/genres.go:17-19 so the two artist caches
 * behave alike. */
export const CACHE_TTL_MS: Record<CacheStatus, number> = {
  ok: 90 * 24 * 3600_000,
  none: 14 * 24 * 3600_000,
  error: 6 * 3600_000,
};

export interface CacheRecord {
  status: CacheStatus;
  at: string; // ISO 8601
  reason?: string;
  /** The workflow's own result, cached so a skip still yields a COMPLETE
   * message. Without it, "skipped" and "found nothing" become
   * indistinguishable downstream. */
  payload?: ImageInfo | BioInfo | TourInfo;
}

export interface CacheEntry {
  artist_key: string;
  performer: string;
  artist?: ArtistInfo;
  workflows: Partial<Record<WorkflowName, CacheRecord>>;
}

export interface EnrichmentCache {
  read(performer: string): Promise<CacheEntry | null>;
  write(performer: string, entry: CacheEntry): Promise<void>;
}

/** v1/ so a schema change is a prefix bump rather than a migration. The key is
 * hashed because normalized band names are not path-safe — "ac/dc" would create
 * a nested prefix, and "Sunn O)))" and emoji names exist. The readable
 * artist_key lives inside the body so an object stays identifiable. */
export function cacheObjectKey(performer: string): string {
  const digest = createHash('sha256').update(artistKey(performer)).digest('hex');
  return `enrichment/v1/${digest}.json`;
}

export function isFresh(record: CacheRecord, now = Date.now()): boolean {
  const at = Date.parse(record.at);
  if (Number.isNaN(at)) return false; // unparseable -> re-run, never cache forever
  return now - at < CACHE_TTL_MS[record.status];
}

export class S3EnrichmentCache implements EnrichmentCache {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  async read(performer: string): Promise<CacheEntry | null> {
    try {
      const out = await this.s3.send(
        new GetObjectCommand({ Bucket: this.bucket, Key: cacheObjectKey(performer) }),
      );
      const body = await out.Body?.transformToString();
      if (!body) return null;
      return JSON.parse(body) as CacheEntry;
    } catch (e) {
      // A genuine miss is NoSuchKey. AccessDenied is a misconfiguration, and
      // swallowing it would silently re-run every workflow on every event
      // forever while looking exactly like "the cache isn't working".
      // See the same warning at terraform/prod/lambda_mastra_handler.tf:57-59.
      if (isNoSuchKey(e)) return null;
      throw e;
    }
  }

  async write(performer: string, entry: CacheEntry): Promise<void> {
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: cacheObjectKey(performer),
        Body: JSON.stringify(entry),
        ContentType: 'application/json',
      }),
    );
  }
}

function isNoSuchKey(e: unknown): boolean {
  // Name only, matching poster-sink.ts's find(). A status-code fallback here
  // would also swallow NoSuchBucket (also a 404) as a miss, which is the same
  // failure mode the AccessDenied rule exists to prevent, just triggered by a
  // bad bucket instead of bad IAM.
  const name = (e as { name?: string })?.name;
  return name === 'NoSuchKey' || name === 'NotFound';
}

/** In-memory cache for local dev and unit tests. Lives beside the production
 * implementation for the same reason StubPosterSink does: it is part of the
 * module's contract surface. */
export class StubEnrichmentCache implements EnrichmentCache {
  readonly entries = new Map<string, CacheEntry>();

  async read(performer: string): Promise<CacheEntry | null> {
    return this.entries.get(cacheObjectKey(performer)) ?? null;
  }

  async write(performer: string, entry: CacheEntry): Promise<void> {
    this.entries.set(cacheObjectKey(performer), entry);
  }
}
