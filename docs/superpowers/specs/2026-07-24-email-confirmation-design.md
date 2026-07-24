# Email Confirmation for Signup

**Date:** 2026-07-24
**Status:** Design approved, not yet implemented

## Deployment runbook (read before shipping)

Three things must land **before** the app image that ships this feature, in this
order. Getting the order wrong locks every new signup out of the product.

### 1. Terraform (auto-applies on merge)

`terraform/prod` adds the apex SES sending identity, DKIM records, SPF/DMARC, and
`ses:SendEmail` on the ECS **task** role. DKIM verification depends on DNS
propagation and is not instant; confirm with:

```bash
AWS_PROFILE=servant aws ses get-identity-verification-attributes \
  --identities hereswhatshappening.app
```

### 2. SES production access — MANUAL, and the long pole

New AWS accounts are in the SES sandbox, where outbound mail is deliverable
**only to verified addresses**. Terraform cannot lift this; it is a support
request in the SES console and typically takes ~24h to approve. Until it is
granted, real user signups will create unconfirmable accounts in prod.

### 3. Env vars — MANUAL, they do NOT auto-apply

Same trap as `TRUST_PROXY`: `aws_ecs_task_definition.api` has
`ignore_changes = [container_definitions]` (`terraform/prod/ecs_api.tf:92`), and
the app pipeline re-registers the live task def with only the image swapped. So
values added to `local.api_env_vars` never reach the running task on their own.

```bash
AWS_PROFILE=servant scripts/taskdef-edit.sh \
  --set-env EMAIL_SENDER=ses \
  --set-env EMAIL_FROM_ADDRESS=noreply@hereswhatshappening.app \
  --set-env APP_BASE_URL=https://hereswhatshappening.app \
  --set-env API_BASE_URL=https://api.hereswhatshappening.app \
  --deploy
```

Running this against the current (pre-feature) image is safe — `config.Load()`
ignores env vars it does not know about.

**`config.Load()` fails fast when these are missing**, rather than warning like
`TRUST_PROXY` does. The precedent does not fit: a misconfigured rate limiter
degrades availability and self-heals, whereas a misconfigured mailer means every
new account is created unconfirmed, gated out of the product, and unable to
receive the mail that would fix it — with no signal to anyone. A crashlooping
task fails the ECS rolling deploy and leaves the previous version serving, which
is the louder and safer failure.

The database migration needs no manual step: `ci/buildspec-app.yml:97-116` runs
`migrate` as a one-off task before the service updates.

## Problem

`POST /auth/signup` (`internal/http/handlers/auth.go:39`) creates a fully
privileged account from an email address nobody has proven control of. It
returns an access token and sets a refresh cookie immediately, so a typo'd or
someone else's address yields a working account that consumes matching compute,
can connect Spotify, and can mint iCal feeds.

## Flows

**Happy path.** Signup creates the row with `confirmed = false`, mints a nonce,
sends a link, and returns 201 as it does today. The SPA routes to
`/confirm-email`. The user clicks the emailed link, which hits the API directly;
the API validates the nonce, flips `confirmed`, and 302s to
`{APP_BASE_URL}/?welcome=true`, where a welcome modal renders.

**Unconfirmed user arrives at the app.** After login or a reload, an
authenticated-but-unconfirmed user is redirected to `/confirm-email`.

**Expired link.** The API 302s to `{APP_BASE_URL}/?confirmerror=true`, which
renders a modal offering to send a fresh link.

## Data model

Migration `0020_email_confirmation`:

```sql
-- Existing users are grandfathered in as confirmed; new rows default to false.
ALTER TABLE users ADD COLUMN confirmed BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ALTER COLUMN confirmed SET DEFAULT FALSE;

CREATE TABLE email_confirmations (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

The two-statement `ALTER` is deliberate: it backfills every existing user as
confirmed while leaving `false` as the default for new rows. Without it, the
deploy would lock the entire existing user base out of the product.

`sqlc generate` regenerates `internal/store/models.go` alongside the new
`email_confirmations.sql.go`; both are committed.

### Why a table and not a stateless HMAC

A signed `HMAC(user_id || expiry)` token needs no table, but it cannot be
revoked, cannot be made single-use, and cannot distinguish "already used" from
"never existed" — which the replay handling below depends on. The table mirrors
patterns already in the codebase (`ical_tokens`, `refresh_tokens`): 32 random
bytes from `auth.GenerateRefresh()`, stored as `sha256` BYTEA. A primary key on
`user_id` means one live token per user, so a resend invalidates the previous
link the same way `UpsertIcalToken` does.

**TTL: 24 hours.**

### Consumed, not deleted

Confirming marks `consumed_at` instead of deleting the row, and a replay of an
already-consumed token redirects to `?welcome=true` rather than the error page.

This is not a nicety. Corporate mail security (Outlook SafeLinks and equivalents)
prefetches links in email. If consuming deleted the row, the scanner's fetch
would confirm the account and the human's real click would then find an unknown
token and land on `?confirmerror=true` — telling a successfully confirmed user
that their link failed. Only unknown tokens, and unconsumed tokens past
`expires_at`, produce the error redirect.

## Enforcement: `confirmed` as a JWT claim

The gate is hard — unconfirmed users are rejected at the API, not merely
redirected by a cooperating browser — but it costs **zero additional database
round trips**. `confirmed` rides in the access-token claims.

### Why not a per-request lookup

A PK read on every authenticated request to enforce a flag that flips once in an
account's lifetime is the wrong trade. An in-process cache was also rejected: it
adds invalidation logic and goes incoherent the moment the API runs more than one
task, while buying nothing over the claim.

### Why the claim is safe

Confirmation is **monotonic**. It goes `false → true` exactly once and never
back. A stale claim can therefore only ever be *too restrictive*, never too
permissive — the worst case is a spurious 403, not an unconfirmed account
slipping through. This is precisely why the same technique would be wrong for a
revocable flag such as an admin bit.

### Mint sites

Every site that signs a token already reads the row it needs, so nothing gains a
query:

| Site | Source of `confirmed` |
|---|---|
| `Signup` | Known to be `false` without asking |
| `Login` | Already reads the user row — add the column to `GetUserByEmail` |
| `Refresh` | `GetActiveRefreshTokenByHash` gains a `JOIN users` — still one query |

`JWTSigner.SignAccess` takes a `confirmed bool` and emits a custom claim;
`VerifyAccess` returns it alongside the user ID, and it enters the request
context next to the user ID in `middleware.RequireAuth`. The `JOIN users` in the
refresh query deliberately carries no `deleted_at` filter, preserving today's
behavior exactly rather than smuggling in a separate fix.

### Staleness

The primary flow has none. The access token lives in a module-level variable in
`web/src/api/client.ts:6` — memory only, discarded on every page load. The
confirm redirect is a full browser navigation, so the SPA reboots with no token,
takes a 401, and `client.ts:81` refreshes into a token minted *after* the DB
flip. The refresh cookie is `SameSite=Lax` and `hereswhatshappening.app` shares a
registrable domain with `api.hereswhatshappening.app`, so the two are same-*site*
and the cookie rides that cross-origin POST — already proven, since it is how
every page load authenticates today.

The residual case is a second tab or device holding a pre-confirmation token,
bounded by the 15m access TTL. That self-heals: `apiFetch` gains a
refresh-and-retry on a 403 `confirmation_required`, mirroring the 401 handling it
already has, retried once to avoid a loop.

### Future caveat

If a "change your email → must re-confirm" feature is ever added, monotonicity
breaks and a stale token would grant up to 15m of access it should not have. The
fix at that point is to revoke refresh tokens on email change. Recorded here so
the assumption is not silently inherited.

## API surface

| Route | Auth | Behavior | Limit |
|---|---|---|---|
| `POST /auth/signup` | public | Creates `confirmed=false`, mints token, sends mail, returns 201 unchanged | existing 3/hr per IP |
| `GET /auth/confirm?token=` | public | 302 to `?welcome=true` or `?confirmerror=true` | 20/hr per IP (`EndpointConfirm`) |
| `POST /auth/confirm/resend` | authed | Upserts a fresh token, sends, 204. No-op 204 if already confirmed | 3/hr per user (`EndpointConfirmResend`) |
| `GET /me` | authed | Gains `confirmed` in the response body | existing |

`GET /auth/confirm` always redirects and never returns JSON — it is a browser
navigation from a mail client, not an API call. A send failure during signup is
logged but does not fail the request: the user still reaches `/confirm-email`,
where resend is one click away.

The two new endpoint constants go in the block at
`internal/http/middleware/ratelimit.go:22`. Both are emit-only; neither needs an
alarm key in `terraform/prod/observability.tf`.

## Routing

The gate is applied so that **guarded is the default**, preserving the
safe-by-default property the rate-limit net already has (`server.go:108`). Rather
than exempting routes inside the guarded group — where a route added later would
silently land outside the gate — the exemptions are hoisted into their own small
group and everything else keeps the gate via `Use`:

```go
// Authenticated, exempt from the confirmation gate: what an unconfirmed user
// needs to get confirmed, or to leave.
r.Group(func(r chi.Router) {
    r.Use(middleware.RequireAuth(s.JWTSigner))
    r.Use(middleware.RateLimitByUser(authedLimiter, middleware.EndpointAuthed))
    r.Get("/me", handlers.GetMe(s.Queries))
    r.With(middleware.RateLimitByUser(confirmResendLimiter, middleware.EndpointConfirmResend)).
        Post("/auth/confirm/resend", handlers.ResendConfirmation(...))
    r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
        Delete("/me", handlers.DeleteMe(s.Queries))
})

// Authenticated + confirmed. Everything else, including routes added later.
r.Group(func(r chi.Router) {
    r.Use(middleware.RequireAuth(s.JWTSigner))
    r.Use(middleware.RateLimitByUser(authedLimiter, middleware.EndpointAuthed))
    r.Use(middleware.RequireConfirmed())
    // ... all existing authenticated routes, unchanged ...
})
```

Both groups share the *same* limiter instances, so budgets do not double.
`DELETE /me` is exempt on purpose: an unconfirmed user must still be able to
delete their account. `RequireConfirmed` reads the claim from the request
context and takes no `*store.Queries`.

## Email delivery

New `internal/email` package:

- `type Sender interface { Send(ctx context.Context, msg Message) error }`
- `Message{To, Subject, HTML, Text}` — both parts, so the mail is not
  spam-scored as HTML-only.
- `sesSender` over `aws-sdk-go-v2/service/sesv2`.
- `logSender` writes to the log instead of sending; the local-dev default.
- A fake in tests asserts on captured messages; no test touches AWS.

`ConfirmationMessage(link string) Message` holds the template. Chosen by
`EMAIL_SENDER` (`ses` | `log`) with no fallback default — an unset value is a
config error, per the runbook.

## Configuration

| Var | Purpose | Local |
|---|---|---|
| `EMAIL_SENDER` | `ses` or `log` | `log` |
| `EMAIL_FROM_ADDRESS` | Envelope/From | `dev@localhost` |
| `APP_BASE_URL` | SPA origin, redirect target | `http://localhost:5173` |
| `API_BASE_URL` | API origin, used to build the emailed link | `http://localhost:8080` |

All four are added to `.env.example`.

`API_BASE_URL` duplicates the value of `ICAL_BASE_URL`
(`https://api.hereswhatshappening.app`). Consolidating them is a follow-up, kept
out of this branch deliberately — renaming an env var that reaches prod only
through a manual taskdef step is its own rollout risk and does not belong
bundled with a feature.

## Frontend

- `User` gains `confirmed: boolean`; `resendConfirmation()` joins
  `web/src/api/auth.ts`.
- **`ConfirmEmailPage`** at `/confirm-email`: "check your inbox at {email}", a
  resend button with sent/rate-limited states, and a sign-out link. On window
  focus it re-runs `/auth/refresh` followed by `/me` and navigates to
  `/calendar` if `confirmed` has become true, so the "signed up on the laptop,
  clicked the link on my phone" case moves the stale laptop tab along instead of
  stranding it.
- **`RequireAuth`** gains `allowUnconfirmed?: boolean`. Without it, an
  authenticated-but-unconfirmed user is redirected to `/confirm-email` — one
  place that covers both the post-login and reload cases. `ConfirmEmailPage`
  itself is the only route that sets it.
- **`SignupDialog`** (`web/src/components/SignupDialog.tsx:9`) navigates to
  `/confirm-email` instead of `/interests`.
- **Modals in `Layout.tsx`**, reading `useSearchParams`, so they survive the
  index route's redirect. `WelcomeModal` on `?welcome=true`; `ConfirmErrorModal`
  on `?confirmerror=true`, offering a fresh link (resend when authenticated,
  otherwise a prompt to sign in). Dismissing clears the param.
- Confirming on a device with no session is a real case — the link is often
  opened on a phone. There the SPA is anonymous, the index route redirects to
  `/login`, and the welcome modal renders over the login screen reading
  "confirmed — now sign in". The modals live in `Layout` rather than on a page
  precisely so this works.
- `LandingPage`'s `<Navigate>` calls must preserve `location.search`, which they
  currently drop — without this the params are lost before any modal can read
  them.

## Terraform

Apex domain identity and DKIM CNAMEs (today's `ses.tf` verifies only
`inbound.<domain>`), SPF and DMARC TXT records, `ses:SendEmail` on the **task**
role — not the execution role — and the four env vars in `local.api_env_vars`,
each carrying the same "does not auto-apply" comment `TRUST_PROXY` has.

The apex TXT record must be checked for an existing value before adding SPF; if
one exists the mechanisms merge into a single record rather than a second one,
which would break SPF evaluation.

## Testing

**Go.** Signup creates an unconfirmed user, a token row, and one captured
message. Confirm covers happy path, expired, unknown, and consumed-replay.
Resend regenerates, invalidates the prior token, and no-ops when already
confirmed. `RequireConfirmed` passes and 403s off the claim. Server-level wiring
asserts an unconfirmed token gets 403 on a guarded route and 200 on `/me`,
`/auth/confirm/resend`, and `DELETE /me`. Round-trip a token through
`SignAccess`/`VerifyAccess` with both claim values.

**Frontend.** `ConfirmEmailPage` render, resend, and rate-limit states;
`RequireAuth` redirect on unconfirmed and pass-through under `allowUnconfirmed`;
both modals keyed off search params; `SignupDialog` navigation; `apiFetch`
refresh-and-retry on 403.

## Out of scope

- Consolidating `API_BASE_URL` and `ICAL_BASE_URL`.
- Re-confirmation on email change (see the caveat above).
- A cleanup job for expired `email_confirmations` rows. The table holds at most
  one row per user and rows are overwritten on resend, so it does not grow
  unbounded.
- Any change to login: unconfirmed users must be able to log in, since that is
  how they reach the page that gets them confirmed.
