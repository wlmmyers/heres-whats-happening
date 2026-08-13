/**
 * Mirrors Go's events.NormalizeString (internal/events/genres.go:64) EXACTLY:
 * NFD, drop nonspacing marks, NFC, lowercase, trim. Pinned in both languages by
 * testdata/artist-key-contract/cases.json.
 *
 * This is deliberately NOT hash.ts's normalize(). That one additionally strips
 * punctuation ("AC/DC" -> "acdc" where this gives "ac/dc"), and it cannot be
 * changed to match because it feeds contentHash() -> source_event_id: re-keying
 * that would break dedup for every email-ingested event.
 */
export function artistKey(performer: string): string {
  return performer
    .normalize('NFD')
    .replace(/\p{Mn}/gu, '')
    .normalize('NFC')
    .toLowerCase()
    .trim();
}
