# Per-user match thresholds — design

**Date:** 2026-06-04

## Problem

The match score threshold is a single global value (`match_config.score_threshold`,
default `0.3`) applied to every user in `MatchStep.Run` (`internal/matcher/match_step.go`).
Users have no control over how permissive or strict their event recommendations are.

We want each user to set their own threshold from the Settings page, persist it, and
have their recommended events recomputed on demand when they change it.

## Goals

- Each user can set a personal match threshold, stored in the DB.
- A user's threshold edit triggers an on-demand recompute of just that user's matches.
- A "Match sensitivity" slider on the Settings page, clamped to a sane range.
- A Save action that confirms before recalculating (recalculation rewrites all of the
  user's recommended events).
- A client-facing API endpoint that updates the threshold and kicks off the recompute.

## Non-goals

- No CLI flag for single-user matching. `app match` / `runMatch()` is unchanged
  (all-users behavior). The single-user capability lives in the matcher package and is
  consumed only by the API path.
- No re-embedding on a threshold change — interests and events are unchanged, so existing
  embeddings are reused. No TEI client is added to the `serve` process.
- No polling/progress UI for the async recompute; the frontend invalidates queries
  optimistically.

## Score range context

`Score()` returns `TotalScore = WString*stringScore + WEmbedding*embedScore` where each
sub-score is clamped to `[0,1]` and the weights sum to `1.0`, so the total is in `[0,1]`.
Default threshold `0.3`. **Higher threshold = stricter = fewer matches.**

The slider is clamped to **20%–60% (0.20–0.60)**. The same range is validated server-side.

## Data model

Add a nullable column to `users`:

```sql
ALTER TABLE users ADD COLUMN score_threshold DOUBLE PRECISION;  -- NULL = inherit global default
```

`NULL` means "use the global `match_config.score_threshold`". This keeps the global value
as the live default, so users who never touch the slider automatically track it. The
resolved value used in scoring is `COALESCE(users.score_threshold, <global default>)`.

New migration pair: `sql/migrations/0014_user_score_threshold.{up,down}.sql`.

## Matcher changes (`internal/matcher`)

- `UserProfile` gains `ScoreThreshold *float64` (nil = inherit global). `MatchStep.loadUsers`
  reads the new column.
- In `MatchStep.Run`, the per-pair check uses the user's threshold:
  ```go
  threshold := m.cfg.ScoreThreshold
  if user.ScoreThreshold != nil {
      threshold = *user.ScoreThreshold
  }
  if score.TotalScore <= threshold { continue }
  ```
- `MatchStep` gains an optional single-user filter (e.g. a `userID *pgtype.UUID` field set
  via constructor option). When set:
  - `loadUsers` loads only that user (still scores against all events).
  - The stale-prune (`DeleteStaleMatchesForUsers`) is already scoped by user IDs, so it
    only prunes that user's matches.
  - `DeleteObsoleteMatches` (global, event-archival cleanup) still runs and is harmless.
- Extract the current private `Job.loadConfig` into a shared package function
  `matcher.LoadConfig(ctx, q, Defaults()) (Config, error)`, reused by both `Job` and the
  API re-score path so both read `match_config` identically.

### Execution paths (shared scoring core)

- **CLI** `app match` → `runMatch()`: unchanged, full Job over all users.
- **API in-process re-score**: builds a single-user `MatchStep` directly (no embedders, so
  no TEI client needed) using `matcher.LoadConfig`, and runs it. Existing embeddings are
  reused.

A small helper, e.g. `matcher.RescoreUser(ctx context.Context, q *store.Queries, userID pgtype.UUID) error`,
encapsulates: load config → build single-user `MatchStep` → `Run`.

## SQL / sqlc

- `ListActiveUsersForMatching`: add `score_threshold` to the selected columns; add a
  single-user variant (or a parameterized filter) for the scoped run.
- `GetUserByID`: add `score_threshold` so `GetMe` can return the resolved value.
- New `UpdateUserScoreThreshold`:
  ```sql
  -- name: UpdateUserScoreThreshold :exec
  UPDATE users SET score_threshold = $2 WHERE id = $1 AND deleted_at IS NULL;
  ```
- Regenerate sqlc (`internal/store`).

## API endpoint + handler

```
PATCH /me/match-threshold      (authenticated)
  body: { "threshold": 0.45 }
```

Handler (`internal/http/handlers/user.go` or a new `match_threshold.go`):

1. Resolve `uid` from auth context.
2. Decode body; validate `0.20 <= threshold <= 0.60`, else `400 invalid_threshold`.
3. `UpdateUserScoreThreshold(uid, threshold)` (short request-scoped timeout). On DB error → `500`.
4. Spawn a background goroutine that runs `matcher.RescoreUser`. It uses
   **`context.Background()` with its own timeout** (NOT the request context, which is
   cancelled when the response returns), and a `*store.Queries` built on the server's pool.
   Errors are logged (no place to surface them post-202).
5. Respond `202 Accepted`.

`GetMe` (`userOut`) gains `score_threshold float64` — the resolved value (stored value, or
the global default if `NULL`) — so the slider initializes correctly. The handler resolves
the default via `matcher.LoadConfig`/`Defaults` or by reading `match_config`.

Route registered in `internal/http/server.go` under the authenticated group. The handler
needs access to the pool (for the goroutine's `*store.Queries`); the `Server` already holds
`DB *pgxpool.Pool`, so the route wires `handlers.UpdateMatchThreshold(s.Queries, s.DB)`
(or equivalent) with no new `Server` fields.

## Frontend

### `web/src/components/ConfirmDialog.tsx` (new, reusable)

```tsx
interface Props {
  open: boolean;
  title?: string;
  message: string;
  confirmLabel?: string;   // default "Confirm"
  cancelLabel?: string;    // default "Cancel"
  onConfirm: () => void;
  onCancel: () => void;    // also fired by backdrop click
}
```

- Renders nothing when `open` is false.
- Backdrop: `fixed inset-0` semi-transparent overlay (`bg-black/40`), flex-centered.
  Clicking the backdrop calls `onCancel`.
- Dialog: white, rounded, padded, `w-[400px] max-w-[90%]`, centered. `stopPropagation` on
  the dialog so clicks inside don't close it.
- Buttons: a row centered at the bottom — `Cancel` (outline, matching existing button
  styles) and `Confirm` (filled, `bg-blue-600`).

### `web/src/api/` — `updateMatchThreshold`

New api function `updateMatchThreshold(threshold: number)` → `PATCH /me/match-threshold`.
`GetMe`/`me` query type extended with `score_threshold`.

### `SettingsPage.tsx` — "Match sensitivity" section

- A `<input type="range">` min=20 max=60 step=1 (percent), live `NN%` label, helper text:
  "Lower = more events, higher = stricter."
- Initialize from `me.score_threshold * 100`. Local state tracks the slider value; **Save**
  button enabled only when changed from the loaded value.
- Save → `setConfirmOpen(true)`.
- `ConfirmDialog` `onConfirm` → run the `updateMatchThreshold` mutation, then close. Message:
  "Updating your match threshold will recalculate all of your recommended events. Continue?"
- `onCancel`/backdrop → close, no-op.
- On mutation success → inline note/toast ("Threshold updated — your events are being
  recalculated") and invalidate the `['me']` and calendar query keys so the UI refreshes
  once recompute lands.

## Testing

- **matcher:** unit test that `MatchStep` honors a per-user `ScoreThreshold` (a pair above
  the global default but below the user's value is excluded, and vice-versa); test the
  single-user filter scopes loading + pruning to one user.
- **handler:** `match_threshold_test.go` — valid update returns 202 and persists the value;
  out-of-range values return 400; unauthenticated returns 401. (Recompute goroutine can be
  verified via the persisted threshold + a direct matcher test rather than racing the
  goroutine.)
- **GetMe:** returns the stored threshold, and the global default when `NULL`.
- **web:** `ConfirmDialog` test (backdrop click cancels, Confirm fires `onConfirm`);
  SettingsPage test that Save opens the dialog and confirming calls the api.

## Summary of changes

| Layer | Change |
|---|---|
| DB | `users.score_threshold DOUBLE PRECISION` nullable (migration 0014) |
| SQL/sqlc | `UpdateUserScoreThreshold`; `score_threshold` in `ListActiveUsersForMatching` (+ single-user variant) and `GetUserByID` |
| matcher | per-user threshold in `MatchStep`; single-user filter; shared `LoadConfig`; `RescoreUser` helper |
| API | `PATCH /me/match-threshold` (validate 0.20–0.60) → update + async re-score goroutine; `GetMe` returns `score_threshold` |
| web | `ConfirmDialog` component; Settings "Match sensitivity" slider (20–60%) + Save + confirm + `updateMatchThreshold` api fn |
