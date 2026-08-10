import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { S3PosterSink } from "./poster-sink.js";

vi.mock("@aws-sdk/s3-request-presigner", () => ({
  getSignedUrl: vi.fn(async (_c: unknown, cmd: any) => `https://signed.test/${cmd.input.Key}`),
}));

const req = { performer: "Khruangbin", venue: "The Fillmore", date: "2026-08-15" };
const provenance = {
  artist: { mbid: "mb", name: "Khruangbin", score: 100 },
  credit: { file: "File:K.jpg", descriptionUrl: "https://commons/K", attributionRequired: true },
};

/** Records every command; GetObject serves whatever `objects` holds. */
function fakeS3(objects: Record<string, string> = {}) {
  const sent: any[] = [];
  const client = {
    sent,
    async send(cmd: any) {
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
  const svgPath = join(root, "p.svg");
  const pngPath = join(root, "p.png");
  await writeFile(svgPath, "<svg/>");
  await writeFile(pngPath, Buffer.from([0x89, 0x50, 0x4e, 0x47]));
  return {
    svg: { path: svgPath, contentType: "image/svg+xml", bytes: 6 },
    png: { path: pngPath, contentType: "image/png", bytes: 4 },
  };
}

describe("S3PosterSink.put", () => {
  it("streams both artifacts with ContentLength rather than buffering them", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);

    const puts = s3.sent.filter((c: any) => c.constructor.name === "PutObjectCommand");
    const svgPut = puts.find((c: any) => c.input.Key.endsWith(".svg"))!;
    expect(svgPut.input.ContentLength).toBe(6);
    expect(svgPut.input.ContentType).toBe("image/svg+xml");
    expect(typeof svgPut.input.Body.pipe).toBe("function"); // a stream, not a Buffer/string
  });

  it("writes the provenance sidecar LAST, so it is a commit marker", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);

    const keys = s3.sent
      .filter((c: any) => c.constructor.name === "PutObjectCommand")
      .map((c: any) => c.input.Key as string);
    expect(keys).toEqual([
      "posters/v1/khruangbin/the-fillmore-2026-08-15.svg",
      "posters/v1/khruangbin/the-fillmore-2026-08-15.png",
      "posters/v1/khruangbin/the-fillmore-2026-08-15.json",
    ]);
  });

  it("returns signed urls plus the provenance it stored", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    const out = await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);
    expect(out.svgUrl).toContain(".svg");
    expect(out.pngUrl).toContain(".png");
    expect(out.credit).toEqual(provenance.credit);
  });
});

describe("S3PosterSink.find", () => {
  const key = "posters/v1/khruangbin/the-fillmore-2026-08-15.json";

  it("returns urls and provenance when the sidecar exists", async () => {
    const s3 = fakeS3({ [key]: JSON.stringify(provenance) });
    const hit = await new S3PosterSink(s3, "bkt").find(req);

    expect(hit).not.toBeNull();
    expect(hit!.artist).toEqual(provenance.artist);
    expect(hit!.credit!.attributionRequired).toBe(true);
    expect(hit!.pngUrl).toContain(".png");
  });

  it("returns null when the sidecar is absent — a half-written poster is not a hit", async () => {
    // svg and png notionally exist; only the commit marker is missing.
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
