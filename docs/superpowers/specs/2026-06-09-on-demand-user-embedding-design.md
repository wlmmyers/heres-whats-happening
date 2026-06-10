# On-demand user embedding

## Problem

`NewUserEmbedder(...).Run` (`internal/matcher/user_embedder.go`) is reached
**only** through `matcher.Job.Run` (`internal/matcher/job.go:33`), which is
called from exactly one place: `runMatch()` in `cmd/app/main.go`, the `match`
CLI subcommand. That subcommand is the once-per-day batch job. No HTTP handler,
and nothing in the long-running `serve` process, ever triggers user embedding.

As a result, the two events that create or change a user's interests produce no
embedding until the next daily batch:

- **Spotify connect** → `SpotifyExchange` handler publishes an
  `events.InterestMessage` to the interests queue → `ingest.InterestHandler`
  (async consumer) writes `user_interests` rows. No re-embed.
- **Manual interest** → `CreateInterest` / `DeleteInterest` handlers write a
  `manual_tag` row directly. No re-embed.

New users can therefore wait up to a day for an `interest_embedding`, during
which they get no matches. We want embedding to happen promptly after either
event.

Note: `SelectUsersNeedingEmbedding` (`sql/queries/users.sql:26`) already
self-scopes — it selects users whose `interest_embedding_updated_at` is older
than their newest `user_interests.updated_at` (or null). The embedder is
idempotent and only does work for users who need it, which makes targeted
on-demand embedding cheap and the daily batch a reliable backstop.

## Approach

Both interest-change paths converge on the **interest consumer** performing a
**targeted, single-user** embed, off the HTTP request path. The API server is
not given a TEI client; only the consumer process embeds.

- **Spotify path:** `InterestHandler.Handle` already writes the user's interest
  rows in the consumer. After the rows are written, it embeds that one user
  inline, before the SQS message is deleted. Write and embed therefore succeed
  or get retried together (a non-nil error leaves the message on the queue).
- **Manual path:** `CreateInterest` / `DeleteInterest` continue to write the
  `manual_tag` row synchronously and return it (no API contract change). They
  then publish a lightweight **embed-only** message to the existing interests
  queue. The consumer picks it up and embeds the user.

### Message discriminator

One queue carries two kinds of work, distinguished by a new `Op` field on
`events.InterestMessage`:

| `Op` value                      | Behavior |
|---------------------------------|----------|
| `"replace_interests_and_embed"` | Existing behavior: replace the user's Spotify-derived rows, **then** embed the user. Used by the Spotify scraper. |
| `"only_embed"`                  | Skip all row replacement; just embed the user. Used by the manual-interest handlers. |

**Backward compatibility:** a message with an empty `Op` (any Spotify snapshot
already in flight at deploy time) is treated as `"replace_interests_and_embed"`.
New code always sets `Op` explicitly.

The manual path cannot reuse a normal snapshot message: it has no Spotify data,
and a snapshot with empty lists would wipe the user's Spotify interests. Hence
the dedicated `"only_embed"` op.

## Components

### `matcher.UserEmbedder` — add `EmbedUser`

Extract the per-user text-build + embed + update logic currently inlined in
`Run` into a private helper that operates on a given set of user IDs. Both
methods call it:

- `Run(ctx)` — unchanged behavior: selects stale users via
  `SelectUsersNeedingEmbedding`, then delegates to the helper.
- `EmbedUser(ctx, userID)` — embeds a single user unconditionally (the message
  is itself the staleness signal). Like `Run`, it skips a user whose built
  interest text is empty (no rows written, embedding left as-is).

The helper reuses `ListUserInterestsBatch` (already takes a slice of IDs),
`BuildUserText`, `foldDeduped`, and `UpdateUserInterestEmbedding` exactly as
`Run` does today — no SQL changes required.

### `ingest.InterestHandler` — add an embedder and branch on `Op`

- New field: `emb matcher.Embedder` (the TEI client; may be nil).
- `Handle` unmarshals the message, then:
  - `"only_embed"` → call `EmbedUser(userID)` and return.
  - `"replace_interests_and_embed"` / empty → run the existing replace logic,
    then call `EmbedUser(userID)`.
- If `emb` is nil (no TEI configured), the embed step is skipped without error.

`EmbedUser` here is invoked via a small embedder/owner the handler holds; the
handler constructs a `matcher.UserEmbedder` (or calls an equivalent function)
using its `*store.Queries` and the injected `Embedder`.

### `handlers.CreateInterest` / `DeleteInterest` — publish an embed message

Both gain two parameters: a `CallbackPublisher` (already defined in
`handlers/spotify.go`) and the interests queue URL — both already held by
`http.Server` as `QueuePublisher` and `InterestsQueueURL`. After the DB write
succeeds, the handler publishes an `events.InterestMessage{UserID, Op:
"only_embed"}`. Publishing is best-effort: if the publisher is nil or the queue
URL is empty (local dev without SQS), the handler skips publishing and relies on
the daily batch.

### `cmd/app/main.go serve()` — wire TEI into the consumer

`serve()` builds a `tei.New(cfg.TEIEndpoint)` client when `cfg.TEIEndpoint` is
set and passes it to `ingest.NewInterestHandler`. When `TEIEndpoint` is empty,
the handler receives a nil embedder and degrades as described above.

The router wiring in `internal/http/server.go` passes `s.QueuePublisher` and
`s.InterestsQueueURL` into the two interest handlers.

## Data flow

```
Spotify connect:
  SpotifyExchange handler
    -> publish InterestMessage{Op: replace_interests_and_embed, ...}  (interests queue)
    -> InterestHandler.Handle: replace Spotify rows -> EmbedUser(uid)

Manual interest add/remove:
  CreateInterest/DeleteInterest handler
    -> write/delete manual_tag row (sync, returned to client)
    -> publish InterestMessage{Op: only_embed, UserID}               (interests queue)
    -> InterestHandler.Handle: EmbedUser(uid)

Daily backstop (unchanged):
  match CLI -> Job.Run -> UserEmbedder.Run (all stale users)
```

## Error handling & degradation

- **Embed failure in the consumer:** `Handle` returns the error; the SQS message
  is not deleted and is retried. For the Spotify path this means the row write
  is retried too (the replace is idempotent), which is acceptable.
- **No TEI configured** (`TEIEndpoint==""`): consumer skips embedding, no error.
  Daily batch is the backstop.
- **No publisher / queue URL** (local dev without SQS): manual handlers skip
  publishing, no error. Daily batch is the backstop.
- **Single-process note:** in `serve()` the HTTP server and the interest
  consumer run in the same process, so the manual path publishes to a queue it
  also drains. The queue is retained (rather than a direct in-process call) for
  durability, retry, consistency with the Spotify path, and to keep the two
  deployable separately later.

## Known limitation (pre-existing, not addressed here)

Deleting a user's *last* interest yields empty interest text, so `EmbedUser`
skips and the previous embedding lingers rather than being cleared. This matches
the current daily-batch behavior and is out of scope for this change; noted so
it is a conscious decision rather than an oversight.

## Testing

- `matcher`: unit-test `EmbedUser` using the existing mock embedder from
  `user_embedder_test.go` — verify a single user's embedding is written, and
  that an empty-interest user is skipped. Verify `Run` still behaves as before
  after the helper extraction.
- `ingest`: test `InterestHandler.Handle` for both `Op` values — `only_embed`
  embeds without touching interest rows; `replace_interests_and_embed` replaces
  rows then embeds; empty `Op` behaves as replace-and-embed; nil embedder skips
  the embed without error.
- `handlers`: test `CreateInterest` and `DeleteInterest` publish an
  `only_embed` message via a fake publisher, and that they succeed (no error)
  when the publisher is nil.

## Out of scope

- No new SQS queue or Terraform changes (reusing the interests queue).
- No change to the daily `match` batch job.
- No clearing of embeddings when interests drop to empty (see limitation).
