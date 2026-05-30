import { afterEach, describe, expect, it, vi } from "vitest";
import { loadModelKey, StubExtractor } from "./extractor.js";
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

describe("loadModelKey", () => {
  afterEach(() => {
    delete process.env.ANTHROPIC_API_KEY; // avoid leaking to other tests in this file
  });

  it("reads the secret and sets ANTHROPIC_API_KEY when unset", async () => {
    delete process.env.ANTHROPIC_API_KEY;
    const fakeSecrets = { getSecretValue: vi.fn().mockResolvedValue("sk-test-123") };
    await loadModelKey(fakeSecrets, "arn:secret");
    expect(process.env.ANTHROPIC_API_KEY).toBe("sk-test-123");
    expect(fakeSecrets.getSecretValue).toHaveBeenCalledWith("arn:secret");
  });

  it("no-ops when ANTHROPIC_API_KEY is already set", async () => {
    process.env.ANTHROPIC_API_KEY = "preset";
    const fakeSecrets = { getSecretValue: vi.fn() };
    await loadModelKey(fakeSecrets, "arn:secret");
    expect(fakeSecrets.getSecretValue).not.toHaveBeenCalled();
  });
});
