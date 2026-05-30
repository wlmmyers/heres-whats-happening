import { z } from "zod";

// Wire shape — MUST match Go internal/events.Message JSON tags exactly.
// Fields tagged `omitempty` in Go are `.optional()` here.
export const VenueSchema = z.object({
  name: z.string(),
  address: z.string().optional(),
  lat: z.number().optional(),
  lng: z.number().optional(),
  website_url: z.string().optional(),
});

export const EventMessageSchema = z.object({
  source_id: z.string(),
  source_event_id: z.string(),
  title: z.string(),
  description: z.string().optional(),
  starts_at: z.string(), // RFC3339, e.g. "2026-06-15T20:00:00Z"
  ends_at: z.string().optional(),
  venue: VenueSchema,
  performers: z.array(z.string()).optional(),
  genres: z.array(z.string()).optional(),
  image_url: z.string().optional(),
  url: z.string().optional(),
});
export type EventMessage = z.infer<typeof EventMessageSchema>;

// LLM output shape — what the Mastra agent returns per event. Distinct from the
// wire shape: no source_id/source_event_id (computed downstream), camelCase,
// performers headliner-first.
export const EventDraftSchema = z.object({
  title: z.string(),
  description: z.string().optional(),
  startsAt: z.string(), // ISO 8601 with timezone offset or Z
  endsAt: z.string().optional(),
  venue: z.object({
    name: z.string(),
    address: z.string().optional(),
    websiteUrl: z.string().optional(),
  }),
  performers: z.array(z.string()).default([]), // headliner first
  genres: z.array(z.string()).default([]),
  url: z.string().optional(),
});
export type EventDraft = z.infer<typeof EventDraftSchema>;

export const EventDraftsSchema = z.object({ events: z.array(EventDraftSchema) });
