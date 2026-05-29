# Build the Postgres DSN from components

**Date:** 2026-05-29
**Status:** Approved, ready for implementation plan

## Problem

The prod `DATABASE_URL` was assembled in Terraform (`terraform/prod/rds.tf`) by
interpolating the RDS-managed master password raw into a `postgres://` URL:

```hcl
database_url = "postgres://${username}:${db_master_password}@${endpoint}/${db_name}?sslmode=require"
```

RDS auto-generated a password containing URL-reserved characters
(`[kkH>6KvYXOHla15:FRkin#…`). Go's `net/url.Parse` — used by both pgx
(`pgxpool.ParseConfig`) and golang-migrate — reads the leading `[` in the
userinfo region as the start of an IPv6 host literal and fails:

```
migrate: init migrate: failed to open database: parse "postgres://app:[kkH>6KvYXOHla15:FRkin": invalid IP-literal
```

This blocked the prod migration task **and** the running API service, since both
read the same `hwh/database-url` secret and parse it as a URL.

Root cause: embedding an un-encoded password into a URL-format DSN. The password
must be percent-encoded, and the cleanest place to guarantee that is in Go via
`url.UserPassword`, which encodes userinfo per RFC 3986. (A one-line `urlencode()`
fix in Terraform was applied as an interim unblock; this design supersedes and
removes it.)

## Decision

- **Components everywhere.** Drop the full-URL `DATABASE_URL` / `TEST_DATABASE_URL`
  contract entirely. Every environment (prod, local dev, tests) supplies discrete
  component env vars; the DSN is assembled in Go in exactly one place.
- **Prod injects components via ECS JSON-key secrets**, reading the RDS-managed
  master secret directly. Remove the Terraform-built combined `database-url`
  secret and the opaque-secret data-source read.

## Design

### 1. New `internal/dsn` package — the single DSN assembler

```go
package dsn

type Components struct { User, Password, Host, Port, Name, SSLMode string }

// DSN percent-encodes userinfo via url.UserPassword so any password parses.
func (c Components) DSN() string {
    u := url.URL{
        Scheme: "postgres",
        User:   url.UserPassword(c.User, c.Password),
        Host:   net.JoinHostPort(c.Host, c.Port),
        Path:   "/" + c.Name,
    }
    if c.SSLMode != "" {
        u.RawQuery = url.Values{"sslmode": {c.SSLMode}}.Encode()
    }
    return u.String()
}

// FromEnv reads <prefix>USER / PASSWORD / HOST / PORT / NAME / SSLMODE.
// Required: USER, PASSWORD, HOST, NAME. PORT defaults to "5432".
// SSLMODE is optional (omitted from the query string when empty).
// Returns an error naming every missing required var.
func FromEnv(prefix string) (Components, error)
```

`DSN()` is pure and trivially unit-testable — it is where the regression test for
this bug lives.

### 2. Wire the two consumers

- `internal/config.Load()` — replace `os.Getenv("DATABASE_URL")` with
  `dsn.FromEnv("DB_")`. Keep the `cfg.DatabaseURL` field holding the assembled
  string so `db.NewPool(cfg.DatabaseURL)` is unchanged.
- `cmd/app/main.go` `runMigrate()` — replace `os.Getenv("DATABASE_URL")` with
  `dsn.FromEnv("DB_")`, pass `c.DSN()` to `migrate.Up`.
- `db.NewPool(ctx, dsn string)` and `migrate.Up(dsn string)` keep their string
  signatures — untouched. They still receive a `postgres://` URL string; only how
  that string is produced changes.

### 3. Tests and local dev (components everywhere)

- **New** `internal/dsn/dsn_test.go`:
  - reserved-char password (`[kkH>6KvYXOHla15:FRkin#z?x`) → `DSN()` output
    `url.Parse`es successfully and the password round-trips exactly.
  - `FromEnv` validation (missing required → error naming them) and PORT default.
- `internal/config/config_test.go`: the ~9 tests that `t.Setenv("DATABASE_URL",
  "postgres://x")` switch to setting `DB_USER` / `DB_PASSWORD` / `DB_HOST` /
  `DB_NAME` (assert `cfg.DatabaseURL` is the assembled DSN).
- `internal/testdb/testdb.go`: replace `TEST_DATABASE_URL` + `DefaultTestDSN`
  with a `dsn.Components` whose defaults (`app` / `app` / `localhost` / `5432` /
  `appdb_test` / `disable`) are overlaid by `TEST_DB_*` env vars when set.
- `internal/db/db_test.go:25` passes a literal bad DSN straight to `NewPool` —
  unaffected, left as is.
- `docker-compose` / `.env` / Makefile `migrate` + `migrate-test` targets: provide
  `DB_*` / `TEST_DB_*` env instead of the URL vars.

### 4. Terraform (prod) — drop the combined-secret hack

- `terraform/prod/ecs_api.tf` (`api_env_vars` / `api_secrets` locals — reused by
  `ecs_schedules.tf` and by the migrate task, which is the api task def with its
  command overridden):
  - add env: `DB_HOST = aws_db_instance.main.address`,
    `DB_PORT = tostring(aws_db_instance.main.port)`,
    `DB_NAME = aws_db_instance.main.db_name`, `DB_SSLMODE = "require"`.
  - replace the `DATABASE_URL` secret with two JSON-key secrets:
    - `DB_USER` → `"${aws_db_instance.main.master_user_secret[0].secret_arn}:username::"`
    - `DB_PASSWORD` → `"${aws_db_instance.main.master_user_secret[0].secret_arn}:password::"`
- `terraform/prod/rds.tf`: delete `data.aws_secretsmanager_secret_version.db_master`,
  the `locals { db_master_password, database_url }` block, and
  `aws_secretsmanager_secret.database_url` + its `_version`. (This removes the
  interim `urlencode()` patch.) Update the now-stale comment.
- `terraform/prod/iam.tf`: drop `aws_secretsmanager_secret.database_url.arn` from
  the execution-role secrets policy. The master-secret ARN it reads is already
  granted on line 33 — no new IAM.
- `terraform/prod/outputs.tf`: remove the `database_url_secret_arn` output. The
  `db_master_user_secret_arn` output stays.

## Why it's safe / correct

- `url.UserPassword` + `url.URL.String()` percent-encode userinfo per RFC 3986;
  pgx and golang-migrate both `url.Parse` and decode it back. Round-trip verified
  against the exact failing password during diagnosis.
- The execution role already reads the RDS master secret, so JSON-key injection
  needs no new IAM permission.
- The master secret uses the default `aws/secretsmanager` KMS key, which needs no
  explicit `kms:Decrypt` grant for ECS injection. **Verify** no customer-managed
  key was set on the master secret before applying.
- JSON-key injection reads the *live* master secret, so an RDS master-password
  rotation is picked up on the next task start — the old combined secret silently
  went stale on rotation.

## Cutover (operator-run; RDS is private, machine is on company AWS)

Order matters — the new task def references `DB_*` that only the new image reads,
and the migrate task uses the api task def:

1. Merge the code change; let the app pipeline build the new image.
2. `terraform apply` in `terraform/prod/` — adds `DB_*` env/secrets, removes the
   `hwh/database-url` secret (enters a 7-day recovery window, not instant-deleted).
3. Run the one-off migrate task from the new api task def revision.
4. Force a new `api` service deployment so running tasks pick up `DB_*`.

## Out of scope

- No change to `db.NewPool` / `migrate.Up` signatures or to the migration SQL.
- No change to non-DB secrets (`app` secrets) or their seeding flow.
- No retention of `hwh/database-url` as a dormant rollback secret (confirmed
  acceptable to let it go to the recovery window).
