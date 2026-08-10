import { describe, expect, it } from "vitest";
import { stubFetch } from "./stub-fetch.js";

describe("stubFetch", () => {
  it("routes by regex and returns JSON", async () => {
    const f = stubFetch([{ match: /\/ws\/2\/artist\?/, json: { artists: [{ id: "x" }] } }]);
    const res = await f("https://musicbrainz.org/ws/2/artist?query=a");
    expect(await res.json()).toEqual({ artists: [{ id: "x" }] });
  });

  it("records url and headers for assertions", async () => {
    const f = stubFetch([{ match: /./, json: {} }]);
    await f("https://example.test/a", { headers: { "User-Agent": "ua/1.0" } });
    expect(f.calls).toHaveLength(1);
    expect(f.calls[0].url).toBe("https://example.test/a");
    expect(f.calls[0].headers["user-agent"]).toBe("ua/1.0");
  });

  it("serves binary bodies for image fetches", async () => {
    const bytes = Buffer.from([0xff, 0xd8, 0xff, 0xe0]);
    const f = stubFetch([{ match: /upload\./, body: bytes }]);
    const res = await f("https://upload.wikimedia.org/x.jpg");
    expect(Buffer.from(await res.arrayBuffer())).toEqual(bytes);
  });

  it("returns the given status and supports a per-call queue", async () => {
    const f = stubFetch([{ match: /./, statuses: [503, 200], json: { ok: true } }]);
    expect((await f("https://x.test/")).status).toBe(503);
    expect((await f("https://x.test/")).status).toBe(200);
  });

  it("throws on an unrouted url so typos surface loudly", async () => {
    const f = stubFetch([{ match: /nope/, json: {} }]);
    await expect(f("https://example.test/other")).rejects.toThrow(/no stub route/);
  });
});
