import { contentHash, eventDateYMD } from "./hash.js";
import { EventMessageSchema, type EventDraft, type EventMessage } from "./schema.js";

export const EMAIL_SOURCE_ID = "email_newsletter";

/** Convert one LLM draft into the canonical wire message. Headliner = performers[0],
 * falling back to title when the draft has no performers. */
export function toMessage(d: EventDraft): EventMessage {
  const headliner = d.performers[0] ?? d.title;
  const msg: EventMessage = {
    source_id: EMAIL_SOURCE_ID,
    source_event_id: contentHash(headliner, d.venue.name, eventDateYMD(d.startsAt)),
    title: d.title,
    description: d.description,
    starts_at: d.startsAt,
    ends_at: d.endsAt,
    venue: {
      name: d.venue.name,
      address: d.venue.address,
      website_url: d.venue.websiteUrl,
    },
    performers: d.performers.length ? d.performers : undefined,
    genres: d.genres.length ? d.genres : undefined,
    url: d.url,
  };
  // Defensive: guarantee what we emit satisfies the wire contract.
  return EventMessageSchema.parse(msg);
}
