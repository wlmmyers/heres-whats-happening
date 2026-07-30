# City-wide calendar fallback for users with no interests

**Date:** 2026-07-29
**Status:** Approved (design)
**Area:** `internal/http/` (Go API), `web/` frontend (React 19, react-query 5, vanilla-extract)

## Goal

A user who has neither connected Spotify nor added a manual interest has nothing
to match against, so `/me/calendar` returns an empty list and `CalendarPage`
shows an empty state. Instead, show that user **every** event in their city,
unfiltered by match score, with a banner explaining why and how to personalize.

## Decisions (from brainstorming)

1. **Endpoint:** `GET /calendar/{cityId}?from&to`, authenticated + confirmed,
   same `from`/`to` (`YYYY-MM-DD`) contract as `/me/calendar`. The range keeps
   the response bounded and keeps the existing 1/3/6-month toggle meaningful in
   fallback mode.
2. **Not city-scoped to the caller:** any authenticated, confirmed user may
   request any `cityId`. Event listings are not private data, and scoping to
   `users.city_id` would add a check with nothing to protect.
3. **Purely city-wide:** the response does not depend on the caller. No
   not-interested filtering, so the endpoint stays cacheable in principle and
   the FE list passes no `onNotInterested`.
4. **Card display:** the `% match` chip is hidden when an event has no match
   (`score <= 0`), on both `EventCard` and `EventDetailPage`. Cards stay
   clickable through to the detail page.
5. **Explainer:** in fallback mode the page title becomes "Everything happening
   in Seattle" and a banner above the list offers the two existing CTAs (connect
   Spotify, add interests).
6. **Gate:** fallback applies when `/integrations/spotify/status` returns
   `{connected: false}` **and** `/me/manual-interests` returns `{interests: []}`.
   Spotify-derived interests are not part of the gate — connecting Spotify is
   itself one of the two conditions.

## Current architecture (baseline)

- `GET /me/calendar` (`handlers.GetMyCalendar`) reads `user_event_match` joined
  to `events`/`venues`. `internal/matcher` only writes pairs scoring **above**
  the user's threshold, so the match table is already the "above threshold" set —
  there is no threshold filter in the read path to bypass.
- `GetUserByID` already selects `city_id`; `GetMe` does not surface it.
- `userOut` (`handlers/auth.go`) is shared by `GetMe` and the signup response.
  Login returns only `access_token`; the FE calls `/me` afterwards.
- `web/src/api/auth.ts` `User` = `{id, email, confirmed, score_threshold?}`, held
  by `AuthProvider` and read via `useAuth()`.
- `CalendarPage` runs two queries today: the calendar (`['calendar', user?.id,
  from, to]`) and Spotify status (`['spotify-status', user?.id]`, used only by
  the empty state). It owns the `markNotInterested` mutation and the range
  toggle. There is no `web/src/hooks/` directory; pages call `useQuery` inline.
- `EventCard` unconditionally renders `{Math.round(score * 100)}% match`;
  `EventDetailPage` does the same. `matched_because` is already conditional.
- The logged-out calendar path is served from static
  `web/src/api/logged-out-calendar-data.json` and is **not** touched here.

## Target architecture

### 1. SQL — `sql/queries/calendar.sql`

```sql
-- name: GetCityCalendarInRange :many
SELECT
    e.id              AS event_id,
    e.title,
    e.description,
    e.starts_at,
    e.ends_at,
    e.image_url,
    e.url,
    v.name            AS venue_name,
    v.address         AS venue_address
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE v.city_id = $1
  AND e.archived_at IS NULL
  AND e.starts_at >= $2
  AND e.starts_at <  $3
  -- Same showable rule as GetUserCalendarInRange: a date-only event runs until
  -- its local day is out, a timed one until it ends.
  AND event_over_at(e.starts_at, e.ends_at, e.time_tbd) > NOW()
ORDER BY e.starts_at ASC;
```

City comes from `venues.city_id` — `events` has no city column. No
`user_event_match` join and no `user_event_not_interested` filter: that absence
is the feature.

Run `sqlc generate` afterwards. It rewrites `internal/store/models.go` as well
as `calendar.sql.go`; both are committed together.

### 2. Handler — `internal/http/handlers/calendar.go`

Extract the `from`/`to` parsing duplicated in `GetMyCalendar` into a helper so
both handlers share one contract and one set of error codes:

```go
// parseDateRange reads the from/to query params (YYYY-MM-DD). It writes the
// error response and returns ok=false when they are missing or malformed.
func parseDateRange(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool)
```

Error codes stay exactly as today: `missing_range`, `bad_from`, `bad_to`.

```go
// GetCityCalendar returns every non-archived, not-yet-over event in the given
// city whose starts_at falls in [from, to) — no match filtering. Used by the
// calendar page when the user has no interests to match against.
func GetCityCalendar(q *store.Queries) http.HandlerFunc
```

- `chi.URLParam(r, "cityId")` → `uuid.Parse`; on failure 400 `bad_city_id`.
- `parseDateRange`; 5-second context timeout, matching `GetMyCalendar`.
- Maps rows into the existing `calendarEvent` struct with `Score: 0` and
  `MatchedBecause: parseBreakdown(nil)` (empty non-nil slices) — the same
  convention `GetEventByIDForUser` already returns for an unmatched event. No
  new response type; the FE reuses `CalendarEvent`.
- An unknown or event-less city is an empty list with 200, not a 404.

### 3. `/me` — city_id

Add to `userOut`:

```go
CityID string `json:"city_id"`
```

Populated in `GetMe` from `row.CityID` (already selected) and in the signup
response from `row.CityID` (`CreateUser` already returns it), so the field is
never silently `""`. Login is unaffected — it returns no user object.

### 4. Route — `internal/http/server.go`

In the authenticated + confirmed group, beside the other reads:

```go
r.Get("/calendar/{cityId}", handlers.GetCityCalendar(s.Queries))
```

Covered by the group's `authedLimiter` net (120/min/user). No new limiter, so no
new `middleware.Endpoint*` key and no mirrored terraform metric to add.

### 5. FE data layer

`web/src/api/auth.ts` — add `city_id: string` to `User`. This is the whole
plumbing: `useAuth().user?.city_id` is then available with no extra fetch.

`web/src/api/calendar.ts` — new handler beside `getCalendar`, sharing the
`CalendarEvent` type:

```ts
export async function getCityCalendar(
  cityId: string,
  from: string,
  to: string,
): Promise<CalendarEvent[]> {
  const params = new URLSearchParams({ from, to });
  const out = await apiFetch<{ events: CalendarEvent[] }>(
    `/calendar/${cityId}?${params.toString()}`,
  );
  return out.events;
}
```

`web/src/hooks/useCityCalendar.ts` — new directory, first hook:

```ts
export function useCityCalendar(
  cityId: string | undefined,
  from: string,
  to: string,
  enabled: boolean,
) {
  return useQuery<CalendarEvent[]>({
    queryKey: ['city-calendar', cityId, from, to],
    queryFn: () => getCityCalendar(cityId!, from, to),
    enabled: enabled && !!cityId,
    placeholderData: keepPreviousData,
  });
}
```

`enabled` keeps the request from firing before the gate resolves or while the
city is unknown; `keepPreviousData` matches the calendar query so switching the
range toggle does not blank the list.

### 6. `CalendarPage` orchestration

Add a manual-interests query alongside the existing Spotify-status one:

```ts
const spotifyQ = useQuery({ queryKey: ['spotify-status', user?.id], queryFn: getSpotifyStatus });
const interestsQ = useQuery({ queryKey: ['manual-interests', user?.id], queryFn: listManualInterests });

// Pending, not `data === undefined`: a *failed* gate query never gets data, and
// waiting on data would spin forever. isPending clears on error too.
const gatePending = spotifyQ.isPending || interestsQ.isPending;
const showCity =
  !gatePending && spotifyQ.data?.connected === false && interestsQ.data?.length === 0;
```

- `useCityCalendar(user?.city_id, from, to, showCity)`.
- **Loading:** the page shows its `Spinner` while `gatePending`, and while the
  city query is loading in fallback mode. Waiting for both gate queries is what
  prevents a flash of the matched calendar's empty state before the fallback
  takes over.
- **Gate query failure:** the optional chaining leaves `showCity` false, so the
  page renders the matched calendar exactly as today — a failed status check
  degrades to current behavior rather than to a city-wide list, and never to a
  stuck spinner.
- **Rendering when `showCity`:** title "Everything happening in Seattle"; the
  explainer banner above the list; `EventCard`s with **no** `onNotInterested`
  (still `interactive`). The range toggle drives `from`/`to` for both queries
  unchanged.
- **Rendering otherwise:** identical to today, including the current empty state
  and its CTAs.
- **Empty city result:** "Nothing on the calendar in Seattle right now." — a
  distinct message, since the personalization CTAs are already in the banner.
- The `/me/calendar` query keeps running unconditionally. It is cheap, its
  result is what "no matches" means, and gating it would add a second
  sequencing dependency for no benefit.

The banner reuses the existing `connectSpotifyMut` handler and the `/interests`
link that today's empty state already has, so no new API surface on the FE.

### 7. `EventCard` / `EventDetailPage`

Gate the score chip on `event.score > 0` in both. Matched events always score
above the user's threshold, which is `> 0`, so this cleanly separates "matched"
from "no match" without a new field on the response. Nothing else changes;
`matched_because` is already conditional on being non-empty.

## Data flow: a user with no interests loads the calendar

1. `CalendarPage` mounts; `/me/calendar`, `/integrations/spotify/status`, and
   `/me/manual-interests` all fire. Spinner while the gate queries are pending.
2. Status returns `{connected: false}`, interests returns `[]` → `showCity`
   flips true → `useCityCalendar` enables and requests
   `/calendar/{user.city_id}?from&to`.
3. The city list renders under the "Everything happening in Seattle" title with
   the explainer banner; cards show no `% match` chip and no "Not interested".
4. The user connects Spotify or adds an interest. That invalidates
   `['spotify-status']` / `['interests']`, `showCity` flips false, the
   city query goes idle, and the matched calendar renders — no reload needed.

## Testing (TDD)

**Go** (`internal/http/handlers/calendar_test.go`, real `testdb`):

- Returns an event that has **no** `user_event_match` row for the caller.
- Returns events regardless of score, including one below the caller's threshold.
- Excludes: another city's venue, an archived event, an event already over, one
  outside `[from, to)`.
- Includes an event the caller marked not-interested (decision 3 — this is the
  guard against someone "helpfully" adding the filter later).
- 400 on a non-UUID `cityId`; 400 on missing/malformed `from`/`to`.
- 401 without a token (route sits behind `RequireAuth`).
- Unknown city → `{"events":[]}` with 200.

**Go** (`user_test.go`): `/me` includes the user's `city_id`.

**FE**:

- `getCityCalendar` requests `/calendar/{id}?from&to` and unwraps `.events`.
- `useCityCalendar` performs no fetch when `enabled` is false or `cityId` is
  undefined.
- `EventCard` and `EventDetailPage` hide the chip at `score: 0`, still show it
  at `score: 0.8`.
- `CalendarPage`: fallback path (status `connected:false` + `interests: []`)
  renders city events, the banner, the new title, and no "Not interested"
  button; connected-Spotify path and has-interests path both render the matched
  calendar with no city request; a **failing** status query renders the matched
  calendar (not a stuck spinner, not the city list).
- **Migration:** `CalendarPage.test.tsx` currently mocks neither `../api/spotify`
  nor `../api/manualInterests`. Both must be mocked so the gate resolves
  deterministically; without it the existing tests would leave the page on its
  spinner.

## Out of scope / non-goals

- The logged-out static-JSON calendar path is unchanged.
- The city name stays hardcoded "Seattle" in the FE; no city-name field is added
  to the API.
- No caching layer or pagination on the new endpoint; the `from`/`to` window is
  the only bound.
- No change to matching, thresholds, or `user_event_match`.
