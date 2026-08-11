import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { GetObjectCommand, PutObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import type { ArtifactRef, ArtistMatch, ImageCredit } from "./mastra/tools/band-image.js";
import type { PosterRequest } from "./poster-schema.js";

/**
 * Bump when the pipeline changes in a way that should invalidate stored posters
 * (a new author prompt, a different canvas, a changed candidate strategy).
 * Without this the cache would freeze output quality permanently, since the key
 * otherwise encodes only performer/venue/date.
 */
export const POSTER_SCHEMA_VERSION = 1;

const SIGNED_URL_TTL_SECONDS = 3600;

export interface PosterProvenance {
  artist?: ArtistMatch;
  credit?: ImageCredit;
}

export interface PosterArtifacts extends PosterProvenance {
  svgUrl: string;
  pngUrl: string;
}

export interface PosterSink {
  /** Signed urls + provenance when a COMPLETE poster already exists, else null. */
  find(req: PosterRequest): Promise<PosterArtifacts | null>;
  put(req: PosterRequest, svg: ArtifactRef, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts>;
}

/** Short, stable digest of an already-normalized string. Long enough that two
 * real-world names will not collide, short enough to keep keys readable. */
function shortDigest(normalized: string): string {
  return createHash("sha256").update(normalized).digest("hex").slice(0, 10);
}

/**
 * Slug ONE key component.
 *
 * A name with no ASCII alphanumerics — 椎名林檎, Мумий Тролль, !!! — slugs to the
 * empty string, so every such act at the same venue on the same night produced the
 * SAME key. That was harmless while the key was write-only, but `find` now READS
 * it: colliding acts would be served each other's poster, photo, and the wrong
 * photographer's CC BY-SA attribution. So when a component slugs to nothing, fall
 * back to a digest of the normalized original. Applied per component (performer,
 * venue and date each go through here), and inert for ASCII input — existing keys
 * are byte-identical.
 */
function slug(s: string): string {
  const normalized = s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // strip diacritics
    .toLowerCase();
  const slugged = normalized.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  // Digest the NORMALIZED form, so the same name typed in a different Unicode
  // composition still lands on one key (and so still hits the cache).
  return slugged || shortDigest(normalized);
}

/** Deterministic, versioned S3 key prefix for a request (no extension). */
export function posterKeyBase(req: PosterRequest): string {
  return `posters/v${POSTER_SCHEMA_VERSION}/${slug(req.performer)}/${slug(req.venue)}-${slug(req.date)}`;
}

export class S3PosterSink implements PosterSink {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  private sign(base: string): Promise<[string, string]> {
    return Promise.all([
      getSignedUrl(this.s3, new GetObjectCommand({ Bucket: this.bucket, Key: `${base}.svg` }), {
        expiresIn: SIGNED_URL_TTL_SECONDS,
      }),
      getSignedUrl(this.s3, new GetObjectCommand({ Bucket: this.bucket, Key: `${base}.png` }), {
        expiresIn: SIGNED_URL_TTL_SECONDS,
      }),
    ]) as Promise<[string, string]>;
  }

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
    const [svgUrl, pngUrl] = await this.sign(base);
    return { svgUrl, pngUrl, ...provenance };
  }

  async put(
    req: PosterRequest,
    svg: ArtifactRef,
    png: ArtifactRef,
    provenance: PosterProvenance,
  ): Promise<PosterArtifacts> {
    const base = posterKeyBase(req);
    await this.putFile(`${base}.svg`, svg);
    await this.putFile(`${base}.png`, png);
    // The sidecar goes LAST. `find` keys off it, so its presence proves the
    // other two objects are complete and a half-written poster is never served.
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: `${base}.json`,
        Body: JSON.stringify(provenance),
        ContentType: "application/json",
      }),
    );
    const [svgUrl, pngUrl] = await this.sign(base);
    return { svgUrl, pngUrl, ...provenance };
  }
}

/** Test double: records puts, serves them back from find, fake urls. */
export class StubPosterSink implements PosterSink {
  public calls: Array<{ req: PosterRequest; svg: ArtifactRef; png: ArtifactRef; provenance: PosterProvenance }> = [];
  private stored = new Map<string, PosterProvenance>();

  async find(req: PosterRequest): Promise<PosterArtifacts | null> {
    const base = posterKeyBase(req);
    const provenance = this.stored.get(base);
    if (!provenance) return null;
    return { svgUrl: `https://stub.local/${base}.svg`, pngUrl: `https://stub.local/${base}.png`, ...provenance };
  }

  async put(
    req: PosterRequest,
    svg: ArtifactRef,
    png: ArtifactRef,
    provenance: PosterProvenance,
  ): Promise<PosterArtifacts> {
    this.calls.push({ req, svg, png, provenance });
    const base = posterKeyBase(req);
    this.stored.set(base, provenance);
    return { svgUrl: `https://stub.local/${base}.svg`, pngUrl: `https://stub.local/${base}.png`, ...provenance };
  }
}
