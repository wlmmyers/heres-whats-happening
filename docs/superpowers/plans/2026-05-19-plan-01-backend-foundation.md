# Plan 1 — Backend Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the Go backend skeleton, local Postgres (with pgvector), schema migrations, and the authenticated user + manual-interests REST API — runnable end-to-end from a developer laptop with `docker compose up` + `go run`, with integration tests against a real Postgres.

**Architecture:** Single Go binary (`cmd/app`) with subcommand dispatch — this plan implements `app serve` (HTTP API). Postgres 16 with the `vector` extension runs in Docker. Schema is versioned via `golang-migrate`. Queries are type-safe via `sqlc`. Authentication is JWT access (15 min, in-memory on client) + opaque refresh token (30 day, `httpOnly` cookie, `sha256` hash stored in DB). Passwords are argon2id. HTTP routing is `chi`. Tests are integration-style against a real Postgres test database.

**Tech Stack:** Go 1.24 · `github.com/go-chi/chi/v5` · `github.com/jackc/pgx/v5` · `github.com/sqlc-dev/sqlc` · `github.com/golang-migrate/migrate/v4` · `github.com/golang-jwt/jwt/v5` · `golang.org/x/crypto/argon2` · `github.com/stretchr/testify` · Postgres 16 + `pgvector/pgvector:pg16` Docker image.

---

## File Structure (what this plan creates)

```
.
├── cmd/app/main.go                              # subcommand dispatch; only `serve` exists in this plan
├── internal/
│   ├── config/config.go                         # env-var parsing
│   ├── db/db.go                                 # pgx pool factory + ping
│   ├── pwhash/pwhash.go                         # argon2id Hash / Verify
│   ├── auth/
│   │   ├── jwt.go                               # access-token sign / verify
│   │   └── refresh.go                           # refresh-token mint, hash, lookup
│   ├── http/
│   │   ├── server.go                            # chi router + handler wiring
│   │   ├── middleware/auth.go                   # Bearer-token middleware
│   │   ├── httperr/httperr.go                   # uniform JSON error responses
│   │   └── handlers/
│   │       ├── health.go                        # GET /healthz, /readyz
│   │       ├── auth.go                          # POST /auth/{signup,login,refresh,logout}
│   │       ├── user.go                          # GET /me, DELETE /me
│   │       └── interests.go                     # GET/POST /me/interests, DELETE /me/interests/:id
│   ├── store/                                   # sqlc-generated
│   │   ├── db.go
│   │   ├── models.go
│   │   ├── users.sql.go
│   │   ├── refresh_tokens.sql.go
│   │   └── user_interests.sql.go
│   └── testdb/testdb.go                         # test-only helper: opens pool, truncates between tests
├── sql/
│   ├── migrations/
│   │   ├── 0001_vector_and_cities.up.sql
│   │   ├── 0001_vector_and_cities.down.sql
│   │   ├── 0002_users.up.sql
│   │   ├── 0002_users.down.sql
│   │   ├── 0003_refresh_tokens.up.sql
│   │   ├── 0003_refresh_tokens.down.sql
│   │   ├── 0004_user_interests.up.sql
│   │   └── 0004_user_interests.down.sql
│   └── queries/
│       ├── users.sql
│       ├── refresh_tokens.sql
│       └── user_interests.sql
├── docker-compose.yml                           # postgres for dev + tests
├── sqlc.yaml
├── Makefile
├── .env.example
├── .gitignore                                   # extend existing
├── Dockerfile                                   # buildable image (multi-stage); not deployed in this plan
├── go.mod
└── go.sum
```

**Test convention:** all tests are `_test.go` files alongside the code under test. Integration tests open `TEST_DATABASE_URL` via `internal/testdb`; that helper runs migrations once per process and truncates all tables between tests. No mocks for the DB layer — we test against a real Postgres because the queries and constraints *are* the contract.

---

### Task 1: Initialize Go module and dependency manifest

**Files:**
- Create: `go.mod`
- Create: `.gitignore` (append; .superpowers/ is already ignored)

- [ ] **Step 1: Initialize go module**

```bash
cd /Users/wmyers/data/heres-whats-happening
go mod init github.com/wmyers/heres-whats-happening
```

Expected: creates `go.mod` declaring `module github.com/wmyers/heres-whats-happening` and `go 1.24`.

- [ ] **Step 2: Add core dependencies**

```bash
go get github.com/go-chi/chi/v5@v5.1.0
go get github.com/jackc/pgx/v5@v5.7.2
go get github.com/golang-jwt/jwt/v5@v5.2.1
go get github.com/stretchr/testify@v1.10.0
go get github.com/golang-migrate/migrate/v4@v4.18.1
go get github.com/joho/godotenv@v1.5.1
```

- [ ] **Step 3: Extend `.gitignore`**

Append to `.gitignore`:

```
# Go
/cmd/app/app
/cmd/app/cmd
*.test
coverage.out

# Env
.env
.env.local

# Build artifacts
/dist/
```

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum .gitignore
git commit -m "feat: init Go module and core dependencies"
```

---

### Task 2: docker-compose for Postgres with pgvector

**Files:**
- Create: `docker-compose.yml`
- Create: `.env.example`
- Create: `Makefile`

- [ ] **Step 1: Write `docker-compose.yml`**

```yaml
services:
  postgres:
    image: pgvector/pgvector:pg16
    container_name: hwh_postgres
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: app
      POSTGRES_DB: appdb
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./scripts/db-init.sh:/docker-entrypoint-initdb.d/db-init.sh:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app -d appdb"]
      interval: 2s
      timeout: 5s
      retries: 20

volumes:
  pgdata:
```

- [ ] **Step 2: Write the test-DB init script**

Create `scripts/db-init.sh`:

```bash
#!/bin/bash
set -euo pipefail
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
  CREATE DATABASE appdb_test OWNER $POSTGRES_USER;
EOSQL
```

Then mark it executable:

```bash
chmod +x scripts/db-init.sh
```

- [ ] **Step 3: Write `.env.example`**

```
DATABASE_URL=postgres://app:app@localhost:5432/appdb?sslmode=disable
TEST_DATABASE_URL=postgres://app:app@localhost:5432/appdb_test?sslmode=disable
HTTP_ADDR=:8080
JWT_SIGNING_KEY=dev-only-change-me-32-bytes-long-xxxxxxx
JWT_ACCESS_TTL=15m
REFRESH_TTL=720h
LOG_LEVEL=debug
```

- [ ] **Step 4: Write `Makefile`**

```makefile
.PHONY: db-up db-down db-reset migrate migrate-test test run

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d postgres

migrate:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
	  -path sql/migrations \
	  -database "$$DATABASE_URL" up

migrate-test:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
	  -path sql/migrations \
	  -database "$$TEST_DATABASE_URL" up

test:
	go test ./... -count=1

run:
	go run ./cmd/app serve
```

- [ ] **Step 5: Verify docker-compose starts and both DBs exist**

```bash
make db-up
sleep 3
docker exec hwh_postgres psql -U app -d appdb -c "SELECT 1;"
docker exec hwh_postgres psql -U app -d appdb_test -c "SELECT 1;"
```

Expected: both queries print `?column? -------- 1`.

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml scripts/db-init.sh .env.example Makefile
git commit -m "feat: docker-compose Postgres + pgvector with test DB"
```

---

### Task 3: First migration — `vector` extension and `cities` table

**Files:**
- Create: `sql/migrations/0001_vector_and_cities.up.sql`
- Create: `sql/migrations/0001_vector_and_cities.down.sql`

- [ ] **Step 1: Write `0001_vector_and_cities.up.sql`**

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE cities (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug      TEXT NOT NULL UNIQUE,
    name      TEXT NOT NULL,
    timezone  TEXT NOT NULL
);

INSERT INTO cities (slug, name, timezone)
VALUES ('v1-city', 'V1 City', 'America/New_York');
```

- [ ] **Step 2: Write `0001_vector_and_cities.down.sql`**

```sql
DROP TABLE IF EXISTS cities;
DROP EXTENSION IF EXISTS vector;
```

- [ ] **Step 3: Run migration against both DBs**

```bash
set -a; source .env.example; set +a
make migrate
make migrate-test
```

Expected: both runs print `... migration applied`.

- [ ] **Step 4: Verify schema**

```bash
docker exec hwh_postgres psql -U app -d appdb -c "\dt"
docker exec hwh_postgres psql -U app -d appdb -c "SELECT slug, name, timezone FROM cities;"
```

Expected: `cities` table listed; one row `v1-city | V1 City | America/New_York`.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/0001_vector_and_cities.up.sql sql/migrations/0001_vector_and_cities.down.sql
git commit -m "feat: migration 0001 — vector extension and cities table"
```

---

### Task 4: Config loader

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:

```go
package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad_AllFieldsParsed(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("JWT_SIGNING_KEY", "k")
	t.Setenv("JWT_ACCESS_TTL", "10m")
	t.Setenv("REFRESH_TTL", "100h")
	t.Setenv("LOG_LEVEL", "info")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://x", cfg.DatabaseURL)
	require.Equal(t, ":9999", cfg.HTTPAddr)
	require.Equal(t, "k", cfg.JWTSigningKey)
	require.Equal(t, 10*time.Minute, cfg.JWTAccessTTL)
	require.Equal(t, 100*time.Hour, cfg.RefreshTTL)
	require.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SIGNING_KEY", "k")
	_, err := Load()
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config -run TestLoad -v
```

Expected: FAIL — `package config; no Go files`.

- [ ] **Step 3: Write the implementation**

`internal/config/config.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	JWTSigningKey string
	JWTAccessTTL  time.Duration
	RefreshTTL    time.Duration
	LogLevel      string
}

func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	signingKey := os.Getenv("JWT_SIGNING_KEY")
	if signingKey == "" {
		return nil, errors.New("JWT_SIGNING_KEY is required")
	}

	accessTTL, err := parseDuration("JWT_ACCESS_TTL", "15m")
	if err != nil {
		return nil, err
	}
	refreshTTL, err := parseDuration("REFRESH_TTL", "720h")
	if err != nil {
		return nil, err
	}

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	return &Config{
		DatabaseURL:   dbURL,
		HTTPAddr:      addr,
		JWTSigningKey: signingKey,
		JWTAccessTTL:  accessTTL,
		RefreshTTL:    refreshTTL,
		LogLevel:      logLevel,
	}, nil
}

func parseDuration(envKey, fallback string) (time.Duration, error) {
	v := os.Getenv(envKey)
	if v == "" {
		v = fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: %w", envKey, v, err)
	}
	return d, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config -run TestLoad -v
```

Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: config loader with env-var parsing"
```

---

### Task 5: Postgres connection pool

**Files:**
- Create: `internal/db/db.go`
- Test: `internal/db/db_test.go`

- [ ] **Step 1: Write the failing test**

`internal/db/db_test.go`:

```go
package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewPool_PingSucceeds(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, pool.Ping(ctx))
}

func TestNewPool_BadDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := NewPool(ctx, "postgres://nope:nope@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/db -v
```

Expected: FAIL — `package db; no Go files`.

- [ ] **Step 3: Write the implementation**

`internal/db/db.go`:

```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
set -a; source .env.example; set +a
go test ./internal/db -v
```

Expected: PASS for `TestNewPool_PingSucceeds` and `TestNewPool_BadDSN`.

- [ ] **Step 5: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "feat: pgx connection pool factory"
```

---

### Task 6: Test-DB helper

**Files:**
- Create: `internal/testdb/testdb.go`

This isn't TDD'd because it's test scaffolding — the tests in later tasks are its tests.

- [ ] **Step 1: Write `internal/testdb/testdb.go`**

```go
// Package testdb provides a shared Postgres pool for integration tests.
// Migrations run once per process; tables are truncated between tests.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var (
	once    sync.Once
	pool    *pgxpool.Pool
	openErr error
)

// MustOpen returns a connection pool to the test DB. Migrations run on first
// call. The returned pool is shared across all callers in the same process.
// A t.Cleanup is registered that truncates all data tables.
func MustOpen(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			openErr = errSkip("TEST_DATABASE_URL not set")
			return
		}
		if err := runMigrations(dsn); err != nil {
			openErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			openErr = err
			return
		}
		pool = p
	})
	if openErr != nil {
		if _, ok := openErr.(errSkip); ok {
			t.Skip(openErr.Error())
		}
		require.NoError(t, openErr)
	}
	t.Cleanup(func() { truncateAll(t, pool) })
	return pool
}

type errSkip string

func (e errSkip) Error() string { return string(e) }

func runMigrations(dsn string) error {
	_, file, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations")
	m, err := migrate.New("file://"+migrationsPath, dsn)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func truncateAll(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Truncate tables that hold test data. `cities` is seeded by the migration
	// and is not truncated. Add tables here as migrations introduce them.
	tables := []string{
		"user_interests",
		"refresh_tokens",
		"users",
	}
	for _, tbl := range tables {
		_, err := p.Exec(ctx, "TRUNCATE TABLE "+tbl+" CASCADE")
		if err != nil {
			// Tables not yet created in earlier-task tests are fine; ignore "does not exist".
			// In practice the migration runs first so this branch shouldn't hit.
			continue
		}
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/testdb
```

Expected: no output, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/testdb/testdb.go
git commit -m "feat: shared test DB helper with one-shot migrations and per-test truncation"
```

> NOTE FOR FUTURE TASKS: when later migrations add new tables, append their names to the `tables` slice in `truncateAll`.

---

### Task 7: Migration 0002 — `users` table

**Files:**
- Create: `sql/migrations/0002_users.up.sql`
- Create: `sql/migrations/0002_users.down.sql`

- [ ] **Step 1: Write `0002_users.up.sql`**

```sql
CREATE TABLE users (
    id                            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email                         TEXT NOT NULL,
    password_hash                 TEXT NOT NULL,
    city_id                       UUID NOT NULL REFERENCES cities(id),
    interest_embedding            vector(384),
    interest_embedding_updated_at TIMESTAMPTZ,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                    TIMESTAMPTZ
);

CREATE UNIQUE INDEX users_email_active
    ON users (email)
    WHERE deleted_at IS NULL;
```

- [ ] **Step 2: Write `0002_users.down.sql`**

```sql
DROP TABLE IF EXISTS users;
```

- [ ] **Step 3: Run migrations**

```bash
set -a; source .env.example; set +a
make migrate
make migrate-test
```

Expected: `2/u users` applied to both DBs.

- [ ] **Step 4: Verify**

```bash
docker exec hwh_postgres psql -U app -d appdb -c "\d users"
```

Expected: shows columns including `interest_embedding vector(384)` and the unique partial index.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/0002_users.up.sql sql/migrations/0002_users.down.sql
git commit -m "feat: migration 0002 — users table"
```

---

### Task 8: Password hashing (argon2id)

**Files:**
- Create: `internal/pwhash/pwhash.go`
- Test: `internal/pwhash/pwhash_test.go`

- [ ] **Step 1: Write the failing test**

`internal/pwhash/pwhash_test.go`:

```go
package pwhash

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashAndVerify_RoundTrip(t *testing.T) {
	h, err := Hash("hunter2")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$"))
	ok, err := Verify("hunter2", h)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestVerify_WrongPasswordRejected(t *testing.T) {
	h, err := Hash("hunter2")
	require.NoError(t, err)
	ok, err := Verify("nope", h)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerify_MalformedHash(t *testing.T) {
	_, err := Verify("x", "not-a-real-hash")
	require.Error(t, err)
}

func TestHash_UniqueSaltsProduceDifferentOutputs(t *testing.T) {
	a, err := Hash("same")
	require.NoError(t, err)
	b, err := Hash("same")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}
```

- [ ] **Step 2: Add the argon2 dependency and run tests to see failure**

```bash
go get golang.org/x/crypto/argon2
go test ./internal/pwhash -v
```

Expected: FAIL — `package pwhash; no Go files`.

- [ ] **Step 3: Write the implementation**

`internal/pwhash/pwhash.go`:

```go
// Package pwhash implements argon2id password hashing using the PHC string format:
// $argon2id$v=19$m=<memKiB>,t=<time>,p=<parallel>$<saltBase64>$<hashBase64>
package pwhash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	timeCost    uint32 = 2
	memoryKiB   uint32 = 64 * 1024
	parallelism uint8  = 1
	keyLen      uint32 = 32
	saltLen     uint32 = 16
	version            = argon2.Version
)

func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, timeCost, memoryKiB, parallelism, keyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		version, memoryKiB, timeCost, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("malformed argon2id hash")
	}
	var ver int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &ver); err != nil {
		return false, fmt.Errorf("parse version: %w", err)
	}
	var mem uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/pwhash -v
```

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pwhash/pwhash.go internal/pwhash/pwhash_test.go go.mod go.sum
git commit -m "feat: argon2id password hashing with PHC string format"
```

---

### Task 9: Set up sqlc and generate `users` queries

**Files:**
- Create: `sqlc.yaml`
- Create: `sql/queries/users.sql`
- Generated (do not hand-edit): `internal/store/db.go`, `internal/store/models.go`, `internal/store/users.sql.go`

- [ ] **Step 1: Install sqlc binary (one-time, host install)**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0
which sqlc
```

Expected: prints the install path. If `which sqlc` is empty, ensure `$(go env GOPATH)/bin` is on `$PATH`.

- [ ] **Step 2: Write `sqlc.yaml`**

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "sql/queries"
    schema: "sql/migrations"
    gen:
      go:
        package: "store"
        out: "internal/store"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_interface: false
        emit_prepared_queries: false
        emit_exact_table_names: false
        emit_empty_slices: true
        emit_pointers_for_null_types: true
        overrides:
          - db_type: "vector"
            go_type:
              type: "any"
              pointer: true
```

- [ ] **Step 3: Write `sql/queries/users.sql`**

```sql
-- name: CreateUser :one
INSERT INTO users (email, password_hash, city_id)
VALUES ($1, $2, $3)
RETURNING id, email, city_id, created_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, city_id, created_at
FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT id, email, city_id, created_at
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteUser :exec
UPDATE users
SET deleted_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
```

- [ ] **Step 4: Generate the Go code**

```bash
sqlc generate
ls internal/store/
```

Expected: files `db.go`, `models.go`, `users.sql.go` exist under `internal/store/`.

- [ ] **Step 5: Verify it compiles**

```bash
go build ./internal/store
```

Expected: no output, no errors.

- [ ] **Step 6: Commit**

```bash
git add sqlc.yaml sql/queries/users.sql internal/store/db.go internal/store/models.go internal/store/users.sql.go
git commit -m "feat: sqlc setup and generated users queries"
```

---

### Task 10: HTTP server skeleton + healthz/readyz

**Files:**
- Create: `internal/http/server.go`
- Create: `internal/http/handlers/health.go`
- Test: `internal/http/handlers/health_test.go`

- [ ] **Step 1: Write the failing test**

`internal/http/handlers/health_test.go`:

```go
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	Healthz()(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/http/handlers -v
```

Expected: FAIL — `undefined: Healthz`.

- [ ] **Step 3: Write the implementation**

`internal/http/handlers/health.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
)

// PingableDB is the subset of *pgxpool.Pool used by Readyz.
type PingableDB interface {
	Ping(ctx context.Context) error
}

func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func Readyz(db PingableDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db-unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 4: Write `internal/http/server.go`**

```go
package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
)

type Server struct {
	Addr string
	DB   *pgxpool.Pool
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", handlers.Healthz())
	r.Get("/readyz", handlers.Readyz(s.DB))

	return r
}

func (s *Server) Run(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/http/handlers -v
```

Expected: `TestHealthz` PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/http/server.go internal/http/handlers/health.go internal/http/handlers/health_test.go
git commit -m "feat: HTTP server skeleton with chi router and health endpoints"
```

---

### Task 11: Uniform JSON error helper

**Files:**
- Create: `internal/http/httperr/httperr.go`
- Test: `internal/http/httperr/httperr_test.go`

- [ ] **Step 1: Write the failing test**

`internal/http/httperr/httperr_test.go`:

```go
package httperr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t,
		`{"error":{"code":"invalid_credentials","message":"email or password is wrong"}}`,
		rec.Body.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/http/httperr -v
```

Expected: FAIL — `package httperr; no Go files`.

- [ ] **Step 3: Write the implementation**

`internal/http/httperr/httperr.go`:

```go
// Package httperr writes a uniform JSON error envelope.
package httperr

import (
	"encoding/json"
	"net/http"
)

type Body struct {
	Error Payload `json:"error"`
}

type Payload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Write(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{Error: Payload{Code: code, Message: msg}})
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/http/httperr -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/httperr/httperr.go internal/http/httperr/httperr_test.go
git commit -m "feat: uniform JSON error envelope helper"
```

---

### Task 12: JWT access-token sign / verify

**Files:**
- Create: `internal/auth/jwt.go`
- Test: `internal/auth/jwt_test.go`

- [ ] **Step 1: Write the failing test**

`internal/auth/jwt_test.go`:

```go
package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSignAndVerifyAccess_RoundTrip(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	gotUID, err := signer.VerifyAccess(tok)
	require.NoError(t, err)
	require.Equal(t, uid, gotUID)
}

func TestVerifyAccess_ExpiredRejected(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", -time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid)
	require.NoError(t, err)
	_, err = signer.VerifyAccess(tok)
	require.Error(t, err)
}

func TestVerifyAccess_TamperedRejected(t *testing.T) {
	signer := NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	_, err := signer.VerifyAccess("not.a.token")
	require.Error(t, err)
}
```

- [ ] **Step 2: Add uuid dependency, run tests**

```bash
go get github.com/google/uuid@v1.6.0
go test ./internal/auth -v
```

Expected: FAIL — `undefined: NewJWTSigner`.

- [ ] **Step 3: Write the implementation**

`internal/auth/jwt.go`:

```go
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type JWTSigner struct {
	key []byte
	ttl time.Duration
}

func NewJWTSigner(signingKey string, ttl time.Duration) *JWTSigner {
	return &JWTSigner{key: []byte(signingKey), ttl: ttl}
}

func (s *JWTSigner) SignAccess(userID uuid.UUID) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.key)
}

func (s *JWTSigner) VerifyAccess(tokenStr string) (uuid.UUID, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.key, nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	claims, ok := parsed.Claims.(*jwt.RegisteredClaims)
	if !ok || !parsed.Valid {
		return uuid.Nil, errors.New("invalid token")
	}
	return uuid.Parse(claims.Subject)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/auth -v
```

Expected: all three tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/jwt.go internal/auth/jwt_test.go go.mod go.sum
git commit -m "feat: JWT access-token signer/verifier (HS256)"
```

---

### Task 13: Migration 0003 — `refresh_tokens` and sqlc queries

**Files:**
- Create: `sql/migrations/0003_refresh_tokens.up.sql`
- Create: `sql/migrations/0003_refresh_tokens.down.sql`
- Create: `sql/queries/refresh_tokens.sql`
- Modify: `internal/testdb/testdb.go` — already lists `refresh_tokens` in the truncate list; no change.

- [ ] **Step 1: Write `0003_refresh_tokens.up.sql`**

```sql
CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX refresh_tokens_user_active
    ON refresh_tokens (user_id)
    WHERE revoked_at IS NULL;
```

- [ ] **Step 2: Write `0003_refresh_tokens.down.sql`**

```sql
DROP TABLE IF EXISTS refresh_tokens;
```

- [ ] **Step 3: Write `sql/queries/refresh_tokens.sql`**

```sql
-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, expires_at, created_at;

-- name: GetActiveRefreshTokenByHash :one
SELECT id, user_id, expires_at, revoked_at, created_at
FROM refresh_tokens
WHERE token_hash = $1
  AND revoked_at IS NULL
  AND expires_at > NOW();

-- name: RevokeRefreshTokenByHash :exec
UPDATE refresh_tokens
SET revoked_at = NOW()
WHERE token_hash = $1 AND revoked_at IS NULL;
```

- [ ] **Step 4: Run migrations and regenerate sqlc**

```bash
set -a; source .env.example; set +a
make migrate
make migrate-test
sqlc generate
```

Expected: `internal/store/refresh_tokens.sql.go` exists.

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add sql/migrations/0003_refresh_tokens.up.sql sql/migrations/0003_refresh_tokens.down.sql sql/queries/refresh_tokens.sql internal/store/
git commit -m "feat: migration 0003 — refresh_tokens table and queries"
```

---

### Task 14: Refresh-token mint / hash / lookup

**Files:**
- Create: `internal/auth/refresh.go`
- Test: `internal/auth/refresh_test.go`

- [ ] **Step 1: Write the failing test**

`internal/auth/refresh_test.go`:

```go
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateRefresh_RandomAnd32Bytes(t *testing.T) {
	a, err := GenerateRefresh()
	require.NoError(t, err)
	b, err := GenerateRefresh()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	// base64url encoding of 32 bytes is 43 chars without padding
	require.Len(t, a, 43)
}

func TestHashRefresh_DeterministicSHA256(t *testing.T) {
	want := sha256.Sum256([]byte("abc"))
	got := HashRefresh("abc")
	require.Equal(t, hex.EncodeToString(want[:]), hex.EncodeToString(got))
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/auth -run Refresh -v
```

Expected: FAIL — `undefined: GenerateRefresh, HashRefresh`.

- [ ] **Step 3: Write the implementation**

`internal/auth/refresh.go`:

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const refreshBytes = 32

// GenerateRefresh returns a base64url-encoded 32-byte random token (43 chars, no padding).
func GenerateRefresh() (string, error) {
	buf := make([]byte, refreshBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashRefresh returns sha256(token) as raw bytes for storage as BYTEA.
func HashRefresh(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/auth -v
```

Expected: all auth tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/refresh.go internal/auth/refresh_test.go
git commit -m "feat: refresh-token generation and hashing"
```

---

### Task 15: Signup endpoint

**Files:**
- Create: `internal/http/handlers/auth.go`
- Test: `internal/http/handlers/auth_test.go`

This task wires the first real handler. It depends on store, pwhash, and the test DB helper.

- [ ] **Step 1: Write the failing test**

`internal/http/handlers/auth_test.go`:

```go
package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func defaultCityID(t *testing.T, q *store.Queries) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	row, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	return row.ID.String()
}

func TestSignup_Success(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q))

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@example.com",
		"password": "hunter22",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.AccessToken)
	require.Equal(t, "alice@example.com", resp.User.Email)

	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			found = true
			require.True(t, c.HttpOnly)
			require.NotEmpty(t, c.Value)
		}
	}
	require.True(t, found, "refresh_token cookie should be set")
}

func TestSignup_DuplicateEmailReturns409(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q))

	send := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"email": "dup@example.com", "password": "hunter22"})
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	require.Equal(t, http.StatusCreated, send().Code)
	require.Equal(t, http.StatusConflict, send().Code)
}

func TestSignup_ShortPasswordReturns400(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := handlers.Signup(q, signer, time.Hour, defaultCityID(t, q))

	body, _ := json.Marshal(map[string]string{"email": "x@example.com", "password": "short"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 2: Add a `GetDefaultCity` query**

Add to `sql/queries/users.sql` (yes, it lives there for now; we'll split if cities grows):

```sql
-- name: GetDefaultCity :one
SELECT id, slug, name, timezone
FROM cities
WHERE slug = 'v1-city';
```

Regenerate:

```bash
sqlc generate
```

- [ ] **Step 3: Run tests to verify they fail with the right error**

```bash
go test ./internal/http/handlers -v -run Signup
```

Expected: FAIL — `undefined: handlers.Signup` (or similar).

- [ ] **Step 4: Write the implementation**

`internal/http/handlers/auth.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/pwhash"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signupResponse struct {
	AccessToken string  `json:"access_token"`
	User        userOut `json:"user"`
}

type userOut struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Signup creates a new user, sets the refresh cookie, and returns an access token.
// cityID is the default city assignment for v1.
func Signup(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration, cityID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req signupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		if !looksLikeEmail(req.Email) {
			httperr.Write(w, http.StatusBadRequest, "invalid_email", "email is not valid")
			return
		}
		if len(req.Password) < 8 {
			httperr.Write(w, http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
			return
		}

		hash, err := pwhash.Hash(req.Password)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "hash_failed", "could not hash password")
			return
		}

		cityUUID, err := uuid.Parse(cityID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "bad_city_id", "city id is invalid")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.CreateUser(ctx, store.CreateUserParams{
			Email:        req.Email,
			PasswordHash: hash,
			CityID:       cityUUID,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				httperr.Write(w, http.StatusConflict, "email_taken", "an account with that email already exists")
				return
			}
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not create user")
			return
		}

		access, err := signer.SignAccess(row.ID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "sign_failed", "could not sign access token")
			return
		}

		refreshTok, err := auth.GenerateRefresh()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "refresh_failed", "could not mint refresh token")
			return
		}
		if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
			UserID:    row.ID,
			TokenHash: auth.HashRefresh(refreshTok),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(refreshTTL), Valid: true},
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist refresh token")
			return
		}
		setRefreshCookie(w, refreshTok, refreshTTL)

		writeJSON(w, http.StatusCreated, signupResponse{
			AccessToken: access,
			User:        userOut{ID: row.ID.String(), Email: row.Email},
		})
	}
}

func setRefreshCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/auth",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && strings.Contains(s[at+1:], ".")
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
set -a; source .env.example; set +a
go test ./internal/http/handlers -v -run Signup
```

Expected: `TestSignup_Success`, `TestSignup_DuplicateEmailReturns409`, `TestSignup_ShortPasswordReturns400` all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/http/handlers/auth.go internal/http/handlers/auth_test.go sql/queries/users.sql internal/store/
git commit -m "feat: POST /auth/signup endpoint"
```

---

### Task 16: Login endpoint

**Files:**
- Modify: `internal/http/handlers/auth.go` — add `Login` handler
- Test: append to `internal/http/handlers/auth_test.go`

- [ ] **Step 1: Append failing tests**

Add to `internal/http/handlers/auth_test.go`:

```go
func signupAndGetCity(t *testing.T) (*store.Queries, *auth.JWTSigner, string) {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	return q, signer, cityID
}

func TestLogin_Success(t *testing.T) {
	q, signer, _ := signupAndGetCity(t)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.AccessToken)
}

func TestLogin_WrongPassword(t *testing.T) {
	q, signer, _ := signupAndGetCity(t)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "nope"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLogin_UnknownEmail(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	body, _ := json.Marshal(map[string]string{"email": "no@example.com", "password": "whatever"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/handlers -v -run Login
```

Expected: FAIL — `undefined: handlers.Login`.

- [ ] **Step 3: Implement `Login`**

Append to `internal/http/handlers/auth.go`:

```go
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
}

func Login(q *store.Queries, signer *auth.JWTSigner, refreshTTL time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetUserByEmail(ctx, req.Email)
		if err != nil {
			httperr.Write(w, http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
			return
		}
		ok, err := pwhash.Verify(req.Password, row.PasswordHash)
		if err != nil || !ok {
			httperr.Write(w, http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
			return
		}

		access, err := signer.SignAccess(row.ID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "sign_failed", "could not sign access token")
			return
		}
		refreshTok, err := auth.GenerateRefresh()
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "refresh_failed", "could not mint refresh token")
			return
		}
		if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
			UserID:    row.ID,
			TokenHash: auth.HashRefresh(refreshTok),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(refreshTTL), Valid: true},
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not persist refresh token")
			return
		}
		setRefreshCookie(w, refreshTok, refreshTTL)

		writeJSON(w, http.StatusOK, loginResponse{AccessToken: access})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
set -a; source .env.example; set +a
go test ./internal/http/handlers -v -run Login
```

Expected: `TestLogin_Success`, `TestLogin_WrongPassword`, `TestLogin_UnknownEmail` all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/auth.go internal/http/handlers/auth_test.go
git commit -m "feat: POST /auth/login endpoint"
```

---

### Task 17: Refresh endpoint

**Files:**
- Modify: `internal/http/handlers/auth.go` — add `Refresh`
- Test: append to `internal/http/handlers/auth_test.go`

- [ ] **Step 1: Append failing tests**

Add to `internal/http/handlers/auth_test.go`:

```go
func TestRefresh_Success(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	// signup to get a refresh cookie
	body, _ := json.Marshal(map[string]string{"email": "rf@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	// call /auth/refresh with the cookie
	req2 := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req2.AddCookie(refreshCookie)
	rec2 := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp))
	require.NotEmpty(t, resp.AccessToken)
}

func TestRefresh_NoCookie(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	rec := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRefresh_RevokedRejected(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "rev@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	// revoke directly via the query
	require.NoError(t, q.RevokeRefreshTokenByHash(context.Background(), auth.HashRefresh(refreshCookie.Value)))

	req2 := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req2.AddCookie(refreshCookie)
	rec2 := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec2, req2)
	require.Equal(t, http.StatusUnauthorized, rec2.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/handlers -v -run Refresh
```

Expected: FAIL — `undefined: handlers.Refresh`.

- [ ] **Step 3: Implement `Refresh`**

Append to `internal/http/handlers/auth.go`:

```go
type refreshResponse struct {
	AccessToken string `json:"access_token"`
}

func Refresh(q *store.Queries, signer *auth.JWTSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("refresh_token")
		if err != nil || c.Value == "" {
			httperr.Write(w, http.StatusUnauthorized, "no_refresh", "refresh token cookie is missing")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row, err := q.GetActiveRefreshTokenByHash(ctx, auth.HashRefresh(c.Value))
		if err != nil {
			httperr.Write(w, http.StatusUnauthorized, "invalid_refresh", "refresh token is not valid")
			return
		}
		access, err := signer.SignAccess(row.UserID)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "sign_failed", "could not sign access token")
			return
		}
		writeJSON(w, http.StatusOK, refreshResponse{AccessToken: access})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
set -a; source .env.example; set +a
go test ./internal/http/handlers -v -run Refresh
```

Expected: all three Refresh tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/auth.go internal/http/handlers/auth_test.go
git commit -m "feat: POST /auth/refresh endpoint"
```

---

### Task 18: Logout endpoint

**Files:**
- Modify: `internal/http/handlers/auth.go` — add `Logout`
- Test: append to `internal/http/handlers/auth_test.go`

- [ ] **Step 1: Append failing test**

Add to `internal/http/handlers/auth_test.go`:

```go
func TestLogout_RevokesAndClears(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "lo@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	req2 := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req2.AddCookie(refreshCookie)
	rec2 := httptest.NewRecorder()
	handlers.Logout(q)(rec2, req2)
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// subsequent refresh should fail
	req3 := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req3.AddCookie(refreshCookie)
	rec3 := httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec3, req3)
	require.Equal(t, http.StatusUnauthorized, rec3.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/http/handlers -v -run Logout
```

Expected: FAIL — `undefined: handlers.Logout`.

- [ ] **Step 3: Implement `Logout`**

Append to `internal/http/handlers/auth.go`:

```go
func Logout(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("refresh_token")
		if err == nil && c.Value != "" {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			_ = q.RevokeRefreshTokenByHash(ctx, auth.HashRefresh(c.Value))
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "refresh_token",
			Value:    "",
			Path:     "/auth",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
set -a; source .env.example; set +a
go test ./internal/http/handlers -v -run Logout
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/auth.go internal/http/handlers/auth_test.go
git commit -m "feat: POST /auth/logout endpoint"
```

---

### Task 19: Bearer-token middleware

**Files:**
- Create: `internal/http/middleware/auth.go`
- Test: `internal/http/middleware/auth_test.go`

- [ ] **Step 1: Write the failing test**

`internal/http/middleware/auth_test.go`:

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
)

func TestRequireAuth_AllowsValidToken(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	uid := uuid.New()
	tok, err := signer.SignAccess(uid)
	require.NoError(t, err)

	called := false
	h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := UserIDFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, uid, got)
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

func TestRequireAuth_MissingHeaderRejected(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_BadTokenRejected(t *testing.T) {
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	h := RequireAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer not.a.token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/middleware -v
```

Expected: FAIL — `undefined: RequireAuth`.

- [ ] **Step 3: Write the implementation**

`internal/http/middleware/auth.go`:

```go
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
)

type ctxKey int

const userIDKey ctxKey = 1

func RequireAuth(signer *auth.JWTSigner) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				httperr.Write(w, http.StatusUnauthorized, "no_token", "Authorization header missing or malformed")
				return
			}
			tok := strings.TrimPrefix(authz, "Bearer ")
			uid, err := signer.VerifyAccess(tok)
			if err != nil {
				httperr.Write(w, http.StatusUnauthorized, "invalid_token", "access token is not valid")
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, uid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	v := ctx.Value(userIDKey)
	if v == nil {
		return uuid.Nil, false
	}
	uid, ok := v.(uuid.UUID)
	return uid, ok
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/http/middleware -v
```

Expected: all three tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/middleware/auth.go internal/http/middleware/auth_test.go
git commit -m "feat: Bearer-token auth middleware"
```

---

### Task 20: `GET /me` and `DELETE /me` endpoints

**Files:**
- Create: `internal/http/handlers/user.go`
- Test: `internal/http/handlers/user_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/http/handlers/user_test.go`:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestGetMe_ReturnsCurrentUser(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	signupAndLogin := func(email string) (string, string) {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter22"})
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var resp struct {
			AccessToken string `json:"access_token"`
			User        struct{ ID, Email string }
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		return resp.AccessToken, resp.User.ID
	}

	access, _ := signupAndLogin("getme@example.com")

	// call GET /me through the middleware chain
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMe(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "getme@example.com", out.Email)
}

func TestDeleteMe_SoftDeletes(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "del@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	req2 := httptest.NewRequest(http.MethodDelete, "/me", nil)
	req2.Header.Set("Authorization", "Bearer "+resp.AccessToken)
	rec2 := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.DeleteMe(q)).ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// verify deleted_at is set
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := q.GetUserByEmail(ctx, "del@example.com")
	require.Error(t, err) // soft-deleted users are filtered out
}
```

Make sure the test file's import block includes `"bytes"` alongside the other imports.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/handlers -v -run TestGetMe
```

Expected: FAIL — `undefined: handlers.GetMe`.

- [ ] **Step 3: Write the implementation**

`internal/http/handlers/user.go`:

```go
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

func GetMe(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		row, err := q.GetUserByID(ctx, uid)
		if err != nil {
			httperr.Write(w, http.StatusNotFound, "no_user", "user not found")
			return
		}
		writeJSON(w, http.StatusOK, userOut{ID: row.ID.String(), Email: row.Email})
	}
}

func DeleteMe(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.SoftDeleteUser(ctx, uid); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete user")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
set -a; source .env.example; set +a
go test ./internal/http/handlers -v
```

Expected: all handler tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/user.go internal/http/handlers/user_test.go
git commit -m "feat: GET /me and DELETE /me endpoints"
```

---

### Task 21: Migration 0004 — `user_interests` and sqlc queries

**Files:**
- Create: `sql/migrations/0004_user_interests.up.sql`
- Create: `sql/migrations/0004_user_interests.down.sql`
- Create: `sql/queries/user_interests.sql`

- [ ] **Step 1: Write `0004_user_interests.up.sql`**

```sql
CREATE TABLE user_interests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind             TEXT NOT NULL CHECK (kind IN ('spotify_top_artist', 'spotify_top_genre', 'manual_tag')),
    value            TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    weight           DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, kind, normalized_value)
);

CREATE INDEX user_interests_user_kind ON user_interests (user_id, kind);
```

- [ ] **Step 2: Write `0004_user_interests.down.sql`**

```sql
DROP TABLE IF EXISTS user_interests;
```

- [ ] **Step 3: Write `sql/queries/user_interests.sql`**

```sql
-- name: ListManualInterestsByUser :many
SELECT id, value, normalized_value, weight, created_at
FROM user_interests
WHERE user_id = $1 AND kind = 'manual_tag'
ORDER BY created_at ASC;

-- name: CreateManualInterest :one
INSERT INTO user_interests (user_id, kind, value, normalized_value, weight)
VALUES ($1, 'manual_tag', $2, $3, 1.0)
RETURNING id, value, normalized_value, weight, created_at;

-- name: DeleteInterestByIDForUser :exec
DELETE FROM user_interests
WHERE id = $1 AND user_id = $2;
```

- [ ] **Step 4: Migrate and regenerate**

```bash
set -a; source .env.example; set +a
make migrate
make migrate-test
sqlc generate
go build ./...
```

Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/0004_user_interests.up.sql sql/migrations/0004_user_interests.down.sql sql/queries/user_interests.sql internal/store/
git commit -m "feat: migration 0004 — user_interests table and queries"
```

---

### Task 22: Interest CRUD endpoints

**Files:**
- Create: `internal/http/handlers/interests.go`
- Test: `internal/http/handlers/interests_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/http/handlers/interests_test.go`:

```go
package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func signupAndAccess(t *testing.T, q *store.Queries, signer *auth.JWTSigner, cityID, email string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp.AccessToken
}

func TestPostInterests_CreatesManualTag(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "int1@example.com")

	body, _ := json.Marshal(map[string]string{"value": "Indie Rock"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var out struct {
		ID              string  `json:"id"`
		Value           string  `json:"value"`
		NormalizedValue string  `json:"normalized_value"`
		Weight          float64 `json:"weight"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "Indie Rock", out.Value)
	require.Equal(t, "indie rock", out.NormalizedValue)
}

func TestPostInterests_DuplicateReturns409(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "int2@example.com")

	send := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"value": "Jazz"})
		req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw := middleware.RequireAuth(signer)
		mw(handlers.CreateInterest(q)).ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusCreated, send().Code)
	require.Equal(t, http.StatusConflict, send().Code)
}

func TestGetInterests_ReturnsOnlyOwn(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "int3@example.com")

	for _, v := range []string{"Rock", "Pop"} {
		body, _ := json.Marshal(map[string]string{"value": v})
		req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw := middleware.RequireAuth(signer)
		mw(handlers.CreateInterest(q)).ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/me/interests", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.ListInterests(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Interests []struct {
			Value string `json:"value"`
		} `json:"interests"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Len(t, out.Interests, 2)
}

func TestDeleteInterest_OwnershipEnforced(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	accessA := signupAndAccess(t, q, signer, cityID, "owner@example.com")
	accessB := signupAndAccess(t, q, signer, cityID, "thief@example.com")

	// owner creates an interest
	body, _ := json.Marshal(map[string]string{"value": "Theater"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+accessA)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	// thief tries to delete it
	r := chi.NewRouter()
	r.With(mw).Delete("/me/interests/{id}", handlers.DeleteInterest(q))

	req2 := httptest.NewRequest(http.MethodDelete, "/me/interests/"+created.ID, nil)
	req2.Header.Set("Authorization", "Bearer "+accessB)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	// DELETE is idempotent: thief's call returns 204 but DB row remains owned by A
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// owner can list and still see it
	req3 := httptest.NewRequest(http.MethodGet, "/me/interests", nil)
	req3.Header.Set("Authorization", "Bearer "+accessA)
	rec3 := httptest.NewRecorder()
	mw(handlers.ListInterests(q)).ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusOK, rec3.Code)
	var out struct {
		Interests []struct {
			Value string `json:"value"`
		} `json:"interests"`
	}
	require.NoError(t, json.NewDecoder(rec3.Body).Decode(&out))
	require.Len(t, out.Interests, 1)
	require.Equal(t, "Theater", out.Interests[0].Value)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/handlers -v -run Interest
```

Expected: FAIL — `undefined: handlers.CreateInterest, ListInterests, DeleteInterest`.

- [ ] **Step 3: Write the implementation**

`internal/http/handlers/interests.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type interestOut struct {
	ID              string  `json:"id"`
	Value           string  `json:"value"`
	NormalizedValue string  `json:"normalized_value"`
	Weight          float64 `json:"weight"`
	CreatedAt       string  `json:"created_at"`
}

type listInterestsResponse struct {
	Interests []interestOut `json:"interests"`
}

func ListInterests(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		rows, err := q.ListManualInterestsByUser(ctx, uid)
		if err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not list interests")
			return
		}
		out := make([]interestOut, 0, len(rows))
		for _, row := range rows {
			out = append(out, interestOut{
				ID:              row.ID.String(),
				Value:           row.Value,
				NormalizedValue: row.NormalizedValue,
				Weight:          row.Weight,
				CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, listInterestsResponse{Interests: out})
	}
}

type createInterestRequest struct {
	Value string `json:"value"`
}

func CreateInterest(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		var req createInterestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
			return
		}
		req.Value = strings.TrimSpace(req.Value)
		if req.Value == "" {
			httperr.Write(w, http.StatusBadRequest, "empty_value", "value must not be empty")
			return
		}
		normalized := strings.ToLower(req.Value)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		row, err := q.CreateManualInterest(ctx, store.CreateManualInterestParams{
			UserID:          uid,
			Value:           req.Value,
			NormalizedValue: normalized,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				httperr.Write(w, http.StatusConflict, "duplicate_interest", "this interest already exists")
				return
			}
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not create interest")
			return
		}
		writeJSON(w, http.StatusCreated, interestOut{
			ID:              row.ID.String(),
			Value:           row.Value,
			NormalizedValue: row.NormalizedValue,
			Weight:          row.Weight,
			CreatedAt:       row.CreatedAt.Time.UTC().Format(time.RFC3339),
		})
	}
}

func DeleteInterest(q *store.Queries) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := middleware.UserIDFromContext(r.Context())
		if !ok {
			httperr.Write(w, http.StatusUnauthorized, "no_user", "user not in context")
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "bad_id", "id is not a valid uuid")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := q.DeleteInterestByIDForUser(ctx, store.DeleteInterestByIDForUserParams{
			ID:     id,
			UserID: uid,
		}); err != nil {
			httperr.Write(w, http.StatusInternalServerError, "db_error", "could not delete interest")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
set -a; source .env.example; set +a
go test ./internal/http/handlers -v -run Interest
```

Expected: all four interest tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/handlers/interests.go internal/http/handlers/interests_test.go
git commit -m "feat: manual-tag interest CRUD endpoints"
```

---

### Task 23: Wire all routes in `server.go`

**Files:**
- Modify: `internal/http/server.go` — add routes for auth, user, interests
- Test: `internal/http/server_test.go` — end-to-end signup → login → /me

- [ ] **Step 1: Write the failing end-to-end test**

`internal/http/server_test.go`:

```go
package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestServer_EndToEnd_SignupLoginMe(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	s := &hs.Server{
		DB:           pool,
		Queries:      q,
		JWTSigner:    signer,
		RefreshTTL:   time.Hour,
		DefaultCityID: city.ID.String(),
	}
	mux := s.Router()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// signup
	body, _ := json.Marshal(map[string]string{"email": "e2e@example.com", "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var su struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&su))
	resp.Body.Close()
	require.NotEmpty(t, su.AccessToken)

	// GET /me
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+su.AccessToken)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var me struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&me))
	resp2.Body.Close()
	require.Equal(t, "e2e@example.com", me.Email)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/http -v -run TestServer_EndToEnd
```

Expected: FAIL — fields on `hs.Server` don't yet exist (`Queries`, `JWTSigner`, `RefreshTTL`, `DefaultCityID`).

- [ ] **Step 3: Update `internal/http/server.go`**

Replace the file with:

```go
package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

type Server struct {
	Addr          string
	DB            *pgxpool.Pool
	Queries       *store.Queries
	JWTSigner     *auth.JWTSigner
	RefreshTTL    time.Duration
	DefaultCityID string
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	// Public
	r.Get("/healthz", handlers.Healthz())
	r.Get("/readyz", handlers.Readyz(s.DB))

	// Auth (public)
	r.Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
	r.Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
	r.Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
	r.Post("/auth/logout", handlers.Logout(s.Queries))

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		r.Get("/me", handlers.GetMe(s.Queries))
		r.Delete("/me", handlers.DeleteMe(s.Queries))
		r.Get("/me/interests", handlers.ListInterests(s.Queries))
		r.Post("/me/interests", handlers.CreateInterest(s.Queries))
		r.Delete("/me/interests/{id}", handlers.DeleteInterest(s.Queries))
	})

	return r
}

func (s *Server) Run(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
```

- [ ] **Step 4: Run all tests**

```bash
set -a; source .env.example; set +a
go test ./... -count=1
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/server.go internal/http/server_test.go
git commit -m "feat: wire all routes in HTTP server"
```

---

### Task 24: `cmd/app/main.go` with `serve` subcommand

**Files:**
- Create: `cmd/app/main.go`

- [ ] **Step 1: Write `cmd/app/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/config"
	"github.com/wmyers/heres-whats-happening/internal/db"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

func main() {
	_ = godotenv.Load() // ignore error if no .env
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: app <subcommand>

subcommands:
  serve   run the HTTP API server
`)
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	q := store.New(pool)
	city, err := q.GetDefaultCity(ctx)
	if err != nil {
		return fmt.Errorf("load default city: %w", err)
	}

	s := &hs.Server{
		Addr:          cfg.HTTPAddr,
		DB:            pool,
		Queries:       q,
		JWTSigner:     auth.NewJWTSigner(cfg.JWTSigningKey, cfg.JWTAccessTTL),
		RefreshTTL:    cfg.RefreshTTL,
		DefaultCityID: city.ID.String(),
	}
	fmt.Printf("listening on %s\n", cfg.HTTPAddr)
	return s.Run(ctx)
}
```

- [ ] **Step 2: Verify build and run**

```bash
go build ./cmd/app
set -a; source .env.example; set +a
./app serve &
SERVE_PID=$!
sleep 1
curl -s http://localhost:8080/healthz
echo
curl -s http://localhost:8080/readyz
echo
kill $SERVE_PID
```

Expected: `{"status":"ok"}` then `{"status":"ready"}`.

- [ ] **Step 3: Smoke test signup → me with curl**

```bash
./app serve &
SERVE_PID=$!
sleep 1

# signup
ACCESS=$(curl -s -X POST http://localhost:8080/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"smoke@example.com","password":"hunter22"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')

# call /me
curl -s http://localhost:8080/me -H "Authorization: Bearer $ACCESS"
echo

kill $SERVE_PID
```

Expected: prints `{"id":"...","email":"smoke@example.com"}`.

- [ ] **Step 4: Commit**

```bash
git add cmd/app/main.go
git commit -m "feat: cmd/app main with serve subcommand"
```

---

### Task 25: Dockerfile for the Go binary

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

This isn't deployed in plan 1, but having it now means later plans don't need to add it.

- [ ] **Step 1: Write `Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/app ./cmd/app

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
CMD ["serve"]
```

- [ ] **Step 2: Write `.dockerignore`**

```
.git
.gitignore
docker-compose.yml
Makefile
README.md
*.md
.env
.env.local
.env.example
docs/
.superpowers/
sql/queries/         # only schema migrations are needed at runtime
cmd/app/app
*.test
coverage.out
```

- [ ] **Step 3: Build the image**

```bash
docker build -t hwh-app:dev .
docker run --rm hwh-app:dev --help 2>&1 || true
```

Expected: image builds; running with `--help` prints the `usage:` block from main.go (exit code 2 is fine).

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "feat: distroless Dockerfile for the Go binary"
```

---

### Task 26: Plan-1 smoke test in README

**Files:**
- Create: `README.md` (or modify existing to add a setup section; current README has 2 lines)

- [ ] **Step 1: Replace `README.md` content with**

```markdown
# event-calendar

A custom event calendar based on your interests.

See [docs/superpowers/specs/2026-05-19-event-calendar-design.md](docs/superpowers/specs/2026-05-19-event-calendar-design.md) for the v1 design.

## Local dev quickstart (Plan 1 — backend foundation)

Prerequisites: Go 1.24, Docker, `sqlc` (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0`).

```bash
cp .env.example .env

# Start Postgres + pgvector (creates appdb and appdb_test)
make db-up

# Apply migrations to both databases
set -a; source .env.example; set +a
make migrate
make migrate-test

# Run the test suite (integration tests against appdb_test)
make test

# Run the server
make run
# In another shell:
curl http://localhost:8080/healthz
```

### Try the auth flow

```bash
ACCESS=$(curl -s -X POST http://localhost:8080/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"hunter22"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')

curl http://localhost:8080/me -H "Authorization: Bearer $ACCESS"
curl -X POST http://localhost:8080/me/interests \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"value":"Indie Rock"}'
curl http://localhost:8080/me/interests -H "Authorization: Bearer $ACCESS"
```
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add Plan 1 backend foundation quickstart"
```

---

## Self-Review

**Spec coverage (Plan 1 scope only — events, scrapers, match-job, frontend, Terraform are deferred to later plans):**

| Spec requirement (Plan 1 scope) | Implemented in |
|---|---|
| Postgres 16 + pgvector | Task 2 (docker-compose) + Task 3 (vector extension) |
| `cities` table seeded with one row | Task 3 |
| `users` table with `interest_embedding vector(384)` | Task 7 |
| `refresh_tokens` table | Task 13 |
| `user_interests` table with `(user_id, kind, normalized_value)` unique constraint | Task 21 |
| argon2id password hashing | Task 8 |
| JWT HS256 access tokens, 15-min TTL (configurable) | Task 12 |
| Refresh token: 32 bytes random, hash stored in DB, `httpOnly` cookie | Tasks 14, 15 |
| POST /auth/signup | Task 15 |
| POST /auth/login | Task 16 |
| POST /auth/refresh | Task 17 |
| POST /auth/logout | Task 18 |
| Bearer-token auth middleware | Task 19 |
| GET /me, DELETE /me | Task 20 |
| GET /me/interests, POST /me/interests, DELETE /me/interests/:id | Task 22 |
| GET /healthz, GET /readyz | Task 10 |
| Single binary subcommand dispatch (`app serve`) | Task 24 |
| chi router, pgx, sqlc, golang-migrate, golang-jwt/jwt/v5, argon2 | All wired across Tasks 1–24 |
| Uniform JSON error envelope | Task 11 |
| Integration tests against real Postgres | Task 6 + every handler test |

**Scoped out of Plan 1 (explicitly handled by later plans):**

- Spotify integration → Plan 3
- iCal endpoints → Plan 5
- Events, venues, performers, genres, match_config, user_event_match tables → Plans 2, 4
- TEI, embeddings, match job → Plan 4
- SQS, scrapers → Plans 2, 3
- Frontend → Plan 6
- Terraform / CI/CD → Plans 7, 8

**Placeholder scan:** no "TBD", "TODO", or "add appropriate validation" in any step. Every code-touching step has full code.

**Type consistency check:**

- `*store.Queries` is the value passed around; `store.New(pool)` constructs it (Task 9 generates `store.New`).
- `*auth.JWTSigner` from `auth.NewJWTSigner` — used consistently in tasks 12, 15, 16, 17, 19, 20, 22, 23, 24.
- `pgtype.Timestamptz{Time:..., Valid: true}` used in tasks 15, 16. No drift.
- Handler signatures: each returns `http.HandlerFunc` from a top-level constructor that captures dependencies. Consistent across all handlers.
- Cookie name is always `refresh_token` (tasks 15, 16, 17, 18).
- JSON envelope is always `{"error":{"code":"...","message":"..."}}` (task 11 + every handler call).

**Plan-internal consistency notes:**

- Task 20's test file `internal/http/handlers/user_test.go` and Task 15's `internal/http/handlers/auth_test.go` are both in package `handlers_test`. Shared helpers (`defaultCityID`, `signupAndAccess`) live in the file where they are first declared; later test files in the same package re-use them without re-declaring.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-19-plan-01-backend-foundation.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
