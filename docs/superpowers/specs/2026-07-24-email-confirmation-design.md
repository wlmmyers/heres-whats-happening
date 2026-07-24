# Email Confirmation for Signup

**Date:** 2026-07-24
**Status:** Design approved, not yet implemented

## Rollout

The hard constraint: **SES production access must be granted before any merge
changes signup behavior.** New AWS accounts are in the SES sandbox, where
outbound mail reaches only verified addresses, and lifting it is a support
request in the SES console that typically takes ~24h. Terraform cannot do it. If
enforcement went live while sandboxed, every real signup would create an account
that is gated out of the product and cannot receive the mail that would fix it.

So the feature ships dark and turns on by config, not by merge. Enforcement is
governed by `EMAIL_CONFIRMATION_MODE`, a tri-state:

| Mode | Signup marks user | Sends mail | Gate installed |
|---|---|---|---|
| `off` | `confirmed = true` | no | no |
| `send` | `confirmed = true` | yes | no |
| `enforce` | `confirmed = false` | yes | yes |

`off` is byte-for-byte today's behavior. `send` exercises the entire flow — nonce,
mail, confirm link, redirect, welcome modal — against real signups while nobody
can be locked out, because the user is already confirmed when the mail goes out.
A tri-state rather than two booleans because "enforce without send" is a total
signup outage and should not be expressible.

### Phase 0 — terraform only (safe to merge now)

Apex SES identity, DKIM CNAMEs, SPF/DMARC, `ses:SendEmail` on the task role.
**Touches no app behavior**, so it can merge and auto-apply immediately. Verify:

```bash
AWS_PROFILE=servant aws ses get-identity-verification-attributes \
  --identities hereswhatshappening.app
```

### Phase 1 — request SES production access (manual, the long pole)

File it as soon as phase 0's DKIM verifies. Production access is account-level,
so it can technically be requested earlier, but a verified domain with DKIM makes
the request credible and is required to test anything.

While still sandboxed, verify your own address as an identity and run the full
signup → mail → confirm → welcome loop against it end to end.

### Phase 2 — merge the app code

Ships with `EMAIL_CONFIRMATION_MODE` unset, which defaults to `off`. Signup
behaves exactly as today and no mail is sent, so this merge cannot break the
signup flow no matter how long phase 1 takes. The migration runs automatically —
`ci/buildspec-app.yml:97-116` runs `migrate` as a one-off task before the service
updates — and backfills every existing user as confirmed.

No env vars are required for this phase, which sidesteps the `TRUST_PROXY` trap
entirely: nothing needs to reach the task for the deploy to be correct.

### Phase 3 — turn on sending (after production access is granted)

Env vars do **not** auto-apply. `aws_ecs_task_definition.api` has
`ignore_changes = [container_definitions]` (`terraform/prod/ecs_api.tf:92`) and
the app pipeline re-registers the live task def with only the image swapped, so
values added to `local.api_env_vars` never reach the running task on their own:

```bash
AWS_PROFILE=servant scripts/taskdef-edit.sh \
  --set-env EMAIL_CONFIRMATION_MODE=send \
  --set-env EMAIL_SENDER=ses \
  --set-env EMAIL_FROM_ADDRESS=noreply@hereswhatshappening.app \
  --set-env APP_BASE_URL=https://hereswhatshappening.app \
  --set-env API_BASE_URL=https://api.hereswhatshappening.app \
  --deploy
```

Real signups now receive mail while remaining confirmed. Soak here long enough to
watch SES bounce and complaint rates and to confirm delivery to the major inbox
providers — deliverability to Gmail and Outlook is the thing you cannot test from
the sandbox, and it is the thing that makes the gate load-bearing.

### Phase 4 — enforce

```bash
AWS_PROFILE=servant scripts/taskdef-edit.sh \
  --set-env EMAIL_CONFIRMATION_MODE=enforce --deploy
```

**Rollback at every phase after 2 is a single flip back**, with no code revert and
no migration rollback. Users created unconfirmed during an enforce window stay
unconfirmed after a rollback to `send`, but the gate is gone, so they are not
stuck — and their confirmation links keep working.

### Config validation

`EMAIL_CONFIRMATION_MODE` defaults to `off` because that default is *safe* —
it is current behavior. But when the mode is `send` or `enforce`, `config.Load()`
**fails fast** if any of `EMAIL_SENDER`, `EMAIL_FROM_ADDRESS`, `APP_BASE_URL`, or
`API_BASE_URL` is missing, rather than warning the way `TRUST_PROXY` does. The
precedent does not fit: a misconfigured rate limiter degrades availability and
self-heals, whereas a half-configured mailer strands users with no signal. A
crashlooping task fails the ECS rolling deploy and leaves the previous version
serving, which is the louder and safer failure. The check lives where it matters
— you cannot turn the feature on half-configured — while keeping phase 2's deploy
a genuine no-op.

## Problem

`POST /auth/signup` (`internal/http/handlers/auth.go:39`) creates a fully
privileged account from an email address nobody has proven control of. It
returns an access token and sets a refresh cookie immediately, so a typo'd or
someone else's address yields a working account that consumes matching compute,
can connect Spotify, and can mint iCal feeds.

## Flows

These describe `enforce`, the end state. In `send` the same mail goes out and the
same link works, but the user is already confirmed, so nothing gates them and the
"unconfirmed user arrives" flow never triggers.

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
| `Signup` | Known from the mode without asking — `false` only in `enforce` |
| `Login` | Already reads the user row — add the column to `GetUserByEmail` |
| `Refresh` | `GetActiveRefreshTokenByHash` gains a `JOIN users` — still one query |

`CreateUser` takes `confirmed` as an explicit parameter rather than leaning on
the column default, so the mode — not the schema — decides.

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
| `POST /auth/signup` | public | Per mode: marks `confirmed`, mints token, sends mail. Returns 201 unchanged in every mode | existing 3/hr per IP |
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
    if s.EmailConfirmationMode == config.ConfirmationEnforce {
        r.Use(middleware.RequireConfirmed())
    }
    // ... all existing authenticated routes, unchanged ...
})
```

Both groups share the *same* limiter instances, so budgets do not double.
`DELETE /me` is exempt on purpose: an unconfirmed user must still be able to
delete their account. `RequireConfirmed` reads the claim from the request
context and takes no `*store.Queries`.

The mode gates only the `Use` call, not the route layout — the two groups exist
in every mode. Keeping the shape identical across modes means phase 4 changes
which middleware runs, not which routes exist, so the flip cannot reshuffle
routing.

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
| `EMAIL_CONFIRMATION_MODE` | `off` \| `send` \| `enforce`; defaults to `off` | `enforce` |
| `EMAIL_SENDER` | `ses` or `log` | `log` |
| `EMAIL_FROM_ADDRESS` | Envelope/From | `dev@localhost` |
| `APP_BASE_URL` | SPA origin, redirect target | `http://localhost:5173` |
| `API_BASE_URL` | API origin, used to build the emailed link | `http://localhost:8080` |

All five are added to `.env.example`. Local dev runs `enforce` with the `log`
sender, so development exercises the real path and the confirmation link is
pasted out of the server log.

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
`inbound.<domain>`), SPF and DMARC TXT records, and `ses:SendEmail` on the
**task** role — not the execution role. This is all of phase 0, and it is the
only part of the feature that merges as terraform.

The five env vars are also declared in `local.api_env_vars`, each carrying the
same "does not auto-apply" comment `TRUST_PROXY` has. They are documentation and
drift-reference only — the values that matter reach the task through
`taskdef-edit.sh` in phases 3 and 4. `EMAIL_CONFIRMATION_MODE` is declared there
as `enforce`, the intended end state, so the file records where the system is
headed rather than whichever phase it happens to be in.

The apex TXT record must be checked for an existing value before adding SPF; if
one exists the mechanisms merge into a single record rather than a second one,
which would break SPF evaluation.

## Testing

**Go.** Signup in `enforce` creates an unconfirmed user, a token row, and one
captured message. Confirm covers happy path, expired, unknown, and
consumed-replay. Resend regenerates, invalidates the prior token, and no-ops when
already confirmed. `RequireConfirmed` passes and 403s off the claim.
Server-level wiring asserts an unconfirmed token gets 403 on a guarded route and
200 on `/me`, `/auth/confirm/resend`, and `DELETE /me`. Round-trip a token
through `SignAccess`/`VerifyAccess` with both claim values.

**Modes.** Each of the three is tested at the signup handler: `off` creates a
confirmed user and sends nothing, `send` creates a confirmed user *and* sends,
`enforce` creates an unconfirmed user and sends. Server-level, `off` and `send`
must return 200 on a route that 403s under `enforce` — that assertion is what
makes phase 2 safe to merge, so it is worth more than the sum of the others.
`config.Load()` errors when a non-`off` mode is missing any required var, and
defaults to `off` when unset.

**Frontend.** `ConfirmEmailPage` render, resend, and rate-limit states;
`RequireAuth` redirect on unconfirmed and pass-through under `allowUnconfirmed`;
both modals keyed off search params; `SignupDialog` navigation; `apiFetch`
refresh-and-retry on 403.

## Out of scope

- **Removing `EMAIL_CONFIRMATION_MODE`.** It is rollout scaffolding, not a
  permanent feature flag. Once `enforce` has been live and stable, delete the
  mode and hardcode enforcement — otherwise the auth path keeps a branch that
  nothing exercises and every future change has to reason about three modes.
  This is a follow-up ticket, not a maybe.
- Consolidating `API_BASE_URL` and `ICAL_BASE_URL`.
- Re-confirmation on email change (see the caveat above).
- A cleanup job for expired `email_confirmations` rows. The table holds at most
  one row per user and rows are overwritten on resend, so it does not grow
  unbounded.
- Any change to login: unconfirmed users must be able to log in, since that is
  how they reach the page that gets them confirmed.
