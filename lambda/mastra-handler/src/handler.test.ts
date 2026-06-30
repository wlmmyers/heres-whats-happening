import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { processEmail } from "./handler.js";
import { StubExtractor } from "./extractor.js";
import type { EventDraft } from "./schema.js";

const dir = fileURLToPath(new URL("./__fixtures__/", import.meta.url));
const load = (f: string) => readFileSync(join(dir, f));

const draft: EventDraft = {
  title: "Phoebe Bridgers",
  startsAt: "2026-01-02T20:00:00-05:00",
  venue: { name: "The Bowl" },
  performers: ["Phoebe Bridgers"],
  genres: [],
};

describe("processEmail", () => {
  it("text email -> extractor called in 'text' mode, mapped message emitted", async () => {
    const stub = new StubExtractor([draft]);
    const sent: unknown[] = [];
    await processEmail(load("text-newsletter.eml"), {
      extractor: stub,
      emit: async (msgs) => void sent.push(...msgs),
    });
    expect(stub.calls[0].mode).toBe("text");
    expect(stub.calls[0].receivedAt).toBeTypeOf("string"); // Date header injected
    expect(sent).toHaveLength(1);
    expect((sent[0] as { source_id: string }).source_id).toBe("email_newsletter");
  });

  it("spam email -> extractor NOT called, nothing emitted", async () => {
    const stub = new StubExtractor([draft]);
    const emit = vi.fn();
    await processEmail(load("spam.eml"), { extractor: stub, emit });
    expect(stub.calls).toHaveLength(0);
    expect(emit).not.toHaveBeenCalled();
  });

  it("flyer-only email -> extractor called in 'image' mode", async () => {
    const stub = new StubExtractor([draft]);
    await processEmail(load("flyer-only.eml"), { extractor: stub, emit: async () => {} });
    expect(stub.calls[0].mode).toBe("image");
    expect(stub.calls[0].images).toHaveLength(1);
  });

  it("drops drafts missing title or venue (no garbage events emitted)", async () => {
    const bad: EventDraft = { ...draft, title: "  " }; // whitespace-only title
    const stub = new StubExtractor([draft, bad]);
    const sent: unknown[] = [];
    await processEmail(load("text-newsletter.eml"), {
      extractor: stub,
      emit: async (msgs) => void sent.push(...msgs),
    });
    expect(sent).toHaveLength(1); // only the valid draft survives
  });
});
