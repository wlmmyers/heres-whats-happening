# Email Confirmation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gate the product behind a confirmed email address, shipped dark behind `EMAIL_CONFIRMATION_MODE` so enforcement turns on by config rather than by merge.

**Architecture:** Signup mints a single-use 32-byte confirmation token (sha256-hashed in `email_confirmations`, one row per user) and emails a link to a public `GET /auth/confirm` that flips `users.confirmed` and 302s back to the SPA. Enforcement rides in the access-token JWT claim rather than a per-request DB read — safe because confirmation is monotonic (`false → true`, never back), so a stale claim can only be too restrictive. Routing puts the gate on a default-guarded group, with an explicit small exemption group for what an unconfirmed user needs (`/me`, resend, `DELETE /me`).

**Tech Stack:** Go 1.x (chi, sqlc/pgx v5, golang-jwt v5, testify), Postgres, AWS SES v2 (`aws-sdk-go-v2/service/sesv2`), React 19 + react-router 7 + vanilla-extract, vitest + @testing-library/react, Terraform.

## Global Constraints

- **`EMAIL_CONFIRMATION_MODE` is a tri-state**: `off` | `send` | `enforce`. Unset defaults to `off`. "Enforce without send" must not be expressible.
- **`off` must be byte-for-byte today's behavior**: user created confirmed, no mail, no gate. This is what makes the app merge (phase 2) safe while SES production access is still pending.
- **Mode gates only the `Use` call, never the route layout.** Both route groups exist in every mode.
- **Confirmation TTL is 24 hours.**
- **Confirming marks `consumed_at`; it never deletes the row.** A replay of an already-consumed token redirects to `?welcome=true`, not the error page — corporate mail scanners (Outlook SafeLinks) prefetch links, and the human's real click must not be told their link failed.
- **Only unknown tokens, and unconsumed tokens past `expires_at`, produce `?confirmerror=true`.**
- **`GET /auth/confirm` always redirects, never returns JSON.** It is a browser navigation from a mail client.
- **A send failure during signup is logged and does not fail the request.** The user still reaches `/confirm-email`, where resend is one click away.
- **`config.Load()` fails fast** when mode is `send` or `enforce` and any of `EMAIL_SENDER`, `EMAIL_FROM_ADDRESS`, `APP_BASE_URL`, `API_BASE_URL` is missing. It does *not* warn the way `TRUST_PROXY` does.
- **Terraform env vars do not auto-apply.** `aws_ecs_task_definition.api` has `ignore_changes = [container_definitions]`. Values in `local.api_env_vars` are documentation/drift-reference only; the running task gets them via `scripts/taskdef-edit.sh`. Every new var carries the same comment `TRUST_PROXY` has.
- **The `JOIN users` added to the refresh query carries no `deleted_at` filter** — preserve today's behavior exactly rather than smuggling in a separate fix.
- **Both authenticated route groups share the same limiter instances** so budgets do not double.
- **Package manager for `web/` is pnpm, not npm.** `pnpm install --frozen-lockfile`, `pnpm test`. (`npm install` fails on a pre-existing eslint peer-dep conflict.)
- Go tests: `go test -p 1 ./... -count=1` (needs local Postgres: `docker compose up -d postgres`). Frontend: `cd web && pnpm test`.

## Deviations from the spec, and why

Two places where this plan makes a call the spec left underspecified. Both are deliberate:

1. **`ConfirmationMessage(to, link string) Message`** rather than the spec's `ConfirmationMessage(link string) Message`. The spec's signature returns a `Message` with an empty `To`, forcing every caller to remember to fill it in. Taking the recipient as a parameter makes the returned value complete and unforgeable-by-omission.
2. **`App.tsx`'s `/calendar → /calendar/seattle` redirect must also preserve `location.search`.** The spec calls out `LandingPage`'s `<Navigate>` calls but misses this one, which sits directly on the `?welcome=true` path (`/?welcome=true` → LandingPage → `/calendar` → `/calendar/seattle`). Without the fix the param is dropped before `Layout` can render the modal, and the welcome modal silently never appears for authenticated users.

## File Structure

**Created:**
| File | Responsibility |
|---|---|
| `sql/migrations/0020_email_confirmation.up.sql` / `.down.sql` | `users.confirmed` column + `email_confirmations` table |
| `sql/queries/email_confirmations.sql` | sqlc source for the token table |
| `internal/email/email.go` | `Sender` interface, `Message`, `ConfirmationMessage` template |
| `internal/email/log.go` | `logSender` — local-dev default |
| `internal/email/ses.go` | `sesSender` over `aws-sdk-go-v2/service/sesv2` |
| `internal/email/fake.go` | `Fake` sender capturing messages, for tests in other packages |
| `internal/email/email_test.go` | template + log/fake sender tests |
| `internal/http/handlers/confirm.go` | `ConfirmEmail`, `ResendConfirmation`, shared `sendConfirmation` helper |
| `internal/http/handlers/confirm_test.go` | confirm + resend handler tests |
| `internal/http/middleware/confirmed.go` | `RequireConfirmed`, confirmed-in-context accessors |
| `internal/http/middleware/confirmed_test.go` | gate pass/403 tests |
| `web/src/pages/ConfirmEmailPage.tsx` / `.css.ts` / `.test.tsx` | "check your inbox" page with resend + focus recheck |
| `web/src/auth/RequireAuth.test.tsx` | gate behavior: anonymous, unconfirmed, `allowUnconfirmed` |
| `web/src/components/WelcomeModal.tsx` / `.css.ts` | `?welcome=true` modal |
| `web/src/components/ConfirmErrorModal.tsx` | `?confirmerror=true` modal, offers a fresh link |
| `web/src/components/ConfirmModals.test.tsx` | both modals keyed off search params |

**Modified:**
| File | Change |
|---|---|
| `sql/queries/users.sql` | `confirmed` into `CreateUser`, `GetUserByEmail`, `GetUserByID`; new `MarkUserConfirmed` |
| `sql/queries/refresh_tokens.sql` | `GetActiveRefreshTokenByHash` gains `JOIN users` for `confirmed` |
| `internal/store/*.sql.go`, `internal/store/models.go` | regenerated by `sqlc generate` |
| `internal/config/config.go` | `ConfirmationMode` type, five vars, fail-fast validation |
| `internal/auth/jwt.go` | `SignAccess(uuid, bool)`, `VerifyAccess → (uuid, bool, error)` |
| `internal/http/middleware/auth.go` | put `confirmed` in request context |
| `internal/http/middleware/ratelimit.go` | `EndpointConfirm`, `EndpointConfirmResend` |
| `internal/http/handlers/auth.go` | mode-aware `Signup`, `confirmed` through login/refresh, `userOut.Confirmed` |
| `internal/http/handlers/user.go` | `GetMe` returns `confirmed` |
| `internal/http/server.go` | new fields, two-group routing, confirm routes + limiters |
| `cmd/app/main.go` | build the `email.Sender`, pass confirmation config to the server |
| `.env.example` | the five new vars |
| `terraform/prod/ses.tf` | apex identity, DKIM, SPF, DMARC |
| `terraform/prod/iam.tf` | `ses:SendEmail` on the **task** role |
| `terraform/prod/ecs_api.tf` | the five vars in `local.api_env_vars`, each with the no-auto-apply comment |
| `web/src/api/client.ts` | `refreshSession()` export; 403 `confirmation_required` refresh-and-retry |
| `web/src/api/auth.ts` | `User.confirmed`, `resendConfirmation()` |
| `web/src/auth/context.ts`, `AuthContext.tsx` | `refreshUser()` |
| `web/src/auth/RequireAuth.tsx` | `allowUnconfirmed?: boolean` |
| `web/src/components/SignupDialog.tsx` | navigate to `/confirm-email` |
| `web/src/components/Layout.tsx` | render both modals off `useSearchParams` |
| `web/src/pages/LandingPage.tsx` | preserve `location.search` in both `<Navigate>` calls |
| `web/src/App.tsx` | `/confirm-email` route; preserve search on `/calendar` redirect |

---

## Task 1: Terraform — SES sending identity (phase 0, merges alone)

Touches no app behavior, so this can merge and auto-apply immediately, ahead of everything else. Phase 1 (the manual SES production-access request) depends on the DKIM records here being verified.

**Files:**
- Modify: `terraform/prod/ses.tf` (append; today it verifies only `inbound.<domain>`)
- Modify: `terraform/prod/iam.tf:52-78` (the `data "aws_iam_policy_document" "task"` block)

**Interfaces:**
- Consumes: nothing.
- Produces: a verified apex SES identity and `ses:SendEmail` on `aws_iam_role.task`. Task 15 adds env vars to the same terraform tree but does not depend on these resources.

- [ ] **Step 1: Check the apex TXT record before writing SPF**

An existing apex TXT record must have SPF mechanisms *merged into it* rather than a second TXT record added — two SPF records is a permanent-error SPF evaluation and breaks deliverability outright.

Run:
```bash
AWS_PROFILE=servant aws route53 list-resource-record-sets \
  --hosted-zone-id "$(AWS_PROFILE=servant aws route53 list-hosted-zones-by-name \
      --dns-name hereswhatshappening.app --query 'HostedZones[0].Id' --output text | cut -d/ -f3)" \
  --query "ResourceRecordSets[?Type=='TXT' && Name=='hereswhatshappening.app.']"
```

Expected: `[]` — no apex TXT record exists, so Step 2's `aws_route53_record.spf` is safe to create as written.

**If it returns a record**, stop and merge by hand: put the existing value's mechanisms and `include:amazonses.com` into a single `"v=spf1 ... include:amazonses.com ~all"` string, and import the existing record instead of creating a new one. Do not proceed with a second TXT.

- [ ] **Step 2: Add apex identity, DKIM, SPF, DMARC**

Append to `terraform/prod/ses.tf`:

```hcl
# ---------------------------------------------------------------------------
# Outbound sending identity for the apex domain.
#
# Distinct from the inbound.<domain> identity above: that one receives
# newsletter mail, this one is the From: domain for transactional mail
# (email confirmation). Verifying the apex + DKIM is also what makes the
# SES production-access request credible.
# ---------------------------------------------------------------------------
resource "aws_ses_domain_identity" "apex" {
  domain = var.domain_name
}

resource "aws_ses_domain_dkim" "apex" {
  domain = aws_ses_domain_identity.apex.domain
}

resource "aws_route53_record" "apex_dkim" {
  count   = 3
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "${aws_ses_domain_dkim.apex.dkim_tokens[count.index]}._domainkey.${var.domain_name}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.apex.dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# SPF. NOTE: only ONE TXT record may carry v=spf1 for a name — a second one is
# a permanent error that fails SPF entirely. If an apex TXT record already
# exists, merge the mechanisms into it instead of adding this resource.
resource "aws_route53_record" "spf" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = var.domain_name
  type    = "TXT"
  ttl     = 600
  records = ["v=spf1 include:amazonses.com ~all"]
}

# DMARC in monitor mode (p=none): report-only, so a misaligned message is
# never rejected while we establish a sending reputation.
resource "aws_route53_record" "dmarc" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "_dmarc.${var.domain_name}"
  type    = "TXT"
  ttl     = 600
  records = ["v=DMARC1; p=none; rua=mailto:dmarc@${var.domain_name}"]
}
```

- [ ] **Step 3: Grant `ses:SendEmail` on the task role**

The **task** role (the running container), not the execution role — the execution role only injects secrets at task start and never calls SES.

In `terraform/prod/iam.tf`, inside `data "aws_iam_policy_document" "task"`, after the `ReadDBMasterSecret` statement:

```hcl
  # Outbound transactional mail (email confirmation). Scoped to the apex
  # identity: the task can send as noreply@<domain>, nothing else.
  statement {
    sid     = "SendTransactionalEmail"
    actions = ["ses:SendEmail"]
    resources = [
      "arn:aws:ses:${var.aws_region}:${data.aws_caller_identity.current.account_id}:identity/${var.domain_name}",
    ]
  }
```

- [ ] **Step 4: Confirm `data.aws_caller_identity.current` exists**

Run: `grep -rn "aws_caller_identity" terraform/prod/*.tf`
Expected: at least one `data "aws_caller_identity" "current" {}` declaration.
If absent, add `data "aws_caller_identity" "current" {}` to `terraform/prod/data.tf`.

- [ ] **Step 5: Validate**

Run:
```bash
cd terraform/prod && terraform init -backend=false && terraform validate
```
Expected: `Success! The configuration is valid.`

- [ ] **Step 6: Commit**

```bash
git add terraform/prod/ses.tf terraform/prod/iam.tf terraform/prod/data.tf
git commit -m "feat(terraform): SES apex sending identity, DKIM, SPF/DMARC, task-role send permission"
```

---

## Task 2: Migration and sqlc queries

**Files:**
- Create: `sql/migrations/0020_email_confirmation.up.sql`, `sql/migrations/0020_email_confirmation.down.sql`
- Create: `sql/queries/email_confirmations.sql`
- Modify: `sql/queries/users.sql`, `sql/queries/refresh_tokens.sql`
- Regenerated: `internal/store/models.go`, `internal/store/users.sql.go`, `internal/store/refresh_tokens.sql.go`, `internal/store/email_confirmations.sql.go`

**Interfaces:**
- Consumes: nothing.
- Produces (sqlc-generated, relied on by Tasks 6, 8, 9, 10):
  - `store.CreateUserParams` gains `Confirmed bool`; `CreateUserRow` gains `Confirmed bool`
  - `store.GetUserByEmailRow`, `store.GetUserByIDRow` gain `Confirmed bool`
  - `store.GetActiveRefreshTokenByHashRow` gains `Confirmed bool`
  - `q.MarkUserConfirmed(ctx, pgtype.UUID) error`
  - `q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{UserID pgtype.UUID, TokenHash []byte, ExpiresAt pgtype.Timestamptz}) error`
  - `q.GetEmailConfirmationByHash(ctx, []byte) (store.EmailConfirmation, error)` — fields `UserID`, `TokenHash`, `ExpiresAt`, `ConsumedAt`, `CreatedAt`
  - `q.ConsumeEmailConfirmation(ctx, pgtype.UUID) error`

- [ ] **Step 1: Write the failing test**

Create `internal/store/email_confirmations_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// newUser creates a user with the given confirmed flag and returns its id.
func newUser(t *testing.T, q *store.Queries, email string, confirmed bool) pgtype.UUID {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        email,
		PasswordHash: "x",
		CityID:       city.ID,
		Confirmed:    confirmed,
	})
	require.NoError(t, err)
	require.Equal(t, confirmed, row.Confirmed)
	return row.ID
}

func TestCreateUser_ConfirmedIsExplicit(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()

	unconfirmed := newUser(t, q, "unconfirmed@example.com", false)
	confirmed := newUser(t, q, "confirmed@example.com", true)

	a, err := q.GetUserByID(ctx, unconfirmed)
	require.NoError(t, err)
	require.False(t, a.Confirmed)

	b, err := q.GetUserByID(ctx, confirmed)
	require.NoError(t, err)
	require.True(t, b.Confirmed)
}

func TestUpsertEmailConfirmation_ReplacesPriorTokenAndClearsConsumed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	uid := newUser(t, q, "upsert@example.com", false)

	expires := pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
	require.NoError(t, q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID: uid, TokenHash: []byte("hash-one"), ExpiresAt: expires,
	}))
	require.NoError(t, q.ConsumeEmailConfirmation(ctx, uid))

	consumed, err := q.GetEmailConfirmationByHash(ctx, []byte("hash-one"))
	require.NoError(t, err)
	require.True(t, consumed.ConsumedAt.Valid)

	// A resend replaces the token and un-consumes the row.
	require.NoError(t, q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID: uid, TokenHash: []byte("hash-two"), ExpiresAt: expires,
	}))

	_, err = q.GetEmailConfirmationByHash(ctx, []byte("hash-one"))
	require.Error(t, err, "the prior token must stop resolving")

	fresh, err := q.GetEmailConfirmationByHash(ctx, []byte("hash-two"))
	require.NoError(t, err)
	require.False(t, fresh.ConsumedAt.Valid)
	require.Equal(t, uid, fresh.UserID)
}

func TestMarkUserConfirmed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	uid := newUser(t, q, "mark@example.com", false)

	require.NoError(t, q.MarkUserConfirmed(ctx, uid))

	row, err := q.GetUserByID(ctx, uid)
	require.NoError(t, err)
	require.True(t, row.Confirmed)
}

func TestGetActiveRefreshTokenByHash_CarriesConfirmed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	uid := newUser(t, q, "refresh-join@example.com", false)

	_, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    uid,
		TokenHash: []byte("refresh-hash"),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	row, err := q.GetActiveRefreshTokenByHash(ctx, []byte("refresh-hash"))
	require.NoError(t, err)
	require.False(t, row.Confirmed)

	require.NoError(t, q.MarkUserConfirmed(ctx, uid))
	row, err = q.GetActiveRefreshTokenByHash(ctx, []byte("refresh-hash"))
	require.NoError(t, err)
	require.True(t, row.Confirmed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/... -run TestCreateUser_ConfirmedIsExplicit -count=1`
Expected: FAIL — compile error, `unknown field Confirmed in struct literal of type store.CreateUserParams`.

- [ ] **Step 3: Write the migration**

Create `sql/migrations/0020_email_confirmation.up.sql`:

```sql
-- Two statements, deliberately. The first backfills every existing user as
-- confirmed; the second makes false the default for rows created from here on.
-- Collapsing them into a single ADD COLUMN ... DEFAULT FALSE would lock the
-- entire existing user base out of the product on deploy.
ALTER TABLE users ADD COLUMN confirmed BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ALTER COLUMN confirmed SET DEFAULT FALSE;

-- One live confirmation token per user: the PK on user_id means a resend
-- overwrites the previous link, the same way ical_tokens rotates.
CREATE TABLE email_confirmations (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Create `sql/migrations/0020_email_confirmation.down.sql`:

```sql
DROP TABLE IF EXISTS email_confirmations;
ALTER TABLE users DROP COLUMN IF EXISTS confirmed;
```

- [ ] **Step 4: Write the queries**

Create `sql/queries/email_confirmations.sql`:

```sql
-- name: UpsertEmailConfirmation :exec
INSERT INTO email_confirmations (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET
    token_hash  = EXCLUDED.token_hash,
    expires_at  = EXCLUDED.expires_at,
    consumed_at = NULL,
    created_at  = NOW();

-- name: GetEmailConfirmationByHash :one
SELECT user_id, token_hash, expires_at, consumed_at, created_at
FROM email_confirmations
WHERE token_hash = $1;

-- name: ConsumeEmailConfirmation :exec
UPDATE email_confirmations
SET consumed_at = NOW()
WHERE user_id = $1;
```

In `sql/queries/users.sql`, replace `CreateUser`, `GetUserByEmail`, and `GetUserByID`, and append `MarkUserConfirmed`:

```sql
-- name: CreateUser :one
INSERT INTO users (email, password_hash, city_id, confirmed)
VALUES ($1, $2, $3, $4)
RETURNING id, email, city_id, confirmed, created_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, city_id, confirmed, created_at
FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT id, email, city_id, confirmed, created_at, score_threshold
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: MarkUserConfirmed :exec
UPDATE users
SET confirmed = TRUE
WHERE id = $1;
```

In `sql/queries/refresh_tokens.sql`, replace `GetActiveRefreshTokenByHash`:

```sql
-- name: GetActiveRefreshTokenByHash :one
-- The JOIN carries `confirmed` so Refresh can mint a token with the claim
-- without a second query. It deliberately does NOT filter on u.deleted_at:
-- that preserves today's behavior exactly. Adding the filter would be a
-- separate (defensible) fix and does not belong bundled here.
SELECT rt.id, rt.user_id, rt.expires_at, rt.revoked_at, rt.created_at, u.confirmed
FROM refresh_tokens rt
JOIN users u ON u.id = rt.user_id
WHERE rt.token_hash = $1
  AND rt.revoked_at IS NULL
  AND rt.expires_at > NOW();
```

- [ ] **Step 5: Regenerate and migrate**

`sqlc generate` rewrites `internal/store/models.go` as well as the per-query files — commit all of it, not just the new file.

Run:
```bash
sqlc generate && make migrate && make migrate-test
```
Expected: no output from `sqlc generate`; both migrate commands end without error.

Run: `git status --short internal/store/`
Expected: `models.go`, `users.sql.go`, `refresh_tokens.sql.go` modified; `email_confirmations.sql.go` untracked.

- [ ] **Step 6: Run tests to verify they pass**

Existing callers of `CreateUser` will not compile yet — Task 6 updates them. Run only the store package:

Run: `go test ./internal/store/... -count=1 -v 2>&1 | tail -20`
Expected: PASS for all four tests.

- [ ] **Step 7: Commit**

```bash
git add sql/migrations/0020_email_confirmation.up.sql sql/migrations/0020_email_confirmation.down.sql \
        sql/queries/email_confirmations.sql sql/queries/users.sql sql/queries/refresh_tokens.sql \
        internal/store/ internal/store/email_confirmations_test.go
git commit -m "feat(db): add users.confirmed and email_confirmations table"
```

---

## Task 3: Config — the tri-state mode and fail-fast validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `.env.example`

**Interfaces:**
- Consumes: nothing.
- Produces (used by Tasks 8, 10, 11, 12):
  - `type ConfirmationMode string` with `config.ConfirmationOff` / `ConfirmationSend` / `ConfirmationEnforce`
  - `Config` fields: `EmailConfirmationMode ConfirmationMode`, `EmailSender string`, `EmailFromAddress string`, `AppBaseURL string`, `APIBaseURL string`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
// setMinimalEnv sets everything Load() needs to succeed with the feature off.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SIGNING_KEY", "test-key-test-key-test-key-32xx")
	t.Setenv("EMAIL_CONFIRMATION_MODE", "")
	t.Setenv("EMAIL_SENDER", "")
	t.Setenv("EMAIL_FROM_ADDRESS", "")
	t.Setenv("APP_BASE_URL", "")
	t.Setenv("API_BASE_URL", "")
}

func TestLoad_ConfirmationModeDefaultsToOff(t *testing.T) {
	setMinimalEnv(t)
	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, config.ConfirmationOff, cfg.EmailConfirmationMode)
}

func TestLoad_RejectsUnknownConfirmationMode(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_CONFIRMATION_MODE", "on")
	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "EMAIL_CONFIRMATION_MODE")
}

func TestLoad_NonOffModeRequiresEveryMailVar(t *testing.T) {
	for _, mode := range []string{"send", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("EMAIL_CONFIRMATION_MODE", mode)
			_, err := config.Load()
			require.Error(t, err)
			for _, want := range []string{"EMAIL_SENDER", "EMAIL_FROM_ADDRESS", "APP_BASE_URL", "API_BASE_URL"} {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestLoad_NonOffModeNamesOnlyTheMissingVars(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_CONFIRMATION_MODE", "enforce")
	t.Setenv("EMAIL_SENDER", "log")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "API_BASE_URL")
	require.NotContains(t, err.Error(), "EMAIL_SENDER")
}

func TestLoad_RejectsUnknownEmailSender(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_CONFIRMATION_MODE", "send")
	t.Setenv("EMAIL_SENDER", "sendgrid")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	t.Setenv("API_BASE_URL", "http://localhost:8080")
	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "EMAIL_SENDER")
}

func TestLoad_FullyConfiguredEnforceSucceeds(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("EMAIL_CONFIRMATION_MODE", "enforce")
	t.Setenv("EMAIL_SENDER", "log")
	t.Setenv("EMAIL_FROM_ADDRESS", "dev@localhost")
	t.Setenv("APP_BASE_URL", "http://localhost:5173")
	t.Setenv("API_BASE_URL", "http://localhost:8080")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, config.ConfirmationEnforce, cfg.EmailConfirmationMode)
	require.Equal(t, "log", cfg.EmailSender)
	require.Equal(t, "dev@localhost", cfg.EmailFromAddress)
	require.Equal(t, "http://localhost:5173", cfg.AppBaseURL)
	require.Equal(t, "http://localhost:8080", cfg.APIBaseURL)
}
```

If `internal/config/config_test.go` uses package `config` (internal) rather than `config_test`, drop the `config.` qualifiers and the import accordingly. Check the first line of the file before writing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestLoad_ConfirmationMode -count=1`
Expected: FAIL — `cfg.EmailConfirmationMode undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/config/config.go`, add the type above `type Config struct`:

```go
// ConfirmationMode governs email-confirmation enforcement. It is a tri-state
// rather than two booleans because "enforce without send" is a total signup
// outage and must not be expressible.
//
//	off     — user marked confirmed, no mail, no gate. Today's behavior.
//	send    — user marked confirmed, mail sent, no gate. Exercises the whole
//	          flow against real signups while nobody can be locked out.
//	enforce — user marked unconfirmed, mail sent, gate installed.
type ConfirmationMode string

const (
	ConfirmationOff     ConfirmationMode = "off"
	ConfirmationSend    ConfirmationMode = "send"
	ConfirmationEnforce ConfirmationMode = "enforce"
)
```

Add to the `Config` struct, after the Plan 7 block:

```go
	// Plan 8 additions
	EmailConfirmationMode ConfirmationMode
	EmailSender           string
	EmailFromAddress      string
	AppBaseURL            string
	APIBaseURL            string
```

In `Load()`, before the `cfg := &Config{...}` literal:

```go
	// Default to off because off is *safe* — it is current behavior. Everything
	// else is validated eagerly.
	mode := ConfirmationMode(os.Getenv("EMAIL_CONFIRMATION_MODE"))
	if mode == "" {
		mode = ConfirmationOff
	}
	switch mode {
	case ConfirmationOff, ConfirmationSend, ConfirmationEnforce:
	default:
		return nil, fmt.Errorf("invalid EMAIL_CONFIRMATION_MODE=%q (want off, send, or enforce)", mode)
	}

	emailSender := os.Getenv("EMAIL_SENDER")
	emailFrom := os.Getenv("EMAIL_FROM_ADDRESS")
	appBaseURL := os.Getenv("APP_BASE_URL")
	apiBaseURL := os.Getenv("API_BASE_URL")

	// Fail fast rather than warn (the TRUST_PROXY precedent does not fit): a
	// misconfigured rate limiter degrades availability and self-heals, but a
	// half-configured mailer strands users with no signal. A crashlooping task
	// fails the ECS rolling deploy and leaves the previous version serving,
	// which is the louder and safer failure.
	if mode != ConfirmationOff {
		var missing []string
		for _, v := range []struct {
			name, val string
		}{
			{"EMAIL_SENDER", emailSender},
			{"EMAIL_FROM_ADDRESS", emailFrom},
			{"APP_BASE_URL", appBaseURL},
			{"API_BASE_URL", apiBaseURL},
		} {
			if v.val == "" {
				missing = append(missing, v.name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("EMAIL_CONFIRMATION_MODE=%s requires %s", mode, strings.Join(missing, ", "))
		}
		if emailSender != "ses" && emailSender != "log" {
			return nil, fmt.Errorf("invalid EMAIL_SENDER=%q (want ses or log)", emailSender)
		}
	}
```

Add to the `cfg := &Config{...}` literal:

```go
		EmailConfirmationMode: mode,
		EmailSender:           emailSender,
		EmailFromAddress:      emailFrom,
		AppBaseURL:            appBaseURL,
		APIBaseURL:            apiBaseURL,
```

`strings` is already imported (used by `CORS_ALLOWED_ORIGINS`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/... -count=1 -v 2>&1 | tail -25`
Expected: PASS, including all six new tests.

- [ ] **Step 5: Document the vars in `.env.example`**

Append to `.env.example`:

```bash
# Email confirmation. Tri-state: off | send | enforce.
#   off     — user created confirmed, no mail, no gate (production default until
#             SES production access is granted; this is today's behavior).
#   send    — mail goes out but the user is already confirmed, so nobody can be
#             locked out. Used to soak deliverability.
#   enforce — user created unconfirmed and gated until they click the link.
# Local dev runs enforce with the log sender, so development exercises the real
# path and the confirmation link is pasted out of the server log.
EMAIL_CONFIRMATION_MODE=enforce
# ses | log. No fallback default — an unset value is a config error whenever
# the mode is not off.
EMAIL_SENDER=log
EMAIL_FROM_ADDRESS=dev@localhost
# SPA origin. Redirect target for the confirm link.
APP_BASE_URL=http://localhost:5173
# API origin. Used to build the link that goes in the email. Duplicates
# ICAL_BASE_URL in production; consolidating them is a deliberate follow-up.
API_BASE_URL=http://localhost:8080
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go .env.example
git commit -m "feat(config): add EMAIL_CONFIRMATION_MODE tri-state with fail-fast validation"
```

---

## Task 4: The `email` package — interface, template, log sender, test fake

**Files:**
- Create: `internal/email/email.go`, `internal/email/log.go`, `internal/email/fake.go`, `internal/email/email_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (used by Tasks 5, 8, 9, 10, 11):
  - `type Message struct { To, Subject, HTML, Text string }`
  - `type Sender interface { Send(ctx context.Context, msg Message) error }`
  - `func ConfirmationMessage(to, link string) Message`
  - `func NewLogSender() Sender`
  - `type Fake struct{ ... }` with `Send`, `Messages() []Message`, `Last() Message`, and a settable `Err error`

- [ ] **Step 1: Write the failing test**

Create `internal/email/email_test.go`:

```go
package email_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/email"
)

func TestConfirmationMessage_CarriesBothPartsAndTheLink(t *testing.T) {
	link := "https://api.example.com/auth/confirm?token=abc123"
	msg := email.ConfirmationMessage("user@example.com", link)

	require.Equal(t, "user@example.com", msg.To)
	require.NotEmpty(t, msg.Subject)
	// Both parts, so the mail is not spam-scored as HTML-only.
	require.NotEmpty(t, msg.HTML)
	require.NotEmpty(t, msg.Text)
	require.Contains(t, msg.HTML, link)
	require.Contains(t, msg.Text, link)
}

func TestConfirmationMessage_EscapesTheLinkInHTML(t *testing.T) {
	msg := email.ConfirmationMessage("user@example.com",
		"https://api.example.com/auth/confirm?token=a&b=\"x\"")
	require.NotContains(t, msg.HTML, `token=a&b="x"`, "raw ampersand/quote must be escaped")
	require.Contains(t, msg.HTML, "&amp;")
}

func TestFake_CapturesMessages(t *testing.T) {
	f := &email.Fake{}
	require.NoError(t, f.Send(context.Background(), email.Message{To: "a@example.com", Subject: "one"}))
	require.NoError(t, f.Send(context.Background(), email.Message{To: "b@example.com", Subject: "two"}))

	require.Len(t, f.Messages(), 2)
	require.Equal(t, "a@example.com", f.Messages()[0].To)
	require.Equal(t, "two", f.Last().Subject)
}

func TestFake_ReturnsConfiguredError(t *testing.T) {
	want := errors.New("ses is down")
	f := &email.Fake{Err: want}
	err := f.Send(context.Background(), email.Message{To: "a@example.com"})
	require.ErrorIs(t, err, want)
	require.Empty(t, f.Messages(), "a failed send must not be recorded as sent")
}

func TestLogSender_WritesTheLinkAndSucceeds(t *testing.T) {
	var buf strings.Builder
	s := email.NewLogSenderTo(&buf)
	require.NoError(t, s.Send(context.Background(), email.Message{
		To: "user@example.com", Subject: "Confirm", Text: "click https://x/confirm?token=t",
	}))
	out := buf.String()
	require.Contains(t, out, "user@example.com")
	require.Contains(t, out, "https://x/confirm?token=t")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/email/... -count=1`
Expected: FAIL — `no required module provides package .../internal/email`.

- [ ] **Step 3: Write the implementation**

Create `internal/email/email.go`:

```go
// Package email sends transactional mail. The Sender interface has exactly one
// method so tests can substitute a fake and no test ever touches AWS.
package email

import (
	"context"
	"fmt"
	"html"
)

// Message is one outbound email. HTML and Text are both populated on every
// message we send: an HTML-only mail scores worse with spam filters, and the
// text part is what plain-text clients render.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

const confirmationSubject = "Confirm your email address"

// ConfirmationMessage builds the signup confirmation mail. link is the full
// API URL that flips the account to confirmed.
//
// Note: this takes the recipient rather than returning a Message with an empty
// To for the caller to fill in — an incomplete Message is too easy to send.
func ConfirmationMessage(to, link string) Message {
	safeLink := html.EscapeString(link)
	return Message{
		To:      to,
		Subject: confirmationSubject,
		Text: fmt.Sprintf(`Welcome to Here's What's Happening.

Confirm your email address by opening this link:

%s

This link expires in 24 hours. If you didn't create an account, you can ignore
this message.
`, link),
		HTML: fmt.Sprintf(`<!doctype html>
<html>
  <body style="font-family: system-ui, -apple-system, sans-serif; line-height: 1.5;">
    <h1 style="font-size: 1.25rem;">Welcome to Here's What's Happening</h1>
    <p>Confirm your email address to start getting your calendar.</p>
    <p>
      <a href="%s" style="display:inline-block;padding:0.75rem 1.25rem;background:#111;color:#fff;text-decoration:none;border-radius:0.375rem;">
        Confirm email address
      </a>
    </p>
    <p style="color:#666;font-size:0.875rem;">
      This link expires in 24 hours. If you didn't create an account, you can
      ignore this message.
    </p>
    <p style="color:#666;font-size:0.75rem;word-break:break-all;">%s</p>
  </body>
</html>
`, safeLink, safeLink),
	}
}
```

Create `internal/email/log.go`:

```go
package email

import (
	"context"
	"fmt"
	"io"
	"os"
)

// logSender writes the message to a writer instead of sending it. This is the
// local-dev default: development runs the enforce path end to end and the
// confirmation link is pasted out of the server log.
type logSender struct{ out io.Writer }

// NewLogSender returns a Sender that writes to stdout.
func NewLogSender() Sender { return &logSender{out: os.Stdout} }

// NewLogSenderTo returns a Sender that writes to w. Used by tests.
func NewLogSenderTo(w io.Writer) Sender { return &logSender{out: w} }

func (s *logSender) Send(_ context.Context, msg Message) error {
	_, err := fmt.Fprintf(s.out,
		"\n=== EMAIL (not sent — EMAIL_SENDER=log) ===\nTo:      %s\nSubject: %s\n\n%s\n===========================================\n\n",
		msg.To, msg.Subject, msg.Text)
	return err
}
```

Create `internal/email/fake.go`:

```go
package email

import (
	"context"
	"sync"
)

// Fake is a Sender that records messages instead of sending them. It lives in
// the package (not a _test.go file) so handler tests in other packages can use
// it. Safe for concurrent use.
type Fake struct {
	// Err, when non-nil, is returned by every Send and the message is not
	// recorded — used to exercise send-failure paths.
	Err error

	mu   sync.Mutex
	sent []Message
}

func (f *Fake) Send(_ context.Context, msg Message) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

// Messages returns a copy of everything sent so far.
func (f *Fake) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.sent))
	copy(out, f.sent)
	return out
}

// Last returns the most recent message, or the zero Message if none were sent.
func (f *Fake) Last() Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return Message{}
	}
	return f.sent[len(f.sent)-1]
}

// Reset drops all recorded messages.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/email/... -count=1 -v 2>&1 | tail -20`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/email/
git commit -m "feat(email): add Sender interface, confirmation template, log sender and test fake"
```

---

## Task 5: The SES sender

**Files:**
- Create: `internal/email/ses.go`, `internal/email/ses_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `Sender`, `Message` from Task 4.
- Produces (used by Task 11): `func NewSESSender(ctx context.Context, region, from string) (Sender, error)`

- [ ] **Step 1: Add the dependency**

Run:
```bash
go get github.com/aws/aws-sdk-go-v2/service/sesv2@latest
```
Expected: `go: added github.com/aws/aws-sdk-go-v2/service/sesv2 vX.Y.Z`

- [ ] **Step 2: Write the failing test**

No test touches AWS. The unit under test is message construction — that the SES request carries both body parts and the right From.

Create `internal/email/ses_test.go`:

```go
package email

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSESSender_BuildsInputWithBothBodyParts(t *testing.T) {
	s := &sesSender{from: "noreply@example.com"}
	in := s.buildInput(Message{
		To:      "user@example.com",
		Subject: "Confirm your email address",
		HTML:    "<p>hi</p>",
		Text:    "hi",
	})

	require.Equal(t, "noreply@example.com", *in.FromEmailAddress)
	require.Equal(t, []string{"user@example.com"}, in.Destination.ToAddresses)
	require.Equal(t, "Confirm your email address", *in.Content.Simple.Subject.Data)
	require.Equal(t, "<p>hi</p>", *in.Content.Simple.Body.Html.Data)
	require.Equal(t, "hi", *in.Content.Simple.Body.Text.Data)
}
```

This is an in-package test (`package email`, not `email_test`) because `buildInput` and `sesSender` are unexported.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/email/... -run TestSESSender -count=1`
Expected: FAIL — `undefined: sesSender`.

- [ ] **Step 4: Write the implementation**

Create `internal/email/ses.go`:

```go
package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// sesSender sends through SES v2. The task role carries ses:SendEmail scoped
// to the apex identity (terraform/prod/iam.tf).
type sesSender struct {
	client *sesv2.Client
	from   string
}

// NewSESSender builds a Sender backed by SES in the given region. from must be
// an address on a verified identity.
func NewSESSender(ctx context.Context, region, from string) (Sender, error) {
	if from == "" {
		return nil, fmt.Errorf("email: SES sender requires a From address")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("email: load aws config: %w", err)
	}
	return &sesSender{client: sesv2.NewFromConfig(cfg), from: from}, nil
}

func (s *sesSender) buildInput(msg Message) *sesv2.SendEmailInput {
	return &sesv2.SendEmailInput{
		FromEmailAddress: &s.from,
		Destination:      &types.Destination{ToAddresses: []string{msg.To}},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: &msg.Subject},
				Body: &types.Body{
					Html: &types.Content{Data: &msg.HTML},
					Text: &types.Content{Data: &msg.Text},
				},
			},
		},
	}
}

func (s *sesSender) Send(ctx context.Context, msg Message) error {
	if _, err := s.client.SendEmail(ctx, s.buildInput(msg)); err != nil {
		return fmt.Errorf("email: ses send: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/email/... -count=1`
Expected: `ok  github.com/wmyers/heres-whats-happening/internal/email`

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add internal/email/ses.go internal/email/ses_test.go go.mod go.sum
git commit -m "feat(email): add SES v2 sender"
```

---

## Task 6: The `confirmed` JWT claim and its plumbing into request context

This is the change that makes enforcement cost zero extra DB round trips. The claim is exactly as forgeable as `sub` — both ride in the same HS256-signed payload — so it takes on no new trust. What makes it *safe* is that confirmation is monotonic: a stale claim can only ever be too restrictive (a spurious 403), never too permissive.

**Files:**
- Modify: `internal/auth/jwt.go`, `internal/auth/jwt_test.go`
- Modify: `internal/http/middleware/auth.go`, `internal/http/middleware/auth_test.go`
- Modify: `internal/http/handlers/auth.go` (call sites, to keep the tree compiling)
- Modify: `internal/http/handlers/auth_test.go`, `internal/http/server_test.go` (call sites)

**Interfaces:**
- Consumes: `store.GetUserByEmailRow.Confirmed`, `store.GetActiveRefreshTokenByHashRow.Confirmed` (Task 2).
- Produces (used by Tasks 7, 8, 10, 12):
  - `func (s *JWTSigner) SignAccess(userID uuid.UUID, confirmed bool) (string, error)`
  - `func (s *JWTSigner) VerifyAccess(tokenStr string) (uuid.UUID, bool, error)`
  - `func middleware.ConfirmedFromContext(ctx context.Context) (bool, bool)`
  - `func middleware.ContextWithConfirmed(ctx context.Context, confirmed bool) context.Context`

- [ ] **Step 1: Write the failing test**

Append to `internal/auth/jwt_test.go`:

```go
func TestSignAccess_RoundTripsConfirmedClaim(t *testing.T) {
	s := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()

	for _, confirmed := range []bool{true, false} {
		tok, err := s.SignAccess(uid, confirmed)
		require.NoError(t, err)

		gotID, gotConfirmed, err := s.VerifyAccess(tok)
		require.NoError(t, err)
		require.Equal(t, uid, gotID)
		require.Equal(t, confirmed, gotConfirmed)
	}
}

func TestVerifyAccess_RejectsTamperedConfirmedClaim(t *testing.T) {
	s := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	tok, err := s.SignAccess(uuid.New(), false)
	require.NoError(t, err)

	// Rewrite the payload to claim confirmed:true, keeping the original
	// signature. The MAC no longer covers these bytes, so it must not verify.
	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	tampered := bytes.Replace(payload, []byte(`"confirmed":false`), []byte(`"confirmed":true`), 1)
	require.NotEqual(t, payload, tampered, "payload must contain the confirmed claim")
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(tampered) + "." + parts[2]

	_, _, err = s.VerifyAccess(forged)
	require.Error(t, err)
}

func TestVerifyAccess_RejectsAlgNone(t *testing.T) {
	s := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"sub":"` + uuid.New().String() + `","confirmed":true,"exp":9999999999}`))

	_, _, err := s.VerifyAccess(header + "." + payload + ".")
	require.Error(t, err)
}
```

Add `bytes`, `encoding/base64`, and `strings` to that file's imports.

Append to `internal/http/middleware/auth_test.go`:

```go
func TestRequireAuth_PutsConfirmedInContext(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	for _, confirmed := range []bool{true, false} {
		tok, err := signer.SignAccess(uuid.New(), confirmed)
		require.NoError(t, err)

		called := false
		h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := ConfirmedFromContext(r.Context())
			require.True(t, ok)
			require.Equal(t, confirmed, got)
			called = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.True(t, called)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestSignAccess_RoundTrips -count=1`
Expected: FAIL — `too many arguments in call to s.SignAccess`.

- [ ] **Step 3: Write the implementation**

Replace the body of `internal/auth/jwt.go` below the imports:

```go
type JWTSigner struct {
	key []byte
	ttl time.Duration
}

func NewJWTSigner(signingKey string, ttl time.Duration) *JWTSigner {
	return &JWTSigner{key: []byte(signingKey), ttl: ttl}
}

// accessClaims is the access token payload. Confirmed rides alongside the
// subject so the confirmation gate costs no database round trip.
//
// This is only sound because confirmation is monotonic: it goes false -> true
// exactly once and never back, so a stale token can only be too restrictive
// (a spurious 403 the client self-heals by refreshing), never too permissive.
// The same technique would be WRONG for a revocable flag such as an admin bit,
// and it would break if a "change your email -> must re-confirm" feature were
// ever added; the fix at that point is to revoke refresh tokens on email change.
type accessClaims struct {
	Confirmed bool `json:"confirmed"`
	jwt.RegisteredClaims
}

func (s *JWTSigner) SignAccess(userID uuid.UUID, confirmed bool) (string, error) {
	now := time.Now()
	claims := accessClaims{
		Confirmed: confirmed,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.key)
}

// VerifyAccess returns the user ID and the confirmed claim. Both come from the
// post-verification claims struct — never re-decode the payload segment, which
// is unsigned base64 the caller controls.
func (s *JWTSigner) VerifyAccess(tokenStr string) (uuid.UUID, bool, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &accessClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.key, nil
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	claims, ok := parsed.Claims.(*accessClaims)
	if !ok || !parsed.Valid {
		return uuid.Nil, false, errors.New("invalid token")
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, false, err
	}
	return uid, claims.Confirmed, nil
}
```

In `internal/http/middleware/auth.go`, add the context key and accessors and set the value in `RequireAuth`:

```go
const (
	userIDKey    ctxKey = 1
	confirmedKey ctxKey = 2
)
```

Change the `RequireAuth` verify block to:

```go
			uid, confirmed, err := signer.VerifyAccess(tok)
			if err != nil {
				httperr.Write(w, http.StatusUnauthorized, "invalid_token", "access token is not valid")
				return
			}
			ctx := ContextWithConfirmed(ContextWithUserID(r.Context(), uid), confirmed)
			next.ServeHTTP(w, r.WithContext(ctx))
```

And append:

```go
// ConfirmedFromContext returns the confirmed claim from the verified access
// token. The second return is false when no authenticated request context is
// present — RequireConfirmed treats that as a rejection.
func ConfirmedFromContext(ctx context.Context) (bool, bool) {
	v := ctx.Value(confirmedKey)
	if v == nil {
		return false, false
	}
	c, ok := v.(bool)
	return c, ok
}

// ContextWithConfirmed returns ctx carrying the confirmed claim, the inverse of
// ConfirmedFromContext. RequireAuth uses it on every authenticated request;
// tests use it to build a request that looks authenticated without minting a JWT.
func ContextWithConfirmed(ctx context.Context, confirmed bool) context.Context {
	return context.WithValue(ctx, confirmedKey, confirmed)
}
```

- [ ] **Step 4: Update the call sites so the tree compiles**

In `internal/http/handlers/auth.go`:

- `Signup` (line ~87): `access, err := signer.SignAccess(userUUID, true)` — today's behavior; Task 8 makes it mode-aware.
- `Login` (line ~148): `access, err := signer.SignAccess(userUUID, row.Confirmed)`
- `Refresh` (line ~191): `access, err := signer.SignAccess(uuid.UUID(row.UserID.Bytes), row.Confirmed)`

`Signup`'s `CreateUser` call also needs the new required param — add `Confirmed: true` to the `store.CreateUserParams` literal for now.

Then fix test call sites. Run this to find them:

```bash
grep -rn "SignAccess(\|VerifyAccess(" --include="*_test.go" internal/
```

Each `SignAccess(uid)` becomes `SignAccess(uid, true)`; each `_, err := signer.VerifyAccess(tok)` becomes `_, _, err := signer.VerifyAccess(tok)`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/auth/... ./internal/http/... -count=1 -p 1`
Expected: `ok` for `internal/auth`, `internal/http`, `internal/http/handlers`, `internal/http/middleware`.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/ internal/http/
git commit -m "feat(auth): carry confirmed as a signed JWT claim and into request context"
```

---

## Task 7: The `RequireConfirmed` middleware

**Files:**
- Create: `internal/http/middleware/confirmed.go`, `internal/http/middleware/confirmed_test.go`
- Modify: `internal/http/middleware/ratelimit.go:22-38` (endpoint constants)

**Interfaces:**
- Consumes: `ConfirmedFromContext` (Task 6).
- Produces (used by Task 10): `func RequireConfirmed() func(http.Handler) http.Handler`; `middleware.EndpointConfirm`, `middleware.EndpointConfirmResend`.

- [ ] **Step 1: Write the failing test**

Create `internal/http/middleware/confirmed_test.go`:

```go
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequireConfirmed_AllowsConfirmedUser(t *testing.T) {
	called := false
	h := RequireConfirmed()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ContextWithConfirmed(req.Context(), true))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}

func TestRequireConfirmed_RejectsUnconfirmedUserWith403(t *testing.T) {
	called := false
	h := RequireConfirmed()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ContextWithConfirmed(req.Context(), false))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, called)

	// The code is the contract the SPA's refresh-and-retry keys on.
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Equal(t, "confirmation_required", body.Error.Code)
}

func TestRequireConfirmed_RejectsWhenClaimAbsent(t *testing.T) {
	h := RequireConfirmed()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run without an authenticated context")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/http/middleware/... -run TestRequireConfirmed -count=1`
Expected: FAIL — `undefined: RequireConfirmed`.

- [ ] **Step 3: Write the implementation**

Create `internal/http/middleware/confirmed.go`:

```go
package middleware

import (
	"net/http"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
)

// RequireConfirmed rejects users whose access token does not carry
// confirmed:true. Install it inside a RequireAuth group — it reads the claim
// out of the request context and takes no *store.Queries, so the gate is hard
// (enforced at the API, not by a cooperating browser) at zero query cost.
//
// A missing claim is a rejection, not a pass: it means the middleware was
// installed outside a RequireAuth group, and failing closed is the only safe
// reading of "we don't know whether this user is confirmed".
func RequireConfirmed() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			confirmed, ok := ConfirmedFromContext(r.Context())
			if !ok || !confirmed {
				httperr.Write(w, http.StatusForbidden, "confirmation_required",
					"confirm your email address to use this feature")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

In `internal/http/middleware/ratelimit.go`, add to the const block:

```go
	// Public (IP-keyed)
	EndpointLogout   = "logout"
	EndpointIcalFeed = "ical_feed"
	EndpointReadyz   = "readyz"
	EndpointConfirm  = "confirm"

	// Authenticated (user-keyed)
	EndpointConfirmResend = "confirm_resend"
```

Place `EndpointConfirmResend` next to the other user-keyed constants and `EndpointConfirm` next to the IP-keyed ones. Both are emit-only — neither needs an alarm key in `terraform/prod/observability.tf`, and the doc comment above the block already says the alarm-mirrored subset is signup/login/refresh/authed/manual_interests/spotify_exchange/ical_feed, so it needs no edit.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/middleware/... -count=1 -v 2>&1 | tail -20`
Expected: PASS for all three new tests.

- [ ] **Step 5: Commit**

```bash
git add internal/http/middleware/confirmed.go internal/http/middleware/confirmed_test.go internal/http/middleware/ratelimit.go
git commit -m "feat(middleware): add RequireConfirmed gate and confirm endpoint constants"
```

---

## Task 8: Mode-aware signup that mints and sends the confirmation link

**Files:**
- Create: `internal/http/handlers/confirm.go` (the shared `sendConfirmation` helper only; the handlers land in Tasks 9 and 10)
- Modify: `internal/http/handlers/auth.go`
- Modify: `internal/http/handlers/auth_test.go`

**Interfaces:**
- Consumes: `config.ConfirmationMode` (Task 3), `email.Sender` + `email.ConfirmationMessage` (Task 4), `q.UpsertEmailConfirmation` (Task 2), `SignAccess(uuid, bool)` (Task 6).
- Produces (used by Tasks 9, 10, 11):
  - `type ConfirmationDeps struct { Mode config.ConfirmationMode; Sender email.Sender; APIBaseURL, AppBaseURL string }`
  - `func Signup(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration, cityID string, conf ConfirmationDeps) http.HandlerFunc`
  - `func sendConfirmation(ctx context.Context, q *store.Queries, conf ConfirmationDeps, toEmail string, userID pgtype.UUID) error` (unexported)
  - `const confirmationTTL = 24 * time.Hour`
  - `userOut` gains `Confirmed bool \`json:"confirmed"\``

- [ ] **Step 1: Write the failing test**

Append to `internal/http/handlers/auth_test.go`:

```go
// signupWith runs a signup in the given mode and returns the recorder plus the
// fake sender, so a test can assert on both the response and the mail.
func signupWith(t *testing.T, mode config.ConfirmationMode, address string) (*store.Queries, *httptest.ResponseRecorder, *email.Fake) {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	fake := &email.Fake{}
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{
		Mode:       mode,
		Sender:     fake,
		APIBaseURL: "https://api.example.com",
		AppBaseURL: "https://app.example.com",
	})

	body, _ := json.Marshal(map[string]string{"email": address, "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return q, rec, fake
}

func confirmedOf(t *testing.T, q *store.Queries, address string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row, err := q.GetUserByEmail(ctx, address)
	require.NoError(t, err)
	return row.Confirmed
}

func TestSignup_ModeOff_ConfirmedAndNoMail(t *testing.T) {
	q, rec, fake := signupWith(t, config.ConfirmationOff, "off@example.com")

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, confirmedOf(t, q, "off@example.com"))
	require.Empty(t, fake.Messages(), "off must send nothing — it is today's behavior byte for byte")
}

func TestSignup_ModeSend_ConfirmedAndMailSent(t *testing.T) {
	q, rec, fake := signupWith(t, config.ConfirmationSend, "send@example.com")

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, confirmedOf(t, q, "send@example.com"),
		"send must leave the user confirmed so nobody can be locked out")
	require.Len(t, fake.Messages(), 1)
	require.Equal(t, "send@example.com", fake.Last().To)
	require.Contains(t, fake.Last().Text, "https://api.example.com/auth/confirm?token=")
}

func TestSignup_ModeEnforce_UnconfirmedTokenRowAndMailSent(t *testing.T) {
	q, rec, fake := signupWith(t, config.ConfirmationEnforce, "enforce@example.com")

	require.Equal(t, http.StatusCreated, rec.Code)
	require.False(t, confirmedOf(t, q, "enforce@example.com"))
	require.Len(t, fake.Messages(), 1)

	// A token row exists and resolves by the hash of the token in the link.
	link := extractConfirmLink(t, fake.Last().Text)
	tok := link[strings.Index(link, "token=")+len("token="):]
	ctx := context.Background()
	row, err := q.GetEmailConfirmationByHash(ctx, auth.HashRefresh(tok))
	require.NoError(t, err)
	require.False(t, row.ConsumedAt.Valid)
	require.True(t, row.ExpiresAt.Time.After(time.Now().Add(23*time.Hour)))
}

func TestSignup_ResponseCarriesConfirmed(t *testing.T) {
	_, rec, _ := signupWith(t, config.ConfirmationEnforce, "body@example.com")

	var resp struct {
		User struct {
			Confirmed bool `json:"confirmed"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.False(t, resp.User.Confirmed)
}

func TestSignup_SendFailureStillReturns201(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	fake := &email.Fake{Err: errors.New("ses is down")}
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q), handlers.ConfirmationDeps{
		Mode: config.ConfirmationEnforce, Sender: fake,
		APIBaseURL: "https://api.example.com", AppBaseURL: "https://app.example.com",
	})

	body, _ := json.Marshal(map[string]string{"email": "sendfail@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	// The user still reaches /confirm-email, where resend is one click away.
	require.Equal(t, http.StatusCreated, rec.Code)
	require.False(t, confirmedOf(t, q, "sendfail@example.com"))
}

// extractConfirmLink pulls the confirm URL out of a plain-text mail body.
func extractConfirmLink(t *testing.T, text string) string {
	t.Helper()
	for _, f := range strings.Fields(text) {
		if strings.Contains(f, "/auth/confirm?token=") {
			return f
		}
	}
	t.Fatalf("no confirm link in message body: %q", text)
	return ""
}
```

Add `errors`, `strings`, and imports for `config`, `email`, and `auth` to that file as needed.

**Update the existing signup/login tests in this file** for the new `Signup` signature — every `handlers.Signup(q, signer, time.Hour, cityID)` becomes:

```go
handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{
	Mode: config.ConfirmationOff, Sender: &email.Fake{},
})
```

`ConfirmationOff` keeps those tests asserting exactly what they assert today.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/http/handlers/... -run TestSignup_Mode -count=1`
Expected: FAIL — `undefined: handlers.ConfirmationDeps`.

- [ ] **Step 3: Write the implementation**

Create `internal/http/handlers/confirm.go`:

```go
package handlers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// confirmationTTL is how long an emailed confirmation link stays valid.
const confirmationTTL = 24 * time.Hour

// ConfirmationDeps is what the auth handlers need to mint, send, and honor
// confirmation links. Grouped into a struct because Signup, ConfirmEmail, and
// ResendConfirmation all need overlapping subsets of it.
type ConfirmationDeps struct {
	Mode   config.ConfirmationMode
	Sender email.Sender
	// APIBaseURL builds the link that goes in the mail — it points at this API,
	// because the mail client navigates straight to GET /auth/confirm.
	APIBaseURL string
	// AppBaseURL is where that handler redirects the browser afterwards.
	AppBaseURL string
}

// sendConfirmation mints a fresh confirmation token for userID — replacing any
// previous one, so a resend invalidates the older link — and emails it.
func sendConfirmation(ctx context.Context, q *store.Queries, conf ConfirmationDeps, toEmail string, userID pgtype.UUID) error {
	raw, err := auth.GenerateRefresh()
	if err != nil {
		return fmt.Errorf("generate confirmation token: %w", err)
	}
	if err := q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID:    userID,
		TokenHash: auth.HashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(confirmationTTL), Valid: true},
	}); err != nil {
		return fmt.Errorf("persist confirmation token: %w", err)
	}
	link := conf.APIBaseURL + "/auth/confirm?token=" + url.QueryEscape(raw)
	if err := conf.Sender.Send(ctx, email.ConfirmationMessage(toEmail, link)); err != nil {
		return fmt.Errorf("send confirmation mail: %w", err)
	}
	return nil
}
```

In `internal/http/handlers/auth.go`:

Add `Confirmed` to `userOut`:

```go
type userOut struct {
	ID             string   `json:"id"`
	Email          string   `json:"email"`
	Confirmed      bool     `json:"confirmed"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}
```

Change the `Signup` signature and body:

```go
// Signup creates a new user, sets the refresh cookie, and returns an access token.
// cityID is the default city assignment for v1.
//
// conf.Mode decides whether the new user is confirmed and whether mail goes
// out. Only enforce creates an unconfirmed user — send still confirms, so the
// whole flow can be exercised against real signups with nobody locked out.
func Signup(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration, cityID string, conf ConfirmationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ... unchanged through the pwhash/cityUUID/ctx setup ...

		// Known from the mode without asking the database.
		confirmed := conf.Mode != config.ConfirmationEnforce

		row, err := q.CreateUser(ctx, store.CreateUserParams{
			Email:        req.Email,
			PasswordHash: hash,
			CityID:       pgtype.UUID{Bytes: cityUUID, Valid: true},
			Confirmed:    confirmed,
		})
		// ... unchanged duplicate-email / db_error handling ...

		userUUID := uuid.UUID(row.ID.Bytes)
		access, err := signer.SignAccess(userUUID, confirmed)
		// ... unchanged refresh-token minting and cookie ...

		// A send failure is logged but must not fail the request: the user still
		// reaches /confirm-email, where resend is one click away.
		if conf.Mode != config.ConfirmationOff {
			if err := sendConfirmation(ctx, q, conf, row.Email, row.ID); err != nil {
				log.Printf("[%s] signup: confirmation mail for %s: %v",
					chimw.GetReqID(r.Context()), row.Email, err)
			}
		}

		writeJSON(w, http.StatusCreated, signupResponse{
			AccessToken: access,
			User:        userOut{ID: row.ID.String(), Email: row.Email, Confirmed: row.Confirmed},
		})
	}
}
```

Add `log`, `chimw "github.com/go-chi/chi/v5/middleware"`, and the `config` import to `auth.go`.

`CreateUser` takes `confirmed` explicitly rather than leaning on the column default, so the *mode* decides, not the schema.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/handlers/... -count=1 -v -run TestSignup 2>&1 | tail -25`
Expected: PASS for all signup tests, old and new.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/confirm.go internal/http/handlers/auth.go internal/http/handlers/auth_test.go
git commit -m "feat(signup): mint and send confirmation links per EMAIL_CONFIRMATION_MODE"
```

---

## Task 9: `GET /auth/confirm` — the redirect handler

**Files:**
- Modify: `internal/http/handlers/confirm.go`
- Create: `internal/http/handlers/confirm_test.go`

**Interfaces:**
- Consumes: `sendConfirmation`, `ConfirmationDeps` (Task 8); `q.GetEmailConfirmationByHash`, `q.MarkUserConfirmed`, `q.ConsumeEmailConfirmation` (Task 2).
- Produces (used by Task 10): `func ConfirmEmail(q *store.Queries, conf ConfirmationDeps) http.HandlerFunc`

- [ ] **Step 1: Write the failing test**

Create `internal/http/handlers/confirm_test.go`:

```go
package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

const (
	welcomeURL = "https://app.example.com/?welcome=true"
	errorURL   = "https://app.example.com/?confirmerror=true"
)

func testDeps(sender email.Sender) handlers.ConfirmationDeps {
	return handlers.ConfirmationDeps{
		Mode:       config.ConfirmationEnforce,
		Sender:     sender,
		APIBaseURL: "https://api.example.com",
		AppBaseURL: "https://app.example.com",
	}
}

// newUnconfirmedUser creates an unconfirmed user with a confirmation token and
// returns the queries handle, the user id, and the raw token.
func newUnconfirmedUser(t *testing.T, address string, expiresIn time.Duration) (*store.Queries, pgtype.UUID, string) {
	t.Helper()
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()

	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: address, PasswordHash: "x", CityID: city.ID, Confirmed: false,
	})
	require.NoError(t, err)

	raw, err := auth.GenerateRefresh()
	require.NoError(t, err)
	require.NoError(t, q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID:    row.ID,
		TokenHash: auth.HashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(expiresIn), Valid: true},
	}))
	return q, row.ID, raw
}

func confirmGet(t *testing.T, q *store.Queries, token string) *httptest.ResponseRecorder {
	t.Helper()
	h := handlers.ConfirmEmail(q, testDeps(&email.Fake{}))
	req := httptest.NewRequest(http.MethodGet, "/auth/confirm?token="+token, nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestConfirmEmail_HappyPathFlipsConfirmedAndRedirectsToWelcome(t *testing.T) {
	q, uid, tok := newUnconfirmedUser(t, "happy@example.com", 24*time.Hour)

	rec := confirmGet(t, q, tok)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, welcomeURL, rec.Header().Get("Location"))

	row, err := q.GetUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.True(t, row.Confirmed)
}

func TestConfirmEmail_ConsumedReplayStillRedirectsToWelcome(t *testing.T) {
	q, _, tok := newUnconfirmedUser(t, "replay@example.com", 24*time.Hour)

	// First fetch: the corporate mail scanner (Outlook SafeLinks) prefetching.
	require.Equal(t, welcomeURL, confirmGet(t, q, tok).Header().Get("Location"))

	// Second fetch: the human actually clicking. Telling them their link failed
	// would be a lie — they are confirmed.
	rec := confirmGet(t, q, tok)
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, welcomeURL, rec.Header().Get("Location"))
}

func TestConfirmEmail_ExpiredTokenRedirectsToError(t *testing.T) {
	q, uid, tok := newUnconfirmedUser(t, "expired@example.com", -1*time.Hour)

	rec := confirmGet(t, q, tok)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, errorURL, rec.Header().Get("Location"))

	row, err := q.GetUserByID(context.Background(), uid)
	require.NoError(t, err)
	require.False(t, row.Confirmed, "an expired link must not confirm")
}

func TestConfirmEmail_UnknownTokenRedirectsToError(t *testing.T) {
	q, _, _ := newUnconfirmedUser(t, "unknown@example.com", 24*time.Hour)

	rec := confirmGet(t, q, "not-a-real-token")

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, errorURL, rec.Header().Get("Location"))
}

func TestConfirmEmail_MissingTokenRedirectsToError(t *testing.T) {
	q, _, _ := newUnconfirmedUser(t, "notoken@example.com", 24*time.Hour)

	h := handlers.ConfirmEmail(q, testDeps(&email.Fake{}))
	req := httptest.NewRequest(http.MethodGet, "/auth/confirm", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, errorURL, rec.Header().Get("Location"))
}

func TestConfirmEmail_NeverReturnsJSON(t *testing.T) {
	q, _, tok := newUnconfirmedUser(t, "nojson@example.com", 24*time.Hour)

	for _, token := range []string{tok, "bogus"} {
		rec := confirmGet(t, q, token)
		require.Equal(t, http.StatusFound, rec.Code)
		require.NotContains(t, rec.Header().Get("Content-Type"), "application/json")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/http/handlers/... -run TestConfirmEmail -count=1`
Expected: FAIL — `undefined: handlers.ConfirmEmail`.

- [ ] **Step 3: Write the implementation**

Append to `internal/http/handlers/confirm.go`:

```go
// ConfirmEmail validates the emailed token, flips users.confirmed, and 302s
// back into the SPA. It ALWAYS redirects and never returns JSON — this is a
// browser navigation from a mail client, not an API call.
//
// Only two states produce the error redirect: an unknown token, and an
// unconsumed token past its expiry. Everything else lands on ?welcome=true.
func ConfirmEmail(q *store.Queries, conf ConfirmationDeps) http.HandlerFunc {
	welcome := conf.AppBaseURL + "/?welcome=true"
	confirmError := conf.AppBaseURL + "/?confirmerror=true"

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetEmailConfirmationByHash(ctx, auth.HashRefresh(token))
		if err != nil {
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}

		// Already consumed: almost always a mail-security scanner having
		// prefetched the link before the human clicked it. The account is
		// confirmed, so send them to the welcome page — reporting a failure
		// here would tell a successfully confirmed user their link was broken.
		if row.ConsumedAt.Valid {
			http.Redirect(w, r, welcome, http.StatusFound)
			return
		}

		if row.ExpiresAt.Time.Before(time.Now()) {
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}

		// Order matters: confirm the user FIRST, then mark the token consumed.
		// The reverse order can strand a user — a consumed token whose owner is
		// still unconfirmed would redirect every future click to ?welcome=true
		// while the gate keeps rejecting them.
		if err := q.MarkUserConfirmed(ctx, row.UserID); err != nil {
			log.Printf("[%s] confirm: mark confirmed: %v", chimw.GetReqID(r.Context()), err)
			http.Redirect(w, r, confirmError, http.StatusFound)
			return
		}
		if err := q.ConsumeEmailConfirmation(ctx, row.UserID); err != nil {
			// Non-fatal: the user is confirmed. An unconsumed row just means a
			// replay re-runs an idempotent update.
			log.Printf("[%s] confirm: mark consumed: %v", chimw.GetReqID(r.Context()), err)
		}

		http.Redirect(w, r, welcome, http.StatusFound)
	}
}
```

Add `log`, `net/http`, and `chimw "github.com/go-chi/chi/v5/middleware"` to `confirm.go`'s imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/handlers/... -run TestConfirmEmail -count=1 -v 2>&1 | tail -25`
Expected: PASS for all six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/confirm.go internal/http/handlers/confirm_test.go
git commit -m "feat(confirm): add GET /auth/confirm redirect handler"
```

---

## Task 10: `POST /auth/confirm/resend` and `confirmed` on `GET /me`

**Files:**
- Modify: `internal/http/handlers/confirm.go`, `internal/http/handlers/confirm_test.go`
- Modify: `internal/http/handlers/user.go`
- Modify: `internal/http/handlers/user_test.go`

**Interfaces:**
- Consumes: `sendConfirmation` (Task 8), `middleware.UserIDFromContext`, `q.GetUserByID` (Task 2).
- Produces (used by Task 11): `func ResendConfirmation(q *store.Queries, conf ConfirmationDeps) http.HandlerFunc`; `GET /me` response gains `confirmed`.

- [ ] **Step 1: Write the failing test**

Append to `internal/http/handlers/confirm_test.go`:

```go
func resendPost(t *testing.T, q *store.Queries, uid pgtype.UUID, sender email.Sender) *httptest.ResponseRecorder {
	t.Helper()
	h := handlers.ResendConfirmation(q, testDeps(sender))
	req := httptest.NewRequest(http.MethodPost, "/auth/confirm/resend", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), uuid.UUID(uid.Bytes)))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestResendConfirmation_SendsFreshLinkAndInvalidatesThePrior(t *testing.T) {
	q, uid, oldTok := newUnconfirmedUser(t, "resend@example.com", 24*time.Hour)
	fake := &email.Fake{}

	rec := resendPost(t, q, uid, fake)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Len(t, fake.Messages(), 1)
	require.Equal(t, "resend@example.com", fake.Last().To)

	// The prior link stops working — one live token per user.
	_, err := q.GetEmailConfirmationByHash(context.Background(), auth.HashRefresh(oldTok))
	require.Error(t, err)

	// The new one resolves and is unconsumed.
	newLink := extractConfirmLink(t, fake.Last().Text)
	newTok := newLink[strings.Index(newLink, "token=")+len("token="):]
	row, err := q.GetEmailConfirmationByHash(context.Background(), auth.HashRefresh(newTok))
	require.NoError(t, err)
	require.False(t, row.ConsumedAt.Valid)
}

func TestResendConfirmation_AlreadyConfirmedIsANoOp204(t *testing.T) {
	q, uid, _ := newUnconfirmedUser(t, "already@example.com", 24*time.Hour)
	require.NoError(t, q.MarkUserConfirmed(context.Background(), uid))
	fake := &email.Fake{}

	rec := resendPost(t, q, uid, fake)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, fake.Messages(), "a confirmed user must not be re-mailed")
}

func TestResendConfirmation_SendFailureReturns500(t *testing.T) {
	q, uid, _ := newUnconfirmedUser(t, "resendfail@example.com", 24*time.Hour)

	// Unlike signup, the user explicitly asked for this — surface the failure
	// so the UI can say the mail did not go out.
	rec := resendPost(t, q, uid, &email.Fake{Err: errors.New("ses is down")})
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestResendConfirmation_NoUserInContextReturns401(t *testing.T) {
	q, _, _ := newUnconfirmedUser(t, "nouser@example.com", 24*time.Hour)

	h := handlers.ResendConfirmation(q, testDeps(&email.Fake{}))
	req := httptest.NewRequest(http.MethodPost, "/auth/confirm/resend", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

Add `errors`, `strings`, `github.com/google/uuid`, and `internal/http/middleware` to the imports.

Append to `internal/http/handlers/user_test.go`:

```go
func TestGetMe_ReturnsConfirmed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "me-confirmed@example.com", PasswordHash: "x", CityID: city.ID, Confirmed: false,
	})
	require.NoError(t, err)
	uid := uuid.UUID(row.ID.Bytes)

	call := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req = req.WithContext(middleware.ContextWithUserID(req.Context(), uid))
		rec := httptest.NewRecorder()
		handlers.GetMe(q)(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		return body
	}

	require.Equal(t, false, call()["confirmed"])
	require.NoError(t, q.MarkUserConfirmed(ctx, row.ID))
	require.Equal(t, true, call()["confirmed"])
}
```

Match the existing import style of `user_test.go`; add anything missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/http/handlers/... -run "TestResendConfirmation|TestGetMe_ReturnsConfirmed" -count=1`
Expected: FAIL — `undefined: handlers.ResendConfirmation`.

- [ ] **Step 3: Write the implementation**

Append to `internal/http/handlers/confirm.go`:

```go
// ResendConfirmation mints a fresh link for the authenticated user and mails
// it, invalidating the previous one. It is a no-op 204 when the user is
// already confirmed, so a stale tab cannot generate pointless mail.
//
// This route is deliberately exempt from the confirmation gate: it is one of
// the few things an unconfirmed user must be able to do.
func ResendConfirmation(q *store.Queries, conf ConfirmationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		pgUID := pgtype.UUID{Bytes: uid, Valid: true}
		row, err := q.GetUserByID(ctx, pgUID)
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "no_user", "user not found")
			return
		}
		if row.Confirmed {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if err := sendConfirmation(ctx, q, conf, row.Email, pgUID); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, "send_failed",
				"could not send the confirmation email", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

Add `internal/http/httperr` and `internal/http/middleware` to `confirm.go`'s imports.

In `internal/http/handlers/user.go`, add `Confirmed` to the `GetMe` response:

```go
		writeJSON(w, http.StatusOK, userOut{
			ID:             uid.String(),
			Email:          row.Email,
			Confirmed:      row.Confirmed,
			ScoreThreshold: threshold,
		})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/http/handlers/... -count=1 2>&1 | tail -10`
Expected: `ok  github.com/wmyers/heres-whats-happening/internal/http/handlers`

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/confirm.go internal/http/handlers/confirm_test.go internal/http/handlers/user.go internal/http/handlers/user_test.go
git commit -m "feat(confirm): add resend endpoint and expose confirmed on GET /me"
```

---

## Task 11: Routing, server wiring, and `main.go`

The routing change is the load-bearing one: **guarded is the default**. Rather than exempting routes inside the guarded group — where a route added later would silently land outside the gate — the exemptions are hoisted into their own small group.

**Files:**
- Modify: `internal/http/server.go`
- Modify: `internal/http/server_test.go`
- Modify: `cmd/app/main.go`

**Interfaces:**
- Consumes: everything from Tasks 3–10.
- Produces: `Server` fields `EmailConfirmationMode config.ConfirmationMode`, `EmailSender email.Sender`, `AppBaseURL string`, `APIBaseURL string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/http/server_test.go`:

```go
// newTestServerWithMode starts a server in the given confirmation mode and
// returns it alongside the fake sender.
func newTestServerWithMode(t *testing.T, mode config.ConfirmationMode) (*httptest.Server, *email.Fake) {
	t.Helper()

	old := observability.Default
	observability.Default = observability.NewEmitter(&bytes.Buffer{})
	t.Cleanup(func() { observability.Default = old })

	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	fake := &email.Fake{}
	s := &hs.Server{
		DB:                    pool,
		Queries:               q,
		JWTSigner:             auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL:            time.Hour,
		DefaultCityID:         uuid.UUID(city.ID.Bytes).String(),
		EmailConfirmationMode: mode,
		EmailSender:           fake,
		AppBaseURL:            "https://app.example.com",
		APIBaseURL:            "https://api.example.com",
	}
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv, fake
}

// signupOn signs up and returns the access token.
func signupOn(t *testing.T, srv *httptest.Server, address string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": address, "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.AccessToken
}

func authedGet(t *testing.T, srv *httptest.Server, path, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestServer_Enforce_UnconfirmedIsGatedOffGuardedRoutes(t *testing.T) {
	srv, _ := newTestServerWithMode(t, config.ConfirmationEnforce)
	tok := signupOn(t, srv, "gated@example.com")

	require.Equal(t, http.StatusForbidden, authedGet(t, srv, "/me/calendar", tok))
	require.Equal(t, http.StatusForbidden, authedGet(t, srv, "/me/manual-interests", tok))
}

func TestServer_Enforce_ExemptRoutesStayReachable(t *testing.T) {
	srv, _ := newTestServerWithMode(t, config.ConfirmationEnforce)
	tok := signupOn(t, srv, "exempt@example.com")

	// What an unconfirmed user needs to get confirmed, or to leave.
	require.Equal(t, http.StatusOK, authedGet(t, srv, "/me", tok))

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/auth/confirm/resend", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// This is the assertion that makes phase 2 safe to merge: with the mode unset
// or set to send, a route that 403s under enforce must still return 200.
func TestServer_OffAndSend_GuardedRoutesStayOpen(t *testing.T) {
	for _, mode := range []config.ConfirmationMode{config.ConfirmationOff, config.ConfirmationSend} {
		t.Run(string(mode), func(t *testing.T) {
			srv, _ := newTestServerWithMode(t, mode)
			tok := signupOn(t, srv, string(mode)+"-open@example.com")
			require.Equal(t, http.StatusOK, authedGet(t, srv, "/me/calendar", tok))
		})
	}
}

func TestServer_ConfirmLinkEndToEnd(t *testing.T) {
	srv, fake := newTestServerWithMode(t, config.ConfirmationEnforce)
	tok := signupOn(t, srv, "e2e-confirm@example.com")
	require.Equal(t, http.StatusForbidden, authedGet(t, srv, "/me/calendar", tok))

	// Follow the emailed link against this server, not the configured API host.
	require.Len(t, fake.Messages(), 1)
	link := extractConfirmLink(t, fake.Last().Text)
	path := link[strings.Index(link, "/auth/confirm"):]

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noRedirect.Get(srv.URL + path)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "https://app.example.com/?welcome=true", resp.Header.Get("Location"))

	// The old token still carries confirmed:false — that is the bounded stale
	// case the SPA self-heals by refreshing. A freshly minted one passes.
	require.Equal(t, http.StatusForbidden, authedGet(t, srv, "/me/calendar", tok))
}
```

`extractConfirmLink` lives in `internal/http/handlers/auth_test.go` (package `handlers_test`), so `server_test.go` (package `http_test`) cannot see it — add a local copy to `server_test.go`:

```go
func extractConfirmLink(t *testing.T, text string) string {
	t.Helper()
	for _, f := range strings.Fields(text) {
		if strings.Contains(f, "/auth/confirm?token=") {
			return f
		}
	}
	t.Fatalf("no confirm link in message body: %q", text)
	return ""
}
```

Add `strings`, `config`, and `email` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/http/ -run TestServer_Enforce -count=1`
Expected: FAIL — `unknown field EmailConfirmationMode in struct literal`.

- [ ] **Step 3: Write the implementation**

In `internal/http/server.go`, add the fields to `Server`:

```go
	// Plan 8 additions — email confirmation. The mode gates only whether
	// RequireConfirmed is installed; the route layout is identical in all three
	// modes, so flipping the mode changes which middleware runs, never which
	// routes exist.
	EmailConfirmationMode config.ConfirmationMode
	EmailSender           email.Sender
	AppBaseURL            string
	APIBaseURL            string
```

Add a helper so handlers get one struct:

```go
func (s *Server) confirmationDeps() handlers.ConfirmationDeps {
	return handlers.ConfirmationDeps{
		Mode:       s.EmailConfirmationMode,
		Sender:     s.EmailSender,
		APIBaseURL: s.APIBaseURL,
		AppBaseURL: s.AppBaseURL,
	}
}
```

Add the two limiters next to the others:

```go
	// Confirmation. IP-keyed: the emailed link is followed by a browser with no
	// Authorization header.
	confirmLimiter := ratelimit.NewMemory(20, time.Hour)
	// User-keyed: each resend costs an outbound email.
	confirmResendLimiter := ratelimit.NewMemory(3, time.Hour)
```

Update the signup registration and add the public confirm route:

```go
	r.With(middleware.RateLimitOnSuccess(signupLimiter, middleware.EndpointSignup)).
		Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID, s.confirmationDeps()))
	// ... login / refresh / logout unchanged ...
	r.With(middleware.RateLimit(confirmLimiter, middleware.EndpointConfirm)).
		Get("/auth/confirm", handlers.ConfirmEmail(s.Queries, s.confirmationDeps()))
```

Then replace the single authenticated group with two. The exempt group comes first; everything else keeps the gate via `Use`:

```go
	// Authenticated, EXEMPT from the confirmation gate: what an unconfirmed
	// user needs to get confirmed, or to leave. Kept as its own small group so
	// the guarded group below can gate by default — a route added there later
	// is covered automatically, which is the property that matters.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		// Same limiter instances as the guarded group, so budgets do not double.
		r.Use(middleware.RateLimitByUser(authedLimiter, middleware.EndpointAuthed))

		r.Get("/me", handlers.GetMe(s.Queries))
		r.With(middleware.RateLimitByUser(confirmResendLimiter, middleware.EndpointConfirmResend)).
			Post("/auth/confirm/resend", handlers.ResendConfirmation(s.Queries, s.confirmationDeps()))
		// Exempt on purpose: an unconfirmed user must still be able to delete
		// their account.
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Delete("/me", handlers.DeleteMe(s.Queries))
	})

	// Authenticated + confirmed. Everything else, including routes added later.
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		// This line must stay above every nested r.Group below: chi copies the
		// middleware stack by value at Group()/With() time, so a group inserted
		// above it would snapshot an incomplete stack and its routes would be
		// silently unlimited.
		r.Use(middleware.RateLimitByUser(authedLimiter, middleware.EndpointAuthed))
		if s.EmailConfirmationMode == config.ConfirmationEnforce {
			r.Use(middleware.RequireConfirmed())
		}

		// ... every existing authenticated route, unchanged, EXCEPT the three
		// moved into the exempt group above: GET /me, DELETE /me. ...
	})
```

Concretely, in the guarded group: delete the `r.Get("/me", ...)` line and the `r.Delete("/me", ...)` line from the write sub-group. Everything else stays exactly as it is.

Add `config` and `email` to `server.go`'s imports.

- [ ] **Step 4: Wire `main.go`**

In `cmd/app/main.go`, before the `s := &hs.Server{...}` literal:

```go
	// config.Load has already rejected an unset/unknown EMAIL_SENDER whenever
	// the mode is not off. The log sender is the fallback for mode=off, where
	// nothing is ever sent — a nil Sender would be a panic waiting on a future
	// mode flip.
	var mailer email.Sender
	switch cfg.EmailSender {
	case "ses":
		mailer, err = email.NewSESSender(ctx, cfg.AWSRegion, cfg.EmailFromAddress)
		if err != nil {
			return fmt.Errorf("email sender: %w", err)
		}
	default:
		mailer = email.NewLogSender()
	}
```

Add to the `hs.Server{...}` literal:

```go
		EmailConfirmationMode: cfg.EmailConfirmationMode,
		EmailSender:           mailer,
		AppBaseURL:            cfg.AppBaseURL,
		APIBaseURL:            cfg.APIBaseURL,
```

Add the `internal/email` import.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go build ./... && go test -p 1 ./... -count=1 2>&1 | tail -35`
Expected: `ok` for every package; no `FAIL` lines.

- [ ] **Step 6: Commit**

```bash
git add internal/http/server.go internal/http/server_test.go cmd/app/main.go
git commit -m "feat(http): gate authenticated routes behind confirmation, default-guarded"
```

---

## Task 12: Terraform — declare the five env vars (phase 3/4 reference)

**Files:**
- Modify: `terraform/prod/ecs_api.tf:8-40` (`local.api_env_vars`)

**Interfaces:**
- Consumes: nothing at apply time.
- Produces: nothing consumed by other tasks — documentation and drift-reference only.

- [ ] **Step 1: Add the vars with the no-auto-apply comment**

Append inside `local.api_env_vars`, after the `TRUST_PROXY` entry:

```hcl
    # NOT AUTO-APPLIED, same as TRUST_PROXY above: the task def has
    # ignore_changes = [container_definitions] and the app pipeline re-registers
    # the live task def with only the image swapped. These values reach the
    # running task only through a manual `scripts/taskdef-edit.sh --set-env ...
    # --deploy`, in rollout phases 3 (send) and 4 (enforce).
    #
    # EMAIL_CONFIRMATION_MODE is declared here as its intended END STATE, so
    # this file records where the system is headed rather than whichever phase
    # it currently happens to be in. The live value will be `off` from the app
    # merge until SES production access is granted, then `send`, then `enforce`.
    { name = "EMAIL_CONFIRMATION_MODE", value = "enforce" },
    { name = "EMAIL_SENDER", value = "ses" },
    { name = "EMAIL_FROM_ADDRESS", value = "noreply@${var.domain_name}" },
    { name = "APP_BASE_URL", value = "https://${var.domain_name}" },
    # Duplicates ICAL_BASE_URL above. Consolidating them is a deliberate
    # follow-up: renaming an env var that reaches prod only through a manual
    # taskdef step is its own rollout risk and does not belong bundled with a
    # feature.
    { name = "API_BASE_URL", value = "https://api.${var.domain_name}" },
```

- [ ] **Step 2: Validate**

Run:
```bash
cd terraform/prod && terraform validate
```
Expected: `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
git add terraform/prod/ecs_api.tf
git commit -m "docs(terraform): declare email-confirmation env vars for phases 3 and 4"
```

---

## Task 13: Frontend API layer — `confirmed`, resend, and 403 refresh-and-retry

**Files:**
- Modify: `web/src/api/client.ts`, `web/src/api/client.test.ts`
- Modify: `web/src/api/auth.ts`
- Modify: `web/src/auth/context.ts`, `web/src/auth/AuthContext.tsx`

**Interfaces:**
- Consumes: the API from Tasks 10–11.
- Produces (used by Tasks 14–16):
  - `User` gains `confirmed: boolean`
  - `resendConfirmation(): Promise<void>` in `web/src/api/auth.ts`
  - `refreshSession(): Promise<boolean>` exported from `web/src/api/client.ts`
  - `AuthState` gains `refreshUser: () => Promise<User>`

- [ ] **Step 1: Write the failing test**

Append to `web/src/api/client.test.ts`. This uses the file's existing conventions — `global.fetch = vi.fn()` (not `vi.stubGlobal`) and the `mockJsonResponse` helper already defined at the top of the file:

```ts
describe('apiFetch confirmation_required handling', () => {
  const forbidden = () =>
    mockJsonResponse(403, { error: { code: 'confirmation_required', message: 'nope' } });

  it('refreshes once and retries on a 403 confirmation_required', async () => {
    setAccessToken('stale');
    let call = 0;
    const spy = vi.fn().mockImplementation((url: string) => {
      call += 1;
      if (call === 1) return Promise.resolve(forbidden());
      if (call === 2 && url.endsWith('/auth/refresh')) {
        return Promise.resolve(mockJsonResponse(200, { access_token: 'fresh' }));
      }
      return Promise.resolve(mockJsonResponse(200, { ok: true }));
    });
    global.fetch = spy;

    const out = await apiFetch<{ ok: boolean }>('/me/calendar');
    expect(out).toEqual({ ok: true });
    expect(call).toBe(3);
    expect(String(spy.mock.calls[1][0])).toContain('/auth/refresh');
  });

  it('does not retry a 403 that is not confirmation_required', async () => {
    let call = 0;
    global.fetch = vi.fn().mockImplementation(() => {
      call += 1;
      return Promise.resolve(mockJsonResponse(403, { error: { code: 'forbidden', message: 'no' } }));
    });

    await expect(apiFetch('/me/calendar')).rejects.toMatchObject({
      status: 403,
      code: 'forbidden',
    });
    expect(call).toBe(1);
  });

  it('retries a confirmation_required 403 only once', async () => {
    setAccessToken('stale');
    let call = 0;
    global.fetch = vi.fn().mockImplementation((url: string) => {
      call += 1;
      if (call === 2 && url.endsWith('/auth/refresh')) {
        return Promise.resolve(mockJsonResponse(200, { access_token: 'fresh' }));
      }
      return Promise.resolve(forbidden());
    });

    await expect(apiFetch('/me/calendar')).rejects.toMatchObject({
      status: 403,
      code: 'confirmation_required',
    });
    expect(call).toBe(3);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- client.test.ts`
Expected: FAIL — the first test gets 1 fetch call, not 3.

- [ ] **Step 3: Write the implementation**

In `web/src/api/client.ts`, export the refresh and add the 403 branch:

```ts
async function refresh(): Promise<boolean> {
  const resp = await rawFetch('/auth/refresh', { method: 'POST' });
  if (!resp.ok) return false;
  const body = (await resp.json()) as { access_token?: string };
  if (!body.access_token) return false;
  accessToken = body.access_token;
  return true;
}

/**
 * refreshSession mints a new access token from the refresh cookie. Exposed so
 * ConfirmEmailPage can pick up a confirmation that happened on another device
 * without waiting for the 15m access-token TTL to lapse.
 */
export async function refreshSession(): Promise<boolean> {
  return refresh();
}

/** Reads the error code from a response without consuming the caller's copy. */
async function errorCode(resp: Response): Promise<string> {
  try {
    const body = (await resp.clone().json()) as ApiErrorBody;
    return body.error?.code ?? '';
  } catch {
    return '';
  }
}
```

In `apiFetch`, after the existing 401 block:

```ts
  // A 403 confirmation_required means this token was minted before the account
  // was confirmed. Confirmation is monotonic, so a refresh always resolves it —
  // this is the self-heal for the second-tab case, bounded by the access TTL.
  // Retried once: if the retry still 403s, the user genuinely is unconfirmed.
  if (resp.status === 403 && (await errorCode(resp)) === 'confirmation_required') {
    const refreshed = await refresh();
    if (refreshed) {
      resp = await rawFetch(path, init);
    }
  }
```

In `web/src/api/auth.ts`:

```ts
export interface User {
  id: string;
  email: string;
  confirmed: boolean;
  score_threshold?: number;
}

export async function resendConfirmation(): Promise<void> {
  await apiFetch<void>('/auth/confirm/resend', { method: 'POST' });
}
```

In `web/src/auth/context.ts`, add to `AuthState`:

```ts
  refreshUser: () => Promise<User>;
```

In `web/src/auth/AuthContext.tsx`, add the implementation and pass it through the provider value:

```tsx
  // Mints a fresh access token before re-reading /me, so a confirmation that
  // happened on another device is visible without waiting out the access TTL.
  const refreshUser = async () => {
    await refreshSession();
    const u = await authApi.getMe();
    setUser(u);
    setStatus('authenticated');
    return u;
  };
```

with `import { refreshSession } from '../api/client';`, and:

```tsx
    <AuthContext.Provider value={{ user, status, login, signup, logout, refreshUser }}>
```

- [ ] **Step 4: Fix existing `useAuth` mocks**

Adding `refreshUser` to `AuthState` and `confirmed` to `User` makes every existing mocked return value structurally incomplete, which `tsc -b` will reject. Eight files mock `useAuth`:

```
src/components/LoginDialog.test.tsx
src/components/LoginForm.test.tsx
src/components/SignupForm.test.tsx
src/components/Layout.test.tsx
src/pages/CalendarPage.test.tsx
src/pages/EventDetailPage.test.tsx
src/pages/InterestsPage.test.tsx
src/pages/SettingsPage.test.tsx
```

Add `refreshUser: vi.fn(),` to every `mockReturnValue({ ... })` in those files. Four of them also build a `user` object that needs `confirmed: true` — these are existing signed-in users, so `true` is the right value and preserves what each test asserts today:

```
src/components/Layout.test.tsx:30       user: { id: 'u', email: 'a@x' },
src/pages/EventDetailPage.test.tsx:34   user: { id: 'u1', email: 'a@x' },
src/pages/SettingsPage.test.tsx:55      user: { id: 'u1', email: 'a@x' },
src/pages/InterestsPage.test.tsx:50     user: { id: 'u1', email: 'a@x' },
```

`LandingPage.test.tsx` mocks the `../api/auth` module rather than `useAuth`, so it needs no `refreshUser` — only the `confirmed: false` added to its signup mock in Task 15.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web && pnpm test 2>&1 | tail -15`
Expected: all test files pass; total count is 62 + 3 new.

Run: `cd web && pnpm exec tsc -b`
Expected: no output (clean typecheck).

- [ ] **Step 6: Commit**

```bash
git add web/src/api/ web/src/auth/
git commit -m "feat(web): carry confirmed through the API layer, retry stale-confirmation 403s"
```

---

## Task 14: `ConfirmEmailPage`

**Files:**
- Create: `web/src/pages/ConfirmEmailPage.tsx`, `web/src/pages/ConfirmEmailPage.css.ts`, `web/src/pages/ConfirmEmailPage.test.tsx`

**Interfaces:**
- Consumes: `useAuth` (`user`, `logout`, `refreshUser`), `resendConfirmation` (Task 13).
- Produces (used by Task 16): default-exported `ConfirmEmailPage`.

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/ConfirmEmailPage.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../api/auth', () => ({ resendConfirmation: vi.fn() }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import ConfirmEmailPage from './ConfirmEmailPage';

function mockAuth(overrides: Partial<ReturnType<typeof useAuth>> = {}) {
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'someone@example.com', confirmed: false },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    ...overrides,
  });
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfirmEmailPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  mockAuth();
});

describe('ConfirmEmailPage', () => {
  it('names the address the mail went to', () => {
    renderPage();
    expect(screen.getByText(/someone@example.com/)).toBeInTheDocument();
  });

  it('resends on click and reports that it sent', async () => {
    vi.mocked(resendConfirmation).mockResolvedValue(undefined);
    renderPage();

    await userEvent.click(screen.getByRole('button', { name: /resend/i }));

    expect(resendConfirmation).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/sent/i)).toBeInTheDocument();
  });

  it('reports the rate limit instead of a generic failure', async () => {
    vi.mocked(resendConfirmation).mockRejectedValue({ status: 429, code: 'rate_limited' });
    renderPage();

    await userEvent.click(screen.getByRole('button', { name: /resend/i }));

    expect(await screen.findByText(/too many/i)).toBeInTheDocument();
  });

  it('moves a stale tab along when the window regains focus after confirming elsewhere', async () => {
    const refreshUser = vi
      .fn()
      .mockResolvedValue({ id: 'u1', email: 'someone@example.com', confirmed: true });
    mockAuth({ refreshUser });
    renderPage();

    window.dispatchEvent(new Event('focus'));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/calendar', { replace: true }));
  });

  it('stays put when the recheck finds the account still unconfirmed', async () => {
    const refreshUser = vi
      .fn()
      .mockResolvedValue({ id: 'u1', email: 'someone@example.com', confirmed: false });
    mockAuth({ refreshUser });
    renderPage();

    window.dispatchEvent(new Event('focus'));

    await waitFor(() => expect(refreshUser).toHaveBeenCalled());
    expect(navigate).not.toHaveBeenCalled();
  });

  it('offers a way out', () => {
    renderPage();
    expect(screen.getByRole('button', { name: /sign out/i })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- ConfirmEmailPage`
Expected: FAIL — cannot resolve `./ConfirmEmailPage`.

- [ ] **Step 3: Write the implementation**

Create `web/src/pages/ConfirmEmailPage.css.ts`:

```ts
import { style } from '@vanilla-extract/css';

export const card = style({
  maxWidth: '28rem',
  margin: '4rem auto',
  padding: '2rem',
  textAlign: 'center',
});

export const title = style({
  fontSize: '1.5rem',
  fontWeight: 600,
  marginBottom: '0.75rem',
});

export const body = style({
  color: '#555',
  lineHeight: 1.6,
  marginBottom: '1.5rem',
});

export const address = style({
  fontWeight: 600,
  wordBreak: 'break-all',
});

export const actions = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.75rem',
  alignItems: 'center',
});

export const status = style({
  minHeight: '1.25rem',
  fontSize: '0.875rem',
  color: '#555',
});

export const linkButton = style({
  background: 'none',
  border: 'none',
  padding: 0,
  color: '#555',
  textDecoration: 'underline',
  cursor: 'pointer',
  fontSize: '0.875rem',
});
```

Create `web/src/pages/ConfirmEmailPage.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import * as s from './ConfirmEmailPage.css';
import * as c from '../styles/common.css';

type ResendState = 'idle' | 'sending' | 'sent' | 'rate-limited' | 'failed';

export default function ConfirmEmailPage() {
  const { user, logout, refreshUser } = useAuth();
  const navigate = useNavigate();
  const [resend, setResend] = useState<ResendState>('idle');

  // "Signed up on the laptop, clicked the link on my phone" — on refocus,
  // re-mint the token and re-read /me so the stale tab moves along instead of
  // sitting on this page forever.
  const recheck = useCallback(async () => {
    try {
      const u = await refreshUser();
      if (u.confirmed) navigate('/calendar', { replace: true });
    } catch {
      // Offline or the session lapsed; RequireAuth handles the latter.
    }
  }, [refreshUser, navigate]);

  useEffect(() => {
    window.addEventListener('focus', recheck);
    return () => window.removeEventListener('focus', recheck);
  }, [recheck]);

  async function onResend() {
    setResend('sending');
    try {
      await resendConfirmation();
      setResend('sent');
    } catch (err) {
      setResend((err as { code?: string }).code === 'rate_limited' ? 'rate-limited' : 'failed');
    }
  }

  return (
    <div className={s.card}>
      <h1 className={s.title}>Check your inbox</h1>
      <p className={s.body}>
        We sent a confirmation link to <span className={s.address}>{user?.email}</span>. Open it to
        finish setting up your account. The link expires in 24 hours.
      </p>

      <div className={s.actions}>
        <button
          type="button"
          onClick={onResend}
          disabled={resend === 'sending'}
          className={c.buttonPrimary}
        >
          {resend === 'sending' ? 'Sending…' : 'Resend the link'}
        </button>

        <div className={s.status} role="status">
          {resend === 'sent' && 'Sent — check your inbox again.'}
          {resend === 'rate-limited' && 'Too many requests. Try again in an hour.'}
          {resend === 'failed' && "We couldn't send that. Please try again."}
        </div>

        <button type="button" onClick={() => void logout()} className={s.linkButton}>
          Sign out
        </button>
      </div>
    </div>
  );
}
```

`common.css.ts` exports `buttonPrimary`, `buttonSecondary`, `buttonSubmit`, `textInput`, `card`, `pageTitle`, `pageHeader`, `sectionTitle`, `section`, `errorText`, `screen`, `bodySection` — there is no plain `button`, so `buttonPrimary` is the one to use.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && pnpm test -- ConfirmEmailPage 2>&1 | tail -15`
Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/ConfirmEmailPage.tsx web/src/pages/ConfirmEmailPage.css.ts web/src/pages/ConfirmEmailPage.test.tsx
git commit -m "feat(web): add ConfirmEmailPage with resend and focus recheck"
```

---

## Task 15: Route the unconfirmed user — `RequireAuth`, `SignupDialog`, search preservation

**Files:**
- Modify: `web/src/auth/RequireAuth.tsx`
- Modify: `web/src/components/SignupDialog.tsx`
- Modify: `web/src/pages/LandingPage.tsx`, `web/src/pages/LandingPage.test.tsx`
- Modify: `web/src/App.tsx`
- Create: `web/src/auth/RequireAuth.test.tsx`

**Interfaces:**
- Consumes: `User.confirmed` (Task 13), `ConfirmEmailPage` (Task 14).
- Produces: `RequireAuth` accepts `allowUnconfirmed?: boolean`; `/confirm-email` route exists.

- [ ] **Step 1: Write the failing test**

Create `web/src/auth/RequireAuth.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from './useAuth';
import RequireAuth from './RequireAuth';

function mockAuth(status: 'loading' | 'authenticated' | 'anonymous', confirmed = true) {
  vi.mocked(useAuth).mockReturnValue({
    status,
    user: status === 'authenticated' ? { id: 'u1', email: 'a@x.com', confirmed } : null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
}

function renderAt(element: React.ReactElement) {
  return render(
    <MemoryRouter initialEntries={['/protected']}>
      <Routes>
        <Route path="/protected" element={element} />
        <Route path="/login" element={<div>login page</div>} />
        <Route path="/confirm-email" element={<div>confirm page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => vi.resetAllMocks());

describe('RequireAuth', () => {
  it('renders children for a confirmed user', () => {
    mockAuth('authenticated', true);
    renderAt(
      <RequireAuth>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('secret')).toBeInTheDocument();
  });

  it('sends an anonymous user to login', () => {
    mockAuth('anonymous');
    renderAt(
      <RequireAuth>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('login page')).toBeInTheDocument();
  });

  it('sends an unconfirmed user to /confirm-email', () => {
    mockAuth('authenticated', false);
    renderAt(
      <RequireAuth>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('confirm page')).toBeInTheDocument();
    expect(screen.queryByText('secret')).not.toBeInTheDocument();
  });

  it('lets an unconfirmed user through when allowUnconfirmed is set', () => {
    mockAuth('authenticated', false);
    renderAt(
      <RequireAuth allowUnconfirmed>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('secret')).toBeInTheDocument();
  });
});
```

**`web/src/pages/LandingPage.test.tsx` already covers signup navigation and this change breaks it.** Its `LandingPage - Signup` block has a test named `'signs up and redirects to interests'` that asserts `interests-route`. That assertion becomes wrong the moment `SignupDialog` targets `/confirm-email`, so update it in place rather than writing a parallel `SignupDialog.test.tsx` — this file drives the dialog through the real `AuthProvider` with `../api/auth` mocked, which is the convention here (no `useAuth` mock).

Rename the test and repoint the assertion:

```tsx
  it('signs up and redirects to confirm-email', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    (authApi.signup as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'u',
      email: 'new@x',
      confirmed: false,
    });

    renderPage(<SignupDialog />);
    await userEvent.type(screen.getByLabelText(/email/i), 'new@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() => expect(screen.getByText(/confirm-email-route/)).toBeInTheDocument());
    expect(authApi.signup).toHaveBeenCalledWith('new@x', 'hunter22');
  });
```

and add the destination to `renderPage`'s route table (it currently declares only `/calendar/seattle` and `/interests`):

```tsx
            <Route path="/confirm-email" element={<div>confirm-email-route</div>} />
```

Then append a search-preservation test to the same file. Note that `renderPage` always passes children, which is exactly the branch that does *not* redirect — this test renders `LandingPage` with no children on purpose:

```tsx
describe('LandingPage - redirect', () => {
  it('preserves query params when redirecting an anonymous visitor to login', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/?welcome=true']}>
          <AuthProvider>
            <Routes>
              <Route path="/" element={<LandingPage />} />
              <Route
                path="/login"
                element={<LocationProbe />}
              />
            </Routes>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    // Without the fix the redirect lands on /login with an empty search, and
    // Layout's welcome modal never gets the param.
    await waitFor(() => expect(screen.getByTestId('search')).toHaveTextContent('?welcome=true'));
  });
});
```

with this helper defined at the bottom of the file (`useLocation` added to the `react-router-dom` import):

```tsx
function LocationProbe() {
  const { search } = useLocation();
  return <div data-testid="search">{search}</div>;
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && pnpm test -- RequireAuth LandingPage`
Expected: FAIL on three counts — `RequireAuth` renders `secret` for an unconfirmed user instead of redirecting; the signup test cannot find `confirm-email-route` (the dialog still navigates to `/interests`); the redirect test finds an empty search string.

- [ ] **Step 3: Write the implementation**

`web/src/auth/RequireAuth.tsx`:

```tsx
import { Navigate, useLocation } from 'react-router-dom';
import type { ReactElement } from 'react';
import { useAuth } from './useAuth';
import Spinner from '../components/Spinner';

/**
 * RequireAuth gates a route on being signed in and — unless allowUnconfirmed is
 * set — on having confirmed the account's email address. One place covers both
 * the post-login and the page-reload cases.
 *
 * This is a convenience redirect, not the security boundary: the API rejects
 * unconfirmed users itself (middleware.RequireConfirmed), so a hand-edited
 * client cannot use it to get past the gate.
 */
export default function RequireAuth({
  children,
  allowUnconfirmed = false,
}: {
  children: ReactElement;
  allowUnconfirmed?: boolean;
}) {
  const { status, user } = useAuth();
  const location = useLocation();
  if (status === 'loading') return <Spinner />;
  if (status === 'anonymous') {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }
  if (!allowUnconfirmed && user && !user.confirmed) {
    return <Navigate to="/confirm-email" replace />;
  }
  return children;
}
```

`web/src/components/SignupDialog.tsx` — change the success target:

```tsx
      <SignupForm onSuccess={() => navigate('/confirm-email')} />
```

`web/src/pages/LandingPage.tsx` — preserve the search string on both redirects. Add `useLocation` to the existing `react-router-dom` import, call it alongside `useAuth`, and change the redirect block:

```tsx
  // location.search must survive: ?welcome=true / ?confirmerror=true arrive on
  // the index route, and Layout's modals read them. Dropping the params here
  // loses the modal before it can ever render.
  if (!children) {
    if (status === 'authenticated') {
      return <Navigate to={`/calendar${location.search}`} replace />;
    } else if (status === 'anonymous') {
      return <Navigate to={`/login${location.search}`} replace />;
    }
  }
```

`web/src/App.tsx` — the `/calendar` hop must preserve search too (it sits directly on the `?welcome=true` path). Add above `export default function App()`:

```tsx
// /calendar is a redirect to the only city we ship. It preserves the query
// string because ?welcome=true reaches it via LandingPage, and Layout's modal
// reads that param after the hop.
function CalendarRedirect() {
  const { search } = useLocation();
  return <Navigate to={`/calendar/seattle${search}`} replace />;
}
```

and change the route plus add the confirm-email route:

```tsx
        <Route path="calendar" element={<CalendarRedirect />} />
        <Route
          path="confirm-email"
          element={
            <RequireAuth allowUnconfirmed>
              <ConfirmEmailPage />
            </RequireAuth>
          }
        />
```

Add `useLocation` to the `react-router-dom` import and `import ConfirmEmailPage from './pages/ConfirmEmailPage';`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && pnpm test 2>&1 | tail -15 && pnpm exec tsc -b`
Expected: all files pass; clean typecheck.

- [ ] **Step 5: Commit**

```bash
git add web/src/auth/RequireAuth.tsx web/src/auth/RequireAuth.test.tsx \
        web/src/components/SignupDialog.tsx \
        web/src/pages/LandingPage.tsx web/src/pages/LandingPage.test.tsx web/src/App.tsx
git commit -m "feat(web): route unconfirmed users to /confirm-email and preserve query params"
```

---

## Task 16: The welcome and confirm-error modals

They live in `Layout`, not on a page, so they survive the index route's redirect **and** render for an anonymous visitor — confirming on a phone with no session is a real case, where the SPA is anonymous, the index route redirects to `/login`, and the welcome modal renders over the login screen.

**Files:**
- Create: `web/src/components/WelcomeModal.tsx`, `web/src/components/ConfirmErrorModal.tsx`, `web/src/components/ConfirmModals.css.ts`, `web/src/components/ConfirmModals.test.tsx`
- Modify: `web/src/components/Layout.tsx`

**Interfaces:**
- Consumes: `useSearchParams`, `useAuth`, `resendConfirmation` (Task 13).
- Produces: nothing downstream — this is the last task.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/ConfirmModals.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../api/auth', () => ({ resendConfirmation: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import Layout from './Layout';

function mockAuth(status: 'authenticated' | 'anonymous') {
  vi.mocked(useAuth).mockReturnValue({
    status,
    user: status === 'authenticated' ? { id: 'u1', email: 'a@x.com', confirmed: true } : null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
}

function renderLayoutAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<div>home</div>} />
          <Route path="login" element={<div>login</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  mockAuth('authenticated');
});

describe('confirmation modals', () => {
  it('shows the welcome modal on ?welcome=true', () => {
    renderLayoutAt('/?welcome=true');
    expect(screen.getByRole('dialog', { name: /welcome/i })).toBeInTheDocument();
  });

  it('shows no modal without the params', () => {
    renderLayoutAt('/');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows the welcome modal to an anonymous visitor confirming on another device', () => {
    mockAuth('anonymous');
    renderLayoutAt('/login?welcome=true');
    expect(screen.getByRole('dialog', { name: /welcome/i })).toBeInTheDocument();
    expect(screen.getByText(/sign in/i)).toBeInTheDocument();
  });

  it('dismisses the welcome modal and clears the param', async () => {
    renderLayoutAt('/?welcome=true');
    await userEvent.click(screen.getByRole('button', { name: /close|got it/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows the error modal on ?confirmerror=true and offers a fresh link', async () => {
    vi.mocked(resendConfirmation).mockResolvedValue(undefined);
    renderLayoutAt('/?confirmerror=true');

    expect(screen.getByRole('dialog', { name: /link/i })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /send.*new link/i }));
    expect(resendConfirmation).toHaveBeenCalledTimes(1);
  });

  it('asks an anonymous visitor to sign in rather than offering resend', () => {
    mockAuth('anonymous');
    renderLayoutAt('/login?confirmerror=true');

    expect(screen.getByRole('dialog', { name: /link/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /send.*new link/i })).not.toBeInTheDocument();
    expect(screen.getByText(/sign in/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm test -- ConfirmModals`
Expected: FAIL — no dialog found on `?welcome=true`.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/ConfirmModals.css.ts`:

```ts
import { style } from '@vanilla-extract/css';

export const backdrop = style({
  position: 'fixed',
  inset: 0,
  background: 'rgba(0,0,0,0.45)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 100,
  padding: '1rem',
});

export const card = style({
  background: '#fff',
  borderRadius: '0.75rem',
  padding: '2rem',
  maxWidth: '26rem',
  width: '100%',
  textAlign: 'center',
});

export const title = style({
  fontSize: '1.25rem',
  fontWeight: 600,
  marginBottom: '0.75rem',
});

export const body = style({
  color: '#555',
  lineHeight: 1.6,
  marginBottom: '1.5rem',
});

export const status = style({
  minHeight: '1.25rem',
  fontSize: '0.875rem',
  color: '#555',
  marginTop: '0.75rem',
});
```

Create `web/src/components/WelcomeModal.tsx`:

```tsx
import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './ConfirmModals.css';

/**
 * Shown after a successful confirmation. Renders for anonymous visitors too:
 * the link is often opened on a phone that has no session, where the SPA
 * redirects to /login and this modal sits over the login screen.
 */
export default function WelcomeModal({ onDismiss }: { onDismiss: () => void }) {
  const { status } = useAuth();
  const authed = status === 'authenticated';

  return (
    <div className={s.backdrop}>
      <div role="dialog" aria-label="Welcome" aria-modal="true" className={s.card}>
        <h2 className={s.title}>You&apos;re all set</h2>
        <p className={s.body}>
          {authed
            ? 'Your email is confirmed. Welcome to Here’s What’s Happening.'
            : 'Your email is confirmed — now sign in to pick up where you left off.'}
        </p>
        {authed ? (
          <button type="button" onClick={onDismiss}>
            Got it
          </button>
        ) : (
          <Link to="/login" onClick={onDismiss}>
            Sign in
          </Link>
        )}
      </div>
    </div>
  );
}
```

Create `web/src/components/ConfirmErrorModal.tsx`:

```tsx
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import * as s from './ConfirmModals.css';

type SendState = 'idle' | 'sending' | 'sent' | 'rate-limited' | 'failed';

/**
 * Shown when a confirmation link was unknown or expired. An authenticated
 * visitor can mint a fresh one in place; an anonymous one has to sign in first,
 * because resend is an authenticated route.
 */
export default function ConfirmErrorModal({ onDismiss }: { onDismiss: () => void }) {
  const { status } = useAuth();
  const [send, setSend] = useState<SendState>('idle');

  async function onResend() {
    setSend('sending');
    try {
      await resendConfirmation();
      setSend('sent');
    } catch (err) {
      setSend((err as { code?: string }).code === 'rate_limited' ? 'rate-limited' : 'failed');
    }
  }

  return (
    <div className={s.backdrop}>
      <div role="dialog" aria-label="Confirmation link problem" aria-modal="true" className={s.card}>
        <h2 className={s.title}>That link didn&apos;t work</h2>
        <p className={s.body}>
          Confirmation links expire after 24 hours. We can send you a fresh one.
        </p>

        {status === 'authenticated' ? (
          <>
            <button type="button" onClick={onResend} disabled={send === 'sending'}>
              {send === 'sending' ? 'Sending…' : 'Send a new link'}
            </button>
            <div className={s.status} role="status">
              {send === 'sent' && 'Sent — check your inbox.'}
              {send === 'rate-limited' && 'Too many requests. Try again in an hour.'}
              {send === 'failed' && "We couldn't send that. Please try again."}
            </div>
          </>
        ) : (
          <p className={s.body}>
            <Link to="/login" onClick={onDismiss}>
              Sign in
            </Link>{' '}
            and we&apos;ll send a new link.
          </p>
        )}

        <button type="button" onClick={onDismiss}>
          Close
        </button>
      </div>
    </div>
  );
}
```

Modify `web/src/components/Layout.tsx`:

```tsx
import { NavLink, Outlet, useSearchParams } from 'react-router-dom';
import clsx from 'clsx';
import { useAuth } from '../auth/useAuth';
import UserMenu from './UserMenu';
import WelcomeModal from './WelcomeModal';
import ConfirmErrorModal from './ConfirmErrorModal';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

export default function Layout() {
  const { status } = useAuth();
  const authed = status === 'authenticated';

  // The modals live here rather than on a page so they survive the index
  // route's redirect and still render when the SPA is anonymous.
  const [params, setParams] = useSearchParams();
  const showWelcome = params.get('welcome') === 'true';
  const showConfirmError = params.get('confirmerror') === 'true';

  const dismiss = (key: string) => {
    const next = new URLSearchParams(params);
    next.delete(key);
    setParams(next, { replace: true });
  };

  return (
    <div className={s.page}>
      {/* ...header and main unchanged... */}
      {showWelcome && <WelcomeModal onDismiss={() => dismiss('welcome')} />}
      {showConfirmError && <ConfirmErrorModal onDismiss={() => dismiss('confirmerror')} />}
    </div>
  );
}
```

Keep the existing `<header>` and `<main>` exactly as they are; add only the two modal lines before the closing `</div>`.

- [ ] **Step 4: Run the full suite**

Run:
```bash
cd web && pnpm test 2>&1 | tail -15 && pnpm exec tsc -b && pnpm lint
```
Expected: all test files pass; clean typecheck; no lint errors.

Run (from repo root):
```bash
go build ./... && go test -p 1 ./... -count=1 2>&1 | tail -35
```
Expected: `ok` for every package.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/WelcomeModal.tsx web/src/components/ConfirmErrorModal.tsx \
        web/src/components/ConfirmModals.css.ts web/src/components/ConfirmModals.test.tsx \
        web/src/components/Layout.tsx
git commit -m "feat(web): add welcome and confirm-error modals in Layout"
```

---

## Rollout after merge

The code is done at Task 16; the feature is still dark. Phases in order:

| Phase | Action | Gate |
|---|---|---|
| 0 | Task 1 merges → auto-applies | — |
| 1 | **Manual**: request SES production access in the console (~24h). File it as soon as phase 0's DKIM verifies. While sandboxed, verify your own address as an identity and run signup → mail → confirm → welcome end to end. | DKIM verified |
| 2 | Tasks 2–16 merge with `EMAIL_CONFIRMATION_MODE` unset → `off`. Migration runs automatically via `ci/buildspec-app.yml:97-116` and backfills every existing user as confirmed. No env vars needed, which sidesteps the `TRUST_PROXY` trap entirely. | — |
| 3 | `taskdef-edit.sh --set-env EMAIL_CONFIRMATION_MODE=send ... --deploy` (plus the other four vars). Soak: watch SES bounce/complaint rates and confirm delivery to Gmail and Outlook. | SES production access granted |
| 4 | `taskdef-edit.sh --set-env EMAIL_CONFIRMATION_MODE=enforce --deploy` | Deliverability soaked |

Verify phase 0:
```bash
AWS_PROFILE=servant aws ses get-identity-verification-attributes \
  --identities hereswhatshappening.app
```

Phase 3 command:
```bash
AWS_PROFILE=servant scripts/taskdef-edit.sh \
  --set-env EMAIL_CONFIRMATION_MODE=send \
  --set-env EMAIL_SENDER=ses \
  --set-env EMAIL_FROM_ADDRESS=noreply@hereswhatshappening.app \
  --set-env APP_BASE_URL=https://hereswhatshappening.app \
  --set-env API_BASE_URL=https://api.hereswhatshappening.app \
  --deploy
```

**Rollback at every phase after 2 is a single flip back** — no code revert, no migration rollback. Users created unconfirmed during an enforce window stay unconfirmed after a rollback to `send`, but the gate is gone, so they are not stuck, and their confirmation links keep working.

## Follow-ups (explicitly out of scope here)

- **Delete `EMAIL_CONFIRMATION_MODE`** once `enforce` has been live and stable, and hardcode enforcement. It is rollout scaffolding, not a permanent feature flag — leaving it means the auth path keeps a branch nothing exercises and every future change has to reason about three modes. **This is a ticket, not a maybe.**
- Consolidate `API_BASE_URL` and `ICAL_BASE_URL`.
- Re-confirmation on email change. This would break monotonicity and let a stale token grant up to 15m of access it should not have; the fix at that point is to revoke refresh tokens on email change.
- A cleanup job for expired `email_confirmations` rows — not needed: at most one row per user, overwritten on resend.
- No change to login: unconfirmed users must be able to log in, since that is how they reach the page that gets them confirmed.
