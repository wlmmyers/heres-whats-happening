import { z } from "zod";

// Identifies the app + a contact. MusicBrainz rejects requests without a
// User-Agent and Wikimedia's UA policy requires the same. Kept identical to the
// string the Go side registers (internal/scraper/spotify/genres.go).
export const USER_AGENT = "heres-whats-happening/1.0 ( wlmmyers@gmail.com )";

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

// The bytes actually embedded in the poster SVG. Shape is unchanged from the
// former web-scrape.tool.ts except for the added optional `credit`.
export const BandImageSchema = z.object({
  imageBase64: z.string(),
  contentType: z.string(),
  width: z.number(),
  height: z.number(),
  sourceUrl: z.string().optional(),
  credit: ImageCreditSchema.optional(),
});
export type BandImage = z.infer<typeof BandImageSchema>;

// A resolved image the loop MAY judge. Metadata only — no bytes. width/height
// are the THUMBNAIL dimensions, since the thumbnail URL is what gets fetched.
export const ImageCandidateSchema = z.object({
  file: z.string(),
  url: z.string(),
  width: z.number(),
  height: z.number(),
  contentType: z.string(),
  source: z.enum(["p18", "category"]),
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
