import { apiFetch } from './client';
import type { Interest } from './manualInterests';

// A group of Spotify-derived interests of one kind. The server owns the kind
// ordering and the human label, so a new kind needs no frontend change.
export interface SpotifyInterestGroup {
  kind: string;
  label: string;
  interests: Interest[];
}

// Read-only. These interests come from the Spotify scraper; there is no create
// or delete counterpart. Groups with no interests are omitted by the server.
export async function listSpotifyInterests(): Promise<SpotifyInterestGroup[]> {
  const out = await apiFetch<{ groups: SpotifyInterestGroup[] }>('/me/spotify-interests');
  return out.groups;
}
