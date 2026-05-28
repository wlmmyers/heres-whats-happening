import { apiFetch } from './client';

// Start the Spotify OAuth flow. The server sets the PKCE/state cookie and
// returns the authorize URL; the caller does a top-level navigation to it.
// Done this way (rather than a 302 redirect) because the connect endpoint is
// behind Bearer auth — top-level link navigations can't attach Authorization.
export async function startSpotifyConnect(): Promise<string> {
  const { authorize_url } = await apiFetch<{ authorize_url: string }>(
    '/integrations/spotify/connect',
  );
  return authorize_url;
}

// Complete the Spotify OAuth flow. Called by the SPA's callback page with the
// {code, state} Spotify appended to the redirect URL. Server validates state
// against the cookie set during connect, exchanges the code for tokens, and
// persists them.
export async function exchangeSpotifyCode(code: string, state: string): Promise<void> {
  await apiFetch<{ status: string }>('/integrations/spotify/exchange', {
    method: 'POST',
    body: { code, state },
  });
}

export async function disconnectSpotify(): Promise<void> {
  await apiFetch<void>('/integrations/spotify', { method: 'DELETE' });
}
