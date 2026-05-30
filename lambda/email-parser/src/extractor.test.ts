import { describe, expect, it } from "vitest";
import { StubExtractor } from "./extractor.js";
import type { EventDraft } from "./schema.js";

const drafts: EventDraft[] = [
  {
    title: "Phoebe Bridgers",
    startsAt: "2026-01-02T20:00:00-05:00",
    venue: { name: "The Bowl" },
    performers: ["Phoebe Bridgers"],
    genres: [],
  },
];

describe("StubExtractor", () => {
  it("returns the canned drafts and records the input it was given", async () => {
    const stub = new StubExtractor(drafts);
    const out = await stub.extract({ mode: "text", text: "hi", images: [], receivedAt: "x" });
    expect(out).toEqual(drafts);
    expect(stub.calls).toHaveLength(1);
    expect(stub.calls[0].mode).toBe("text");
  });
});
