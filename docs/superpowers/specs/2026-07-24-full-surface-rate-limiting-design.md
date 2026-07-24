# Full-Surface Rate Limiting

**Date:** 2026-07-24
**Status:** Designed — not yet implemented

Extends [API Rate Limiting](2026-07-22-api-rate-limiting-design.md), which covered the
logged-out auth surface. This design covers everything else: the authenticated API, the
remaining public routes, and a consolidation of the limiter backends onto one implementation.

## Problem

`2026-07-22` rate-limited `POST /auth/signup`, `/auth/login`, and `/auth/refresh` — the
anonymous attack surface. Past that wall nothing is metered. Every route inside the
`RequireAuth` group (`internal/http/server.go:87-108`) is unbounded, and authentication is a
weak gate for the thing that actually needs protecting there.

The logged-out limits stop credential stuffing and bulk account creation. They do not stop an
authenticated caller from consuming resources: signup costs an attacker one successful request,
after which all 17 authenticated routes are open at unlimited rate. Two of those routes are
expensive in ways that reach outside this service:

- `POST /me/manual-interests` and `DELETE /me/manual-interests/{id}` both publish to the
  interests queue, converting unbounded requests into unbounded queue depth and downstream
  embedding compute.
- `POST /integrations/spotify/exchange` calls the Spotify API. That quota is **shared across
  all users** — one abusive account can get the app throttled and break the integration for
  everyone.

Two public routes were also left uncovered by the prior design and are revisited below.

## Scope: coverage map

Every route registered in `Router()`. Each request passes through **at most two** limiters:
the group-wide net plus at most one specific bucket.

### Public surface — IP-keyed

| Route | Bucket | Limit | Status |
|---|---|---|---|
| `POST /auth/signup` | `signup` | 3/hr | existing; backend changes (below) |
| `POST /auth/login` | `login` | 10/min | existing, unchanged |
| `POST /auth/refresh` | `refresh` | 30/min | existing, unchanged |
| `POST /auth/logout` | `logout` | 30/min | **new** |
| `GET /ical/{token}` | `ical_feed` | 60/min | **new** — reverses a prior decision |
| `GET /readyz` | `readyz` | 30/min | **new** |
| `GET /healthz` | — | none | **permanently exempt** |

`/healthz` is the ALB health check target (`terraform/prod/alb.tf:22`). Rate limiting it would
let a request flood fail the health check and cycle otherwise-healthy tasks. It does no work
beyond writing a static JSON body, so there is nothing to protect. This exemption is
deliberate and must survive future "cover everything" passes.

### Authenticated surface — user-keyed

A group-wide net installed with `r.Use`, so it covers every route in the block including ones
added later:

| Bucket | Limit | Applies to |
|---|---|---|
| `authed` | 120/min | all 17 authenticated routes |

Stacked on top, at most one per route:

| Bucket | Limit | Routes |
|---|---|---|
| `authed_write` | 30/min | `DELETE /me`, `PATCH /me/match-threshold`, `POST` + `DELETE /me/not-interested`, `DELETE /integrations/spotify`, `DELETE /me/ical-token` |
| `manual_interests` | 60/hr | `POST /me/manual-interests`, `DELETE /me/manual-interests/{id}` |
| `spotify_exchange` | 10/hr | `POST /integrations/spotify/exchange` |
| `ical_token` | 10/hr | `POST /me/ical-token` |

Read routes — `GET /me`, `/me/manual-interests`, `/me/spotify-interests`, `/me/calendar`,
`/events/{id}`, `/integrations/spotify/connect`, `/integrations/spotify/status` — are covered
by the `authed` net alone.

`manual_interests` is deliberately **shared between POST and DELETE** rather than split. Both
publish to the interests queue, so both cost downstream compute. A shared 60/hr budget is well
above realistic curation and avoids the trap where exhausting an add-budget locks the user out
of deleting.

### Why a stacked net rather than exclusive per-route assignment

The alternative — assigning each route exactly one bucket via `r.With` — gives cleaner
accounting but means a route added later with no explicit limiter silently gets none, with
nothing to catch the omission. The `r.Use` net makes coverage the default and per-route
tightening the opt-in. Cost is one extra limiter check on the routes that stack.

## Reversal: `GET /ical/{token}`

`2026-07-22-api-rate-limiting-design.md:49-53` excluded this route, reasoning that "calendar
clients poll on schedules outside our control, so any limit tight enough to matter would break
legitimate subscriptions."

That conflates a limit **per subscription** with a limit **per source IP**. Real polling
cadence is measured in minutes to hours — Google Calendar refreshes a subscribed feed every
few hours, Apple Calendar every 5 minutes at its most aggressive. A 60/min per-IP ceiling
(~1 req/sec sustained) is invisible to legitimate clients even behind a shared corporate NAT,
while still capping a flood.

The exposure is not brute force — the token is 32 random bytes (`internal/http/handlers/ical.go:27`)
and is not guessable. It is that every request to an unauthenticated route costs a
`GetIcalTokenByHash` lookup, and every request with a *valid* token costs full calendar
generation, with no ceiling on either.

Keying on the token instead of the IP was considered and rejected: an attacker sending random
tokens mints a fresh bucket per request and is never limited. It would only constrain a
misbehaving legitimate client.

## Design: `internal/ratelimit`

### Remove the Postgres backend

`Postgres` is deleted. The package keeps one implementation, `Memory`. The `Limiter` interface
stays — middleware tests depend on substituting fakes.

Consequence: no rate limit survives a process restart. With `desired_count = 1`
(`terraform/prod/ecs_api.tf:100`) restarts are deploys, which are infrequent, so an hourly
budget resets rarely. This is an accepted trade for deleting a table, a migration, generated
code, and a background goroutine.

### Fix the `Allow` / `Record` split

The `Limiter` docstring already claims `Allow` and `Record` are separable so a caller can "gate
on every request but count only some of them." `Memory` does not honor that: `Allow` consumes a
token via `ReserveN` and `Record` is a no-op (`internal/ratelimit/memory.go:88-100`). So
`RateLimitOnSuccess` paired with a `Memory` limiter is **silently identical to `RateLimit`** —
the count-on-success behavior does nothing. This is latent today only because signup, its sole
user, is Postgres-backed.

Moving signup to `Memory` without fixing this would delete a deliberate behavior. Signup uses
`RateLimitOnSuccess` precisely so a failed signup costs nothing; `internal/http/middleware/ratelimit.go:41-44`
states the reason: "one typo would lock a real user out for the whole window without an account
to show for it." Unfixed, three typo'd emails or rejected passwords lock a real user out of
signing up for an hour.

`Memory` therefore changes to:

- **`Allow`** — non-consuming peek. `golang.org/x/time v0.15.0` provides `TokensAt(t)`
  (`rate/rate.go:86`). Allowed when `TokensAt(now) >= 1`. On denial, `RetryAfter` is the time to
  accrue the deficit: `(1 - TokensAt(now)) / rate`. Guard against a zero or degenerate rate,
  which would divide to an infinite duration.
- **`Record`** — debits one token **unconditionally**, via `ReserveN(now, 1)` with the
  reservation left uncancelled. `ReserveN` delegates to `reserveN(t, n, InfDuration)`
  (`rate/rate.go:227-230`); with an unbounded future reserve it always succeeds for `n = 1`
  whenever `burst >= 1` — always true here, since `NewMemory` sets `burst = limit` — and debits
  the token even when that drives the balance negative. A conditional consume (`AllowN`) would
  be wrong: in the on-success path the handler has already run and a concurrent request may
  have drained the bucket in between, so a conditional debit would silently fail to count a
  signup that really did create an account.

Bucket sweeping, the hard cap, and eviction (`memory.go:12-18`, `105-149`) are unaffected.

### Accepted race

`Allow` and `Record` acquire the mutex separately, so concurrent requests on one key can both
peek-pass before either debits, briefly overshooting the limit. The overshoot is bounded by
per-key concurrency, which is low for per-user and per-IP buckets. The deleted `Postgres`
limiter had the identical race between its `COUNT` and its `INSERT`; this is not a regression.

### Signup window shape

`Postgres` was a sliding window: 3 events in any trailing 60 minutes. `Memory` is a token
bucket: burst 3, refilling ~1 token per 20 minutes. A limited caller regains one signup every
20 minutes instead of waiting out a full hour — slightly more forgiving, and worth recording so
the change is not mistaken for a bug later.

## Design: `internal/http/middleware`

### Key strategy

`checkAllowed` currently derives its own key by calling `ClientIP(r)`
(`internal/http/middleware/ratelimit.go:74`). It gains an explicit `key` parameter, supplied by
a `keyFunc`:

```go
type keyFunc func(*http.Request) string

func ipKey(r *http.Request) string { return "ip:" + ClientIP(r) }

// userKey prefers the authenticated user's UUID. RequireAuth 401s before this
// runs, so an absent ID means a middleware-ordering bug — fall back to the
// client IP rather than an empty key, which would collapse every such request
// into a single shared bucket.
func userKey(r *http.Request) string {
	if uid, ok := UserIDFromContext(r.Context()); ok {
		return "u:" + uid.String()
	}
	log.Printf("ratelimit: no user id in context, falling back to client IP")
	return "ip:" + ClientIP(r)
}
```

Keying authenticated traffic on the user ID rather than the IP is the substantive difference
from the logged-out design, and it matters in both directions. IP-keying authenticated requests
produces false positives — CGNAT and corporate proxies put many real users behind one address —
and false negatives, since an attacker holding one account and a proxy pool defeats IP keying
entirely.

The `u:` / `ip:` prefixes keep the two key spaces disjoint and make the fallback path visible
in logs. Adding the `ip:` prefix changes the key format for the three existing IP-keyed
buckets. That is safe precisely because the Postgres backend is removed in the same change:
all limiter state is in-process and resets on deploy regardless, so no persisted keys in the
old format survive to be orphaned.

### Middleware surface

Three exported middlewares, all delegating to one unexported core so the 429 response,
`Retry-After` header, and metric emission live in a single place:

| Middleware | Key | Counting |
|---|---|---|
| `RateLimit` | client IP | `Allow`, then `Record` immediately |
| `RateLimitOnSuccess` | client IP | `Allow`, run handler, `Record` only on 2xx |
| `RateLimitByUser` | user UUID, IP fallback | `Allow`, then `Record` immediately |

`RateLimit` and `RateLimitOnSuccess` keep their exact current signatures; existing wiring and
tests are untouched.

There is deliberately **no `RateLimitByUserOnSuccess`**. At 60/hr and 10/hr, a handful of failed
requests burning budget is immaterial, and the `authed` net already absorbs garbage floods. It
is a small addition if a concrete need appears.

`RateLimitOnSuccess` gains a doc comment recording that it requires a limiter whose `Allow` does
not consume, so the pairing that is now correct is not silently broken again.

### New endpoint constants

Added to `internal/http/middleware/ratelimit.go` beside the existing three:

```go
// Authenticated (user-keyed)
EndpointAuthed          = "authed"
EndpointAuthedWrite     = "authed_write"
EndpointManualInterests = "manual_interests"
EndpointSpotifyExchange = "spotify_exchange"
EndpointIcalToken       = "ical_token"

// Public (IP-keyed)
EndpointLogout   = "logout"
EndpointIcalFeed = "ical_feed"
EndpointReadyz   = "readyz"
```

The existing header comment claims these values are mirrored as alarm map keys in
`terraform/prod/observability.tf`. That stops being true — see below — so the comment is
rewritten to distinguish values that are merely emitted from values that are also alarmed.

## Observability

`RateLimitRejection(endpoint, ip)` keeps its signature. User-keyed buckets still emit
`ClientIP(r)` as the `ip` property, which is the field worth pivoting on during an incident;
the property is not a dimension, so cardinality is unaffected. `internal/observability/emf.go`
needs no change, and `TestMetricContractConstants` and the terraform dimension wiring are
untouched. Only the set of *values* for the `endpoint` dimension grows, from 3 to 11.

Metrics are emitted for all 11 buckets. Alarms are added for **four** of the new ones:

| Bucket | Alarmed | Rationale |
|---|---|---|
| `authed` | yes | Sustained rejections mean a compromised or scripted account |
| `manual_interests` | yes | Directly proxies runaway queue publishing |
| `spotify_exchange` | yes | Shared third-party quota; highest blast radius |
| `ical_feed` | yes | Unauthenticated route; a spike is the abuse signal |
| `authed_write`, `ical_token`, `logout`, `readyz` | no | Rejections are individually unremarkable; still queryable in Logs Insights |

Alarming all eleven would add config whose only response is "look at the logs."

## Schema removal

Migration `0018_rate_limit_events` is already applied in production and is **not** deleted. A
new `0019_drop_rate_limit_events` drops the table, with a down migration recreating it.

Also removed:

- `internal/ratelimit/postgres.go` and `postgres_test.go`
- `sql/queries/rate_limit.sql`
- `internal/store/rate_limit.sql.go` and the `RateLimitEvent` model in `internal/store/models.go`
  — both regenerated by `sqlc generate` and committed alongside the query deletion
- the cleanup goroutine, `rateLimitRetention`, and `deleteExpiredRateLimitEvents`
  (`internal/http/server.go:167-190`), plus the `runRateLimitCleanup` call site
- the `rate_limit_events` entry in the truncate list (`internal/testdb/testdb.go:162`)

## Testing

- **`internal/ratelimit/memory_test.go`** — rewritten for the new split. `Allow` called 100
  times consumes nothing; `Record` consumes; `RetryAfter` is correct as the bucket approaches
  empty; a degenerate rate does not yield an infinite duration. Sweep and eviction tests in
  `memory_internal_test.go` should pass unchanged — if they do not, the peek/consume split
  broke `lastSeen` bookkeeping.
- **`internal/http/middleware/ratelimit_test.go`** — `RateLimitByUser` gives two distinct users
  independent budgets; a request whose context carries no user ID falls back to the IP key and
  logs; a 429 carries the correct `endpoint` dimension and a `Retry-After` of at least 1.
- **`internal/http/server_test.go`** — end-to-end through the real router: each new bucket
  returns 429 at its threshold, and `/healthz` still answers 200 under a flood well past every
  other limit.
- **Contract test** — extended to the eight new constants.
- **Signup regression** — the behavior this design exists to preserve: three consecutive
  non-2xx signups consume no budget, and a fourth attempt still succeeds. This test is the
  reason the `Allow`/`Record` fix is in scope; it must fail against an unfixed `Memory`.
- **Deleted** — `postgres_test.go`, the rate-limit cleanup case in
  `internal/http/cleanup_internal_test.go`, and the `rate_limit_events` case in
  `internal/testdb/truncate_test.go`.

## Implementation order

The dependency that matters: signup must not sit on an unfixed `Memory` at any commit, or it
silently loses typo-tolerance.

1. `Memory` `Allow`/`Record` split, with tests — self-contained, nothing depends on it yet.
2. Middleware `keyFunc` refactor, `RateLimitByUser`, and the `RateLimitOnSuccess` doc comment.
3. Move signup to `Memory`, together with the signup typo-tolerance regression test. Steps 1
   and 2 must both be in place first.
4. Delete the Postgres limiter, its queries and generated code, and the cleanup goroutine; add
   migration `0019`; run `sqlc generate` and commit the regenerated `models.go` alongside.
5. Wire the new buckets in `Router()`, with end-to-end tests.
6. Add the four CloudWatch alarms.

Steps 5 and 6 are independent of 1-4 in principle, but land after so that the new buckets are
never wired against limiter semantics that are mid-change.

## Assumptions and tripwires

**`desired_count = 1`.** Every limit is now in-process. If `terraform/prod/ecs_api.tf:100` ever
rises above 1, or autoscaling is introduced, every limit in this design silently multiplies by
the task count — signup becomes 3/hr *per task*. Nothing in the code detects this. Revisiting
the backend choice is a prerequisite for scaling the API service out, not a follow-up.

**`TRUST_PROXY` remains required.** Every IP-keyed limit still depends on it, under the same
deployment constraints documented in the `2026-07-22` runbook. The authenticated limits added
here are user-keyed and unaffected.

## Out of scope

**Per-user login failure limiting** — unchanged from the prior design.

**AWS WAF** — unchanged from the prior design; still a reasonable future volumetric shield in
front of these application-level limits.

**Distributed rate limiting** (Redis/ElastiCache, or returning to a database-backed limiter) —
the correct answer if the API scales past one task. Explicitly deferred, and gated by the
tripwire above.
