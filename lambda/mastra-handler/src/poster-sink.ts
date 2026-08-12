import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { GetObjectCommand, PutObjectCommand, S3Client } from "@aws-sdk/client-s3";
import type { ArtifactRef, ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";
import type { PosterRequest } from "./poster-schema.js";

/**
 * Bump when the pipeline changes in a way that should invalidate stored posters
 * (a new author prompt, a different canvas, a changed candidate strategy).
 * Without this the cache would freeze output quality permanently, since the key
 * otherwise encodes only performer/venue/date.
 */
export const POSTER_SCHEMA_VERSION = 2;

export interface PosterProvenance {
  artist?: ArtistMatch;
  credit?: ImageCredit;
}

export interface PosterArtifacts extends PosterProvenance {
  /** S3 object key. The API service presigns this at read time — storing a
   *  presigned URL anywhere would serve a dead link after its 3600s expiry. */
  pngKey: string;
}

export interface PosterSink {
  /** Object keys + provenance when a COMPLETE poster already exists, else null. */
  find(req: PosterRequest): Promise<PosterArtifacts | null>;
  put(req: PosterRequest, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts>;
}

/** Short, stable digest of an already-normalized string. Long enough that two
 * real-world names will not collide, short enough to keep keys readable. */
function shortDigest(normalized: string): string {
  return createHash("sha256").update(normalized).digest("hex").slice(0, 10);
}

/** Fold to a comparable form: composition-insensitive, diacritic-free, lowercase. */
function normalize(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // strip diacritics
    .toLowerCase();
}

/**
 * Slug ONE key component into something readable.
 *
 * A name with no ASCII alphanumerics — 椎名林檎, Мумий Тролль, !!! — slugs to the
 * empty string, so it falls back to a digest rather than leaving an empty path
 * segment. This is the READABLE half of the key only; identity is carried by the
 * digest in posterKeyBase, so this function is deliberately allowed to be
 * many-to-one.
 */
function slug(s: string): string {
  const normalized = normalize(s);
  const slugged = normalized.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return slugged || shortDigest(normalized);
}

/**
 * Deterministic, versioned S3 key prefix for a request (no extension).
 *
 * Two things beyond the readable slugs make this key safe to both read and write:
 *
 * 1. **It is scoped by user.** The DB row was always per-user, but the object it
 *    pointed at was global, so `force: true` — which skips the cache read and
 *    always re-puts — let any confirmed user overwrite any other user's poster.
 *    userId is a UUID by the time it arrives (the zod schema enforces it), so it
 *    needs no slugging and cannot introduce a "/" or "..".
 *
 * 2. **It ends in a digest of all three normalized fields.** `[^a-z0-9]+` deletes
 *    every non-Latin character, so the readable slugs alone are many-to-one:
 *    "La Luz", "La Luz Мумий" and "La Luz 椎名林檎" all slug to "la-luz". Since
 *    `find` READS these keys, a collision serves one act another act's poster,
 *    photo, and the wrong photographer's CC BY-SA attribution. The digest makes
 *    the mapping injective, so any text that changes the poster changes the key.
 *
 * Fields are length-prefixed before hashing. Joining on a separator would let
 * ("ab","c") and ("a","bc") hash alike — the same ambiguity that had to be fixed
 * in poster.JobID on the Go side.
 */
export function posterKeyBase(req: PosterRequest): string {
  const fields = [req.performer, req.venue, req.date].map(normalize);
  const digest = shortDigest(fields.map((f) => `${f.length}:${f}`).join(""));
  return [
    `posters/v${POSTER_SCHEMA_VERSION}`,
    `u-${req.userId}`,
    slug(req.performer),
    `${slug(req.venue)}-${slug(req.date)}-${digest}`,
  ].join("/");
}

export class S3PosterSink implements PosterSink {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  /** Stream from disk. ContentLength is what lets a stream body avoid buffering. */
  private async putFile(key: string, ref: ArtifactRef): Promise<void> {
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: key,
        Body: createReadStream(ref.path),
        ContentLength: ref.bytes,
        ContentType: ref.contentType,
      }),
    );
  }

  async find(req: PosterRequest): Promise<PosterArtifacts | null> {
    const base = posterKeyBase(req);
    let provenance: PosterProvenance;
    try {
      const res = await this.s3.send(new GetObjectCommand({ Bucket: this.bucket, Key: `${base}.json` }));
      const body = await res.Body?.transformToString();
      if (!body) return null;
      provenance = JSON.parse(body) as PosterProvenance;
    } catch (e) {
      // Absent sidecar == no complete poster. Anything else is a real problem
      // the caller should see (it decides whether to degrade to a miss).
      const name = (e as { name?: string })?.name;
      if (name === "NoSuchKey" || name === "NotFound") return null;
      throw e;
    }
    return { pngKey: `${base}.png`, ...provenance };
  }

  async put(req: PosterRequest, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts> {
    const base = posterKeyBase(req);
    await this.putFile(`${base}.png`, png);
    // The sidecar goes LAST. `find` keys off it, so its presence proves the png
    // is complete and a half-written poster is never served.
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: `${base}.json`,
        Body: JSON.stringify(provenance),
        ContentType: "application/json",
      }),
    );
    return { pngKey: `${base}.png`, ...provenance };
  }
}

/** Test double: records puts, serves them back from find, fake keys. */
export class StubPosterSink implements PosterSink {
  public calls: Array<{ req: PosterRequest; png: ArtifactRef; provenance: PosterProvenance }> = [];
  private stored = new Map<string, PosterProvenance>();

  async find(req: PosterRequest): Promise<PosterArtifacts | null> {
    const base = posterKeyBase(req);
    const provenance = this.stored.get(base);
    if (!provenance) return null;
    return { pngKey: `${base}.png`, ...provenance };
  }

  async put(req: PosterRequest, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts> {
    this.calls.push({ req, png, provenance });
    const base = posterKeyBase(req);
    this.stored.set(base, provenance);
    return { pngKey: `${base}.png`, ...provenance };
  }
}
