import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { gate, parseEmail } from "./email.js";

const dir = fileURLToPath(new URL("./__fixtures__/", import.meta.url));
const load = (f: string) => readFileSync(join(dir, f));

describe("parseEmail + gate", () => {
  it("text newsletter -> 'text' with body and Date", async () => {
    const p = await parseEmail(load("text-newsletter.eml"));
    expect(p.spamFail).toBe(false);
    expect(p.text).toMatch(/Phoebe Bridgers/);
    expect(p.date).toBeTypeOf("string");
    expect(gate(p)).toBe("text");
  });

  it("flyer-only -> 'image' with at least one image attachment", async () => {
    const p = await parseEmail(load("flyer-only.eml"));
    expect(p.text.trim().length).toBeLessThan(40);
    expect(p.images.length).toBe(1);
    expect(p.images[0].contentType).toBe("image/png");
    expect(gate(p)).toBe("image");
  });

  it("spam-FAIL -> 'skip'", async () => {
    const p = await parseEmail(load("spam.eml"));
    expect(p.spamFail).toBe(true);
    expect(gate(p)).toBe("skip");
  });
});
