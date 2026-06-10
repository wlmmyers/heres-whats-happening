# On-demand Match After Embed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After the interest consumer re-embeds a user, immediately recompute that user's matches so a newly Spotify-connected (or interest-changed) user sees matched events within seconds instead of waiting for the nightly batch.

**Architecture:** Extend `InterestHandler`'s post-interest-change step from "embed" to "embed, then match", both scoped to the single user, by replacing the consumer's `embedUser` method with `embedAndMatch`. The match reuses the existing `matcher.RescoreUser` helper (which composes `LoadConfig(Defaults())` + `NewMatchStepForUser(...).Run()`). Both steps are idempotent, so a returned error safely redelivers the SQS message and re-runs the whole recompute.

**Tech Stack:** Go, pgx/pgvector, AWS SQS consumer, sqlc `store` queries, testify, Postgres-backed `testdb` harness.

**Spec:** `docs/superpowers/specs/2026-06-10-on-demand-match-design.md`

---

## File Structure

- `internal/ingest/interests.go` — replace the `embedUser` method with `embedAndMatch`, which embeds then calls `matcher.RescoreUser`. `Handle`'s final line changes to call `embedAndMatch`. No struct/constructor changes; `matcher` is already imported.
- `internal/ingest/interests_test.go` — add two DB-backed tests proving the consumer now writes a `user_event_match` row (and that a nil embedder writes none).

No other files change. The API handlers, message format, `serve()` wiring, and the Spotify scraper are untouched.

---

## Task 1: Embed → match in the interest consumer

**Files:**
- Modify: `internal/ingest/interests.go` (the `embedUser` method and `Handle`'s final return)
- Test: `internal/ingest/interests_test.go`

### Context for the implementer

- The consumer's `InterestHandler.Handle` already, for both message kinds, ends by calling `h.embedUser(ctx, pgUID)`. The current method is:

  ```go
  // embedUser re-embeds the user via the matcher's single-user path. If no
  // embedder is configured (no TEI endpoint), embedding is skipped — the daily
  // match batch is the backstop.
  func (h *InterestHandler) embedUser(ctx context.Context, pgUID pgtype.UUID) error {
  	if h.emb == nil {
  		return nil
  	}
  	if err := matcher.NewUserEmbedder(h.q, h.emb).EmbedUser(ctx, pgUID); err != nil {
  		return fmt.Errorf("embed user: %w", err)
  	}
  	return nil
  }
  ```

- `matcher.RescoreUser(ctx, q, userID)` (`internal/matcher/rescore.go:54`) loads config and runs the single-user match step. It does NOT re-embed. Signature: `func RescoreUser(ctx context.Context, q *store.Queries, userID pgtype.UUID) error`.
- `matcher.NewMatchStepForUser(...).Run` writes `user_event_match` rows for the user for every upcoming event scoring above threshold (default `score_threshold` is 0.3).
- The `Score` function: `total = 0.6*stringScore + 0.4*embedScore`. `embedScore = (cosine+1)/2`. The ingest test's `newFakeEmbedder()` maps every input to a constant 384-dim 0.1 vector; a parallel event embedding gives cosine 1.0 → `embedScore = 1.0` → 0.4 from embedding alone, already above 0.3.
- `testdb.MustOpen(t)` returns a `*pgxpool.Pool` over a migrated DB (reference rows like the default city and the `ticketmaster` event source are seeded). Tests assert match rows via a raw `pool.QueryRow`, the same pattern used in `internal/matcher/match_step_test.go`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ingest/interests_test.go`. Add `"github.com/pgvector/pgvector-go"` to the file's imports (the other identifiers used below — `context`, `encoding/json`, `testing`, `time`, pgx `pgtype`, testify `require`, and the internal `events`, `ingest`, `store`, `testdb` packages, plus the `newFakeEmbedder()` helper and `pgtypeUUIDToString` helper — already exist in this file).

```go
func TestInterestHandler_ReplaceEmbedAndMatch_WritesMatchRow(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "embed-match@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	// Seed an upcoming event: matching performer + a parallel embedding so the
	// recomputed match clears the default 0.3 threshold.
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V", NormalizedName: "v",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "embed-match-1",
		Title:         "PB Live",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))
	// newFakeEmbedder() embeds every input to a constant 0.1 vector; a parallel
	// event vector gives cosine 1.0, so embedding score alone (0.4) clears 0.3.
	eventVec := make([]float32, 384)
	for i := range eventVec {
		eventVec[i] = 0.1
	}
	ev := pgvector.NewVector(eventVec)
	require.NoError(t, q.UpdateEventEmbedding(ctx, store.UpdateEventEmbeddingParams{
		ID: eventID, Embedding: &ev,
	}))

	emb := newFakeEmbedder()
	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, emb)
	require.NoError(t, h.Handle(ctx, body))

	// A match row was written for this user/event by the consumer.
	var score float64
	err = pool.QueryRow(ctx,
		`SELECT score FROM user_event_match WHERE user_id = $1 AND event_id = $2`,
		userRow.ID, eventID).Scan(&score)
	require.NoError(t, err)
	require.Greater(t, score, 0.3)
}

func TestInterestHandler_NilEmbedder_WritesNoMatchRows(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	userRow, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "nil-emb-match@example.com",
		PasswordHash: "stub",
		CityID:       city.ID,
	})
	require.NoError(t, err)

	// Seed an event that WOULD match if matching ran.
	src, err := q.GetEventSourceByName(ctx, "ticketmaster")
	require.NoError(t, err)
	venueID, err := q.UpsertVenue(ctx, store.UpsertVenueParams{
		CityID: city.ID, Name: "V2", NormalizedName: "v2",
	})
	require.NoError(t, err)
	eventID, err := q.UpsertEvent(ctx, store.UpsertEventParams{
		SourceID:      src.ID,
		SourceEventID: "nil-emb-match-1",
		Title:         "PB Live 2",
		StartsAt:      pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		VenueID:       venueID,
	})
	require.NoError(t, err)
	require.NoError(t, q.InsertEventPerformer(ctx, store.InsertEventPerformerParams{
		EventID: eventID, PerformerName: "Phoebe Bridgers", NormalizedName: "phoebe bridgers",
	}))

	body, _ := json.Marshal(&events.InterestMessage{
		UserID: pgtypeUUIDToString(t, userRow.ID),
		Op:     events.OpReplaceInterestsAndEmbed,
		SpotifyTopArtists: []events.SpotifyTopItem{
			{Name: "Phoebe Bridgers", Rank: 1},
		},
	})
	h := ingest.NewInterestHandler(q, nil) // no TEI → skip embed AND match
	require.NoError(t, h.Handle(ctx, body))

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_event_match WHERE user_id = $1`,
		userRow.ID).Scan(&count))
	require.Equal(t, 0, count)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ingest/ -run 'TestInterestHandler_ReplaceEmbedAndMatch_WritesMatchRow' -v`
Expected: FAIL — the consumer embeds but does not yet match, so `SELECT score FROM user_event_match ...` returns `pgx.ErrNoRows` and `require.NoError(t, err)` fails.

(The `NilEmbedder_WritesNoMatchRows` test passes already — it is a regression guard confirming the nil-embedder path never matches.)

- [ ] **Step 3: Implement `embedAndMatch`**

In `internal/ingest/interests.go`, replace the `embedUser` method with `embedAndMatch`:

```go
// embedAndMatch re-embeds the user, then recomputes their matches against
// upcoming events — both scoped to this one user. If no embedder is configured
// (no TEI endpoint), it skips both; the daily match-job is the backstop.
//
// Returning an error leaves the SQS message for redelivery. EmbedUser (writes
// interest_embedding) and RescoreUser (per-user match upsert + scoped prune) are
// both idempotent, so the whole recompute re-runs safely on retry.
func (h *InterestHandler) embedAndMatch(ctx context.Context, pgUID pgtype.UUID) error {
	if h.emb == nil {
		return nil
	}
	if err := matcher.NewUserEmbedder(h.q, h.emb).EmbedUser(ctx, pgUID); err != nil {
		return fmt.Errorf("embed user: %w", err)
	}
	if err := matcher.RescoreUser(ctx, h.q, pgUID); err != nil {
		return fmt.Errorf("match user: %w", err)
	}
	return nil
}
```

Then update `Handle`'s final line from:

```go
	return h.embedUser(ctx, pgUID)
```

to:

```go
	return h.embedAndMatch(ctx, pgUID)
```

`matcher`, `fmt`, and `pgtype` are already imported in this file; `matcher.RescoreUser` and `matcher.NewUserEmbedder` already exist. No import changes needed.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ingest/ -run 'TestInterestHandler' -v`
Expected: PASS — both new tests and the three pre-existing `TestInterestHandler_*` tests pass (the pre-existing tests seed no events, so the added match step is a no-op for them and their assertions are unchanged).

Then run the full suite to confirm nothing else regressed:

Run: `go build ./... && go test ./...`
Expected: all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/interests.go internal/ingest/interests_test.go
git commit -m "Match user on-demand after embed in interest consumer"
```
End the commit message with a trailing blank line then:
`Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

---

## Verification

- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] End-to-end trace: a `replace_interests_and_embed` message for a user with a matching upcoming event results in a `user_event_match` row (covered by `TestInterestHandler_ReplaceEmbedAndMatch_WritesMatchRow`), and a nil-embedder message produces none (`TestInterestHandler_NilEmbedder_WritesNoMatchRows`).

## Notes / decisions (from the spec)

- **Fail → retry both:** match failure returns an error, redelivering the message; both steps are idempotent so re-running is safe and yields the same end state.
- **nil embedder skips both:** without TEI there is no fresh embedding to match on; the daily batch backstops.
- **Reuses `matcher.RescoreUser`:** this existing helper is exactly `LoadConfig(Defaults())` + `NewMatchStepForUser(...).Run()` — the two steps the spec describes. Using it keeps the consumer DRY (the same helper the API uses after a threshold change).
- **No archiver / no event embedding / no global recompute** on the on-demand path — those remain owned by the nightly `Job`. The single-user `MatchStep.Run` does invoke the global `DeleteObsoleteMatches` cleanup internally; that is pre-existing behavior, unchanged.
