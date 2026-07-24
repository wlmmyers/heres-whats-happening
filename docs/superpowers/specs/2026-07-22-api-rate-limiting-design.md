# API Rate Limiting

**Date:** 2026-07-22
**Status:** Implemented and reviewed

## Deployment runbook (read before shipping)

Rate limiting is active the moment the new image runs — the routes are wired
unconditionally. Whether it keys on a trustworthy client IP depends on the
`TRUST_PROXY` env var, and that var does **not** propagate through the normal
deploy flow:

- `terraform/prod/ecs_api.tf` sets `TRUST_PROXY=true`, but the task definition has
  `ignore_changes = [container_definitions]`, so `terraform apply` never pushes it.
- The app pipeline re-registers the currently-live task definition with only the
  image swapped, so it copies whatever env is already live — it won't introduce a
  new var either.

**Required step:** before (or together with) the first image that ships rate
limiting, set the var on the running task with
`scripts/taskdef-edit.sh --set-env TRUST_PROXY=true --deploy`.

If the limiting image runs while `TRUST_PROXY` is unset, `resolveClientIP` falls
back to `RemoteAddr`, which behind the ALB is the load balancer's own IP — a
single shared value. Every caller then lands in one bucket and the limits apply
**site-wide** (signup 3/hour, login 10/min, refresh 30/min for the entire user
base). This is an availability regression, not a security hole, and it self-heals
once the var is set. The app logs a startup `WARNING` whenever it boots with
`TRUST_PROXY` unset, so a misconfigured task is visible in CloudWatch.

## Problem

The public API surface has no rate limiting. The motivating concern is bots bulk-creating
accounts through `POST /auth/signup`, but the same gap leaves `POST /auth/login` with no
brute-force or credential-stuffing protection at all, which is the higher-severity exposure.

### Scope correction

The original request named `/me` as a logged-out endpoint. It is not — `/me` sits inside the
`RequireAuth` group (`internal/http/server.go:70-72`) and is unreachable without a valid access
token. The genuinely public routes are:

| Route | Registered at | Risk | In scope |
|---|---|---|---|
| `POST /auth/signup` | `server.go:64` | Bulk bot account creation | Yes |
| `POST /auth/login` | `server.go:65` | Credential stuffing / brute force | Yes |
| `POST /auth/refresh` | `server.go:66` | Token guessing, DB load | Yes |
| `POST /auth/logout` | `server.go:67` | Low — cheap, requires a valid cookie | No |
| `GET /ical/{token}` | `server.go:61` | DB load only; token is 32 random bytes | No |
| `GET /healthz`, `/readyz` | `server.go:58-59` | None | No |

`/auth/logout` and `/ical/{token}` are deliberately excluded. Logout requires a valid cookie and
does one indexed delete. The iCal token is not guessable, and calendar clients poll on schedules
outside our control, so any limit tight enough to matter would break legitimate subscriptions.

## Prerequisite: trustworthy client IP

Every limit below is keyed on client IP, so this must land first — without it the entire feature
is decorative.

`internal/http/server.go:49` installs `chimw.RealIP`, which derives `RemoteAddr` from
`True-Client-IP`, then `X-Real-IP`, then the **first** comma-separated entry of `X-Forwarded-For`
(`go-chi/chi/v5@v5.2.5/middleware/realip.go:42-51`). The ALB *appends* to `X-Forwarded-For`
rather than replacing it, and strips none of those three headers. An attacker sends
`True-Client-IP: <random>` on each request and every IP-keyed bucket sees a fresh key.

### Design

New `internal/http/middleware/clientip.go`, replacing `chimw.RealIP` in the chain. It mutates
`r.RemoteAddr` in place, matching `RealIP`'s contract, so `chimw.Logger` also stops recording
forgeable values.

- Ignore `True-Client-IP` and `X-Real-IP` entirely. Nothing in the request path sets them.
- When trusting the proxy, take the **rightmost** `X-Forwarded-For` entry — the hop the ALB
  appended. Everything to its left is client-supplied and untrusted.
- Otherwise use the host portion of `r.RemoteAddr`, for local `make run` and tests.
- If the header is absent or unparseable, fall back to `r.RemoteAddr`. Never fall back to a
  constant — that would collapse all clients into one shared bucket.

Behavior is selected by a new `TRUST_PROXY` boolean added to `Config` and read in `Load()`
(`internal/config/config.go:49`) following the existing `os.Getenv` pattern, plumbed onto
`http.Server`, and set to `true` in the API task definition (`terraform/prod/ecs_api.tf`). It
stays unset for local development.

## Architecture

Two backends behind one interface, in a new `internal/ratelimit` package:

```go
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type Limiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
	Record(ctx context.Context, key string) error
}
```

Splitting the interface this way is what lets signup count only completed account creations
while login and refresh gate on every request.

### `ratelimit.Memory` — login and refresh

Token buckets from `golang.org/x/time/rate` (already a direct dependency, used in
`internal/musicbrainz/client.go:17`) in a mutex-guarded map keyed by IP. `Record` is a no-op;
`Allow` consumes a token.

A background sweep evicts buckets that are full and untouched for longer than their refill
window. This is required, not an optimization: without eviction an attacker rotating source IPs
grows the map without bound, turning the rate limiter into a memory-exhaustion vector.

State is per-task and resets on deploy or restart. With `desired_count = 1`
(`terraform/prod/ecs_api.tf:91`) that is accurate except during rolling deploys, when two tasks
briefly run and the effective limit doubles. Acceptable for these limits.

### `ratelimit.Postgres` — signup

Durable sliding-window count, so the signup ceiling survives restarts and any future task-count
increase. New migration `sql/migrations/0018_rate_limit_events.{up,down}.sql`:

```sql
CREATE TABLE rate_limit_events (
    id         BIGSERIAL PRIMARY KEY,
    bucket     TEXT        NOT NULL,
    key        TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX rate_limit_events_lookup
    ON rate_limit_events (bucket, key, created_at DESC);
```

`bucket` namespaces the limit ("signup"), `key` holds the client IP. Queries live in
`sql/queries/rate_limit.sql`:

- `CountRateLimitEvents` — `WHERE bucket = ... AND key = ... AND created_at > since`
- `OldestRateLimitEvent` — the earliest event still inside the window, for an accurate
  `Retry-After`
- `InsertRateLimitEvent` — takes `created_at` explicitly
- `DeleteRateLimitEventsBefore` — for cleanup

The window boundary is passed as a `timestamptz` computed in Go rather than as a SQL interval,
and inserts carry an explicit `created_at` rather than relying on the column default. Both exist
so the limiter's clock is the single authority on time: a limiter comparing against an injected
test clock while rows were stamped by the database's `NOW()` would make every window test
meaningless.

Running `sqlc generate` regenerates `internal/store/models.go` alongside the new
`rate_limit.sql.go`; both are committed.

## Limits

| Endpoint | Limit | Key | Backend | Counts |
|---|---|---|---|---|
| `POST /auth/signup` | 3 / hour | client IP | Postgres | Successful creations only |
| `POST /auth/login` | 10 / minute | client IP | Memory | Every request |
| `POST /auth/refresh` | 30 / minute | client IP | Memory | Every request |

**Signup is 3/hour, not the originally proposed 1/hour, and counts only `201` responses.**
Counting every request would lock a user out for an hour for mistyping a password and receiving
the `weak_password` 400 from `internal/http/handlers/auth.go:52` — a full hour of lockout with no
account created. Counting successes means a real person hits the limit only after actually
creating accounts. The burst of 3 accommodates shared egress IPs (CGNAT, offices, campus wifi),
where a strict 1/hour would block the second person on a network from signing up at all. A bot
farm behind one IP is still capped at 3 accounts per hour.

Refresh is deliberately loose. A legitimate open tab refreshes on a timer, and blocking it would
break an active session — a worse outcome than the abuse being prevented.

Per-email login failure limiting was considered and **explicitly cut**. It would defend a single
targeted account against distributed credential stuffing, but it cannot live in middleware (the
body is consumed by the time the email is known), requiring changes inside `handlers.Login` plus
a second limiter. Per-IP limiting covers the common case. Revisit if targeted attacks appear.

## Middleware

Two middlewares in `internal/http/middleware/ratelimit.go`, because signup needs the response
status and the other two do not.

**`RateLimit(l, bucket)`** — gate only. Calls `Allow`; on denial writes 429 and stops.

**`RateLimitOnSuccess(l, bucket)`** — gate, then record conditionally:

```
POST /auth/signup
  ├─ Allow(ip)  → count(bucket='signup', key=ip, last 1h) >= 3?  → 429, stop
  ├─ run handlers.Signup with a wrapped ResponseWriter
  └─ status == 201?  → Record(ip)      (400 / 409 record nothing)
```

Status capture uses chi's existing `chimw.WrapResponseWriter`. `handlers.Signup` is not modified —
it stays unaware that it is rate limited.

Wiring in `internal/http/server.go`:

```go
r.With(middleware.RateLimitOnSuccess(signupLimiter, "signup")).
	Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
r.With(middleware.RateLimit(loginLimiter, "login")).
	Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
r.With(middleware.RateLimit(refreshLimiter, "refresh")).
	Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
```

Limiters are constructed once in `Server.Router()` and closed over by the middlewares. They are
not `Server` fields: they hold mutable runtime state rather than configuration, and constructing
them per-`Router()` call keeps each test server isolated instead of sharing buckets.

## Error handling

**Denied requests** get `Retry-After: <seconds>` followed by
`httperr.Write(w, http.StatusTooManyRequests, "rate_limited", "too many requests, try again later")`,
matching the existing four-argument `httperr.Write` shape used throughout `handlers/auth.go`.

**Frontend.** `web/src/api/client.ts:20` already throws `ApiError` carrying status and body code
for any non-OK response, so 429 propagates without client changes. The signup and login views
need a message for the `rate_limited` code; the default error path is otherwise acceptable.

**Postgres limiter errors fail open.** If the count query fails, the request proceeds. A database
problem should not make signup unavailable — the limiter protects against bulk abuse, not against
a single request, and the failure is already visible through the DB error path.

**Concurrency.** Two simultaneous signups from one IP can both pass the check before either
records, yielding at most a 4th account. Not worth a lock or a serializable transaction at this
limit.

**Cleanup.** A ticker goroutine started in `Server.Run`, alongside the existing consumer
goroutines at `internal/http/server.go:105-110`, calls `DeleteRateLimitEventsBefore` daily for
rows older than the longest window. It respects the same `ctx` cancellation as the consumers.

## Testing

`x/time/rate` accepts an explicit timestamp via `AllowN(t, 1)`, so memory-limiter tests advance a
fake clock instead of sleeping and stay deterministic.

- **`internal/ratelimit`** — memory limiter allows to the limit then denies; refills after the
  window; the sweep evicts idle buckets. Postgres limiter tested against `appdb_test` via
  `testdb.MustOpen(t)`, the pattern already used in `internal/http/handlers/auth_test.go:30`.
- **`internal/http/middleware`** — client IP resolution: rightmost `X-Forwarded-For` entry wins;
  `True-Client-IP` is ignored; absent header falls back to `RemoteAddr`. The regression test that
  matters: a request carrying a spoofed `True-Client-IP` does **not** get a fresh bucket.
- **Signup counting semantics** — three consecutive `400`s consume no budget, and the fourth
  successful signup within the hour returns 429 with `Retry-After` set.
- **Route wiring** — a 429 from the limiter never reaches the handler.

## Out of scope

**AWS WAF** rate-based rules on the ALB were considered as an edge-enforcement layer. They are
bounded by a fixed set of short evaluation windows and a request-count floor well above what this
design needs, so the tightest limit they can express is far looser than 3-per-hour and they cannot
deliver the signup requirement. (Exact current bounds are worth re-checking against AWS docs if
this is revisited.) WAF remains a reasonable future
addition as a coarse volumetric shield in front of the application-level limits described here,
at the cost of a monthly web ACL charge plus per-request fees.

**Per-email login failure limiting** — see Limits above.
