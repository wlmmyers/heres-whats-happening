import { describe, expect, it } from "vitest";
import { ArtistMatchSchema, ImageCreditSchema, ImageRefSchema, USER_AGENT } from "./band-image.js";

describe("ImageCreditSchema", () => {
  it("defaults attributionRequired to false when absent (public-domain files omit it)", () => {
    const c = ImageCreditSchema.parse({
      file: "File:Example.jpg",
      descriptionUrl: "https://commons.wikimedia.org/wiki/File:Example.jpg",
    });
    expect(c.attributionRequired).toBe(false);
    expect(c.artist).toBeUndefined();
    expect(c.license).toBeUndefined();
  });

  it("round-trips a fully populated credit", () => {
    const c = ImageCreditSchema.parse({
      file: "File:La Luz.jpg",
      descriptionUrl: "https://commons.wikimedia.org/wiki/File:La_Luz.jpg",
      artist: "Shark2000br",
      credit: "Own work",
      license: "cc-by-sa-4.0",
      licenseShortName: "CC BY-SA 4.0",
      licenseUrl: "https://creativecommons.org/licenses/by-sa/4.0",
      usageTerms: "Creative Commons Attribution-Share Alike 4.0",
      attributionRequired: true,
    });
    expect(c.licenseShortName).toBe("CC BY-SA 4.0");
    expect(c.attributionRequired).toBe(true);
  });
});

describe("ImageRefSchema", () => {
  it("carries a path and a byte count instead of base64", () => {
    const ref = ImageRefSchema.parse({
      path: "/tmp/hwh-poster/run-1/band-1.jpg",
      contentType: "image/jpeg",
      bytes: 257_432,
      width: 1080,
      height: 810,
    });
    expect(ref.path).toContain("band-1.jpg");
    expect(ref.bytes).toBe(257_432);
    expect("imageBase64" in ref).toBe(false);
    expect(ref.credit).toBeUndefined();
  });
});

describe("ArtistMatchSchema", () => {
  it("accepts a match with only the required fields", () => {
    const a = ArtistMatchSchema.parse({ mbid: "abc", name: "La Luz", score: 100 });
    expect(a.disambiguation).toBeUndefined();
  });
});

describe("USER_AGENT", () => {
  it("identifies the app and a contact, as both services require", () => {
    expect(USER_AGENT).toBe("heres-whats-happening/1.0 ( wlmmyers@gmail.com )");
  });
});
