# Build the Postgres DSN from components — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Assemble the Postgres DSN in Go from discrete components (user/password/host/port/name/sslmode) via `url.UserPassword`, so passwords containing URL-reserved characters never break `url.Parse`; inject those components into prod from the RDS-managed master secret instead of a Terraform-built combined `DATABASE_URL` secret.

**Architecture:** A new `internal/dsn` package is the single place a DSN string is produced. `config.Load()` and the `migrate` subcommand read `DB_*` env vars through it; tests and local dev supply the same component vars; prod injects `DB_USER`/`DB_PASSWORD` via ECS JSON-key secret references off the RDS master secret, plus plain `DB_HOST`/`DB_PORT`/`DB_NAME`/`DB_SSLMODE` env. The full-URL `DATABASE_URL`/`TEST_DATABASE_URL` contract is removed everywhere.

**Tech Stack:** Go 1.24, `net/url`, pgx v5, golang-migrate v4, testify, Terraform (AWS ECS Fargate, Secrets Manager, RDS), Make.

**Spec:** `docs/superpowers/specs/2026-05-29-dsn-from-components-design.md`

> **Branch note:** Work happens on `dsn-from-components`. The working tree may still carry an uncommitted interim `urlencode()` patch in `terraform/prod/rds.tf` — Task 7 deletes that whole block, so the patch is superseded. Do not commit it separately.

---

## File Structure

| File | Responsibility | Action |
|------|----------------|--------|
| `internal/dsn/dsn.go` | Sole DSN assembler: `Components` struct, `DSN()`, `FromEnv(prefix)` | Create |
| `internal/dsn/dsn_test.go` | Unit tests incl. reserved-char regression | Create |
| `internal/config/config.go` | `Load()` sources DSN via `dsn.FromEnv("DB_")` | Modify |
| `internal/config/config_test.go` | Set `DB_*` instead of `DATABASE_URL` | Modify |
| `cmd/app/main.go` | `runMigrate()` sources DSN via `dsn.FromEnv("DB_")` | Modify |
| `cmd/app/main_test.go` | Test `runMigrate` errors clearly on missing `DB_*` | Create |
| `internal/testdb/testdb.go` | `DSN()` assembles from `TEST_DB_*` + defaults | Modify |
| `internal/testdb/testdb_test.go` | Unit test default + overlay | Create |
| `.env.example`, `.env` | `DB_*` / `TEST_DB_*` component vars | Modify |
| `Makefile` | `migrate` / `migrate-test` run the app binary | Modify |
| `terraform/prod/ecs_api.tf` | `DB_*` env + JSON-key secrets in locals | Modify |
| `terraform/prod/rds.tf` | Delete combined-secret + data-source block | Modify |
| `terraform/prod/iam.tf` | Drop `database_url.arn` from exec-role policy | Modify |
| `terraform/prod/outputs.tf` | Drop `database_url_secret_arn` output | Modify |

`docker-compose.yml` (sets `POSTGRES_*` for the container, not app env) and `ci/buildspec-app.yml` (relies on `testdb.DSN()`'s unchanged default) need **no** changes.

---

### Task 1: Create the `internal/dsn` package

**Files:**
- Create: `internal/dsn/dsn.go`
- Test: `internal/dsn/dsn_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/dsn/dsn_test.go`:

```go
package dsn

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// A password full of URL-reserved characters (the kind RDS auto-generates) must
// survive a DSN round-trip: assembling then re-parsing yields the exact password.
// This is the regression test for the "invalid IP-literal" prod migration bug.
func TestDSN_ReservedCharPasswordRoundTrips(t *testing.T) {
	c := Components{
		User:     "app",
		Password: "[kkH>6KvYXOHla15:FRkin#z?x",
		Host:     "db.example.com",
		Port:     "5432",
		Name:     "appdb",
		SSLMode:  "require",
	}
	u, err := url.Parse(c.DSN())
	require.NoError(t, err)
	pw, ok := u.User.Password()
	require.True(t, ok)
	require.Equal(t, c.Password, pw)
	require.Equal(t, "db.example.com:5432", u.Host)
	require.Equal(t, "/appdb", u.Path)
	require.Equal(t, "require", u.Query().Get("sslmode"))
}

func TestDSN_OmitsSSLModeWhenEmpty(t *testing.T) {
	c := Components{User: "app", Password: "pw", Host: "localhost", Port: "5432", Name: "appdb"}
	require.Equal(t, "postgres://app:pw@localhost:5432/appdb", c.DSN())
}

func TestFromEnv_AssemblesAndDefaultsPort(t *testing.T) {
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "pw")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_NAME", "appdb")
	t.Setenv("DB_PORT", "")    // unset -> defaults to 5432
	t.Setenv("DB_SSLMODE", "") // unset -> omitted
	c, err := FromEnv("DB_")
	require.NoError(t, err)
	require.Equal(t, "5432", c.Port)
	require.Equal(t, "postgres://app:pw@localhost:5432/appdb", c.DSN())
}

func TestFromEnv_MissingRequiredListsThem(t *testing.T) {
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	_, err := FromEnv("DB_")
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB_USER")
	require.Contains(t, err.Error(), "DB_PASSWORD")
	require.Contains(t, err.Error(), "DB_HOST")
	require.Contains(t, err.Error(), "DB_NAME")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dsn/...`
Expected: FAIL — build error, `undefined: Components` / `undefined: FromEnv`.

- [ ] **Step 3: Write the implementation**

Create `internal/dsn/dsn.go`:

```go
// Package dsn assembles Postgres connection strings from individual components.
// Building the DSN in one place via url.UserPassword guarantees the password is
// percent-encoded, so credentials containing URL-reserved characters (which
// RDS-managed passwords routinely include) never break url.Parse in pgx or
// golang-migrate.
package dsn

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Components are the parts of a Postgres connection string.
type Components struct {
	User     string
	Password string
	Host     string
	Port     string
	Name     string
	SSLMode  string
}

// DSN renders the components as a postgres:// URL. url.UserPassword percent-
// encodes the userinfo, so any password parses cleanly when read back.
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

// FromEnv reads components from <prefix>USER, <prefix>PASSWORD, <prefix>HOST,
// <prefix>PORT, <prefix>NAME, <prefix>SSLMODE. USER, PASSWORD, HOST and NAME are
// required; PORT defaults to "5432"; SSLMODE is optional. It returns an error
// naming every missing required variable.
func FromEnv(prefix string) (Components, error) {
	c := Components{
		User:     os.Getenv(prefix + "USER"),
		Password: os.Getenv(prefix + "PASSWORD"),
		Host:     os.Getenv(prefix + "HOST"),
		Port:     os.Getenv(prefix + "PORT"),
		Name:     os.Getenv(prefix + "NAME"),
		SSLMode:  os.Getenv(prefix + "SSLMODE"),
	}
	var missing []string
	if c.User == "" {
		missing = append(missing, prefix+"USER")
	}
	if c.Password == "" {
		missing = append(missing, prefix+"PASSWORD")
	}
	if c.Host == "" {
		missing = append(missing, prefix+"HOST")
	}
	if c.Name == "" {
		missing = append(missing, prefix+"NAME")
	}
	if len(missing) > 0 {
		return Components{}, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	return c, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/dsn/...`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/dsn/dsn.go internal/dsn/dsn_test.go
git commit -m "feat(dsn): assemble Postgres DSN from components with encoded userinfo"
```

---

### Task 2: Source `config.Load()`'s DSN from components

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go:46-50` and imports

- [ ] **Step 1: Update the tests to use `DB_*` (write the new expectations first)**

In `internal/config/config_test.go`, add this helper at the bottom of the file:

```go
// setRequiredDB sets the DB_* component vars every Load() call now needs.
func setRequiredDB(t *testing.T) {
	t.Helper()
	t.Setenv("DB_USER", "app")
	t.Setenv("DB_PASSWORD", "pw")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "appdb")
	t.Setenv("DB_SSLMODE", "disable")
}
```

Replace the body of `TestLoad_AllFieldsParsed` (currently starts with `t.Setenv("DATABASE_URL", "postgres://x")`) so its first line is `setRequiredDB(t)` and its DSN assertion matches the assembled value:

```go
func TestLoad_AllFieldsParsed(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("JWT_ACCESS_TTL", "10m")
	t.Setenv("REFRESH_TTL", "100h")
	t.Setenv("LOG_LEVEL", "info")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://app:pw@localhost:5432/appdb?sslmode=disable", cfg.DatabaseURL)
	require.Equal(t, ":9999", cfg.HTTPAddr)
	require.Equal(t, "k", cfg.JWTSigningKey)
	require.Equal(t, 10*time.Minute, cfg.JWTAccessTTL)
	require.Equal(t, 100*time.Hour, cfg.RefreshTTL)
	require.Equal(t, "info", cfg.LogLevel)
}
```

Replace `TestLoad_MissingRequired` so it exercises a missing DB component:

```go
func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	t.Setenv("JWT_SIGNING_KEY", "k")
	_, err := Load()
	require.Error(t, err)
}
```

In the remaining seven tests — `TestLoad_QueueAndScraperFields`, `TestLoad_IngestWorkersDefault`, `TestLoad_SpotifyAndCryptoFields`, `TestLoad_BadEncKey`, `TestLoad_TEIEndpoint`, `TestLoad_IcalBaseURL`, `TestLoad_CORSAllowedOrigins` — replace their `t.Setenv("DATABASE_URL", "postgres://x")` line with `setRequiredDB(t)`. (Leave every other line in those tests unchanged.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/...`
Expected: FAIL — `Load()` still reads `DATABASE_URL`, so it returns "DATABASE_URL is required" and `cfg.DatabaseURL` is `""` not the assembled DSN.

- [ ] **Step 3: Update `config.go`**

Add the `dsn` import (new internal-import group line, gofmt will order it):

```go
import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/dsn"
)
```

Replace lines 46-50 — the `DATABASE_URL` lookup at the top of `Load()`:

```go
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
```

with:

```go
func Load() (*Config, error) {
	dbComponents, err := dsn.FromEnv("DB_")
	if err != nil {
		return nil, err
	}
	dbURL := dbComponents.DSN()
```

(The `cfg.DatabaseURL: dbURL` assignment further down is unchanged. `errors` is still used by the `JWT_SIGNING_KEY` check, so its import stays. The later `accessTTL, err := parseDuration(...)` reuses `err` via `:=` with a new LHS var — valid Go, no change needed.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/...`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): build DatabaseURL from DB_* components"
```

---

### Task 3: Source the `migrate` subcommand's DSN from components

**Files:**
- Create: `cmd/app/main_test.go`
- Modify: `cmd/app/main.go` imports and `runMigrate()` (lines 248-262)

- [ ] **Step 1: Write the failing test**

Create `cmd/app/main_test.go`:

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// runMigrate must fail fast with a clear, named-variable error when the DB_*
// components are absent — never a confusing url.Parse error or a hang trying to
// dial a half-formed DSN.
func TestRunMigrate_MissingDBEnv(t *testing.T) {
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	err := runMigrate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB_USER")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/app/...`
Expected: FAIL — `runMigrate` still reads `DATABASE_URL`; with it unset it returns "DATABASE_URL is required" (does not contain "DB_USER").

- [ ] **Step 3: Update `main.go`**

Add the `dsn` import to the internal-import group (gofmt orders it between `internal/db` and `internal/http`):

```go
	"github.com/wmyers/heres-whats-happening/internal/db"
	"github.com/wmyers/heres-whats-happening/internal/dsn"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
```

Replace `runMigrate()` (lines 248-262) with:

```go
// runMigrate applies the embedded SQL migrations and exits. It reads only the
// DB_* connection components (not the full config) so a one-off migration task
// needs nothing beyond the database credentials.
func runMigrate() error {
	c, err := dsn.FromEnv("DB_")
	if err != nil {
		return err
	}
	fmt.Println("applying migrations ...")
	if err := migrate.Up(c.DSN()); err != nil {
		return err
	}
	fmt.Println("migrations applied")
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/app/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/app/main.go cmd/app/main_test.go
git commit -m "feat(cmd): migrate subcommand reads DB_* components"
```

---

### Task 4: Assemble the test DSN from components

**Files:**
- Create: `internal/testdb/testdb_test.go`
- Modify: `internal/testdb/testdb.go:21-32` and imports

- [ ] **Step 1: Write the failing test**

Create `internal/testdb/testdb_test.go`:

```go
package testdb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// With no TEST_DB_* set, DSN() must equal the value docker-compose + CI rely on.
func TestDSN_DefaultWhenUnset(t *testing.T) {
	for _, k := range []string{"TEST_DB_USER", "TEST_DB_PASSWORD", "TEST_DB_HOST", "TEST_DB_PORT", "TEST_DB_NAME", "TEST_DB_SSLMODE"} {
		t.Setenv(k, "")
	}
	require.Equal(t, "postgres://app:app@localhost:5432/appdb_test?sslmode=disable", DSN())
}

func TestDSN_OverlaysEnv(t *testing.T) {
	t.Setenv("TEST_DB_HOST", "db.internal")
	t.Setenv("TEST_DB_PASSWORD", "s3cr3t")
	got := DSN()
	require.Contains(t, got, "app:s3cr3t@db.internal:5432/appdb_test")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/testdb/... -run TestDSN`
Expected: FAIL — `DSN()` reads `TEST_DATABASE_URL` and returns the `DefaultTestDSN` constant; `TestDSN_OverlaysEnv` fails because `TEST_DB_*` is ignored.

- [ ] **Step 3: Update `testdb.go`**

Add the `dsn` import (internal group):

```go
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/dsn"
```

Replace lines 21-32 (the `DefaultTestDSN` const and the `DSN()` function):

```go
// DefaultTestDSN matches the test database provisioned by docker-compose.yml
// and scripts/db-init.sh. Used when TEST_DATABASE_URL is unset so that
// `go test ./...` works without sourcing .env.example.
const DefaultTestDSN = "postgres://app:app@localhost:5432/appdb_test?sslmode=disable"

// DSN returns TEST_DATABASE_URL, falling back to DefaultTestDSN.
func DSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return DefaultTestDSN
}
```

with:

```go
// defaultTestComponents match the test database provisioned by docker-compose.yml
// and scripts/db-init.sh. Used for any TEST_DB_* field left unset so that
// `go test ./...` works without sourcing .env.example.
var defaultTestComponents = dsn.Components{
	User: "app", Password: "app", Host: "localhost", Port: "5432",
	Name: "appdb_test", SSLMode: "disable",
}

// DSN assembles the test DSN from TEST_DB_* env vars, falling back to
// defaultTestComponents for any field left unset.
func DSN() string {
	c := defaultTestComponents
	if v := os.Getenv("TEST_DB_USER"); v != "" {
		c.User = v
	}
	if v := os.Getenv("TEST_DB_PASSWORD"); v != "" {
		c.Password = v
	}
	if v := os.Getenv("TEST_DB_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("TEST_DB_PORT"); v != "" {
		c.Port = v
	}
	if v := os.Getenv("TEST_DB_NAME"); v != "" {
		c.Name = v
	}
	if v := os.Getenv("TEST_DB_SSLMODE"); v != "" {
		c.SSLMode = v
	}
	return c.DSN()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/testdb/... -run TestDSN`
Expected: PASS (2 tests). These don't touch a database.

- [ ] **Step 5: Commit**

```bash
git add internal/testdb/testdb.go internal/testdb/testdb_test.go
git commit -m "feat(testdb): assemble test DSN from TEST_DB_* components"
```

---

### Task 5: Whole-module verification gate

**Files:** none (verification only)

- [ ] **Step 1: Build and vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0. Confirms no stray reference to the removed `DATABASE_URL` / `DefaultTestDSN` paths and all imports resolve.

- [ ] **Step 2: Run all DB-free unit tests**

Run: `go test ./internal/dsn/... ./internal/config/... ./internal/testdb/... ./cmd/app/...`
Expected: PASS.

- [ ] **Step 3: Run the full integration suite (needs Docker Postgres)**

Run: `make db-up && make test`
Expected: PASS. (`make test` is `go test -p 1 ./... -count=1`; `internal/testdb` migrates `appdb_test` in-process using the unchanged default DSN, so integration tests connect exactly as before.)

If Docker is unavailable in this environment, note that and defer Step 3 to a machine with Docker; Steps 1-2 must still pass here.

---

### Task 6: Update local-dev wiring (env + Makefile)

**Files:**
- Modify: `.env.example`
- Modify: `.env` (local, git-ignored — edit by hand to match)
- Modify: `Makefile` (`migrate`, `migrate-test` targets)

- [ ] **Step 1: Update `.env.example`**

Replace the first two lines:

```
DATABASE_URL=postgres://app:app@localhost:5432/appdb?sslmode=disable
TEST_DATABASE_URL=postgres://app:app@localhost:5432/appdb_test?sslmode=disable
```

with:

```
# Postgres connection components — assembled into a DSN in Go (internal/dsn).
DB_USER=app
DB_PASSWORD=app
DB_HOST=localhost
DB_PORT=5432
DB_NAME=appdb
DB_SSLMODE=disable

# Test database (same server, separate db). Used by internal/testdb and `make migrate-test`.
TEST_DB_USER=app
TEST_DB_PASSWORD=app
TEST_DB_HOST=localhost
TEST_DB_PORT=5432
TEST_DB_NAME=appdb_test
TEST_DB_SSLMODE=disable
```

- [ ] **Step 2: Apply the same change to your local `.env`**

Edit `.env` (not committed) and replace its `DATABASE_URL=` and `TEST_DATABASE_URL=` lines with the same `DB_*` / `TEST_DB_*` block above, preserving any non-default local values. This is required for `make migrate` / `make run` to work locally.

- [ ] **Step 3: Update the `Makefile` migrate targets**

Replace the `migrate` and `migrate-test` targets (lines 32-40):

```makefile
migrate:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
	  -path sql/migrations \
	  -database "$$DATABASE_URL" up

migrate-test:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
	  -path sql/migrations \
	  -database "$$TEST_DATABASE_URL" up
```

with:

```makefile
# Dev DB: the app binary assembles the DSN from DB_* (internal/dsn) and applies
# the embedded migrations — the same code path as prod.
migrate:
	go run ./cmd/app migrate

# Test DB: same server, separate db. Map TEST_DB_* onto the DB_* the binary reads.
# godotenv.Load() does not override already-set vars, so these win over .env.
migrate-test:
	DB_USER="$$TEST_DB_USER" DB_PASSWORD="$$TEST_DB_PASSWORD" DB_HOST="$$TEST_DB_HOST" \
	DB_PORT="$$TEST_DB_PORT" DB_NAME="$$TEST_DB_NAME" DB_SSLMODE="$$TEST_DB_SSLMODE" \
	go run ./cmd/app migrate
```

- [ ] **Step 4: Verify locally (needs Docker Postgres)**

Run: `make db-up && make migrate && make migrate-test`
Expected: both print `applying migrations ...` then `migrations applied` (or no-op if already current), exit 0.

- [ ] **Step 5: Commit**

```bash
git add .env.example Makefile
git commit -m "chore(dev): switch local env + make targets to DB_* components"
```

(`.env` is git-ignored; it is not part of the commit.)

---

### Task 7: Update Terraform source of truth

> The live ECS task def has `ignore_changes = [container_definitions]` (`ecs_api.tf:62-66`), so these `locals` edits do **not** alter the running task def on `terraform apply`. They keep Terraform's source of truth correct (for any future task-def recreation) and let `terraform apply` safely delete the now-unreferenced `database_url` secret. The *live* env/secret flip is performed in Task 8.

**Files:**
- Modify: `terraform/prod/ecs_api.tf` (locals)
- Modify: `terraform/prod/rds.tf` (delete block)
- Modify: `terraform/prod/iam.tf` (policy resources)
- Modify: `terraform/prod/outputs.tf` (delete output)

- [ ] **Step 1: `ecs_api.tf` — add `DB_*` env vars**

In the `api_env_vars` list, add these four entries (place them right after the `{ name = "AWS_REGION", ... }` line):

```hcl
    { name = "DB_HOST", value = aws_db_instance.main.address },
    { name = "DB_PORT", value = tostring(aws_db_instance.main.port) },
    { name = "DB_NAME", value = aws_db_instance.main.db_name },
    { name = "DB_SSLMODE", value = "require" },
```

- [ ] **Step 2: `ecs_api.tf` — replace the `DATABASE_URL` secret with JSON-key secrets**

In the `api_secrets` list, replace this single line:

```hcl
    { name = "DATABASE_URL", valueFrom = aws_secretsmanager_secret.database_url.arn },
```

with:

```hcl
    { name = "DB_USER", valueFrom = "${aws_db_instance.main.master_user_secret[0].secret_arn}:username::" },
    { name = "DB_PASSWORD", valueFrom = "${aws_db_instance.main.master_user_secret[0].secret_arn}:password::" },
```

- [ ] **Step 3: `rds.tf` — delete the combined-secret block**

Delete everything from the comment beginning `# Construct the full DATABASE_URL secret.` (line 61) through the end of the `resource "aws_secretsmanager_secret_version" "database_url"` block (line 89) — i.e. the `data "aws_secretsmanager_secret_version" "db_master"`, the `locals` block (including the interim `urlencode()` patch), and both `aws_secretsmanager_secret.database_url` resources. After this edit, `rds.tf` ends at the closing `}` of `resource "aws_db_instance" "main"`.

- [ ] **Step 4: `iam.tf` — drop the `database_url` ARN from the exec-role policy**

Replace (lines 30-34):

```hcl
    resources = concat(
      [aws_secretsmanager_secret.database_url.arn],
      [for s in aws_secretsmanager_secret.app : s.arn],
      [aws_db_instance.main.master_user_secret[0].secret_arn],
    )
```

with:

```hcl
    resources = concat(
      [for s in aws_secretsmanager_secret.app : s.arn],
      [aws_db_instance.main.master_user_secret[0].secret_arn],
    )
```

(The master-secret ARN — what the JSON-key secrets read — remains granted.)

- [ ] **Step 5: `outputs.tf` — delete the `database_url_secret_arn` output**

Delete the whole block (lines 36-39):

```hcl
output "database_url_secret_arn" {
  description = "ARN of the secret holding the full DATABASE_URL DSN."
  value       = aws_secretsmanager_secret.database_url.arn
}
```

(Leave the `db_master_user_secret_arn` output above it in place.)

- [ ] **Step 6: Format and verify no dangling references**

Run: `terraform fmt -recursive terraform/prod`
Run: `grep -rn "aws_secretsmanager_secret.database_url\|local.database_url\|local.db_master_password" terraform/prod/`
Expected: `grep` returns nothing. (Do **not** run `terraform plan`/`apply` here — the operator does that in Task 8.)

- [ ] **Step 7: Commit**

```bash
git add terraform/prod/ecs_api.tf terraform/prod/rds.tf terraform/prod/iam.tf terraform/prod/outputs.tf
git commit -m "feat(terraform): inject DB_* components, drop combined database-url secret"
```

---

### Task 8: Production cutover (operator-run)

> RDS is private; the operator runs these from a machine on company AWS. Region `us-east-1`, cluster `hwh-cluster`, family `hwh-api`. Because of `ignore_changes`, the live env/secret flip is a one-time `register-task-definition`; afterward the app-deploy pipeline carries it forward (it inherits env/secrets from the latest revision). This greenfield DB has never served traffic, so the brief window where the old revision is invalid is acceptable.

- [ ] **Step 1: Land the new image**

Merge the branch to `master`. The app pipeline builds/pushes the new image and (deploy phase) registers a `hwh-api` revision with that image. Confirm the latest revision points at the new SHA:

```bash
aws ecs describe-task-definition --task-definition hwh-api --region us-east-1 \
  --query 'taskDefinition.containerDefinitions[0].image' --output text
```

- [ ] **Step 2: Register a revision with the flipped env/secrets**

```bash
cd terraform/prod
MASTER_ARN=$(terraform output -raw db_master_user_secret_arn)
ENDPOINT=$(terraform output -raw db_endpoint)   # host:port
DB_HOST=${ENDPOINT%:*}; DB_PORT=${ENDPOINT##*:}

aws ecs describe-task-definition --task-definition hwh-api --region us-east-1 \
  --query 'taskDefinition' > taskdef.json

jq --arg masterArn "$MASTER_ARN" --arg host "$DB_HOST" --arg port "$DB_PORT" '
  .containerDefinitions[0].environment = (
    (.containerDefinitions[0].environment // [] | map(select(.name | startswith("DB_") | not)))
    + [ {name:"DB_HOST",value:$host}, {name:"DB_PORT",value:$port},
        {name:"DB_NAME",value:"appdb"}, {name:"DB_SSLMODE",value:"require"} ]
  )
  | .containerDefinitions[0].secrets = (
    (.containerDefinitions[0].secrets // [] | map(select(.name != "DATABASE_URL")))
    + [ {name:"DB_USER",valueFrom:($masterArn + ":username::")},
        {name:"DB_PASSWORD",valueFrom:($masterArn + ":password::")} ]
  )
  | del(.taskDefinitionArn,.revision,.status,.requiresAttributes,.compatibilities,.registeredAt,.registeredBy)
' taskdef.json > taskdef.new.json

NEW_ARN=$(aws ecs register-task-definition --cli-input-json file://taskdef.new.json \
  --region us-east-1 --query 'taskDefinition.taskDefinitionArn' --output text)
echo "registered $NEW_ARN"
```

- [ ] **Step 3: Run the migration on the new revision**

`make migrate-prod` targets `--task-definition hwh-api` (the latest active revision = the one just registered, with new image + `DB_*`):

```bash
make migrate-prod
make migrate-prod-status   # expect lastStatus=STOPPED, exitCode=0, log "migrations applied"
```

- [ ] **Step 4: Roll the API service to the new revision**

```bash
aws ecs update-service --cluster hwh-cluster --service hwh-api \
  --task-definition "$NEW_ARN" --region us-east-1
```

Wait for the service to reach a steady state and confirm the api task is RUNNING and healthy (target group healthy).

- [ ] **Step 5: Apply Terraform to remove the dead secret**

Only after Step 4 (no running task references `DATABASE_URL` anymore):

```bash
cd terraform/prod && terraform apply
```

Expected plan: delete `aws_secretsmanager_secret.database_url` (+ its version), update the exec-role policy, remove the `database_url_secret_arn` output. The `hwh-api` task def shows **no change** (container_definitions ignored). Confirm the plan matches before approving.

- [ ] **Step 6: Clean up scratch files**

```bash
rm -f terraform/prod/taskdef.json terraform/prod/taskdef.new.json
```

---

## Self-Review

**Spec coverage:**
- "New `internal/dsn` package / `DSN()` / `FromEnv`" → Task 1. ✓
- "Wire `config.Load()` and `runMigrate()`; keep `NewPool`/`migrate.Up` signatures" → Tasks 2, 3. ✓
- "Components everywhere: config_test, testdb, db_test untouched, docker-compose/.env/Makefile" → Tasks 2, 4, 6; `db_test.go:25` passes a literal DSN to `NewPool` and is intentionally left as-is (noted in File Structure). ✓
- "New `dsn_test.go` reserved-char round-trip + FromEnv validation" → Task 1. ✓
- "Terraform: ecs_api env+JSON-key secrets, rds delete block, iam drop arn, outputs drop" → Task 7. ✓
- "Why safe: exec role already grants master secret; default KMS key" → Task 7 keeps the master ARN grant; KMS CMK check is a cutover prerequisite (Step 5 plan review will surface a decrypt failure if a CMK is set). ✓
- "Cutover order" → Task 8, **refined**: the spec assumed `terraform apply` pushes the env/secrets; the `ignore_changes` lifecycle means the live flip is a `register-task-definition` (Step 2) instead. Documented in Task 7/8 headers.

**Placeholder scan:** No TBD/TODO/"handle errors" — every code and command step is concrete.

**Type consistency:** `Components{User,Password,Host,Port,Name,SSLMode}`, `(Components).DSN()`, and `FromEnv(prefix) (Components, error)` are used identically across Tasks 1-4. `dsn.FromEnv("DB_")` + `.DSN()` in both config.go and main.go. testdb uses the same `dsn.Components` literal.
