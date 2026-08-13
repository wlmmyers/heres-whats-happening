import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { ImageCredit } from "./mastra/tools/band-image.js";
import { EnrichedMessageSchema, toWireCredit } from "./enrichment-schema.js";

const baseEvent = {
  source_id: "ticketmaster",
  source_event_id: "tm-aaa",
  title: "La Luz",
  starts_at: "2026-09-02T20:00:00Z",
  venue: { name: "The Chapel" },
};

describe("EnrichedMessageSchema", () => {
  it("accepts a plain event with no enrichment", () => {
    const parsed = EnrichedMessageSchema.parse(baseEvent);
    expect(parsed.enrichment).toBeUndefined();
  });

  it("accepts a fully enriched event", () => {
    const parsed = EnrichedMessageSchema.parse({
      ...baseEvent,
      enrichment: {
        attempted_at: "2026-08-12T04:11:22Z",
        artist: { performer: "La Luz", display_name: "La Luz", status: "ok" },
        image: { status: "ok", url: "https://x/y.jpg", width: 640, height: 427 },
        bio: { status: "none", reason: "no article" },
        tour: { status: "ok", tour_name: "T", observed_date: "2026-07-14" },
      },
    });
    expect(parsed.enrichment?.artist?.status).toBe("ok");
    expect(parsed.enrichment?.tour?.observed_date).toBe("2026-07-14");
  });

  it("rejects an unknown status", () => {
    expect(() =>
      EnrichedMessageSchema.parse({
        ...baseEvent,
        enrichment: {
          attempted_at: "2026-08-12T04:11:22Z",
          artist: { performer: "x", display_name: "x", status: "ok" },
          bio: { status: "maybe" },
        },
      }),
    ).toThrow();
  });

  it("rejects an observed_date that is not YYYY-MM-DD", () => {
    expect(() =>
      EnrichedMessageSchema.parse({
        ...baseEvent,
        enrichment: {
          attempted_at: "2026-08-12T04:11:22Z",
          artist: { performer: "x", display_name: "x", status: "ok" },
          tour: { status: "ok", observed_date: "14-07-2026" },
        },
      }),
    ).toThrow();
  });

  it("parses the Go contract fixture", () => {
    const raw = readFileSync(
      new URL("../../../testdata/enriched-message-contract/full.json", import.meta.url),
      "utf8",
    );
    expect(() => EnrichedMessageSchema.parse(JSON.parse(raw))).not.toThrow();
  });
});

// The Lambda's internal credit is camelCase; the wire is snake_case, matching
// the Go struct tags. Without an explicit mapper the fields silently vanish.
describe("toWireCredit", () => {
  it("renames every camelCase field", () => {
    const internal: ImageCredit = {
      file: "La_Luz.jpg",
      descriptionUrl: "https://commons/desc",
      artist: "Photographer",
      license: "cc-by-sa-4.0",
      licenseShortName: "CC BY-SA 4.0",
      licenseUrl: "https://creativecommons.org/x",
      usageTerms: "terms",
      attributionRequired: true,
    };
    expect(toWireCredit(internal)).toEqual({
      file: "La_Luz.jpg",
      description_url: "https://commons/desc",
      artist: "Photographer",
      license: "cc-by-sa-4.0",
      license_short_name: "CC BY-SA 4.0",
      license_url: "https://creativecommons.org/x",
      usage_terms: "terms",
      attribution_required: true,
    });
  });
});
