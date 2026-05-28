# Stateful Spotify section on the Settings page

## Goal

On the Settings page, the Spotify section should reflect whether the
authenticated user is currently connected to Spotify. When connected, show
only the `Disconnect` button; when not connected, show only the
`Connect Spotify` button.

Today both buttons are rendered unconditionally because the frontend has no
way to ask the server whether tokens exist.

## Backend

Add a small read-only endpoint that reports connection status for the
authenticated user.

- Route: `GET /integrations/spotify/status` (added under the existing
  authenticated group in `internal/http/server.go`).
- Response: `200 {"connected": true}` or `200 {"connected": false}`.
- Handler: `SpotifyStatus(q *store.Queries) http.HandlerFunc` in
  `internal/http/handlers/spotify.go`.
  - Look up the user from the auth context (mirrors `SpotifyDisconnect`).
  - Call `q.GetUserSpotifyTokens(ctx, pgUID)`.
  - If `err == nil` → `connected: true`.
  - If `errors.Is(err, pgx.ErrNoRows)` → `connected: false`.
  - Any other error → `500 db_error`.
- No new SQL or sqlc changes — `GetUserSpotifyTokens` already exists.

Why a dedicated endpoint instead of extending `GET /me`: keeps Spotify
concerns inside the spotify route group, avoids reshaping the user payload,
and lets the frontend invalidate this query independently after
connect/disconnect.

## Frontend API client

In `web/src/api/spotify.ts`:

```ts
export async function getSpotifyStatus(): Promise<{ connected: boolean }> {
  return apiFetch<{ connected: boolean }>('/integrations/spotify/status');
}
```

## SettingsPage

In `web/src/pages/SettingsPage.tsx`:

1. Add a `useQuery`:
   ```ts
   const { data: spotifyStatus, isLoading: spotifyStatusLoading } = useQuery({
     queryKey: ['spotify-status'],
     queryFn: getSpotifyStatus,
   });
   ```
2. Update the disconnect mutation's `onSuccess` to invalidate the query:
   ```ts
   onSuccess: () => qc.invalidateQueries({ queryKey: ['spotify-status'] }),
   ```
3. Render logic inside the Spotify section:
   - While `spotifyStatusLoading`: heading + description only, no buttons.
     This avoids briefly flashing the wrong button on first load.
   - When `spotifyStatus?.connected === true`: show "Connected." status
     text + `Disconnect` button only.
   - When `spotifyStatus?.connected === false`: show `Connect Spotify`
     button only.

The OAuth round-trip already returns the user to `/settings`, so the query
will refetch automatically on mount.

## Tests

### Backend — `internal/http/handlers/spotify_test.go`

- `TestSpotifyStatus_Connected`: seed a token row via
  `UpsertUserSpotifyTokens` for a fresh user, hit `GET /integrations/spotify/status`
  with that user's bearer, expect `200` and `{"connected": true}`.
- `TestSpotifyStatus_NotConnected`: create a user with no token row, hit
  the endpoint, expect `200` and `{"connected": false}`.

### Frontend — `web/src/pages/SettingsPage.test.tsx`

- Add `getSpotifyStatus: vi.fn()` to the `../api/spotify` mock.
- In `beforeEach`, default mock to resolve `{ connected: false }`.
- Update the existing "navigates to authorize URL" test to keep working
  (the Connect button still appears when disconnected).
- New test: "shows only Disconnect when Spotify is connected" — mock
  `getSpotifyStatus` to return `{ connected: true }`; assert the
  `Disconnect` button is present and the `Connect Spotify` button is not.
- New test: "shows only Connect when Spotify is not connected" — default
  mock; assert the inverse.

## Out of scope

- Surfacing `last_synced_at` or scope to the UI.
- Polling for status changes; the page refetches on mount, which covers
  the disconnect-then-reconnect flow.
- Reflecting "connecting in progress" while the OAuth round-trip is open
  in another tab.
