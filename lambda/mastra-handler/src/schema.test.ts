import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { EventMessageSchema } from "./schema.js";

// ESM has no __dirname; derive it from import.meta.url. From src/, three levels
// up is the repo root holding testdata/.
const here = fileURLToPath(new URL(".", import.meta.url));
const FIXTURE_DIR = join(here, "..", "..", "..", "testdata", "event-message-contract");

describe("EventMessageSchema accepts shared contract fixtures", () => {
  const files = readdirSync(FIXTURE_DIR).filter((f) => f.endsWith(".json"));
  it("finds fixtures", () => expect(files.length).toBeGreaterThan(0));
  for (const f of files) {
    it(`validates ${f}`, () => {
      const data = JSON.parse(readFileSync(join(FIXTURE_DIR, f), "utf8"));
      const r = EventMessageSchema.safeParse(data);
      expect(r.success, r.success ? "" : JSON.stringify(r.error.issues)).toBe(true);
    });
  }
});
