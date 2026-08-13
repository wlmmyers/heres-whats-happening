import { z } from 'zod';
import type { ImageCredit } from './mastra/tools/band-image.js';
import { EventMessageSchema } from './schema.js';

// Wire shape — MUST match Go internal/events/enriched.go JSON tags exactly.
// snake_case throughout, including nested credit fields.

const StatusSchema = z.enum(['ok', 'none', 'error']);

export const WireCreditSchema = z
  .object({
    file: z.string().optional(),
    description_url: z.string().optional(),
    artist: z.string().optional(),
    credit: z.string().optional(),
    license: z.string().optional(),
    license_short_name: z.string().optional(),
    license_url: z.string().optional(),
    usage_terms: z.string().optional(),
    attribution_required: z.boolean().default(false),
  })
  .strict();

export const ArtistInfoSchema = z
  .object({
    performer: z.string(), // RAW name; the consumer normalizes it
    display_name: z.string(),
    mbid: z.string().optional(),
    disambiguation: z.string().optional(),
    type: z.string().optional(),
    country: z.string().optional(),
    begin_year: z.string().optional(),
    status: z.enum(['ok', 'not_found']),
  })
  .strict();

export const ImageInfoSchema = z
  .object({
    status: StatusSchema,
    url: z.string().optional(),
    width: z.number().int().optional(),
    height: z.number().int().optional(),
    file: z.string().optional(),
    source: z.enum(['p18', 'category']).optional(),
    credit: WireCreditSchema.optional(),
    reason: z.string().optional(),
  })
  .strict();

export const BioSourceSchema = z
  .object({
    kind: z.enum(['wikipedia', 'musicbrainz']),
    title: z.string().optional(),
    url: z.string().optional(),
    revision_id: z.number().int().optional(),
    mbid: z.string().optional(),
  })
  .strict();

export const BioInfoSchema = z
  .object({
    status: StatusSchema,
    bio_md: z.string().optional(),
    sources: z.array(BioSourceSchema).optional(),
    model: z.string().optional(),
    reason: z.string().optional(),
  })
  .strict();

export const SetlistSongSchema = z
  .object({
    name: z.string(),
    encore: z.number().int().optional(),
    cover_of: z.string().optional(),
    tape: z.boolean().optional(),
    info: z.string().optional(),
  })
  .strict();

export const TourInfoSchema = z
  .object({
    status: StatusSchema,
    tour_name: z.string().optional(),
    songs: z.array(SetlistSongSchema).optional(),
    // Plain calendar date. Go parses this with layout "2006-01-02" into a DATE
    // column; anything else errors the consumer.
    observed_date: z
      .string()
      .regex(/^\d{4}-\d{2}-\d{2}$/)
      .optional(),
    observed_venue: z.string().optional(),
    observed_city: z.string().optional(),
    setlist_url: z.string().optional(),
    blurb: z.string().optional(),
    blurb_model: z.string().optional(),
    reason: z.string().optional(),
  })
  .strict();

export const EnrichmentSchema = z
  .object({
    artist: ArtistInfoSchema.optional(),
    image: ImageInfoSchema.optional(),
    bio: BioInfoSchema.optional(),
    tour: TourInfoSchema.optional(),
    attempted_at: z.string().datetime({ offset: true }),
  })
  .strict();

export const EnrichedMessageSchema = EventMessageSchema.extend({
  enrichment: EnrichmentSchema.optional(),
});

export type WireCredit = z.infer<typeof WireCreditSchema>;
export type ArtistInfo = z.infer<typeof ArtistInfoSchema>;
export type ImageInfo = z.infer<typeof ImageInfoSchema>;
export type BioInfo = z.infer<typeof BioInfoSchema>;
export type TourInfo = z.infer<typeof TourInfoSchema>;
export type Enrichment = z.infer<typeof EnrichmentSchema>;
export type EnrichedMessage = z.infer<typeof EnrichedMessageSchema>;

/** Map the Lambda's internal camelCase ImageCredit onto the snake_case wire
 * shape. Explicit rather than a generic case converter: a silent rename miss
 * here drops attribution that a CC-BY licence requires. */
export function toWireCredit(c: ImageCredit): WireCredit {
  return {
    file: c.file,
    description_url: c.descriptionUrl,
    artist: c.artist,
    credit: c.credit,
    license: c.license,
    license_short_name: c.licenseShortName,
    license_url: c.licenseUrl,
    usage_terms: c.usageTerms,
    attribution_required: c.attributionRequired,
  };
}
