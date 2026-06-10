# On-demand match after on-demand embed

## Problem

The on-demand-user-embedding feature (see
`2026-06-09-on-demand-user-embedding-design.md`) re-embeds a user's
`interest_embedding` promptly after they connect Spotify or change manual
interests, via the interest consumer. But matches are still only produced by the
once-daily `match` batch job (`runMatch` → `Job.Run` → `MatchStep`). So a newly
Spotify-connected user has a fresh embedding within seconds, yet sees no matched
events until the nightly run — up to a day later.

We want matches generated immediately too: after `EmbedUser` succeeds in the
consumer, run a single-user match step for that user.

## Existing building blocks (no new query/SQL needed)

- `matcher.NewMatchStepForUser(q, cfg, userID)` (`internal/matcher/match_step.go:31`)
  already scopes the match to one user, scored against all upcoming events. Its
  `Run` upserts above-threshold rows into `user_event_match` and prunes that
  user's stale matches — both idempotent. It loads the user via
  `GetUserForMatching` and returns a no-op when the user has no row.
- `matcher.LoadConfig(ctx, q, def)` (`internal/matcher/rescore.go:16`) reads
  `match_config` from the DB with a fallback `Config`. `Job.Run` uses it so
  SQL-tuned scoring values take effect without a restart.
- `matcher.Defaults()` (`internal/matcher/types.go`) provides the fallback config.
- The consumer's `InterestHandler` already holds a `matcher.Embedder` (nil when
  TEI is unconfigured) and calls `EmbedUser` after writing interest rows.

## Approach

Extend the consumer's post-interest-change recompute from **embed** to
**embed, then match**, both scoped to the single user. Replace the current
`embedUser` call at the end of `InterestHandler.Handle` with an
`embedAndMatch(ctx, pgUID)` method:

1. If `h.emb == nil` (no TEI configured) → return nil. Skip both embed and
   match; the daily batch is the backstop. In production TEI is always
   configured, so embed+match always run together.
2. `matcher.NewUserEmbedder(h.q, h.emb).EmbedUser(ctx, pgUID)` — on error,
   return it (message redelivers).
3. `cfg, err := matcher.LoadConfig(ctx, h.q, matcher.Defaults())` — on error,
   return it.
4. `matcher.NewMatchStepForUser(h.q, cfg, pgUID).Run(ctx)` — on error, return it.

This applies to **both** message kinds: a Spotify snapshot
(`replace_interests_and_embed`) and a manual change (`only_embed`) both end with
embed→match for that user.

### Failure handling: fail the message, retry both

Because `Handle` returns a non-nil error to leave the SQS message for
redelivery, a transient failure in either embed or match re-runs the whole
recompute. Both `EmbedUser` (writes `interest_embedding`) and the single-user
`MatchStep` (per-user upsert + scoped prune) are idempotent, so re-running is
safe and produces the same end state. This guarantees the user is matched
at-least-once without waiting for the nightly batch. The cost — a redelivery
re-runs the TEI embed call — is acceptable because redeliveries are rare.

### Config: loaded per message, same as the nightly job

`embedAndMatch` calls `LoadConfig(ctx, h.q, matcher.Defaults())` per message so
on-demand matches use the exact same scoring configuration as the daily batch.
`InterestHandler` gains no config field; the fallback is `matcher.Defaults()`.

## Components

- **`internal/ingest/interests.go`** — rename/replace `embedUser` with
  `embedAndMatch(ctx, pgUID pgtype.UUID) error` implementing the four steps
  above. `Handle`'s final line becomes `return h.embedAndMatch(ctx, pgUID)`.
  Error wrapping: `"embed user: %w"`, `"load match config: %w"`,
  `"match user: %w"`. No struct or constructor changes (`InterestHandler`
  already holds `q` and `emb`).

No changes to:
- The manual-interest API handlers (`CreateInterest`/`DeleteInterest`) — they
  already publish `only_embed`; matching is a consumer-side addition.
- The message format (`events.InterestMessage`).
- `serve()` wiring — the consumer already receives the embedder; it uses the
  same `q` for the match step and config load.
- The Spotify scraper.

## Data flow

```
Spotify connect:
  SpotifyExchange -> publish replace_interests_and_embed
    -> InterestHandler.Handle: replaceInterests -> embedAndMatch
         -> EmbedUser -> LoadConfig -> MatchStepForUser.Run
         -> user_event_match rows appear for the user

Manual interest add/remove:
  CreateInterest/DeleteInterest -> publish only_embed
    -> InterestHandler.Handle: embedAndMatch
         -> EmbedUser -> LoadConfig -> MatchStepForUser.Run

Daily backstop (unchanged):
  match CLI -> Job.Run -> EventEmbedder -> UserEmbedder.Run
            -> MatchStep (all users) -> Archiver
```

## What is deliberately out of scope

- **No EventEmbedder, no Archiver on the on-demand path.** Those are global
  steps the nightly `Job` owns; on-demand we recompute only the one user.
- **No global recompute.** Scope stays a single user, scored against all
  upcoming events (existing `NewMatchStepForUser` behavior).
- **Pre-existing global cleanup is unchanged.** `MatchStep.Run` already calls
  the global `DeleteObsoleteMatches` internally even in single-user mode; this
  change does not alter that.
- **nil-embedder match-on-stale-embedding** is intentionally not done — without
  TEI there is no fresh embedding to match on, so both steps are skipped.

## Error handling summary

| Situation | Behavior |
|-----------|----------|
| No TEI (`emb == nil`) | Skip embed and match, return nil. Daily batch backstops. |
| Embed fails | Return error → message redelivers → embed+match re-run. |
| LoadConfig fails | Return error → message redelivers. |
| Match fails | Return error → message redelivers → embed (idempotent) + match re-run. |
| Malformed message / bad user_id | Existing behavior: log, return nil (message deleted). |

## Testing

DB-backed consumer tests in `internal/ingest/interests_test.go`, using the
`testdb` harness and the existing fake embedder:

1. **End-to-end embed→match writes a match row.** Seed an upcoming event whose
   performer/genre (and/or embedding) scores a test user above threshold. Send a
   message for that user with a matching interest. After `Handle`, assert a row
   exists in `user_event_match` for that (user, event) pair — proving the match
   step ran inside the consumer.
2. **nil-embedder writes no match rows.** With a nil embedder, `Handle` for a
   message must not create any `user_event_match` rows (embed and match both
   skipped), and must not error.
3. Existing `InterestHandler` tests continue to pass; where they used a fake
   embedder with no seeded events, no match rows are produced (matching against
   an empty event set is a no-op), so they remain valid.

The exact seeding (event embedding vector, performer name, threshold) will be
worked out in the implementation plan against the existing `Score`/threshold
logic and the store queries used by `loadEvents`
(`ListUpcomingEventsForMatching`, performer/genre inserts).
