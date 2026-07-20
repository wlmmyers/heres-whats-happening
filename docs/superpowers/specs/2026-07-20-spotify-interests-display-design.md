# Read-only Spotify interests display — design

**Date:** 2026-07-20

## Problem

The `user_interests` table holds five kinds of interest. One is user-authored
(`manual_tag`); the other four are derived from the user's Spotify account by the
scraper: `spotify_top_artist`, `spotify_top_track_artist`, `spotify_saved_song_artist`,
and `spotify_top_genre`.

Only manual tags are visible in the product. The `/me/manual-interests` endpoints
deliberately scope to `manual_tag`, so a user has no way to see the Spotify-derived
interests that drive a large share of their event matches. This makes matches feel
arbitrary: a user sees an event surfaced for an artist affinity they cannot inspect.

This design adds a read-only view of the four Spotify-derived kinds: one new GET
endpoint and a new section on the Interests page.

## Goals

- A new `GET /me/spotify-interests` returns the requesting user's Spotify-derived
  interests, grouped by kind, in a fixed display order.
- All four Spotify kinds are included; `spotify_top_genre` renders as its own group
  rather than being merged with the artist kinds.
- The Interests page renders these as read-only chips under a "From your Spotify"
  section, grouped with a heading per kind.
- Groups longer than 20 items collapse behind a "Show all (N)" toggle.
- Empty states distinguish "Spotify not connected" from "connected but nothing
  scraped yet".
- Manual tags and Spotify interests never bleed into each other's endpoints.

## Non-goals

- **No mutation.** GET only. No POST, PATCH, or DELETE on Spotify interests. Users
  cannot edit, delete, or re-weight them; the scraper owns those rows. Removing them
  is done by disconnecting Spotify, which already deletes them
  (`TestSpotifyDisconnect_RemovesTokensAndInterests`).
- **No changes to `/me/manual-interests`.** That endpoint's `manual_tag` scoping is
  correct and stays as is.
- **No changes to the scraper or the matcher.** This is a display feature only.
- **No server-side pagination.** All rows for the user are returned; collapsing is a
  frontend concern (see Volume).

## API

### `GET /me/spotify-interests`

Auth: `RequireAuth`, same as the manual-interest handlers. 5s context timeout,
matching the existing pattern.

Response `200`:

```json
{
  "groups": [
    {
      "kind": "spotify_top_artist",
      "label": "Top artists",
      "interests": [
        {
          "id": "9f0c…",
          "value": "Phoebe Bridgers",
          "normalized_value": "phoebe bridgers",
          "weight": 0.9,
          "created_at": "2026-07-01T00:00:00Z"
        }
      ]
    }
  ]
}
```

Group order is fixed and server-owned:

1. `spotify_top_artist` — "Top artists"
2. `spotify_top_track_artist` — "Artists from your top tracks"
3. `spotify_saved_song_artist` — "Artists from your saved songs"
4. `spotify_top_genre` — "Top genres"

**Empty groups are omitted** from the response, so the frontend never renders a
heading with nothing under it. A user with no Spotify data gets `{"groups": []}`.

Within a group, interests are ordered by `weight DESC, normalized_value ASC`,
matching the existing `ListInterestsByUserAndKind` ordering.

Unauthenticated requests get `401`; DB failures get `500` via `httperr.WriteErr`.

## Data layer

New query in `sql/queries/user_interests.sql`:

```sql
-- name: ListInterestsByUserAndKinds :many
SELECT id, kind, value, normalized_value, weight, created_at
FROM user_interests
WHERE user_id = $1 AND kind = ANY($2::text[])
ORDER BY weight DESC, normalized_value ASC;
```

One round trip for all four kinds. The existing
`user_interests_user_kind (user_id, kind)` index covers this predicate.

Running `sqlc generate` regenerates `internal/store/models.go` alongside
`internal/store/user_interests.sql.go`; both are committed together per project
convention.

The existing `ListInterestsByUserAndKind` (singular) is left alone — it is used by
the Spotify ingest path and its tests.

## Backend structure

New file `internal/http/handlers/spotify_interests.go`, not appended to
`manual_interests.go`: different resource, and that file is already ~160 lines.

It owns:

- `spotifyKinds` — the ordered slice of the four kind strings, the single source of
  truth for both the query argument and the group ordering.
- `kindLabels` — `map[string]string` of kind to human label.
- `SpotifyInterests(q *store.Queries) http.HandlerFunc` — fetches, groups, emits.

Labels and ordering live on the server so that adding a fifth Spotify kind is a
backend-only change; the frontend renders whatever groups it is handed.

Route registered in `internal/http/server.go` inside the existing authenticated
group, next to the manual-interest routes.

## Frontend

### `web/src/api/spotifyInterests.ts` (new)

Follows the `manualInterests.ts` / `notInterested.ts` camelCase file convention.

```ts
export interface SpotifyInterestGroup {
  kind: string;
  label: string;
  interests: Interest[];
}

export async function listSpotifyInterests(): Promise<SpotifyInterestGroup[]>;
```

Reuses the `Interest` type exported from `manualInterests.ts`.

### `web/src/components/TagList.tsx` (new)

A read-only chip list: `{ values: string[] }`. `TagInput` is not reused — it bakes in
an input and per-chip remove buttons, and adding a `readOnly` mode would make one
component branch across two purposes. Chip styling is duplicated into
`TagList.css.ts`; the duplication is a padding/border-radius rule, and the two
components stay single-purpose.

### `InterestsPage.tsx`

Adds two queries: `['spotifyInterests']` and `['spotifyStatus']` (the latter via the
existing `getSpotifyStatus` in `api/spotify.ts`).

The two queries load independently. If groups arrive before status, they render
immediately rather than blocking on status — status only gates empty-state copy.

Below the existing manual-tag section, a new `<section>` headed "From your Spotify",
rendering one sub-heading plus `<TagList>` per group.

**Visibility (onboarding-aware).** The Interests page doubles as the signup
onboarding step — `SignupPage` redirects here and the page ends in a "Continue"
button. A brand-new user has no Spotify connection and no scraped data, so an
always-visible section would put a dead-end "go to Settings" prompt in the middle of
signup. Therefore:

- groups non-empty → render the section
- groups empty and `connected: true` → render the section with
  "We haven't pulled your listening history yet. Check back soon."
- groups empty and `connected: false` → **render nothing**
- status still loading and groups empty → render nothing

This keeps onboarding clean while giving returning and connected users the full view.

**Collapsing.** Each group renders its first 20 chips. If the group is longer, a
`Show all (N)` button reveals the rest and toggles to `Show less`. Expanded state is
per-group, held in the page as a `Set<string>` of expanded kinds.

## Testing

TDD: tests first, then handler, then page.

### Backend — `internal/http/handlers/spotify_interests_test.go`

Real Postgres via `testdb.MustOpen`, matching the existing handler tests.

`CreateManualInterest` hardcodes `kind = 'manual_tag'`, so no existing query can
insert other kinds. Tests get a local `insertInterest(t, pool, userID, kind, value,
weight)` helper doing direct SQL — preferable to adding a production query that
exists only for tests.

- `TestGetSpotifyInterests_GroupsByKind` — all four kinds seeded; asserts group order
  and per-group membership
- `TestGetSpotifyInterests_ExcludesManualTags` — a `manual_tag` seeded alongside never
  appears in the response
- `TestGetSpotifyInterests_OmitsEmptyGroups` — only one kind seeded; exactly one group
  returned
- `TestGetSpotifyInterests_ReturnsOnlyOwn` — a second user's rows are not visible
- `TestGetSpotifyInterests_OrdersByWeightDesc`
- `TestGetSpotifyInterests_Unauthenticated` — 401

### Frontend — `InterestsPage.test.tsx`

Extends the existing file, reusing its `vi.mock` pattern.

- renders grouped chips with headings when the API returns groups
- Spotify chips are read-only: no `Remove …` button in that section (guards the
  read-only requirement against future `TagInput` reuse)
- group of >20 shows `Show all (N)`; clicking reveals the remainder
- section absent when groups empty and `connected: false`
- section shows the "haven't pulled yet" copy when groups empty and `connected: true`

## Risks

- **Payload size.** `spotify_saved_song_artist` is unbounded: `internal/spotify/client.go`
  paginates all saved tracks with no cap, so a heavy user could have several hundred
  artists. We return them all and collapse client-side, accepting a larger response in
  exchange for full fidelity and no pagination machinery. If this proves too large in
  practice, the mitigation is a `LIMIT` per kind in the query — a backend-only change
  that the grouped response shape already accommodates.
