import { createTool } from "@mastra/core/tools";
import { convert as htmlToText } from "html-to-text";
import { z } from "zod";
import { ImageCandidateSchema, USER_AGENT, type ImageCandidate, type ImageCredit } from "./band-image.js";
import type { FetchFn } from "./musicbrainz.tool.js";

const DEFAULT_WIKIDATA_BASE_URL = "https://www.wikidata.org";
const DEFAULT_COMMONS_BASE_URL = "https://commons.wikimedia.org";
const TIMEOUT_MS = 15_000;

// Matches the poster canvas width in svg-author.agent.ts (1080x1350). The image
// is embedded as a base64 data URI, so anything wider is carried through
// workflow state and into resvg for nothing.
const THUMB_WIDTH = 1080;
const MAX_CANDIDATES = 12;
// 25 comfortably exceeds what is ever used: at most MAX_CANDIDATES (12) are kept
// and at most MAX_IMAGE_ATTEMPTS (3) are ever judged. MediaWiki caps `titles` at
// 50, but the real constraint is GET url length — 50 long Commons filenames
// measured ~7200 chars against a ~8000 limit, which is too little headroom.
const CATEGORY_LIMIT = 25;
const IMAGEINFO_BATCH = 25;
const ALLOWED_MIME = new Set(["image/jpeg", "image/png"]);

const EXTMETA_FIELDS = [
  "License",
  "LicenseShortName",
  "LicenseUrl",
  "UsageTerms",
  "Artist",
  "Credit",
  "AttributionRequired",
  "Restrictions",
].join("|");

export interface WikimediaOptions {
  wikidataBaseUrl?: string;
  commonsBaseUrl?: string;
  userAgent?: string;
  fetchFn?: FetchFn;
}

export interface WikimediaClient {
  resolveImageCandidates(mbid: string, opts?: { artistName?: string }): Promise<ImageCandidate[]>;
  /** Raw bytes of the candidate's thumbnail. The caller decides where they land. */
  fetchImageBytes(candidate: ImageCandidate): Promise<Buffer>;
}

interface RawImageInfo {
  mime?: string;
  thumbwidth?: number;
  thumbheight?: number;
  thumburl?: string;
  descriptionurl?: string;
  extmetadata?: Record<string, { value?: unknown }>;
}

/** Commons stores file titles with spaces or underscores interchangeably. */
function normalizeTitle(title: string): string {
  return title.replace(/^File:/i, "").replace(/_/g, " ").trim().toLowerCase();
}

/** extmetadata values may contain anchors and spans; we want the bare text. */
function plainText(value: unknown): string | undefined {
  if (value === undefined || value === null) return undefined;
  const text = htmlToText(String(value), {
    wordwrap: false,
    selectors: [{ selector: "a", options: { ignoreHref: true } }],
  }).trim();
  return text.length > 0 ? text : undefined;
}

function toCredit(title: string, info: RawImageInfo): ImageCredit {
  const meta = info.extmetadata ?? {};
  const field = (key: string) => plainText(meta[key]?.value);
  return {
    file: title,
    descriptionUrl: info.descriptionurl ?? "",
    artist: field("Artist"),
    credit: field("Credit"),
    license: field("License"),
    licenseShortName: field("LicenseShortName"),
    licenseUrl: field("LicenseUrl"),
    usageTerms: field("UsageTerms"),
    attributionRequired: String(meta.AttributionRequired?.value ?? "").toLowerCase() === "true",
  };
}

export function createWikimediaClient(options: WikimediaOptions = {}): WikimediaClient {
  const wikidataBaseUrl = options.wikidataBaseUrl ?? DEFAULT_WIKIDATA_BASE_URL;
  const commonsBaseUrl = options.commonsBaseUrl ?? DEFAULT_COMMONS_BASE_URL;
  const userAgent = options.userAgent ?? USER_AGENT;
  const doFetch = options.fetchFn ?? globalThis.fetch;

  async function getJson(base: string, params: Record<string, string>): Promise<any> {
    const url = `${base}/w/api.php?${new URLSearchParams({ ...params, format: "json" }).toString()}`;
    const res = await doFetch(url, {
      headers: { "User-Agent": userAgent, Accept: "application/json" },
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`wikimedia ${res.status} for ${base}`);
    return res.json();
  }

  /** MBID -> QID via Wikidata's reverse P434 index. Avoids a rate-limited MB call. */
  async function resolveQid(mbid: string): Promise<string | null> {
    const payload = await getJson(wikidataBaseUrl, {
      action: "query",
      list: "search",
      srsearch: `haswbstatement:P434=${mbid}`,
    });
    return payload?.query?.search?.[0]?.title ?? null;
  }

  async function claimValue(qid: string, property: string): Promise<string | null> {
    const payload = await getJson(wikidataBaseUrl, { action: "wbgetclaims", entity: qid, property });
    const value = payload?.claims?.[property]?.[0]?.mainsnak?.datavalue?.value;
    return typeof value === "string" && value.length > 0 ? value : null;
  }

  async function categoryFiles(category: string): Promise<string[]> {
    const payload = await getJson(commonsBaseUrl, {
      action: "query",
      list: "categorymembers",
      cmtitle: `Category:${category}`,
      cmtype: "file",
      cmlimit: String(CATEGORY_LIMIT),
    });
    const members = payload?.query?.categorymembers ?? [];
    return members.map((m: { title: string }) => m.title).filter(Boolean);
  }

  /**
   * Batched imageinfo. `pages` comes back keyed by pageid in ARBITRARY order,
   * so results are joined by normalized title, never by position.
   */
  async function imageInfo(titles: string[]): Promise<Map<string, RawImageInfo>> {
    const out = new Map<string, RawImageInfo>();
    for (let i = 0; i < titles.length; i += IMAGEINFO_BATCH) {
      const batch = titles.slice(i, i + IMAGEINFO_BATCH);
      const payload = await getJson(commonsBaseUrl, {
        action: "query",
        prop: "imageinfo",
        titles: batch.join("|"),
        iiprop: "url|size|mime|extmetadata",
        iiextmetadatafilter: EXTMETA_FIELDS,
        iiurlwidth: String(THUMB_WIDTH),
      });
      const pages = payload?.query?.pages ?? {};
      for (const page of Object.values<any>(pages)) {
        const info = page?.imageinfo?.[0];
        if (page?.title && info) out.set(normalizeTitle(page.title), info as RawImageInfo);
      }
    }
    return out;
  }

  function toCandidate(title: string, info: RawImageInfo, source: "p18" | "category"): ImageCandidate | null {
    const mime = info.mime ?? "";
    if (!ALLOWED_MIME.has(mime)) return null;
    if (!info.thumburl || !info.thumbwidth || !info.thumbheight) return null;
    return {
      file: title,
      url: info.thumburl,
      width: info.thumbwidth,
      height: info.thumbheight,
      contentType: mime,
      source,
      credit: toCredit(title, info),
    };
  }

  return {
    async resolveImageCandidates(mbid, opts) {
      const qid = await resolveQid(mbid);
      if (!qid) return [];

      const [p18, category] = await Promise.all([claimValue(qid, "P18"), claimValue(qid, "P373")]);
      if (!p18 && !category) return [];

      const p18Title = p18 ? `File:${p18}` : null;
      const categoryTitles = category ? await categoryFiles(category) : [];

      // Dedupe BEFORE fetching: the P18 file is usually also a category member
      // (it is for La Luz), and sending the title twice wastes url budget in a
      // request whose length is the binding constraint.
      const wanted: string[] = [];
      const requested = new Set<string>();
      for (const title of [...(p18Title ? [p18Title] : []), ...categoryTitles]) {
        const key = normalizeTitle(title);
        if (requested.has(key)) continue;
        requested.add(key);
        wanted.push(title);
      }
      if (wanted.length === 0) return [];
      const info = await imageInfo(wanted);

      const seen = new Set<string>();
      const ordered: ImageCandidate[] = [];

      if (p18Title) {
        const raw = info.get(normalizeTitle(p18Title));
        const candidate = raw ? toCandidate(p18Title, raw, "p18") : null;
        if (candidate) {
          ordered.push(candidate);
          seen.add(normalizeTitle(p18Title));
        }
      }

      // Commons category order is alphabetical, which buries the band photos
      // under solo shots and panel photos. Promote titles naming the artist.
      const needle = opts?.artistName?.trim().toLowerCase();
      const rest = categoryTitles
        .filter((t) => !seen.has(normalizeTitle(t)))
        .map((t) => {
          const raw = info.get(normalizeTitle(t));
          return raw ? toCandidate(t, raw, "category") : null;
        })
        .filter((c): c is ImageCandidate => c !== null)
        .sort((a, b) => {
          const aHit = needle && a.file.toLowerCase().includes(needle) ? 0 : 1;
          const bHit = needle && b.file.toLowerCase().includes(needle) ? 0 : 1;
          if (aHit !== bHit) return aHit - bHit;
          return a.file.localeCompare(b.file);
        });

      return [...ordered, ...rest].slice(0, MAX_CANDIDATES);
    },

    async fetchImageBytes(candidate) {
      const res = await doFetch(candidate.url, {
        headers: { "User-Agent": userAgent },
        signal: AbortSignal.timeout(TIMEOUT_MS),
      });
      if (!res.ok) throw new Error(`commons ${res.status} fetching ${candidate.file}`);
      return Buffer.from(await res.arrayBuffer());
    },
  };
}

/** Production client. */
export const wikimediaClient = createWikimediaClient();

export function resolveImageCandidates(mbid: string, opts?: { artistName?: string }): Promise<ImageCandidate[]> {
  return wikimediaClient.resolveImageCandidates(mbid, opts);
}

export function fetchImageBytes(candidate: ImageCandidate): Promise<Buffer> {
  return wikimediaClient.fetchImageBytes(candidate);
}

/** Thin wrapper so the lookup is inspectable in Mastra Studio. */
export const wikimediaImagesTool = createTool({
  id: "wikimedia-artist-images",
  description: "Resolve a MusicBrainz artist MBID to Wikimedia Commons image candidates with licensing.",
  inputSchema: z.object({ mbid: z.string(), artistName: z.string().optional() }),
  outputSchema: z.object({ candidates: z.array(ImageCandidateSchema) }),
  execute: async ({ mbid, artistName }) => ({ candidates: await resolveImageCandidates(mbid, { artistName }) }),
});
