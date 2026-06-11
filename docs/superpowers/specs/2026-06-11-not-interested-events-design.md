# "Not interested" events — design

**Date:** 2026-06-11

## Problem

A user's matched calendar (`GET /me/calendar`) shows every event the matcher scored
for them. There is no way for a user to say "I don't care about this one" and have it
stay out of their calendar. We want a per-user "not interested" list that hides specific
events from the calendar, a one-click control on each `EventCard`, and a way to reset the
whole list from Settings.

## Goals

- A user can mark a specific event "not interested" from its `EventCard` on the Calendar page.
- The dismissal is persisted per user in a new DB table linking users to events.
- `GET /me/calendar` excludes any event on the requesting user's not-interested list.
- A user can reset (clear) their entire not-interested list, exposed as an API endpoint
  and triggered from a button on the Settings page.
- Clicking "not interested" invalidates the client-side `['calendar']` query cache so the
  calendar is re-pulled from the (now-filtered) API.

## Non-goals

- **No matcher changes.** The nightly match batch may still compute/insert a
  `user_event_match` row for a dismissed event; it stays hidden because the calendar filter
  is independent of matches. Dismissals therefore survive re-matching.
- **No change to `GET /events/{id}`.** A dismissed event is still reachable by direct link
  (e.g. an existing bookmark); only the calendar listing filters it out.
- **No per-event undo** beyond the global reset. There is no "show again" on a single event.
- **No server-side HTTP cache.** "Invalidate the cache" refers to the client-side React
  Query cache; this codebase has no server response cache.

## Approach

A new `user_event_not_interested(user_id, event_id)` join table records dismissals. The
`/me/calendar` query gains a `NOT EXISTS` clause against it. This matches the requested
"new DB table" shape and is robust: dismissals are decoupled from `user_event_match`, so
they are not lost when match rows are recomputed or pruned
(`DeleteStaleMatchesForUsers`), and "reset" is a single `DELETE ... WHERE user_id = $1`.

Rejected alternative: a `dismissed` boolean on `user_event_match`. Simpler join, but the
flag is lost when the match row is recomputed/pruned, it cannot hide an event the user has
no match row for, and it does not satisfy the "new table" requirement.

## Data model

New migration pair `sql/migrations/0016_user_event_not_interested.{up,down}.sql`.

```sql
CREATE TABLE user_event_not_interested (
    user_id    UUID NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, event_id)
);
```

The composite primary key makes dismissals idempotent and gives the lookup index. Both FKs
cascade on delete, so removing a user or an event auto-cleans the rows. The `.down.sql`
drops the table.

## SQL queries (sqlc)

New file `sql/queries/event_not_interested.sql`:

```sql
-- name: AddNotInterested :exec
INSERT INTO user_event_not_interested (user_id, event_id)
VALUES ($1, $2)
ON CONFLICT (user_id, event_id) DO NOTHING;

-- name: ClearNotInterested :exec
DELETE FROM user_event_not_interested
WHERE user_id = $1;
```

Modify the existing `GetUserCalendarInRange` (`sql/queries/calendar.sql`) to add, inside
its `WHERE`:

```sql
  AND NOT EXISTS (
      SELECT 1 FROM user_event_not_interested ni
      WHERE ni.user_id = m.user_id AND ni.event_id = e.id
  )
```

Regenerate `internal/store` with sqlc after the query changes.

## API endpoints

New handler file `internal/http/handlers/not_interested.go`, registered inside the
authenticated group in `internal/http/server.go`, following the existing
`POST/DELETE /me/ical-token` pattern.

- **`POST /me/not-interested`** — body `{ "event_id": "<uuid>" }`.
  - Missing/invalid UUID → `400 bad_event`.
  - Unknown event (FK violation, pg code `23503`) → `400 unknown_event`.
  - Success (including a repeat dismissal, which is a no-op via `ON CONFLICT`) → `204`.
- **`DELETE /me/not-interested`** — clears the caller's list → `204`.

`GET /me/calendar` needs no handler change; the filtering lives entirely in its SQL.

## Frontend

### `web/src/api/notInterested.ts` (new)

```ts
import { apiFetch } from './client';

export async function markNotInterested(eventId: string): Promise<void> {
  await apiFetch<void>('/me/not-interested', { method: 'POST', body: { event_id: eventId } });
}

export async function resetNotInterested(): Promise<void> {
  await apiFetch<void>('/me/not-interested', { method: 'DELETE' });
}
```

`apiFetch` already returns `undefined` on `204`, so these resolve cleanly to `void`.

### `EventCard.tsx`

Currently the whole card is a single `<Link>`. To add a real `<button>` without nesting
interactive content inside an `<a>` (invalid HTML), restructure:

- Outer element becomes a `<div>` with the card styling.
- Title / date / matched-because content stays wrapped in the `<Link to={/events/:id}>`.
- A **"Not interested"** button is a sibling row at the bottom of the card (matches the
  approved mockup).

The button takes a new **optional** prop `onNotInterested?: (id: string) => void` and only
renders when the prop is provided, keeping the component reusable. Its `onClick` calls
`onNotInterested(event.id)` (no navigation concern, since it is no longer inside the link).

### `CalendarPage.tsx`

Owns the mutation, because it knows the active `['calendar', from, to]` query key. Optimistic
removal with rollback, plus invalidate-on-settled to re-pull the server-filtered list:

```ts
const niMut = useMutation({
  mutationFn: (id: string) => markNotInterested(id),
  onMutate: async (id) => {
    await qc.cancelQueries({ queryKey: ['calendar', from, to] });
    const prev = qc.getQueryData<CalendarEvent[]>(['calendar', from, to]);
    qc.setQueryData<CalendarEvent[]>(['calendar', from, to],
      (old) => (old ?? []).filter((e) => e.id !== id));
    return { prev };
  },
  onError: (_e, _id, ctx) => {
    if (ctx?.prev) qc.setQueryData(['calendar', from, to], ctx.prev);
  },
  onSettled: () => qc.invalidateQueries({ queryKey: ['calendar'] }),
});
```

Passes `onNotInterested={(id) => niMut.mutate(id)}` to each `EventCard`. The card disappears
instantly; `onSettled` invalidation refetches so the API is authoritative; `onError`
restores the card if the request fails.

### `SettingsPage.tsx`

Add a "Hidden events" section with a **Reset not-interested list** button. The button opens
the existing `ConfirmDialog`; on confirm it calls `resetNotInterested`, then invalidates the
`['calendar']` query so previously hidden events can reappear.

## Testing (TDD)

**Go** (`internal/http/handlers/not_interested_test.go`, plus a calendar-filter case),
following the existing `calendar_test.go` / `interests_test.go` harness:

- `POST /me/not-interested` with a valid event → `204`; repeat → `204` (idempotent).
- `POST` with a malformed UUID → `400`; with an unknown event id → `400`.
- `DELETE /me/not-interested` → `204` and the list is empty afterward.
- `GetUserCalendarInRange` excludes an event once a not-interested row exists for that user,
  and still returns it for a *different* user.

**Web** (Vitest + Testing Library, matching existing page tests):

- `EventCard` renders the button when `onNotInterested` is provided and invokes it with the
  event id; no button when the prop is absent.
- `CalendarPage`: clicking "Not interested" removes the card optimistically and calls the API.
- `SettingsPage`: the reset button opens the confirm dialog and, on confirm, calls the
  reset endpoint.

## Migration / deploy notes

The new migration applies via the standard path (`make migrate` locally; `make migrate-prod`
in prod, which launches the migrate ECS task). No backfill needed — an absent row simply
means "not dismissed".
