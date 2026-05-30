import { createHash } from "node:crypto";

/** Deterministic normalization for hashing. Independent of Go's NormalizeString;
 * only needs to be stable within this Lambda. Lowercase, strip diacritics &
 * punctuation, collapse whitespace, trim. */
export function normalize(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "") // combining diacritics
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s]/gu, "") // drop punctuation/symbols
    .replace(/\s+/g, " ")
    .trim();
}

/** UTC day of an ISO timestamp, as YYYYMMDD. Time-of-day is intentionally dropped
 * so "doors 8pm" vs "9pm" don't split the same show. */
export function eventDateYMD(startsAtISO: string): string {
  const d = new Date(startsAtISO);
  if (Number.isNaN(d.getTime())) throw new Error(`invalid startsAt: ${startsAtISO}`);
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${y}${m}${day}`;
}

/** source_event_id = sha256(normHeadliner | normVenue | eventDate). */
export function contentHash(headliner: string, venue: string, dateYMD: string): string {
  return createHash("sha256")
    .update([normalize(headliner), normalize(venue), dateYMD].join("|"))
    .digest("hex");
}
