# API Rate Limiting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rate limit the three abusable public endpoints — `/auth/signup` (3/hour per IP, successful creations only), `/auth/login` (10/min per IP), `/auth/refresh` (30/min per IP) — on a client IP that callers cannot forge.

**Architecture:** A new `internal/ratelimit` package exposes one `Limiter` interface with two backends: an in-memory token bucket for login and refresh, and a Postgres sliding-window counter for signup (durable, and countable only on success). Two chi middlewares consume that interface. Everything is keyed on a client IP resolved by a new middleware that replaces `chimw.RealIP`, which today reads forgeable headers.

**Tech Stack:** Go 1.26, chi v5, pgx v5 + sqlc, `golang.org/x/time/rate`, testify, golang-migrate. Frontend: React + TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-07-22-api-rate-limiting-design.md`

## Global Constraints

- Client IP is **never** derived from `True-Client-IP` or `X-Real-IP`. Only the rightmost `X-Forwarded-For` entry, and only when `TRUST_PROXY` is true.
- Signup counts **only `201` responses**. `400` and `409` responses consume no budget.
- Limits: signup **3 per hour**, login **10 per minute**, refresh **30 per minute**. All per client IP.
- Denied requests return **429** with a `Retry-After` header in seconds and error code `rate_limited`.
- The Postgres limiter **fails open**: if its query errors, the request proceeds.
- No new Go module dependencies. `golang.org/x/time` is already a direct dependency (`go.mod:19`).
- Existing handler code in `internal/http/handlers/auth.go` is **not modified**. Rate limiting lives entirely in middleware.
- Integration tests use `testdb.MustOpen(t)`, which runs migrations and truncates all tables in `t.Cleanup`.
- Run `make test` before every commit.

## File Structure

| File | Responsibility |
|---|---|
| `internal/http/middleware/clientip.go` (create) | Resolve a trustworthy client IP; expose `ClientIP(r)` accessor |
| `internal/http/middleware/clientip_test.go` (create) | Spoofing regression tests |
| `internal/config/config.go` (modify) | `TrustProxy bool` from `TRUST_PROXY` |
| `internal/ratelimit/ratelimit.go` (create) | `Decision`, `Limiter` interface |
| `internal/ratelimit/memory.go` (create) | In-memory token bucket + eviction |
| `internal/ratelimit/memory_test.go` (create) | Deterministic clock tests |
| `internal/ratelimit/postgres.go` (create) | Sliding-window counter over `rate_limit_events` |
| `internal/ratelimit/postgres_test.go` (create) | Integration tests against `appdb_test` |
| `sql/migrations/0018_rate_limit_events.{up,down}.sql` (create) | Table + index |
| `sql/queries/rate_limit.sql` (create) | sqlc source for count/insert/oldest/delete |
| `internal/store/rate_limit.sql.go`, `models.go` (generated) | sqlc output |
| `internal/http/middleware/ratelimit.go` (create) | `RateLimit`, `RateLimitOnSuccess` |
| `internal/http/middleware/ratelimit_test.go` (create) | Middleware behavior + status-conditional recording |
| `internal/http/server.go` (modify) | Swap `RealIP`, construct limiters, wire routes, start cleanup ticker |
| `cmd/app/main.go` (modify) | Pass `TrustProxy` through |
| `terraform/prod/ecs_api.tf` (modify) | `TRUST_PROXY=true` |
| `web/src/components/SignupForm.tsx`, `LoginForm.tsx` (modify) | Message for `rate_limited` |

---

### Task 1: Trustworthy client IP

Everything downstream keys on this. `chimw.RealIP` (installed at `internal/http/server.go:49`) reads `True-Client-IP`, then `X-Real-IP`, then the **first** `X-Forwarded-For` entry (`go-chi/chi/v5@v5.2.5/middleware/realip.go:42-51`). The ALB appends to `X-Forwarded-For` and strips none of these, so any caller can pick their own bucket by sending a random header.

**Files:**
- Create: `internal/http/middleware/clientip.go`
- Create: `internal/http/middleware/clientip_test.go`
- Modify: `internal/config/config.go` (struct at :15-45, `Load()` at :49, literal at :106-128)
- Modify: `internal/http/server.go:21-44` (Server struct), `:49` (middleware)
- Modify: `cmd/app/main.go:158-174`
- Modify: `terraform/prod/ecs_api.tf` (env list, after line 28)

**Interfaces:**
- Consumes: nothing.
- Produces: `middleware.ClientIPResolver(trustProxy bool) func(http.Handler) http.Handler` and `middleware.ClientIP(r *http.Request) string`. Task 4 calls `ClientIP`. `config.Config.TrustProxy bool`; `http.Server.TrustProxy bool`.

- [ ] **Step 1: Write the failing tests**

Create `internal/http/middleware/clientip_test.go`:

```go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
)

// resolve runs the middleware over a request and reports the IP the chain saw.
func resolve(t *testing.T, trustProxy bool, remoteAddr string, headers map[string]string) string {
	t.Helper()
	var got string
	h := middleware.ClientIPResolver(trustProxy)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = middleware.ClientIP(r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIP_RightmostXFFWins(t *testing.T) {
	// The ALB appends the real peer; everything left of it came from the caller.
	got := resolve(t, true, "10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 203.0.113.9",
	})
	require.Equal(t, "203.0.113.9", got)
}

// The regression that motivates this task: a forged header must not mint a fresh
// rate-limit bucket.
func TestClientIP_IgnoresSpoofableHeaders(t *testing.T) {
	got := resolve(t, true, "10.0.0.5:443", map[string]string{
		"True-Client-IP":  "9.9.9.9",
		"X-Real-IP":       "8.8.8.8",
		"X-Forwarded-For": "203.0.113.9",
	})
	require.Equal(t, "203.0.113.9", got)
}

func TestClientIP_TrueClientIPAloneIsIgnored(t *testing.T) {
	got := resolve(t, true, "203.0.113.9:443", map[string]string{
		"True-Client-IP": "9.9.9.9",
	})
	require.Equal(t, "203.0.113.9", got)
}

func TestClientIP_NoProxyTrustIgnoresXFF(t *testing.T) {
	got := resolve(t, false, "203.0.113.9:443", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
	})
	require.Equal(t, "203.0.113.9", got)
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	got := resolve(t, true, "203.0.113.9:443", nil)
	require.Equal(t, "203.0.113.9", got)
}

// A garbage rightmost entry must fall back to the peer, never to a constant —
// a constant would collapse every caller into one shared bucket.
func TestClientIP_UnparseableXFFFallsBack(t *testing.T) {
	got := resolve(t, true, "203.0.113.9:443", map[string]string{
		"X-Forwarded-For": "not-an-ip",
	})
	require.Equal(t, "203.0.113.9", got)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/middleware/ -run TestClientIP -v
```

Expected: compile failure — `undefined: middleware.ClientIPResolver`.

- [ ] **Step 3: Implement the middleware**

Create `internal/http/middleware/clientip.go`:

```go
package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ClientIPResolver returns a middleware that normalizes r.RemoteAddr to a bare
// client IP address, replacing chi's RealIP.
//
// When trustProxy is true the address is taken from the RIGHTMOST entry of
// X-Forwarded-For — the hop our ALB appended. Every entry to its left was
// supplied by the caller and is forgeable. True-Client-IP and X-Real-IP are
// ignored entirely: nothing in our request path sets them, so honoring them
// would let any caller choose their own rate-limit bucket.
//
// When trustProxy is false (local development, tests) the host portion of
// r.RemoteAddr is used as-is.
func ClientIPResolver(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RemoteAddr = resolveClientIP(r, trustProxy)
			next.ServeHTTP(w, r)
		})
	}
}

func resolveClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			candidate := strings.TrimSpace(parts[len(parts)-1])
			if net.ParseIP(candidate) != nil {
				return candidate
			}
		}
	}
	return hostOnly(r.RemoteAddr)
}

// ClientIP returns the bare client IP for r. ClientIPResolver should be
// installed early in the chain; this stays correct either way.
func ClientIP(r *http.Request) string {
	return hostOnly(r.RemoteAddr)
}

func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/http/middleware/ -run TestClientIP -v
```

Expected: all six PASS.

- [ ] **Step 5: Add `TrustProxy` to config**

In `internal/config/config.go`, add to the `Config` struct after `CORSAllowedOrigins []string` (line 44):

```go
	// Plan 7 additions
	TrustProxy bool
```

In `Load()`, insert after the `corsOrigins` block (after line 104):

```go
	trustProxy := false
	if v := os.Getenv("TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid TRUST_PROXY=%q: %w", v, err)
		}
		trustProxy = b
	}
```

Add to the `cfg := &Config{...}` literal after `CORSAllowedOrigins: corsOrigins,` (line 127):

```go
		TrustProxy:          trustProxy,
```

`strconv` and `fmt` are already imported (`config.go:8`, `:6`).

- [ ] **Step 6: Add a config test**

Append to `internal/config/config_test.go`. That file is `package config` (an internal test), so `Load` is called unqualified, and `require` is already imported:

```go
func TestLoad_TrustProxy(t *testing.T) {
	setRequiredDB(t)
	t.Setenv("JWT_SIGNING_KEY", "k")

	t.Setenv("TRUST_PROXY", "")
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.TrustProxy, "must default to false so local dev never trusts XFF")

	t.Setenv("TRUST_PROXY", "true")
	cfg, err = Load()
	require.NoError(t, err)
	require.True(t, cfg.TrustProxy)

	t.Setenv("TRUST_PROXY", "banana")
	_, err = Load()
	require.Error(t, err)
}
```

`setRequiredDB(t)` is the existing helper used by `TestLoad_AllFieldsParsed` (`internal/config/config_test.go:11`) to satisfy the required `DB_*` variables.

- [ ] **Step 7: Wire it into the server**

In `internal/http/server.go`, add to the `Server` struct after `CORSAllowedOrigins []string` (line 43):

```go
	// Plan 7 addition — when true, derive the client IP from the rightmost
	// X-Forwarded-For entry. Set only when running behind our ALB.
	TrustProxy bool
```

Replace line 49:

```go
	r.Use(chimw.RequestID)
	r.Use(middleware.ClientIPResolver(s.TrustProxy))
```

(Delete `r.Use(chimw.RealIP)`. `chimw` is still used for `RequestID`, `Logger`, `Recoverer`, `Timeout`, so the import stays.)

In `cmd/app/main.go`, add to the `&hs.Server{...}` literal after `CORSAllowedOrigins: cfg.CORSAllowedOrigins,` (line 173):

```go
		TrustProxy:         cfg.TrustProxy,
```

- [ ] **Step 8: Set the env var in Terraform**

In `terraform/prod/ecs_api.tf`, add to the environment list after line 28:

```hcl
    { name = "TRUST_PROXY", value = "true" },
```

This is the **only** infrastructure change in this task. Do not run Terraform, `taskdef-edit.sh`, or any deploy command — the repo owner handles rollout. Until the variable is live in the running task definition, prod falls back to `RemoteAddr` (the ALB's address), which buckets all callers together; that is a safe-but-blunt failure mode, not a security regression.

- [ ] **Step 9: Run the full suite**

```bash
make test
```

Expected: PASS. Nothing else reads `RemoteAddr`, so swapping `RealIP` is behavior-preserving for existing tests.

- [ ] **Step 10: Commit**

```bash
git add internal/http/middleware/clientip.go internal/http/middleware/clientip_test.go \
        internal/config/config.go internal/config/config_test.go \
        internal/http/server.go cmd/app/main.go terraform/prod/ecs_api.tf
git commit -m "fix: derive client IP from rightmost XFF instead of spoofable headers"
```

---

### Task 2: In-memory token bucket limiter

**Files:**
- Create: `internal/ratelimit/ratelimit.go`
- Create: `internal/ratelimit/memory.go`
- Create: `internal/ratelimit/memory_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `ratelimit.Decision{Allowed bool; RetryAfter time.Duration}`
  - `ratelimit.Limiter` interface with `Allow(ctx context.Context, key string) (Decision, error)` and `Record(ctx context.Context, key string) error`
  - `ratelimit.NewMemory(limit int, window time.Duration) *Memory`
  - `ratelimit.NewMemoryWithClock(limit int, window time.Duration, now func() time.Time) *Memory`
  - `(*Memory).Sweep(now time.Time) int`

Tasks 4 and 5 depend on these exact names.

- [ ] **Step 1: Write the failing tests**

Create `internal/ratelimit/memory_test.go`. `x/time/rate` takes an explicit timestamp, so these use a fake clock and never sleep:

```go
package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
)

// fakeClock is a hand-advanced clock so limiter tests are deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestMemory_AllowsUpToLimitThenDenies(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(3, time.Minute, clk.Now)
	ctx := context.Background()

	for i := range 3 {
		d, err := l.Allow(ctx, "1.1.1.1")
		require.NoError(t, err)
		require.True(t, d.Allowed, "request %d should be allowed", i+1)
	}

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Greater(t, d.RetryAfter, time.Duration(0), "denials must carry a Retry-After")
	require.LessOrEqual(t, d.RetryAfter, time.Minute)
}

func TestMemory_KeysAreIndependent(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(1, time.Minute, clk.Now)
	ctx := context.Background()

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	d, err = l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	d, err = l.Allow(ctx, "2.2.2.2")
	require.NoError(t, err)
	require.True(t, d.Allowed, "a different IP must have its own bucket")
}

func TestMemory_RefillsAfterWindow(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(2, time.Minute, clk.Now)
	ctx := context.Background()

	for range 2 {
		d, _ := l.Allow(ctx, "1.1.1.1")
		require.True(t, d.Allowed)
	}
	d, _ := l.Allow(ctx, "1.1.1.1")
	require.False(t, d.Allowed)

	clk.Advance(time.Minute)

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.True(t, d.Allowed, "bucket must refill after one full window")
}

// A denied request must not consume a token, or a client hammering the endpoint
// would push its own recovery further and further out.
func TestMemory_DenialDoesNotConsumeBudget(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(1, time.Minute, clk.Now)
	ctx := context.Background()

	d, _ := l.Allow(ctx, "1.1.1.1")
	require.True(t, d.Allowed)

	for range 10 {
		d, _ = l.Allow(ctx, "1.1.1.1")
		require.False(t, d.Allowed)
	}

	clk.Advance(time.Minute)
	d, _ = l.Allow(ctx, "1.1.1.1")
	require.True(t, d.Allowed, "10 denials must not have delayed the refill")
}

func TestMemory_RecordIsNoOp(t *testing.T) {
	l := ratelimit.NewMemory(1, time.Minute)
	require.NoError(t, l.Record(context.Background(), "1.1.1.1"))
}

// Without eviction, an attacker rotating source IPs grows the map without bound
// and turns the rate limiter into a memory-exhaustion vector.
func TestMemory_SweepEvictsIdleBuckets(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(5, time.Minute, clk.Now)
	ctx := context.Background()

	for i := range 100 {
		_, err := l.Allow(ctx, fmt.Sprintf("10.0.0.%d", i))
		require.NoError(t, err)
	}
	require.Equal(t, 0, l.Sweep(clk.Now()), "fresh buckets must survive")

	clk.Advance(3 * time.Minute)
	require.Equal(t, 100, l.Sweep(clk.Now()), "buckets idle beyond 2 windows are full and safe to drop")
	require.Equal(t, 0, l.Sweep(clk.Now()))
}

func TestMemory_SweepKeepsActiveBuckets(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(5, time.Minute, clk.Now)
	ctx := context.Background()

	_, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	clk.Advance(3 * time.Minute)
	_, err = l.Allow(ctx, "1.1.1.1") // touched again, now recent
	require.NoError(t, err)

	require.Equal(t, 0, l.Sweep(clk.Now()))
}

func TestMemory_ConcurrentAllowIsRaceFree(t *testing.T) {
	l := ratelimit.NewMemory(1000, time.Minute)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 20 {
				_, _ = l.Allow(ctx, fmt.Sprintf("10.0.0.%d", i%5))
			}
		}(i)
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/ratelimit/ -v
```

Expected: build failure — no such package.

- [ ] **Step 3: Define the interface**

Create `internal/ratelimit/ratelimit.go`:

```go
// Package ratelimit provides request rate limiting keyed on an opaque string
// (in practice, a client IP address).
package ratelimit

import (
	"context"
	"time"
)

// Decision is the outcome of a limit check. RetryAfter is meaningful only when
// Allowed is false.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter checks and records usage against a limit.
//
// Allow and Record are separate so a caller can gate on every request but count
// only some of them — signup counts only successful account creations, so its
// middleware calls Allow before the handler and Record only on a 201.
type Limiter interface {
	// Allow reports whether a request for key may proceed. Implementations that
	// return a non-nil error must fail open (Allowed true) so an infrastructure
	// problem cannot take the endpoint down.
	Allow(ctx context.Context, key string) (Decision, error)

	// Record registers one use against key.
	Record(ctx context.Context, key string) error
}
```

- [ ] **Step 4: Implement the memory limiter**

Create `internal/ratelimit/memory.go`:

```go
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxBucketsBeforeSweep bounds map growth between opportunistic sweeps.
const maxBucketsBeforeSweep = 10_000

type bucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// Memory is an in-process token-bucket Limiter. State is per-process: it resets
// on restart and is not shared across tasks, which is acceptable for the login
// and refresh limits but not for signup (see Postgres).
//
// Record is a no-op — Memory counts every Allow.
type Memory struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	limit  rate.Limit
	burst  int
	window time.Duration
	now    func() time.Time
}

// NewMemory returns a limiter permitting limit requests per window, per key.
func NewMemory(limit int, window time.Duration) *Memory {
	return NewMemoryWithClock(limit, window, time.Now)
}

// NewMemoryWithClock is NewMemory with an injectable clock, for tests.
func NewMemoryWithClock(limit int, window time.Duration, now func() time.Time) *Memory {
	return &Memory{
		buckets: make(map[string]*bucket),
		limit:   rate.Limit(float64(limit) / window.Seconds()),
		burst:   limit,
		window:  window,
		now:     now,
	}
}

// Allow consumes one token for key. It never returns an error.
func (m *Memory) Allow(_ context.Context, key string) (Decision, error) {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.buckets) >= maxBucketsBeforeSweep {
		m.sweepLocked(now)
	}

	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(m.limit, m.burst)}
		m.buckets[key] = b
	}
	b.lastSeen = now

	// ReserveN tells us how long until a token is available. Cancelling the
	// reservation on denial is what keeps a denied request from consuming
	// budget and pushing the client's own recovery further out.
	res := b.lim.ReserveN(now, 1)
	if !res.OK() {
		return Decision{Allowed: false, RetryAfter: m.window}, nil
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return Decision{Allowed: false, RetryAfter: d}, nil
	}
	return Decision{Allowed: true}, nil
}

// Record is a no-op. Memory counts at Allow time.
func (m *Memory) Record(context.Context, string) error { return nil }

// Sweep drops buckets untouched for more than two windows and returns how many
// were removed. A bucket refills completely within one window, so anything idle
// that long is necessarily full and carries no state worth keeping.
func (m *Memory) Sweep(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweepLocked(now)
}

func (m *Memory) sweepLocked(now time.Time) int {
	cutoff := now.Add(-2 * m.window)
	n := 0
	for k, b := range m.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(m.buckets, k)
			n++
		}
	}
	return n
}

var _ Limiter = (*Memory)(nil)
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/ratelimit/ -v
go test ./internal/ratelimit/ -race -run TestMemory_Concurrent
```

Expected: all PASS, no race detected.

- [ ] **Step 6: Commit**

```bash
git add internal/ratelimit/ratelimit.go internal/ratelimit/memory.go internal/ratelimit/memory_test.go
git commit -m "feat: add in-memory token bucket rate limiter"
```

---

### Task 3: Postgres sliding-window limiter

Signup needs a limit that survives restarts and does not depend on task count.

**Files:**
- Create: `sql/migrations/0018_rate_limit_events.up.sql`
- Create: `sql/migrations/0018_rate_limit_events.down.sql`
- Create: `sql/queries/rate_limit.sql`
- Create: `internal/ratelimit/postgres.go`
- Create: `internal/ratelimit/postgres_test.go`
- Generated: `internal/store/rate_limit.sql.go`, `internal/store/models.go`

**Interfaces:**
- Consumes: `ratelimit.Decision`, `ratelimit.Limiter` (Task 2).
- Produces:
  - `ratelimit.NewPostgres(q *store.Queries, bucket string, limit int, window time.Duration) *Postgres`
  - `ratelimit.NewPostgresWithClock(q *store.Queries, bucket string, limit int, window time.Duration, now func() time.Time) *Postgres`
  - `store.CountRateLimitEvents`, `store.InsertRateLimitEvent`, `store.OldestRateLimitEvent`, `store.DeleteRateLimitEventsBefore`

Task 5 uses `NewPostgres` and `DeleteRateLimitEventsBefore`.

- [ ] **Step 1: Write the migration**

Create `sql/migrations/0018_rate_limit_events.up.sql`:

```sql
CREATE TABLE rate_limit_events (
    id         BIGSERIAL PRIMARY KEY,
    bucket     TEXT        NOT NULL,  -- namespaces the limit, e.g. 'signup'
    key        TEXT        NOT NULL,  -- client IP
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Serves both the windowed count and the oldest-in-window lookup.
CREATE INDEX rate_limit_events_lookup
    ON rate_limit_events (bucket, key, created_at DESC);
```

Create `sql/migrations/0018_rate_limit_events.down.sql`:

```sql
DROP TABLE rate_limit_events;
```

- [ ] **Step 2: Write the sqlc queries**

Create `sql/queries/rate_limit.sql`. These use `sqlc.arg()` rather than the `$N` style seen elsewhere in `sql/queries/` specifically so the generated parameter is named `Since` instead of `CreatedAt`, which would read as the wrong thing entirely:

```sql
-- name: CountRateLimitEvents :one
SELECT COUNT(*)
FROM rate_limit_events
WHERE bucket = sqlc.arg(bucket)
  AND key = sqlc.arg(key)
  AND created_at > sqlc.arg(since);

-- name: OldestRateLimitEvent :one
SELECT created_at
FROM rate_limit_events
WHERE bucket = sqlc.arg(bucket)
  AND key = sqlc.arg(key)
  AND created_at > sqlc.arg(since)
ORDER BY created_at ASC
LIMIT 1;

-- name: InsertRateLimitEvent :exec
INSERT INTO rate_limit_events (bucket, key, created_at)
VALUES (sqlc.arg(bucket), sqlc.arg(key), sqlc.arg(created_at));

-- name: DeleteRateLimitEventsBefore :exec
DELETE FROM rate_limit_events
WHERE created_at < sqlc.arg(before);
```

- [ ] **Step 3: Generate and apply**

```bash
sqlc generate
make migrate
make migrate-test
git status --short
```

Expected: `internal/store/rate_limit.sql.go` created and `internal/store/models.go` modified (it regenerates whenever a new table is added — commit it alongside).

Confirm the generated signatures before writing Task 3's Go code:

```bash
grep -n "func (q \*Queries) \(Count\|Oldest\|Insert\|Delete\)RateLimit" -A 3 internal/store/rate_limit.sql.go
```

Expected shape (params are structs where there is more than one argument):

```go
func (q *Queries) CountRateLimitEvents(ctx context.Context, arg CountRateLimitEventsParams) (int64, error)
func (q *Queries) OldestRateLimitEvent(ctx context.Context, arg OldestRateLimitEventParams) (pgtype.Timestamptz, error)
func (q *Queries) InsertRateLimitEvent(ctx context.Context, arg InsertRateLimitEventParams) error
func (q *Queries) DeleteRateLimitEventsBefore(ctx context.Context, before pgtype.Timestamptz) error
```

`InsertRateLimitEvent` takes `created_at` explicitly rather than relying on the column default. That is what makes the limiter's clock authoritative: if rows carried the database's `NOW()` while `Allow` compared against an injected test clock, the two would disagree and the window tests would be meaningless.

If sqlc names anything differently, use the generated names in Step 5 rather than editing generated files.

- [ ] **Step 4: Write the failing tests**

Create `internal/ratelimit/postgres_test.go`:

```go
package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestPostgres_AllowsUntilLimitReached(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	clk := newFakeClock()
	l := ratelimit.NewPostgresWithClock(q, "signup", 3, time.Hour, clk.Now)
	ctx := context.Background()

	for i := range 3 {
		d, err := l.Allow(ctx, "1.1.1.1")
		require.NoError(t, err)
		require.True(t, d.Allowed, "check %d", i+1)
		require.NoError(t, l.Record(ctx, "1.1.1.1"))
	}

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Greater(t, d.RetryAfter, time.Duration(0))
	require.LessOrEqual(t, d.RetryAfter, time.Hour)
}

func TestPostgres_KeysAreIndependent(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	l := ratelimit.NewPostgres(q, "signup", 1, time.Hour)
	ctx := context.Background()

	require.NoError(t, l.Record(ctx, "1.1.1.1"))

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	d, err = l.Allow(ctx, "2.2.2.2")
	require.NoError(t, err)
	require.True(t, d.Allowed)
}

func TestPostgres_BucketsAreIndependent(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	signup := ratelimit.NewPostgres(q, "signup", 1, time.Hour)
	other := ratelimit.NewPostgres(q, "other", 1, time.Hour)

	require.NoError(t, signup.Record(ctx, "1.1.1.1"))

	d, err := signup.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	d, err = other.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.True(t, d.Allowed, "a different bucket must not share budget")
}

func TestPostgres_EventsAgeOutOfWindow(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	clk := newFakeClock()
	l := ratelimit.NewPostgresWithClock(q, "signup", 1, time.Hour, clk.Now)
	ctx := context.Background()

	require.NoError(t, l.Record(ctx, "1.1.1.1"))
	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	// Record stamped the row from the fake clock, so advancing past the window
	// genuinely pushes that event out of it.
	clk.Advance(2 * time.Hour)

	d, err = l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.True(t, d.Allowed, "events older than the window must not count")
}

func TestPostgres_RetryAfterReflectsOldestEvent(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	clk := newFakeClock()
	l := ratelimit.NewPostgresWithClock(q, "signup", 1, time.Hour, clk.Now)
	ctx := context.Background()

	require.NoError(t, l.Record(ctx, "1.1.1.1"))
	clk.Advance(50 * time.Minute)

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	// The oldest event ages out 10 minutes from now. Both sides come from the
	// same fake clock, so allow only enough slack for timestamptz rounding.
	require.InDelta(t, (10 * time.Minute).Seconds(), d.RetryAfter.Seconds(), 2)
}

// A database problem must not take signup offline. The limiter guards against
// bulk abuse, not against any single request.
func TestPostgres_FailsOpenOnQueryError(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	l := ratelimit.NewPostgres(q, "signup", 1, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes the query fail

	d, err := l.Allow(ctx, "1.1.1.1")
	require.Error(t, err, "the error must surface so the caller can log it")
	require.True(t, d.Allowed, "but the request must still proceed")
}
```

`fakeClock` comes from `memory_test.go` — same `ratelimit_test` package, so it is shared, not redeclared.

- [ ] **Step 5: Implement the Postgres limiter**

Create `internal/ratelimit/postgres.go`:

```go
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Postgres is a durable sliding-window Limiter backed by the rate_limit_events
// table. Unlike Memory it survives restarts and stays correct across tasks,
// which is what the signup limit needs.
//
// Allow and Record are deliberately separate: the signup middleware checks
// before running the handler and records only when the handler created an
// account.
type Postgres struct {
	q      *store.Queries
	bucket string
	limit  int
	window time.Duration
	now    func() time.Time
}

// NewPostgres returns a limiter permitting limit recorded events per window,
// per key, within the named bucket.
func NewPostgres(q *store.Queries, bucket string, limit int, window time.Duration) *Postgres {
	return NewPostgresWithClock(q, bucket, limit, window, time.Now)
}

// NewPostgresWithClock is NewPostgres with an injectable clock, for tests.
func NewPostgresWithClock(q *store.Queries, bucket string, limit int, window time.Duration, now func() time.Time) *Postgres {
	return &Postgres{q: q, bucket: bucket, limit: limit, window: window, now: now}
}

// Allow reports whether key is under its limit. On a query error it returns the
// error alongside Allowed=true: the caller should log it and let the request
// through rather than fail the endpoint closed.
func (p *Postgres) Allow(ctx context.Context, key string) (Decision, error) {
	now := p.now()
	since := pgtype.Timestamptz{Time: now.Add(-p.window), Valid: true}

	n, err := p.q.CountRateLimitEvents(ctx, store.CountRateLimitEventsParams{
		Bucket: p.bucket,
		Key:    key,
		Since:  since,
	})
	if err != nil {
		return Decision{Allowed: true}, fmt.Errorf("count rate limit events: %w", err)
	}
	if n < int64(p.limit) {
		return Decision{Allowed: true}, nil
	}

	// Over the limit. Budget frees up when the oldest event in the window ages
	// out, which is a far more useful Retry-After than the whole window.
	retryAfter := p.window
	oldest, err := p.q.OldestRateLimitEvent(ctx, store.OldestRateLimitEventParams{
		Bucket: p.bucket,
		Key:    key,
		Since:  since,
	})
	if err == nil && oldest.Valid {
		if d := oldest.Time.Add(p.window).Sub(now); d > 0 && d < retryAfter {
			retryAfter = d
		}
	}
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return Decision{Allowed: false, RetryAfter: retryAfter}, nil
}

// Record writes one event for key, timestamped from this limiter's clock so
// that Allow and Record always agree on what "now" means.
func (p *Postgres) Record(ctx context.Context, key string) error {
	if err := p.q.InsertRateLimitEvent(ctx, store.InsertRateLimitEventParams{
		Bucket:    p.bucket,
		Key:       key,
		CreatedAt: pgtype.Timestamptz{Time: p.now(), Valid: true},
	}); err != nil {
		return fmt.Errorf("insert rate limit event: %w", err)
	}
	return nil
}

var _ Limiter = (*Postgres)(nil)
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test ./internal/ratelimit/ -v
```

Expected: all PASS. If Postgres tests fail to connect, run `make db-up && make migrate-test` first.

- [ ] **Step 7: Commit**

```bash
git add sql/migrations/0018_rate_limit_events.up.sql sql/migrations/0018_rate_limit_events.down.sql \
        sql/queries/rate_limit.sql internal/store/rate_limit.sql.go internal/store/models.go \
        internal/ratelimit/postgres.go internal/ratelimit/postgres_test.go
git commit -m "feat: add Postgres-backed sliding window rate limiter"
```

---

### Task 4: Rate limit middlewares

**Files:**
- Create: `internal/http/middleware/ratelimit.go`
- Create: `internal/http/middleware/ratelimit_test.go`

**Interfaces:**
- Consumes: `ratelimit.Limiter`, `ratelimit.Decision` (Tasks 2-3); `middleware.ClientIP` (Task 1); `httperr.Write` (existing, 4 args: `w, status, code, message`).
- Produces:
  - `middleware.RateLimit(l ratelimit.Limiter, name string) func(http.Handler) http.Handler`
  - `middleware.RateLimitOnSuccess(l ratelimit.Limiter, name string) func(http.Handler) http.Handler`

`name` is used only for log lines. The bucket identity already lives inside the limiter.

- [ ] **Step 1: Write the failing tests**

Create `internal/http/middleware/ratelimit_test.go`:

```go
package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
)

// stubLimiter lets each test dictate decisions and observe Record calls.
type stubLimiter struct {
	decision ratelimit.Decision
	allowErr error
	recorded []string
}

func (s *stubLimiter) Allow(_ context.Context, _ string) (ratelimit.Decision, error) {
	return s.decision, s.allowErr
}

func (s *stubLimiter) Record(_ context.Context, key string) error {
	s.recorded = append(s.recorded, key)
	return nil
}

func okHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	})
}

func TestRateLimit_AllowsWhenUnderLimit(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimit_Returns429WithRetryAfter(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 90 * time.Second}}
	reached := false
	h := middleware.RateLimit(l, "login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.False(t, reached, "a limited request must never reach the handler")

	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	require.Equal(t, 90, secs)

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "rate_limited", body.Error.Code)
}

// Sub-second retry windows must not round down to "Retry-After: 0", which would
// invite an immediate retry.
func TestRateLimit_RetryAfterRoundsUpToAtLeastOne(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: 200 * time.Millisecond}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Equal(t, "1", rec.Header().Get("Retry-After"))
}

func TestRateLimit_FailsOpenOnLimiterError(t *testing.T) {
	l := &stubLimiter{
		decision: ratelimit.Decision{Allowed: true},
		allowErr: context.DeadlineExceeded,
	}
	h := middleware.RateLimit(l, "signup")(okHandler(http.StatusOK))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimitOnSuccess_RecordsOn201(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusCreated))

	req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, []string{"203.0.113.9"}, l.recorded)
}

// The core of the successes-only policy: a mistyped password must cost nothing.
func TestRateLimitOnSuccess_DoesNotRecordOn400(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusBadRequest))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Empty(t, l.recorded, "a validation failure must not consume signup budget")
}

func TestRateLimitOnSuccess_DoesNotRecordOn409(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusConflict))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Empty(t, l.recorded, "a duplicate email must not consume signup budget")
}

func TestRateLimitOnSuccess_DoesNotRecordWhenDenied(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: time.Hour}}
	h := middleware.RateLimitOnSuccess(l, "signup")(okHandler(http.StatusCreated))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Empty(t, l.recorded)
}

// A handler that writes a body without an explicit WriteHeader implies 200.
func TestRateLimitOnSuccess_RecordsOnImplicit200(t *testing.T) {
	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimitOnSuccess(l, "signup")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/signup", nil))

	require.Len(t, l.recorded, 1)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/middleware/ -run TestRateLimit -v
```

Expected: compile failure — `undefined: middleware.RateLimit`.

- [ ] **Step 3: Implement the middlewares**

Create `internal/http/middleware/ratelimit.go`:

```go
package middleware

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
)

// RateLimit rejects requests over the limit before they reach the handler.
// Every request counts. name appears in log lines only.
func RateLimit(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !checkAllowed(w, r, l, name) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitOnSuccess rejects requests over the limit, then counts the request
// only if the handler returned 2xx.
//
// Signup uses this: a rejected signup (bad email, weak password, duplicate
// address) must cost the caller nothing, or one typo would lock a real user out
// for the whole window without an account to show for it.
func RateLimitOnSuccess(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !checkAllowed(w, r, l, name) {
				return
			}

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // handler wrote a body without WriteHeader
			}
			if status < 200 || status > 299 {
				return
			}
			if err := l.Record(r.Context(), ClientIP(r)); err != nil {
				log.Printf("ratelimit %s: record failed: %v", name, err)
			}
		})
	}
}

// checkAllowed reports whether the request may proceed, writing 429 if not.
func checkAllowed(w http.ResponseWriter, r *http.Request, l ratelimit.Limiter, name string) bool {
	d, err := l.Allow(r.Context(), ClientIP(r))
	if err != nil {
		// Allow already failed open; log and honor its decision.
		log.Printf("ratelimit %s: check failed: %v", name, err)
	}
	if d.Allowed {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
	httperr.Write(w, http.StatusTooManyRequests, "rate_limited",
		"too many requests, please try again later")
	return false
}

// retryAfterSeconds rounds up, never below 1 — "Retry-After: 0" would invite an
// immediate retry.
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	return int(math.Max(1, math.Ceil(d.Seconds())))
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/http/middleware/ -v
```

Expected: all PASS, including the Task 1 client IP tests.

If the `rate_limited` assertion fails, check the JSON shape `httperr.Write` produces (`internal/http/httperr/`) and adjust the test's struct to match — the frontend's `ApiErrorBody` in `web/src/api/client.ts:31` expects `{ error: { code, message } }`.

- [ ] **Step 5: Commit**

```bash
git add internal/http/middleware/ratelimit.go internal/http/middleware/ratelimit_test.go
git commit -m "feat: add rate limit middlewares with success-conditional recording"
```

---

### Task 5: Wire limits into the router

**Files:**
- Modify: `internal/http/server.go:46-94` (`Router`)
- Modify: `internal/http/server_test.go` (append a wiring test)

**Interfaces:**
- Consumes: `ratelimit.NewMemory`, `ratelimit.NewPostgres` (Tasks 2-3); `middleware.RateLimit`, `middleware.RateLimitOnSuccess` (Task 4).
- Produces: rate-limited routes. Task 6 adds the cleanup ticker to `Run`.

- [ ] **Step 1: Write the failing test**

Append to `internal/http/server_test.go`. This uses `/auth/refresh` deliberately: `handlers.Refresh` returns 401 on a missing cookie before touching the database (`internal/http/handlers/auth.go:178-181`), so the wiring is testable without DB fixtures.

```go
func TestServer_RefreshIsRateLimited(t *testing.T) {
	s := &hs.Server{
		JWTSigner:  auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL: time.Hour,
	}
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	// The refresh limit is 30/min. Without a cookie each call short-circuits to
	// 401 without a DB round trip.
	for i := range 30 {
		resp, err := http.Post(srv.URL+"/auth/refresh", "application/json", nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Post(srv.URL+"/auth/refresh", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/http/ -run TestServer_RefreshIsRateLimited -v
```

Expected: FAIL — the 31st request returns 401, not 429.

- [ ] **Step 3: Wire the routes**

In `internal/http/server.go`, add the `ratelimit` import:

```go
	"github.com/wmyers/heres-whats-happening/internal/ratelimit"
```

Construct the limiters inside `Router()`, immediately after the middleware `Use` block (after line 55) and before the public routes. They are locals rather than `Server` fields because they hold mutable runtime state, and per-`Router()` construction keeps each test server's buckets isolated:

```go
	// Rate limiters for the public auth surface. Signup is Postgres-backed so
	// the ceiling survives restarts; login and refresh are in-process, which is
	// accurate enough for limits this loose.
	signupLimiter := ratelimit.NewPostgres(s.Queries, "signup", 3, time.Hour)
	loginLimiter := ratelimit.NewMemory(10, time.Minute)
	refreshLimiter := ratelimit.NewMemory(30, time.Minute)
```

Replace lines 64-66:

```go
	r.With(middleware.RateLimitOnSuccess(signupLimiter, "signup")).
		Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
	r.With(middleware.RateLimit(loginLimiter, "login")).
		Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
	r.With(middleware.RateLimit(refreshLimiter, "refresh")).
		Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
```

Leave `/auth/logout` (line 67) unchanged — it is out of scope.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/http/ -v
```

Expected: PASS, including the existing `TestServer_EndToEnd_SignupLoginMe`. That test performs one signup, well under 3/hour, and `testdb` truncates `rate_limit_events` in `t.Cleanup`, so repeated runs do not accumulate.

- [ ] **Step 5: Run the full suite**

```bash
make test
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/http/server.go internal/http/server_test.go
git commit -m "feat: rate limit signup, login, and refresh endpoints"
```

---

### Task 6: Expire old rate limit rows

Without this, `rate_limit_events` grows forever.

**Files:**
- Modify: `internal/http/server.go:96-120` (`Run`)
- Create: `internal/http/cleanup_internal_test.go`

**Interfaces:**
- Consumes: `store.DeleteRateLimitEventsBefore`, `store.InsertRateLimitEventParams`, `store.CountRateLimitEventsParams` (Task 3).
- Produces: nothing downstream.

The deletion itself is split out from the ticker loop so it can be tested directly. The `select`/`time.Ticker` scaffolding around it is not worth injecting a clock for; the query it runs is.

- [ ] **Step 1: Write the failing test**

Create `internal/http/cleanup_internal_test.go`. This is `package http` (not `http_test`) because it exercises an unexported method — Go permits both test packages in one directory, and `server_test.go` stays external:

```go
package http

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestDeleteExpiredRateLimitEvents(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	s := &Server{Queries: q}
	ctx := context.Background()
	now := time.Now()

	insert := func(key string, at time.Time) {
		t.Helper()
		require.NoError(t, q.InsertRateLimitEvent(ctx, store.InsertRateLimitEventParams{
			Bucket:    "signup",
			Key:       key,
			CreatedAt: pgtype.Timestamptz{Time: at, Valid: true},
		}))
	}
	count := func(key string, since time.Time) int64 {
		t.Helper()
		n, err := q.CountRateLimitEvents(ctx, store.CountRateLimitEventsParams{
			Bucket: "signup",
			Key:    key,
			Since:  pgtype.Timestamptz{Time: since, Valid: true},
		})
		require.NoError(t, err)
		return n
	}

	insert("1.1.1.1", now)                      // fresh
	insert("2.2.2.2", now.Add(-48*time.Hour))   // past the 24h retention

	require.NoError(t, s.deleteExpiredRateLimitEvents(ctx, now))

	require.Equal(t, int64(1), count("1.1.1.1", now.Add(-time.Hour)),
		"a row inside the retention window must survive")
	require.Equal(t, int64(0), count("2.2.2.2", now.Add(-72*time.Hour)),
		"a row past retention must be deleted")
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/http/ -run TestDeleteExpiredRateLimitEvents -v
```

Expected: compile failure — `s.deleteExpiredRateLimitEvents` undefined.

- [ ] **Step 3: Add the cleanup code**

In `internal/http/server.go`, add below `Run`:

```go
// rateLimitRetention is how long rate limit rows are kept. Comfortably longer
// than the widest window (1h) so cleanup can never delete a live row.
const rateLimitRetention = 24 * time.Hour

// deleteExpiredRateLimitEvents removes rate limit rows past the retention
// window, measured from now.
func (s *Server) deleteExpiredRateLimitEvents(ctx context.Context, now time.Time) error {
	cutoff := pgtype.Timestamptz{Time: now.Add(-rateLimitRetention), Valid: true}
	return s.Queries.DeleteRateLimitEventsBefore(ctx, cutoff)
}

// runRateLimitCleanup deletes expired rate limit rows until ctx is cancelled.
func (s *Server) runRateLimitCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			delCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := s.deleteExpiredRateLimitEvents(delCtx, time.Now())
			cancel()
			if err != nil {
				log.Printf("rate limit cleanup: %v", err)
			}
		}
	}
}
```

Add the imports `log` and `github.com/jackc/pgx/v5/pgtype` to `server.go`.

Start it in `Run`, after the consumer goroutines (after line 110). It does not write to `errCh` — a failed cleanup pass is logged, not fatal:

```go
	if s.Queries != nil {
		go s.runRateLimitCleanup(ctx)
	}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/http/ -run TestDeleteExpiredRateLimitEvents -v
go build ./... && make test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/server.go internal/http/cleanup_internal_test.go
git commit -m "feat: expire rate limit rows older than 24h"
```

---

### Task 7: Surface 429 in the web UI

`web/src/api/client.ts:20` already throws `ApiError` carrying `status` and `code`, so 429 reaches the forms today — it just renders the raw fallback message.

**Files:**
- Modify: `web/src/components/SignupForm.tsx:22-30`
- Modify: `web/src/components/LoginForm.tsx:22-27`
- Create: `web/src/components/SignupForm.test.tsx`
- Modify: `web/src/components/LoginForm.test.tsx`

**Interfaces:**
- Consumes: the `rate_limited` error code from Task 4.
- Produces: nothing downstream.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/SignupForm.test.tsx`, following the structure of the existing `LoginForm.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import SignupForm from './SignupForm';

beforeEach(() => {
  vi.resetAllMocks();
});

function mockSignup(signup: ReturnType<typeof vi.fn>) {
  vi.mocked(useAuth).mockReturnValue({
    status: 'anonymous',
    user: null,
    login: vi.fn(),
    signup,
    logout: vi.fn(),
  });
}

describe('SignupForm', () => {
  it('shows a friendly message when rate limited', async () => {
    const err = Object.assign(new Error('too many requests, please try again later'), {
      code: 'rate_limited',
    });
    mockSignup(vi.fn().mockRejectedValueOnce(err));
    render(
      <MemoryRouter>
        <SignupForm />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x.com');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() =>
      expect(screen.getByText(/too many sign-ups from your network/i)).toBeInTheDocument(),
    );
  });
});
```

Add to the `describe('LoginForm', ...)` block in `web/src/components/LoginForm.test.tsx`:

```tsx
  it('shows a friendly message when rate limited', async () => {
    const err = Object.assign(new Error('too many requests, please try again later'), {
      code: 'rate_limited',
    });
    const login = vi.fn().mockRejectedValueOnce(err);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
    });
    render(
      <MemoryRouter>
        <LoginForm />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() =>
      expect(screen.getByText(/too many login attempts/i)).toBeInTheDocument(),
    );
  });
```

Both tests attach `code` to a plain `Error` rather than constructing `ApiError`, matching how `LoginForm.test.tsx:39` already simulates coded failures. The forms read `(err as { code?: string }).code`, so the shape is what matters.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd web && pnpm test -- SignupForm LoginForm
```

Expected: FAIL — the raw server message renders instead of the friendly one.

- [ ] **Step 3: Handle the code in SignupForm**

In `web/src/components/SignupForm.tsx`, add a branch before the final `else` (line 28):

```tsx
      } else if (code === 'rate_limited') {
        setError('Too many sign-ups from your network. Please try again later.');
```

- [ ] **Step 4: Handle the code in LoginForm**

In `web/src/components/LoginForm.tsx`, the `catch` block reads `const code = (err as { code?: string }).code;` (line 23) and branches on `invalid_credentials` (line 24). Add a branch after it:

```tsx
      } else if (code === 'rate_limited') {
        setError('Too many login attempts. Please wait a moment and try again.');
```

- [ ] **Step 5: Run the frontend checks**

```bash
cd web && pnpm test && pnpm lint && pnpm build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/SignupForm.tsx web/src/components/LoginForm.tsx \
        web/src/components/SignupForm.test.tsx web/src/components/LoginForm.test.tsx
git commit -m "feat: show a friendly message when rate limited"
```

---

### Task 8: Document the new env var

**Files:**
- Modify: `.env.example`

- [ ] **Step 1: Add the variable**

Append to `.env.example`, near `CORS_ALLOWED_ORIGINS`:

```bash
# Set to true ONLY when running behind our ALB. When true the client IP is taken
# from the rightmost X-Forwarded-For entry (used for rate limiting). Leave unset
# locally: trusting XFF without a proxy in front lets any caller forge their IP.
TRUST_PROXY=false
```

- [ ] **Step 2: Verify and commit**

```bash
make test
git add .env.example
git commit -m "docs: document TRUST_PROXY"
```

---

## Verification

After Task 8, confirm the whole feature end to end:

```bash
make test
cd web && pnpm test && pnpm build
```

Manual check against a local server (`make run`):

```bash
# 4th signup from one IP within the hour is refused
for i in 1 2 3 4; do
  curl -s -o /dev/null -w "%{http_code} " -X POST localhost:8080/auth/signup \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"rl$i@example.com\",\"password\":\"hunter22\"}"
done
echo
# Expected: 201 201 201 429

# Validation failures cost nothing — all 400, none consume budget
for i in 1 2 3 4 5; do
  curl -s -o /dev/null -w "%{http_code} " -X POST localhost:8080/auth/signup \
    -H 'Content-Type: application/json' -d '{"email":"x@y.com","password":"short"}'
done
echo
# Expected: 400 400 400 400 400
```

Note the second check must be run against a fresh `rate_limit_events` table (`TRUNCATE rate_limit_events;`) if the first check already exhausted the budget for `127.0.0.1`.
