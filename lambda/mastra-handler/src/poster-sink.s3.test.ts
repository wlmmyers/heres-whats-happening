import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { posterKeyBase, S3PosterSink } from "./poster-sink.js";

const req = { userId: "550e8400-e29b-41d4-a716-446655440000", performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15", force: false };
const provenance = {
  artist: { mbid: "mb", name: "Khruangbin", score: 100 },
  credit: { file: "File:K.jpg", descriptionUrl: "https://commons/K", attributionRequired: true },
};

/**
 * Records every command; GetObject serves whatever `objects` holds.
 *
 * `send` crosses TWO real microtask boundaries before recording the call, and
 * tracks how many `send` invocations are in flight at once (`maxInFlight`).
 * This is deliberate: an `async send(){ sent.push(cmd); ... }` with no await
 * before the push runs synchronously to completion on invocation, so `sent`
 * would only ever capture INVOCATION order — `Promise.all([a(), b(), c()])`
 * invokes a, b, c synchronously in that order too, so a naive fake can't tell
 * "sequential" from "concurrent" apart. Forcing a real suspension point (and
 * counting concurrent in-flight calls across it) makes `maxInFlight > 1`
 * observable when three sends are fired concurrently instead of awaited one
 * at a time.
 */
function fakeS3(objects: Record<string, string> = {}) {
  const sent: any[] = [];
  let inFlight = 0;
  let maxInFlight = 0;
  const client = {
    sent,
    get maxInFlight() {
      return maxInFlight;
    },
    async send(cmd: any) {
      inFlight++;
      maxInFlight = Math.max(maxInFlight, inFlight);
      await Promise.resolve();
      await Promise.resolve();
      inFlight--;
      sent.push(cmd);
      const name = cmd.constructor.name;
      if (name === "GetObjectCommand") {
        const body = objects[cmd.input.Key];
        if (body === undefined) {
          const err: any = new Error("NoSuchKey");
          err.name = "NoSuchKey";
          throw err;
        }
        return { Body: { transformToString: async () => body } };
      }
      return {};
    },
  };
  return client as any;
}

let root: string;
beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "sink-test-"));
});

async function refs() {
  const pngPath = join(root, "p.png");
  await writeFile(pngPath, Buffer.from([0x89, 0x50, 0x4e, 0x47]));
  return {
    png: { path: pngPath, contentType: "image/png", bytes: 4 },
  };
}

describe("S3PosterSink.put", () => {
  it("streams the artifact with ContentLength rather than buffering it", async () => {
    const s3 = fakeS3();
    const { png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, png, provenance);

    const puts = s3.sent.filter((c: any) => c.constructor.name === "PutObjectCommand");
    const pngPut = puts.find((c: any) => c.input.Key.endsWith(".png"))!;
    expect(pngPut.input.ContentLength).toBe(4);
    expect(pngPut.input.ContentType).toBe("image/png");
    expect(typeof pngPut.input.Body.pipe).toBe("function"); // a stream, not a Buffer/string
  });

  it("writes exactly TWO objects: the png, then the sidecar last", async () => {
    const s3 = fakeS3();
    const { png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, png, provenance);

    const keys = s3.sent
      .filter((c: any) => c.constructor.name === "PutObjectCommand")
      .map((c: any) => c.input.Key as string);
    expect(keys).toEqual([`${posterKeyBase(req)}.png`, `${posterKeyBase(req)}.json`]);
    // The sidecar is still the commit marker, so the writes stay sequential.
    expect(s3.maxInFlight).toBe(1);
  });

  it("returns only a png key", async () => {
    const s3 = fakeS3();
    const { png } = await refs();
    const out = await new S3PosterSink(s3, "bkt").put(req, png, provenance);
    expect(out.pngKey).toBe(`${posterKeyBase(req)}.png`);
    expect("svgKey" in out).toBe(false);
    expect("pngUrl" in out).toBe(false);
    expect(out.credit).toEqual(provenance.credit);
  });
});

describe("S3PosterSink.find", () => {
  const key = `${posterKeyBase(req)}.json`;

  it("returns keys and provenance when the sidecar exists", async () => {
    const s3 = fakeS3({ [key]: JSON.stringify(provenance) });
    const hit = await new S3PosterSink(s3, "bkt").find(req);

    expect(hit).not.toBeNull();
    expect(hit!.pngKey).toBe(`${posterKeyBase(req)}.png`);
    expect("svgKey" in hit!).toBe(false);
    expect(hit!.artist).toEqual(provenance.artist);
  });

  it("returns null when the sidecar is absent — a half-written poster is not a hit", async () => {
    // png notionally exists; only the commit marker is missing.
    const s3 = fakeS3({});
    expect(await new S3PosterSink(s3, "bkt").find(req)).toBeNull();
  });

  it("rethrows a non-404 so the caller can decide", async () => {
    const s3 = {
      async send() {
        const err: any = new Error("AccessDenied");
        err.name = "AccessDenied";
        throw err;
      },
    } as any;
    await expect(new S3PosterSink(s3, "bkt").find(req)).rejects.toThrow(/AccessDenied/);
  });
});
