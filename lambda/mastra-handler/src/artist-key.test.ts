import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { artistKey } from "./artist-key.js";

// The same fixture internal/events/artistkey_contract_test.go asserts. The
// Lambda keys its S3 skip cache on artistKey() and the database keys
// artists.name_key on Go's NormalizeString; a silent divergence would merge
// two artists in one place and split them in the other.
const cases: { in: string; out: string; why: string }[] = JSON.parse(
  readFileSync(new URL("../../../testdata/artist-key-contract/cases.json", import.meta.url), "utf8"),
);

describe("artistKey", () => {
  it("has fixture cases to run", () => {
    expect(cases.length).toBeGreaterThan(0);
  });

  for (const c of cases) {
    it(`${JSON.stringify(c.in)} -> ${JSON.stringify(c.out)} (${c.why})`, () => {
      expect(artistKey(c.in)).toBe(c.out);
    });
  }
});
