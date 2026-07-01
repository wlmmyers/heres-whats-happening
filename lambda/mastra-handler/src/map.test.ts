import { describe, expect, it } from "vitest";
import { contentHash, eventDateYMD } from "./hash.js";
import { toMessage } from "./map.js";
import type { EventDraft } from "./schema.js";

const draft: EventDraft = {
  title: "Phoebe Bridgers Live",
  startsAt: "2026-06-15T20:00:00Z",
  venue: { name: "The Bowl", address: "100 Main St" },
  performers: ["Phoebe Bridgers", "MUNA"],
  genres: ["indie"],
};

describe("toMessage", () => {
  it("sets the shared email source and a headliner-based content hash", () => {
    const m = toMessage(draft);
    expect(m.source_id).toBe("email_newsletter");
    expect(m.source_event_id).toBe(
      contentHash("Phoebe Bridgers", "The Bowl", eventDateYMD(draft.startsAt)),
    );
    expect(m.venue.website_url).toBeUndefined();
    expect(m.performers).toEqual(["Phoebe Bridgers", "MUNA"]);
  });

  it("falls back to title when there are no performers", () => {
    const m = toMessage({ ...draft, performers: [] });
    expect(m.source_event_id).toBe(
      contentHash("Phoebe Bridgers Live", "The Bowl", eventDateYMD(draft.startsAt)),
    );
    expect(m.performers).toBeUndefined();
  });

  it("re-mapping the same draft yields the same hash (idempotent re-sends)", () => {
    expect(toMessage(draft).source_event_id).toBe(toMessage(draft).source_event_id);
  });
});
