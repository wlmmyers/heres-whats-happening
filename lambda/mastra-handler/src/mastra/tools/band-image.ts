import { z } from 'zod';

// Identifies the app + a contact. MusicBrainz rejects requests without a
// User-Agent and Wikimedia's UA policy requires the same. Kept identical to the
// string the Go side registers (internal/scraper/spotify/genres.go).
export const USER_AGENT = 'heres-whats-happening/1.0 ( wlmmyers@gmail.com )';

// Attribution data for one Wikimedia Commons file. Every field except the
// identifiers is optional — public-domain files legitimately carry no author or
// licence, and dropping such a candidate would be worse than crediting nothing.
export const ImageCreditSchema = z.object({
  file: z.string(),
  descriptionUrl: z.string(),
  artist: z.string().optional(),
  credit: z.string().optional(),
  license: z.string().optional(),
  licenseShortName: z.string().optional(),
  licenseUrl: z.string().optional(),
  usageTerms: z.string().optional(),
  attributionRequired: z.boolean().default(false),
});
export type ImageCredit = z.infer<typeof ImageCreditSchema>;

// A file on local disk. `bytes` is the SIZE, not the content — it feeds S3's
// ContentLength so artifacts can stream from disk without being buffered.
export const ArtifactRefSchema = z.object({
  path: z.string(),
  contentType: z.string(),
  bytes: z.number(),
});
export type ArtifactRef = z.infer<typeof ArtifactRefSchema>;

// The band photo, as a reference rather than 335 KB of base64. Replaces the
// former BandImageSchema; every field except imageBase64 -> path/bytes is the same.
export const ImageRefSchema = ArtifactRefSchema.extend({
  width: z.number(),
  height: z.number(),
  sourceUrl: z.string().optional(),
  credit: ImageCreditSchema.optional(),
});
export type ImageRef = z.infer<typeof ImageRefSchema>;

// A resolved image the loop MAY judge. Metadata only — no bytes. width/height
// are the THUMBNAIL dimensions, since the thumbnail URL is what gets fetched.
export const ImageCandidateSchema = z.object({
  file: z.string(),
  url: z.string(),
  width: z.number(),
  height: z.number(),
  contentType: z.string(),
  source: z.enum(['p18', 'category']),
  credit: ImageCreditSchema,
});
export type ImageCandidate = z.infer<typeof ImageCandidateSchema>;

// One MusicBrainz artist search hit, carrying the disambiguation fields that
// distinguish "La Luz, US rock band" from "La Luz, Belgium based house group".
export const ArtistMatchSchema = z.object({
  mbid: z.string(),
  name: z.string(),
  score: z.number(),
  disambiguation: z.string().optional(),
  type: z.string().optional(),
  country: z.string().optional(),
  beginYear: z.string().optional(),
});
export type ArtistMatch = z.infer<typeof ArtistMatchSchema>;
