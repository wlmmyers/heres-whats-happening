import { apiFetch } from './client';

const BASE = import.meta.env.VITE_API_BASE_URL ?? '';

// The Spotify Connect endpoint is a redirect — we build the URL and let the
// browser navigate to it. apiFetch can't be used (no JSON response and
// the request must be issued by the browser top-level navigation so the
// session cookies + redirects all flow correctly).
export function buildSpotifyConnectURL(): string {
  if (BASE === '') return '/api/integrations/spotify/connect';
  return `${BASE}/integrations/spotify/connect`;
}

export async function disconnectSpotify(): Promise<void> {
  await apiFetch<void>('/integrations/spotify', { method: 'DELETE' });
}
