import { GetObjectCommand, PutObjectCommand, type S3Client } from '@aws-sdk/client-s3';
import { createHash } from 'node:crypto';
import { z } from 'zod';
import { artistKey } from './artist-key.js';
import {
  ArtistInfoSchema,
  BioInfoSchema,
  ImageInfoSchema,
  StatusSchema,
  TourInfoSchema,
  type ArtistInfo,
  type BioInfo,
  type ImageInfo,
  type TourInfo,
} from './enrichment-schema.js';

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

// Runtime validation for a cached object read back from S3. The Lambda wrote
// it, so it SHOULD already match CacheEntry/CacheRecord — but "should" is not
// "trusted": a schema change mid-rollout, hand-edited object, or bit-rot must
// be treated as a cache miss rather than crash the workflow that reads it (or
// worse, feed it downstream half-typed). Returning a miss also self-heals a
// poisoned entry, since the next successful run overwrites it.
const CacheRecordSchema = z.object({
  status: StatusSchema,
  at: z.string(),
  reason: z.string().optional(),
  payload: z.union([ImageInfoSchema, BioInfoSchema, TourInfoSchema]).optional(),
});

export const CacheEntrySchema = z.object({
  artist_key: z.string(),
  performer: z.string(),
  artist: ArtistInfoSchema.optional(),
  workflows: z.object({
    image: CacheRecordSchema.optional(),
    bio: CacheRecordSchema.optional(),
    tour: CacheRecordSchema.optional(),
  }),
});

/** v1/ so a schema change is a prefix bump rather than a migration. The key is
 * hashed because normalized band names are not path-safe — "ac/dc" would create
 * a nested prefix, and "Sunn O)))" and emoji names exist. The readable
 * artist_key lives inside the body so an object stays identifiable. */
export function cacheObjectKey(performer: string): string {
  const digest = createHash('sha256').update(artistKey(performer)).digest('hex');
  return `enrichment/v1/${digest}.json`;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
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
    let body: string | undefined;
    try {
      const out = await this.s3.send(
        new GetObjectCommand({ Bucket: this.bucket, Key: cacheObjectKey(performer) }),
      );
      body = await out.Body?.transformToString();
    } catch (e) {
      // A genuine miss is NoSuchKey. AccessDenied is a misconfiguration, and
      // swallowing it would silently re-run every workflow on every event
      // forever while looking exactly like "the cache isn't working".
      // See the same warning at terraform/prod/lambda_mastra_handler.tf:57-59.
      if (isNoSuchKey(e)) return null;
      throw e;
    }
    if (!body) return null;

    // A malformed cached object (corrupt JSON, or JSON that no longer matches
    // the schema) is a cache MISS, not a trusted read — this is a mis-parsed
    // provider response away from crashing the workflow that reads it, and
    // treating it as a miss self-heals the entry on the very next write.
    let raw: unknown;
    try {
      raw = JSON.parse(body);
    } catch (e) {
      console.error(
        JSON.stringify({ msg: 'enrichment-cache-corrupt-json', performer, error: message(e) }),
      );
      return null;
    }
    const parsed = CacheEntrySchema.safeParse(raw);
    if (!parsed.success) {
      console.error(
        JSON.stringify({
          msg: 'enrichment-cache-invalid-entry',
          performer,
          error: parsed.error.message,
        }),
      );
      return null;
    }
    return parsed.data;
  }

  async write(performer: string, entry: CacheEntry): Promise<void> {
    // Validate on the way IN too, not just on the way out. A deterministically
    // invalid payload (e.g. a non-integer width cast unchecked from
    // Wikimedia's thumbwidth, or a non-string extmetadata credit field in
    // enrich-image.ts) would otherwise round-trip forever: write() persists
    // it, read()'s validation (added alongside this) rejects it as a miss
    // every time, and the next run just rewrites the same bad object. The
    // cache gate silently stops working for that artist and all three
    // workflows re-run on every scrape — exactly the expensive failure the
    // cache exists to prevent. Skipping the write instead leaves the entry
    // absent, so the next run gets a clean retry rather than a permanently
    // poisoned one.
    const parsed = CacheEntrySchema.safeParse(entry);
    if (!parsed.success) {
      console.error(
        JSON.stringify({
          msg: 'enrichment-cache-invalid-write',
          performer,
          error: parsed.error.message,
        }),
      );
      return;
    }
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
