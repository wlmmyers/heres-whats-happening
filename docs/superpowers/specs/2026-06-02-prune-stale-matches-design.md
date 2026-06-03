# Design — Prune stale matches on recompute

**Date:** 2026-06-02

## Problem

`MatchStep.Run` (`internal/matcher/match_step.go`) recomputes matches by looping
over every active user × every upcoming event, upserting each pair that scores
above the threshold into `user_event_match` (stamping `computed_at = NOW()`).
After the loop it calls only `DeleteObsoleteMatches`, which removes matches whose
event is archived or already past.

The gap: a `(user, event)` pair that scored above threshold in a prior run but
now scores at/below threshold (e.g. the user changed their interests, or the
event's performers/genres changed) is simply skipped by the upsert loop. Its row
is never re-stamped and never deleted. The stale match lingers in
`user_event_match` indefinitely and keeps appearing in the user's calendar API
response and iCal feed.

## Goal

After a match run, each recomputed user's `user_event_match` rows should be
**exactly** the set of upcoming events that currently match them above the
threshold — no stale leftovers.

## Approach

A single run timestamp threads through both the write and the prune.

1. Capture one `runAt := time.Now().UTC()` at the start of `Run`.
2. Every upsert in this run stamps `computed_at = runAt`.
3. Collect the IDs of the users actually recomputed this run.
4. After the loop, delete those users' matches whose `computed_at < runAt` — any
   pair not refreshed this run.

Because the stored value and the delete cutoff are the **same** `runAt`, there is
no clock-skew ambiguity: we never compare the application clock against the DB
clock. Rows written this run have `computed_at == runAt` (not `< runAt`, so they
survive); rows from any prior run have an earlier timestamp and are pruned.

The delete is scoped to the recomputed user IDs (decision: per-user scoped, not a
global sweep). This makes a run that loads zero users a safe no-op and avoids the
failure mode where a transient empty-events load would wipe every match.

## Changes

### 1. `UpsertUserEventMatch` takes `computed_at` as a parameter

`sql/queries/user_event_match.sql` — currently hardcodes `NOW()`:

```sql
-- name: UpsertUserEventMatch :exec
INSERT INTO user_event_match (user_id, event_id, score, score_breakdown, computed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, event_id) DO UPDATE SET
    score           = EXCLUDED.score,
    score_breakdown = EXCLUDED.score_breakdown,
    computed_at     = EXCLUDED.computed_at;
```

Making the timestamp explicit lets the run thread one value through every upsert
and the prune, and makes timestamps deterministic in tests. `MatchStep` is the
only production caller; test seed helpers that call `UpsertUserEventMatch` will be
updated to pass a timestamp.

### 2. New query `DeleteStaleMatchesForUsers`

`sql/queries/user_event_match.sql`:

```sql
-- name: DeleteStaleMatchesForUsers :exec
DELETE FROM user_event_match
WHERE user_id = ANY($1::uuid[])
  AND computed_at < $2;
```

### 3. `MatchStep.Run`

- Capture `runAt := time.Now().UTC()` at the top.
- Build `userIDs []pgtype.UUID` from the loaded users.
- Pass `runAt` as `ComputedAt` to every `UpsertUserEventMatch` call.
- After the upsert loop, if `len(userIDs) > 0`, call
  `DeleteStaleMatchesForUsers(ctx, {UserIds: userIDs, ComputedAt: runAt})`.
- Keep the existing `DeleteObsoleteMatches(ctx)` call.

### 4. Keep `DeleteObsoleteMatches` as a backstop

For active (recomputed) users the new per-user sweep already removes aged-out
event matches (those events are not in the upcoming set, so they are not
re-stamped and fall below the cutoff). `DeleteObsoleteMatches` is retained because
it also cleans matches belonging to *inactive* (soft-deleted) users whose events
archived or passed — those users are not in the recompute set, so the per-user
sweep never touches them. Two clearly-named, clearly-purposed deletes; the
overlap on active users is harmless and defensive.

## Testing

TDD, integration tests against real Postgres via `internal/testdb`, in
`internal/matcher`:

1. **Threshold drop is pruned.** Seed a user and two upcoming events such that
   both score above threshold. Run `MatchStep`. Assert both `user_event_match`
   rows exist. Mutate the user's interests so one event now scores at/below
   threshold. Run again. Assert the dropped event's row is **gone** and the
   still-matching event's row **remains**.
2. **Empty user set is a no-op.** A run that loads zero users must not delete any
   existing matches (guards the `len(userIDs) > 0` condition).

The exact seeding follows the existing `internal/matcher` test patterns and the
`store` query helpers.

## Out of scope

- Cleaning stale feeds for soft-deleted users at the feed/token layer (a
  pre-existing concern, unaffected by this change).
- Any change to the scoring algorithm or threshold.
