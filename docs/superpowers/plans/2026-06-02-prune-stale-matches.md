# Prune Stale Matches on Recompute — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the matcher recomputes, delete a recomputed user's `user_event_match` rows that were not re-stamped this run (pairs that dropped to/below threshold), so stale matches stop appearing in the calendar API and iCal feed.

**Architecture:** Thread one run timestamp (`runAt`) through the whole match run. Every upsert stamps `computed_at = runAt`; after the upsert loop, delete the recomputed users' matches whose `computed_at < runAt`. Because the stored value and the delete cutoff are the same `runAt`, there is no clock-skew comparison. The delete is scoped to the user IDs processed this run, so a zero-user run is a no-op. The existing `DeleteObsoleteMatches` (archived/past events) is retained as a backstop for inactive users.

**Tech Stack:** Go 1.24+ · sqlc · pgx/v5 · integration tests against real Postgres via `internal/testdb`. No new dependencies.

---

## File Structure

```
.
├── sql/queries/user_event_match.sql          # MODIFY: param'd upsert; ADD DeleteStaleMatchesForUsers
├── internal/store/user_event_match.sql.go     # REGENERATE via sqlc
├── internal/matcher/match_step.go             # MODIFY: thread runAt; call stale-prune
├── internal/matcher/match_step_test.go        # ADD: prune + no-op tests
└── internal/http/handlers/calendar_test.go    # MODIFY: seed helper passes ComputedAt
```

**Boundaries:**
- The SQL/codegen change makes `computed_at` an explicit parameter — a pure structural change with no behavior shift on its own.
- `match_step.go` owns the run timestamp and the prune call.
- Tests live beside the code they exercise; `calendar_test.go` only changes because the upsert param struct gains a field.

---

## Prerequisites

- Postgres dev + test DBs reachable (`make db-up`), migrations applied. No new migration — `user_event_match.computed_at` already exists (migration 0010).
- `sqlc` installed and runnable (used elsewhere in this repo).

---

### Task 1: SQL + codegen — make `computed_at` explicit and add the prune query

This task is a behavior-preserving refactor: the upsert takes `computed_at` as a
parameter (matcher passes the run time), and a new delete query is generated but
not yet wired in. The full suite must stay green.

**Files:**
- Modify: `sql/queries/user_event_match.sql`
- Regenerate: `internal/store/user_event_match.sql.go`
- Modify: `internal/matcher/match_step.go`
- Modify: `internal/http/handlers/calendar_test.go:45-50`

- [ ] **Step 1: Rewrite `sql/queries/user_event_match.sql`**

Replace the entire file with:

```sql
-- name: UpsertUserEventMatch :exec
INSERT INTO user_event_match (user_id, event_id, score, score_breakdown, computed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, event_id) DO UPDATE SET
    score           = EXCLUDED.score,
    score_breakdown = EXCLUDED.score_breakdown,
    computed_at     = EXCLUDED.computed_at;

-- name: DeleteObsoleteMatches :exec
DELETE FROM user_event_match
WHERE event_id IN (
    SELECT id FROM events
    WHERE archived_at IS NOT NULL OR starts_at <= NOW()
);

-- name: DeleteStaleMatchesForUsers :exec
DELETE FROM user_event_match
WHERE user_id = ANY(@user_ids::uuid[])
  AND computed_at < @cutoff;
```

Notes:
- `$5` is the new `computed_at` parameter; `ON CONFLICT` now writes `EXCLUDED.computed_at` instead of `NOW()`.
- `DeleteStaleMatchesForUsers` uses sqlc **named parameters** (`@user_ids`, `@cutoff`) so the generated struct fields are `UserIds []pgtype.UUID` and `Cutoff pgtype.Timestamptz` rather than `Dollar1`. (Bare `ANY($1::uuid[])` generates an opaque `dollar_1` name in this repo — see `ListEventPerformersBatch`.)

- [ ] **Step 2: Regenerate sqlc**

Run: `sqlc generate`
Expected: clean exit. `internal/store/user_event_match.sql.go` now has:
- `UpsertUserEventMatchParams` with a new `ComputedAt pgtype.Timestamptz` field.
- A `DeleteStaleMatchesForUsers(ctx, DeleteStaleMatchesForUsersParams{UserIds, Cutoff})` method.

Confirm the exact generated field names before continuing:

Run: `grep -n "ComputedAt\|UserIds\|Cutoff\|DeleteStaleMatchesForUsersParams" internal/store/user_event_match.sql.go`
Expected: shows `ComputedAt pgtype.Timestamptz`, and a `DeleteStaleMatchesForUsersParams` struct with `UserIds []pgtype.UUID` and `Cutoff pgtype.Timestamptz`. If names differ, use the actual generated names in Steps 4–5.

- [ ] **Step 3: Verify the build fails (call sites now need `ComputedAt`)**

Run: `go build ./...`
Expected: FAIL — `internal/matcher/match_step.go` and `internal/http/handlers/calendar_test.go` construct `UpsertUserEventMatchParams` without the now-required behavior of supplying a non-null `computed_at`. (Go compiles a missing struct field as the zero value, so the build may actually still pass; if it does, the real signal is that an upsert with a zero `ComputedAt` would insert NULL into a NOT NULL column. Proceed to wire the timestamp regardless.)

- [ ] **Step 4: Thread `runAt` through `match_step.go`**

In `internal/matcher/match_step.go`, add `"time"` to the import block:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)
```

Then replace the body of `Run` (from `for _, user := range users {` through the final `return nil`) with:

```go
	// One timestamp for the whole run: every upsert stamps computed_at = runAt.
	// (The stale-prune in Task 2 deletes anything not stamped this run.)
	runAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	for _, user := range users {
		for _, event := range events {
			score := Score(user, event, m.cfg)
			if score.TotalScore <= m.cfg.ScoreThreshold {
				continue
			}
			bd, err := json.Marshal(map[string]any{
				"string_score":       score.StringScore,
				"embedding_score":    score.EmbeddingScore,
				"matched_performers": score.MatchedPerformers,
				"matched_genres":     score.MatchedGenres,
			})
			if err != nil {
				return fmt.Errorf("marshal breakdown: %w", err)
			}
			if err := m.q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
				UserID:         user.UserID,
				EventID:        event.EventID,
				Score:          score.TotalScore,
				ScoreBreakdown: bd,
				ComputedAt:     runAt,
			}); err != nil {
				return fmt.Errorf("upsert match: %w", err)
			}
		}
	}

	if err := m.q.DeleteObsoleteMatches(ctx); err != nil {
		return fmt.Errorf("delete obsolete: %w", err)
	}
	return nil
```

(No prune call yet — that is Task 2. This step only makes `computed_at` explicit.)

- [ ] **Step 5: Update the test seed helper in `calendar_test.go`**

In `internal/http/handlers/calendar_test.go`, the `UpsertUserEventMatch` call in `seedCalendarFixture` (around lines 45-50) gains a `ComputedAt`. Replace:

```go
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userRow.ID,
		EventID:        eventID,
		Score:          0.82,
		ScoreBreakdown: []byte(`{"matched_performers":["Phoebe Bridgers"],"matched_genres":["indie"]}`),
	}))
```

with:

```go
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID:         userRow.ID,
		EventID:        eventID,
		Score:          0.82,
		ScoreBreakdown: []byte(`{"matched_performers":["Phoebe Bridgers"],"matched_genres":["indie"]}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}))
```

`pgtype` and `time` are already imported in that file (verify with `grep -n '"time"\|pgx/v5/pgtype' internal/http/handlers/calendar_test.go`; add any missing import).

- [ ] **Step 6: Build + full suite green**

Run: `go build ./... && make test`
Expected: full suite PASSES. Behavior is unchanged (matches still written; only archived/past matches pruned). The new `DeleteStaleMatchesForUsers` method is generated but unused — that is fine in Go.

- [ ] **Step 7: Commit**

```bash
git add sql/queries/user_event_match.sql internal/store/user_event_match.sql.go internal/matcher/match_step.go internal/http/handlers/calendar_test.go
git commit -m "refactor(matcher): make user_event_match.computed_at an explicit param + add stale-prune query

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Prune dropped matches in `MatchStep.Run`

**Files:**
- Modify: `internal/matcher/match_step.go`
- Modify: `internal/matcher/match_step_test.go`

- [ ] **Step 1: Write the failing prune test**

Append to `internal/matcher/match_step_test.go`. Also add `"github.com/jackc/pgx/v5/pgxpool"` to the import block (the `matchCount` helper needs the pool type).

```go
func matchCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID, eventID pgtype.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1 AND event_id = $2",
		userID, eventID).Scan(&n))
	return n
}

func TestMatchStep_PrunesDroppedMatch(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "prune@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	// Two artist interests: one drives event A, the other event B.
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Phoebe Bridgers", NormalizedValue: "phoebe bridgers", Weight: 1.0,
	}))
	require.NoError(t, q.InsertSpotifyInterest(ctx, store.InsertSpotifyInterestParams{
		UserID: userRow.ID, Kind: "spotify_top_artist",
		Value: "Radiohead", NormalizedValue: "radiohead", Weight: 1.0,
	}))
	// User embedding e_u = [1,0,0,...].
	userVec := make([]float32, 384)
	userVec[0] = 1.0
	uv := pgvector.NewVector(userVec)
	require.NoError(t, q.UpdateUserInterestEmbedding(ctx, store.UpdateUserInterestEmbeddingParams{
		ID: userRow.ID, InterestEmbedding: &uv,
	}))

	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	// Event embedding e_ev = [0,1,0,...] is orthogonal to the user → embedScore
	// 0.5 → 0.4*0.5 = 0.2. With Defaults() an artist match adds 0.6*(1.0/3.0) =
	// 0.2 → total 0.4 > 0.3 threshold; without the artist match only 0.2 < 0.3.
	eventVec := make([]float32, 384)
	eventVec[1] = 1.0
	ev := pgvector.NewVector(eventVec)

	eventA, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "prune-a", Title: "PB Live",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventA, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{ID: eventA, Embedding: &ev}))

	eventB, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "prune-b", Title: "Radiohead Live",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventB, PerformerName: "Radiohead", NormalizedName: "radiohead",
	}))
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{ID: eventB, Embedding: &ev}))

	step := matcher.NewMatchStep(q, matcher.Defaults())

	// Run 1: both events match (artist + embedding).
	require.NoError(t, step.Run(ctx))
	require.Equal(t, 1, matchCount(t, pool, ctx, userRow.ID, eventA))
	require.Equal(t, 1, matchCount(t, pool, ctx, userRow.ID, eventB))

	// User drops "Phoebe Bridgers" → event A loses its artist match and falls
	// below threshold; "Radiohead" (event B) stays above.
	_, err := pool.Exec(ctx,
		"DELETE FROM user_interests WHERE user_id = $1 AND normalized_value = $2",
		userRow.ID, "phoebe bridgers")
	require.NoError(t, err)

	// Run 2: event A is pruned; event B remains.
	require.NoError(t, step.Run(ctx))
	require.Equal(t, 0, matchCount(t, pool, ctx, userRow.ID, eventA), "dropped match should be pruned")
	require.Equal(t, 1, matchCount(t, pool, ctx, userRow.ID, eventB), "still-matching event should remain")
}
```

- [ ] **Step 2: Run the test (FAIL expected)**

Run: `go test ./internal/matcher -run TestMatchStep_PrunesDroppedMatch -v`
Expected: FAIL on the `eventA` assertion after Run 2 — `dropped match should be pruned` (got 1, want 0), because `Run` does not yet delete stale matches.

- [ ] **Step 3: Wire the prune into `Run`**

In `internal/matcher/match_step.go`, collect the processed user IDs and call the prune after the upsert loop. Replace the section that currently reads:

```go
	// One timestamp for the whole run: every upsert stamps computed_at = runAt.
	// (The stale-prune in Task 2 deletes anything not stamped this run.)
	runAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	for _, user := range users {
```

with:

```go
	// One timestamp for the whole run: every upsert stamps computed_at = runAt,
	// and the stale-prune below deletes anything not stamped this run. Using a
	// single value for both write and prune removes any clock-skew ambiguity.
	runAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	userIDs := make([]pgtype.UUID, len(users))
	for i, u := range users {
		userIDs[i] = u.UserID
	}

	for _, user := range users {
```

Then, immediately before the existing `if err := m.q.DeleteObsoleteMatches(ctx); err != nil {` line, insert:

```go
	// Prune matches for the recomputed users that were not re-stamped this run
	// (pairs that dropped to/below threshold). Scoped to processed users so a
	// zero-user run is a no-op.
	if len(userIDs) > 0 {
		if err := m.q.DeleteStaleMatchesForUsers(ctx, store.DeleteStaleMatchesForUsersParams{
			UserIds: userIDs,
			Cutoff:  runAt,
		}); err != nil {
			return fmt.Errorf("delete stale matches: %w", err)
		}
	}

```

(Use the exact field names confirmed in Task 1 Step 2 if they differ from `UserIds`/`Cutoff`.)

- [ ] **Step 4: Run the prune test (PASS expected)**

Run: `go test ./internal/matcher -run TestMatchStep_PrunesDroppedMatch -v`
Expected: PASS.

- [ ] **Step 5: Write the zero-user no-op test**

Append to `internal/matcher/match_step_test.go`:

```go
func TestMatchStep_NoActiveUsers_DoesNotPrune(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, _ := q.GetDefaultCity(ctx)
	userRow, _ := q.CreateUser(ctx, store.CreateUserParams{
		Email: "noprune@example.com", PasswordHash: "stub", CityID: city.ID,
	})
	src, _ := q.GetEventSourceByName(ctx, "ticketmaster")
	venueID, _ := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "VN", NormalizedName: "vn",
	})
	eventID, _ := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID: src.ID, SourceEventID: "noprune-1", Title: "Future Show",
		StartsAt: pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:  venueID,
	})
	// Pre-seed a match with an old computed_at.
	require.NoError(t, q.UpsertUserEventMatch(ctx, store.UpsertUserEventMatchParams{
		UserID: userRow.ID, EventID: eventID, Score: 0.9,
		ScoreBreakdown: []byte(`{}`),
		ComputedAt:     pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour).UTC(), Valid: true},
	}))
	// Soft-delete the user so the run loads zero active users.
	_, err := pool.Exec(ctx, "UPDATE users SET deleted_at = NOW() WHERE id = $1", userRow.ID)
	require.NoError(t, err)

	step := matcher.NewMatchStep(q, matcher.Defaults())
	require.NoError(t, step.Run(ctx))

	// Zero users loaded → stale-prune skipped → the seeded match survives (the
	// event is still upcoming, so DeleteObsoleteMatches leaves it too).
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM user_event_match WHERE user_id = $1", userRow.ID).Scan(&n))
	require.Equal(t, 1, n)
}
```

- [ ] **Step 6: Run the full matcher package + suite**

Run: `go test ./internal/matcher -v && make test`
Expected: all PASS (existing matcher tests, both new tests, and the rest of the suite).

- [ ] **Step 7: Commit**

```bash
git add internal/matcher/match_step.go internal/matcher/match_step_test.go
git commit -m "feat(matcher): prune matches that drop below threshold on recompute

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Implemented in |
|---|---|
| `UpsertUserEventMatch` takes `computed_at` param; `ON CONFLICT` writes `EXCLUDED.computed_at` | Task 1, Steps 1-2 |
| New `DeleteStaleMatchesForUsers(user_ids, cutoff)` query | Task 1, Step 1 |
| `Run` captures one `runAt`, stamps every upsert with it | Task 1 Step 4 + Task 2 Step 3 |
| Per-user scoped prune after the loop, guarded by `len(userIDs) > 0` | Task 2, Step 3 |
| `DeleteObsoleteMatches` retained as backstop | Task 1 Step 4 (kept) |
| Test: threshold drop is pruned, still-matching event remains | Task 2, Step 1 |
| Test: zero-user run is a no-op | Task 2, Step 5 |
| Invariant: recomputed user's matches == current above-threshold set | Task 2 (verified by prune test) |

**Placeholder scan:** none — every code step shows complete code and exact commands.

**Type consistency:**
- `UpsertUserEventMatchParams.ComputedAt` (`pgtype.Timestamptz`) — added in Task 1, used in Task 1 Step 5 and Task 2 Steps 1/5.
- `DeleteStaleMatchesForUsersParams{UserIds []pgtype.UUID, Cutoff pgtype.Timestamptz}` — generated in Task 1 Step 2, used in Task 2 Step 3. Both tasks instruct confirming the exact generated names before use.
- `runAt` is a single `pgtype.Timestamptz` used as both the upsert `ComputedAt` and the prune `Cutoff`.
- `matchCount(t, pool, ctx, userID, eventID)` defined once in Task 2 Step 1, used within the same test.

**Scope check:** single subsystem (matcher recompute + its one query file). Appropriate for one plan.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-02-prune-stale-matches.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
