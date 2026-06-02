import { z } from "zod";

// Wire shape — MUST match Go internal/events.Message JSON tags exactly.
// Fields tagged `omitempty` in Go are `.optional()` here.
export const VenueSchema = z.object({
  name: z.string(),
  address: z.string().optional(),
  lat: z.number().optional(),
  lng: z.number().optional(),
  website_url: z.string().optional(),
}).strict();

export const EventMessageSchema = z.object({
  source_id: z.string(),
  source_event_id: z.string(),
  title: z.string(),
  description: z.string().optional(),
  starts_at: z.string().datetime({ offset: true }), // RFC3339, e.g. "2026-06-15T20:00:00Z"
  ends_at: z.string().datetime({ offset: true }).optional(),
  venue: VenueSchema,
  performers: z.array(z.string()).optional(),
  genres: z.array(z.string()).optional(),
  image_url: z.string().optional(),
  url: z.string().optional(),
}).strict();
export type EventMessage = z.infer<typeof EventMessageSchema>;

// LLM output shape — what the Mastra agent returns per event. Distinct from the
// wire shape: no source_id/source_event_id (computed downstream), camelCase,
// performers headliner-first.
export const EventDraftSchema = z.object({
  title: z.string().describe("The show title, e.g. 'Khruangbin' or 'Khruangbin with opener Mdou Moctar'. Keep it concise; do not include venue or date."),
  description: z.string().optional().describe("A brief description of the event if one is explicitly present in the source. Do not fabricate or infer."),
  startsAt: z.string().datetime({ offset: true }).describe("Show start time as ISO 8601 with timezone offset or Z (e.g. '2026-06-15T20:00:00-07:00'). Use `receivedAt` to resolve the correct year for relative dates like 'this Friday'. If only a date is given with no time, use T00:00:00 with the local offset if known, or Z."),
  endsAt: z.string().datetime({ offset: true }).optional().describe("Show end time in the same format as startsAt. Omit if not stated."),
  venue: z.object({
    name: z.string().describe("Venue name exactly as written, e.g. 'The Fillmore'."),
    address: z.string().optional().describe("Street address if shown, e.g. '1805 Geary Blvd, San Francisco, CA'. Omit if not present."),
    websiteUrl: z.string().optional().describe("Venue website URL if shown. Omit if not present."),
  }),
  performers: z.array(z.string()).default([]).describe("Artist names as plain strings, headliner first. Each entry is just the name — no role labels like 'feat.' or 'w/'."),
  genres: z.array(z.string()).default([]).describe("Music genres only if explicitly stated in the source. Do not infer from artist name."),
  url: z.string().optional().describe("Ticket purchase or event detail URL. Prefer the most specific link (e.g. the ticketing page, not the venue homepage)."),
});
export type EventDraft = z.infer<typeof EventDraftSchema>;

export const EventDraftsSchema = z.object({ events: z.array(EventDraftSchema) });
export type EventDrafts = z.infer<typeof EventDraftsSchema>;
