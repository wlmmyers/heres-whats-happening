import { PutObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import { GetObjectCommand } from "@aws-sdk/client-s3";
import type { PosterRequest } from "./poster-schema.js";

export interface PosterSink {
  put(req: PosterRequest, svg: string, pngBase64: string): Promise<{ svgUrl: string; pngUrl: string }>;
}

function slug(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // strip diacritics
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** Deterministic S3 key prefix for a request (no extension). */
export function posterKeyBase(req: PosterRequest): string {
  return `posters/${slug(req.performer)}/${slug(req.venue)}-${slug(req.date)}`;
}

/** Writes svg + png to S3 and returns presigned GET URLs (1 hour). */
export class S3PosterSink implements PosterSink {
  constructor(
    private readonly s3: S3Client,
    private readonly bucket: string,
  ) {}

  async put(req: PosterRequest, svg: string, pngBase64: string): Promise<{ svgUrl: string; pngUrl: string }> {
    const base = posterKeyBase(req);
    const svgKey = `${base}.svg`;
    const pngKey = `${base}.png`;
    await this.s3.send(new PutObjectCommand({ Bucket: this.bucket, Key: svgKey, Body: svg, ContentType: "image/svg+xml" }));
    await this.s3.send(new PutObjectCommand({ Bucket: this.bucket, Key: pngKey, Body: Buffer.from(pngBase64, "base64"), ContentType: "image/png" }));
    const [svgUrl, pngUrl] = await Promise.all([
      getSignedUrl(this.s3 as any, new GetObjectCommand({ Bucket: this.bucket, Key: svgKey }) as any, { expiresIn: 3600 }),
      getSignedUrl(this.s3 as any, new GetObjectCommand({ Bucket: this.bucket, Key: pngKey }) as any, { expiresIn: 3600 }),
    ]);
    return { svgUrl, pngUrl };
  }
}

/** Test double: records puts, returns deterministic fake URLs. */
export class StubPosterSink implements PosterSink {
  public calls: Array<{ req: PosterRequest; svg: string; pngBase64: string }> = [];
  async put(req: PosterRequest, svg: string, pngBase64: string): Promise<{ svgUrl: string; pngUrl: string }> {
    this.calls.push({ req, svg, pngBase64 });
    const base = posterKeyBase(req);
    return { svgUrl: `https://stub.local/${base}.svg`, pngUrl: `https://stub.local/${base}.png` };
  }
}
