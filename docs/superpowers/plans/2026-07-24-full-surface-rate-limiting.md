# Full-Surface Rate Limiting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rate-limit every public-facing API endpoint — the authenticated surface keyed on user ID, the remaining public routes keyed on IP — and consolidate onto a single in-memory limiter backend.

**Architecture:** `Memory` becomes the only `Limiter` implementation, with `Allow` fixed to peek without consuming and `Record` fixed to debit unconditionally. The middleware gains a `keyFunc` indirection so `RateLimitByUser` can key on the JWT subject while `RateLimit` keys on client IP. Authenticated routes get a group-wide per-user net via `r.Use` plus tighter per-route buckets stacked on the expensive ones.

**Tech Stack:** Go, chi v5, `golang.org/x/time/rate` v0.15.0, sqlc, Postgres, Terraform, CloudWatch EMF.

**Spec:** `docs/superpowers/specs/2026-07-24-full-surface-rate-limiting-design.md`

## Global Constraints

- Run all tests with `make test` (`go test -p 1 ./... -count=1`). `-p 1` is required — tests share one database.
- Tests touching the DB need `make db-up && make migrate-test` first.
- `sqlc generate` regenerates `internal/store/models.go` as well as the `*.sql.go` files. Always commit the regenerated `models.go` alongside — a partial commit breaks the build.
- Migrations already applied in production are never edited or deleted. Schema changes are always a new numbered migration.
- `GET /healthz` must never be rate-limited — it is the ALB health check target (`terraform/prod/alb.tf:22`).
- Every limiter is in-process. This is correct only while `desired_count = 1` in `terraform/prod/ecs_api.tf:100`.
- Limiter `Allow` implementations must fail open (return `Allowed: true` alongside any error) so an infrastructure fault cannot take an endpoint down.
- AWS CLI commands need `AWS_PROFILE=servant`.

---

### Task 1: Fix `Memory`'s Allow/Record split

`Memory.Allow` currently consumes a token and `Memory.Record` is a no-op, which makes `RateLimitOnSuccess` a silent no-op when paired with `Memory`. This task makes `Allow` a non-consuming peek and `Record` the unconditional debit, matching what the `Limiter` interface docstring already promises.

**Files:**
- Modify: `internal/ratelimit/memory.go:58-100`
- Test: `internal/ratelimit/memory_test.go`

**Interfaces:**
- Consumes: nothing — this task is self-contained.
- Produces: `Memory.Allow(ctx, key) (ratelimit.Decision, error)` — non-consuming; `Memory.Record(ctx, key) error` — debits exactly one token, always succeeds, returns nil.

- [ ] **Step 1: Write the failing tests**

Replace the body of `internal/ratelimit/memory_test.go` from `TestMemory_AllowsUpToLimitThenDenies` through `TestMemory_RecordIsNoOp` (lines 37-115) with the following. Keep the `fakeClock` helper (lines 15-35) and every test from `TestMemory_SweepEvictsIdleBuckets` (line 119) onward unchanged.

```go
// Allow is a peek: it reports budget without spending any. Only Record spends.
func TestMemory_AllowDoesNotConsume(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(3, time.Minute, clk.Now)
	ctx := context.Background()

	for i := range 100 {
		d, err := l.Allow(ctx, "1.1.1.1")
		require.NoError(t, err)
		require.True(t, d.Allowed, "Allow must not consume budget (call %d)", i+1)
	}
}

func TestMemory_RecordConsumes(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(3, time.Minute, clk.Now)
	ctx := context.Background()

	for i := range 3 {
		d, err := l.Allow(ctx, "1.1.1.1")
		require.NoError(t, err)
		require.True(t, d.Allowed, "request %d should be allowed", i+1)
		require.NoError(t, l.Record(ctx, "1.1.1.1"))
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
	require.NoError(t, l.Record(ctx, "1.1.1.1"))

	d, err = l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	d, err = l.Allow(ctx, "2.2.2.2")
	require.NoError(t, err)
	require.True(t, d.Allowed, "a different key must have its own bucket")
}

func TestMemory_RefillsAfterWindow(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(2, time.Minute, clk.Now)
	ctx := context.Background()

	for range 2 {
		d, _ := l.Allow(ctx, "1.1.1.1")
		require.True(t, d.Allowed)
		require.NoError(t, l.Record(ctx, "1.1.1.1"))
	}
	d, _ := l.Allow(ctx, "1.1.1.1")
	require.False(t, d.Allowed)

	clk.Advance(time.Minute)

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.True(t, d.Allowed, "bucket must refill after one full window")
}

// A denied request must not push its own recovery further out.
func TestMemory_DenialDoesNotDelayRefill(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(1, time.Minute, clk.Now)
	ctx := context.Background()

	d, _ := l.Allow(ctx, "1.1.1.1")
	require.True(t, d.Allowed)
	require.NoError(t, l.Record(ctx, "1.1.1.1"))

	for range 10 {
		d, _ = l.Allow(ctx, "1.1.1.1")
		require.False(t, d.Allowed)
	}

	clk.Advance(time.Minute)
	d, _ = l.Allow(ctx, "1.1.1.1")
	require.True(t, d.Allowed, "10 denials must not have delayed the refill")
}

// RetryAfter is the time to accrue the missing fraction of one token, not the
// whole window — a client that is barely over should be told to wait barely.
func TestMemory_RetryAfterReflectsDeficit(t *testing.T) {
	clk := newFakeClock()
	// 60 per minute == 1 token per second.
	l := ratelimit.NewMemoryWithClock(60, time.Minute, clk.Now)
	ctx := context.Background()

	for range 60 {
		require.NoError(t, l.Record(ctx, "1.1.1.1"))
	}

	d, err := l.Allow(ctx, "1.1.1.1")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.InDelta(t, float64(time.Second), float64(d.RetryAfter), float64(50*time.Millisecond),
		"one token accrues per second at 60/min")
}

// Record debits even when the bucket is already empty, so a 2xx handler result
// is always counted. Without this, a concurrent request draining the bucket
// between Allow and Record would silently lose the count.
func TestMemory_RecordDebitsWhenEmpty(t *testing.T) {
	clk := newFakeClock()
	l := ratelimit.NewMemoryWithClock(1, time.Minute, clk.Now)
	ctx := context.Background()

	for range 3 {
		require.NoError(t, l.Record(ctx, "1.1.1.1"))
	}

	// Three debits against a 1-token bucket leaves it two tokens in arrears, so
	// one window of refill is not enough to recover.
	clk.Advance(time.Minute)
	d, _ := l.Allow(ctx, "1.1.1.1")
	require.False(t, d.Allowed, "debits past empty must still count")
}
```

Also update `TestMemory_ConcurrentAllowIsRaceFree` (line 149) to exercise both methods — rename it and add the `Record` call:

```go
func TestMemory_ConcurrentAccessIsRaceFree(t *testing.T) {
	l := ratelimit.NewMemory(1000, time.Minute)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 20 {
				key := fmt.Sprintf("10.0.0.%d", i%5)
				if d, _ := l.Allow(ctx, key); d.Allowed {
					_ = l.Record(ctx, key)
				}
			}
		}(i)
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/ratelimit/ -run TestMemory -v
```

Expected: `TestMemory_AllowDoesNotConsume` FAILs (Allow currently consumes, so call 4 is denied). `TestMemory_RecordConsumes` FAILs. `TestMemory_RetryAfterReflectsDeficit` FAILs. `TestMemory_RecordDebitsWhenEmpty` FAILs.

- [ ] **Step 3: Extract bucket lookup**

Both `Allow` and `Record` now need the same get-or-create-and-sweep logic. In `internal/ratelimit/memory.go`, add this method (place it directly above `Allow`):

```go
// bucketLocked returns key's bucket, creating it if absent, and marks it seen.
// Callers must hold m.mu.
func (m *Memory) bucketLocked(key string, now time.Time) *bucket {
	if len(m.buckets) >= hardBucketCap {
		m.sweepLocked(now)
		if len(m.buckets) >= hardBucketCap {
			// Nothing idle enough to reclaim — an attack is already underway.
			// Evicting the least-recently-seen buckets hands those keys a
			// fresh allowance, which is acceptable: it's the least-harmful
			// choice available once the hard cap is hit.
			m.evictOldestLocked(len(m.buckets) / 4)
		}
	} else if len(m.buckets) >= maxBucketsBeforeSweep && now.Sub(m.lastSweep) >= m.window {
		m.sweepLocked(now)
	}

	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(m.limit, m.burst)}
		m.buckets[key] = b
	}
	b.lastSeen = now
	return b
}
```

- [ ] **Step 4: Rewrite `Allow` and `Record`**

Replace `internal/ratelimit/memory.go:58-100` (the whole of `Allow` plus the `Record` no-op) with:

```go
// Allow reports whether key has at least one token, WITHOUT spending it. Call
// Record to spend. It never returns an error.
//
// Separating the check from the spend is what lets RateLimitOnSuccess gate every
// request but count only the ones whose handler succeeded.
func (m *Memory) Allow(_ context.Context, key string) (Decision, error) {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.bucketLocked(key, now)

	tokens := b.lim.TokensAt(now)
	if tokens >= 1 {
		return Decision{Allowed: true}, nil
	}

	// Budget frees up as the missing fraction of a token accrues, which is a
	// more useful Retry-After than the whole window.
	perSecond := float64(m.limit)
	if perSecond <= 0 {
		return Decision{Allowed: false, RetryAfter: m.window}, nil
	}
	retryAfter := time.Duration((1 - tokens) / perSecond * float64(time.Second))
	if retryAfter > m.window {
		retryAfter = m.window
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	return Decision{Allowed: false, RetryAfter: retryAfter}, nil
}

// Record spends one token for key. The debit is unconditional — it applies even
// when the bucket is already empty, driving the balance negative.
//
// ReserveN, not AllowN: AllowN would decline to debit an empty bucket, so a
// request whose handler already succeeded would go uncounted whenever a
// concurrent request drained the bucket between Allow and Record. ReserveN
// delegates to reserveN(t, n, InfDuration), which always succeeds for n=1 while
// burst >= 1 and debits regardless of the current balance. The reservation is
// deliberately never cancelled — cancelling is what would undo the debit.
func (m *Memory) Record(_ context.Context, key string) error {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.bucketLocked(key, now)
	b.lim.ReserveN(now, 1)
	return nil
}
```

`bucketLocked` returns a `*bucket`, so the reservation is taken on `b.lim`, the
`*rate.Limiter` it wraps — not on the bucket itself.

Update the `Memory` type doc comment (line 25-29) — the sentence "Record is a no-op — Memory counts every Allow." is now false. Replace those two lines with:

```go
// Memory is an in-process token-bucket Limiter. State is per-process: it resets
// on restart and is not shared across tasks, which is acceptable while the API
// runs a single ECS task (terraform/prod/ecs_api.tf desired_count).
//
// Allow peeks and Record spends. Every caller must pair them — an Allow with no
// matching Record consumes nothing.
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/ratelimit/ -v
```

Expected: PASS, including the unchanged `TestMemory_SweepEvictsIdleBuckets` and `TestMemory_SweepKeepsActiveBuckets`. If either sweep test fails, `bucketLocked` is not updating `lastSeen` — recheck Step 3.

- [ ] **Step 6: Run the race detector**

```bash
go test ./internal/ratelimit/ -race -run TestMemory_ConcurrentAccessIsRaceFree -v
```

Expected: PASS, no race warnings.

- [ ] **Step 7: Commit**

```bash
git add internal/ratelimit/memory.go internal/ratelimit/memory_test.go
git commit -m "fix: make Memory.Allow a peek and Memory.Record the debit

RateLimitOnSuccess paired with Memory was a silent no-op: Allow consumed
the token and Record did nothing. Split them so the Limiter interface
contract holds on both backends."
```

---

### Task 2: Add `RateLimitByUser` and the key-function indirection

`checkAllowed` derives its own key by calling `ClientIP(r)`. This task lifts key selection into a `keyFunc` so a user-keyed middleware can exist, and makes `RateLimit` spend the token it checks.

**Files:**
- Modify: `internal/http/middleware/ratelimit.go:26-87`
- Modify: `internal/http/middleware/auth.go` (add `ContextWithUserID`)
- Test: `internal/http/middleware/ratelimit_test.go`

**Interfaces:**
- Consumes: `Memory.Allow` / `Memory.Record` from Task 1; `middleware.UserIDFromContext(ctx) (uuid.UUID, bool)` (existing, `auth.go`).
- Produces:
  - `middleware.RateLimitByUser(l ratelimit.Limiter, name string) func(http.Handler) http.Handler`
  - `middleware.ContextWithUserID(ctx context.Context, uid uuid.UUID) context.Context`
  - `middleware.RateLimit` and `middleware.RateLimitOnSuccess` keep their existing signatures.

- [ ] **Step 1: Write the failing tests**

Append to `internal/http/middleware/ratelimit_test.go`. These need two new imports — add `"github.com/google/uuid"` and `"github.com/wmyers/heres-whats-happening/internal/http/middleware"` is already imported.

```go
// keyRecordingLimiter captures the key each call was made with, so tests can
// assert on the key strategy rather than only on the status code.
type keyRecordingLimiter struct {
	allowedKeys []string
	recordedKeys []string
}

func (k *keyRecordingLimiter) Allow(_ context.Context, key string) (ratelimit.Decision, error) {
	k.allowedKeys = append(k.allowedKeys, key)
	return ratelimit.Decision{Allowed: true}, nil
}

func (k *keyRecordingLimiter) Record(_ context.Context, key string) error {
	k.recordedKeys = append(k.recordedKeys, key)
	return nil
}

func TestRateLimitByUser_KeysOnUserID(t *testing.T) {
	uid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	l := &keyRecordingLimiter{}
	h := middleware.RateLimitByUser(l, middleware.EndpointAuthed)(okHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), uid))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"u:" + uid.String()}, l.allowedKeys)
	require.Equal(t, []string{"u:" + uid.String()}, l.recordedKeys,
		"an allowed request must spend its token")
}

func TestRateLimitByUser_TwoUsersHaveIndependentBudgets(t *testing.T) {
	alice := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	bob := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	l := ratelimit.NewMemory(1, time.Minute)
	h := middleware.RateLimitByUser(l, middleware.EndpointAuthed)(okHandler(http.StatusOK))

	do := func(uid uuid.UUID) int {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req = req.WithContext(middleware.ContextWithUserID(req.Context(), uid))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusOK, do(alice))
	require.Equal(t, http.StatusTooManyRequests, do(alice), "alice is out of budget")
	require.Equal(t, http.StatusOK, do(bob), "bob has his own budget")
}

// RequireAuth 401s before this middleware runs, so a missing user ID means a
// middleware-ordering bug. Falling back to the IP degrades to the old behavior
// instead of collapsing every such request into one shared empty-string bucket.
func TestRateLimitByUser_FallsBackToIPWhenNoUser(t *testing.T) {
	l := &keyRecordingLimiter{}
	h := middleware.RateLimitByUser(l, middleware.EndpointAuthed)(okHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.RemoteAddr = "203.0.113.9:5555"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"ip:203.0.113.9"}, l.allowedKeys)
}

func TestRateLimit_KeysOnIPAndSpendsToken(t *testing.T) {
	l := &keyRecordingLimiter{}
	h := middleware.RateLimit(l, middleware.EndpointLogin)(okHandler(http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "198.51.100.4:1234"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, []string{"ip:198.51.100.4"}, l.allowedKeys)
	require.Equal(t, []string{"ip:198.51.100.4"}, l.recordedKeys)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/http/middleware/ -run 'TestRateLimitByUser|TestRateLimit_KeysOnIP' -v
```

Expected: compile failure — `undefined: middleware.RateLimitByUser`, `undefined: middleware.ContextWithUserID`, `undefined: middleware.EndpointAuthed`.

- [ ] **Step 3: Add `ContextWithUserID`**

In `internal/http/middleware/auth.go`, add the exported setter and make `RequireAuth` use it. Replace the line `ctx := context.WithValue(r.Context(), userIDKey, uid)` inside `RequireAuth` with `ctx := ContextWithUserID(r.Context(), uid)`, then add below `UserIDFromContext`:

```go
// ContextWithUserID returns ctx carrying uid, the inverse of UserIDFromContext.
// RequireAuth uses it on every authenticated request; tests use it to build a
// request that looks authenticated without minting a JWT.
func ContextWithUserID(ctx context.Context, uid uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, uid)
}
```

- [ ] **Step 4: Add the placeholder constant**

`EndpointAuthed` is wired for real in Task 6, but Task 2's tests reference it. Add it now to `internal/http/middleware/ratelimit.go`, inside the existing `const` block at lines 20-24:

```go
	EndpointAuthed  = "authed"
```

- [ ] **Step 5: Add the key functions and rewrite the middlewares**

Replace `internal/http/middleware/ratelimit.go:26-87` (from `// RateLimit rejects...` through the end of `checkAllowed`) with:

```go
// keyFunc selects the rate-limit bucket key for a request.
type keyFunc func(*http.Request) string

// ipKey buckets by client IP — the right choice for routes reachable without
// authentication.
func ipKey(r *http.Request) string { return "ip:" + ClientIP(r) }

// userKey buckets by authenticated user.
//
// Keying authenticated traffic by IP fails in both directions: CGNAT and
// corporate proxies put many real users behind one address, while an attacker
// holding one account and a proxy pool defeats IP keying entirely.
//
// RequireAuth 401s before this runs, so an absent user ID means a
// middleware-ordering bug. Falling back to the client IP degrades to the
// pre-existing behavior; returning an empty key would collapse every affected
// request into a single shared bucket.
func userKey(r *http.Request) string {
	if uid, ok := UserIDFromContext(r.Context()); ok {
		return "u:" + uid.String()
	}
	log.Printf("ratelimit: no user id in context, falling back to client IP")
	return ipKey(r)
}

// RateLimit rejects requests over the limit before they reach the handler,
// keyed on client IP. Every request counts. name appears in log lines and as
// the metric's endpoint dimension.
func RateLimit(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return limitEvery(l, name, ipKey)
}

// RateLimitByUser is RateLimit keyed on the authenticated user instead of the
// client IP. Install it inside a RequireAuth group.
func RateLimitByUser(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return limitEvery(l, name, userKey)
}

// limitEvery gates on the limit and spends a token for every request that gets
// through.
func limitEvery(l ratelimit.Limiter, name string, key keyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if !checkAllowed(w, r, l, name, k) {
				return
			}
			if err := l.Record(r.Context(), k); err != nil {
				log.Printf("ratelimit %s: record failed: %v", name, err)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitOnSuccess rejects requests over the limit, then counts the request
// only if the handler returned 2xx. Keyed on client IP.
//
// Signup uses this: a rejected signup (bad email, weak password, duplicate
// address) must cost the caller nothing, or one typo would lock a real user out
// for the whole window without an account to show for it.
//
// This requires a Limiter whose Allow does not itself consume — see the Memory
// doc comment. Pairing it with a limiter that spends on Allow silently degrades
// it to RateLimit.
func RateLimitOnSuccess(l ratelimit.Limiter, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := ipKey(r)
			if !checkAllowed(w, r, l, name, k) {
				return
			}

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				// The handler wrote nothing at all — no WriteHeader, no Write — so
				// the wrapper never observed a status. net/http still sends an
				// implicit 200 once the response completes, so treat it as one.
				status = http.StatusOK
			}
			if status < 200 || status > 299 {
				return
			}
			if err := l.Record(r.Context(), k); err != nil {
				log.Printf("ratelimit %s: record failed: %v", name, err)
			}
		})
	}
}

// checkAllowed reports whether the request may proceed, writing 429 if not.
func checkAllowed(w http.ResponseWriter, r *http.Request, l ratelimit.Limiter, name, key string) bool {
	d, err := l.Allow(r.Context(), key)
	if err != nil {
		// Allow already failed open; log and honor its decision.
		log.Printf("ratelimit %s: check failed: %v", name, err)
	}
	if d.Allowed {
		return true
	}
	// The IP rides along as a searchable property even for user-keyed buckets —
	// it is what an incident responder pivots on. It is never a dimension.
	observability.Default.RateLimitRejection(name, ClientIP(r))
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
	httperr.Write(w, http.StatusTooManyRequests, "rate_limited",
		"too many requests, please try again later")
	return false
}
```

- [ ] **Step 6: Run the middleware tests**

```bash
go test ./internal/http/middleware/ -v
```

Expected: PASS, including the pre-existing `TestRateLimit_*` and `TestRateLimitOnSuccess_*` tests.

- [ ] **Step 7: Confirm nothing else broke**

```bash
go build ./... && go test ./internal/... -count=1
```

Expected: build succeeds; all packages PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/http/middleware/ratelimit.go internal/http/middleware/auth.go internal/http/middleware/ratelimit_test.go
git commit -m "feat: add RateLimitByUser keyed on the authenticated user

Lifts bucket-key selection into a keyFunc so authenticated routes can key
on the JWT subject instead of the client IP, which produces false
positives behind CGNAT and false negatives against a proxy pool."
```

---

### Task 3: Move signup onto `Memory`

**Files:**
- Modify: `internal/http/server.go:64-69`
- Test: `internal/http/middleware/ratelimit_test.go`

**Interfaces:**
- Consumes: `ratelimit.NewMemory(limit int, window time.Duration) *Memory`; `middleware.RateLimitOnSuccess`.
- Produces: nothing new. `Server.Router()` no longer constructs a Postgres limiter, so `Server.Queries` is not required for rate limiting.

- [ ] **Step 1: Write the failing regression test**

This is the guard for the behavior the whole `Memory` fix exists to protect. Append to `internal/http/middleware/ratelimit_test.go`:

```go
// The signup limit is 3/hour. A user who fat-fingers their email or picks a
// weak password must not burn that budget — otherwise three typos lock them out
// for an hour with no account to show for it. This is an integration test
// against the real Memory limiter on purpose: it is exactly the pairing that
// used to be a silent no-op.
func TestRateLimitOnSuccess_WithMemory_FailuresConsumeNoBudget(t *testing.T) {
	l := ratelimit.NewMemory(3, time.Hour)

	status := http.StatusBadRequest
	h := middleware.RateLimitOnSuccess(l, middleware.EndpointSignup)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
		req.RemoteAddr = "203.0.113.7:4444"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := range 3 {
		require.Equal(t, http.StatusBadRequest, do(), "rejected signup %d", i+1)
	}

	status = http.StatusCreated
	require.Equal(t, http.StatusCreated, do(),
		"three failed signups must leave the hourly budget untouched")
}

func TestRateLimitOnSuccess_WithMemory_SuccessesConsumeBudget(t *testing.T) {
	l := ratelimit.NewMemory(3, time.Hour)
	h := middleware.RateLimitOnSuccess(l, middleware.EndpointSignup)(
		okHandler(http.StatusCreated))

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", nil)
		req.RemoteAddr = "203.0.113.8:4444"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := range 3 {
		require.Equal(t, http.StatusCreated, do(), "signup %d", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, do(), "the 4th signup in an hour is limited")
}
```

- [ ] **Step 2: Run them**

```bash
go test ./internal/http/middleware/ -run TestRateLimitOnSuccess_WithMemory -v
```

Expected: PASS. Tasks 1 and 2 already made this work — these tests lock the behavior in before Task 4 removes the Postgres backend that currently provides it. If either FAILs, stop: Task 1 is incomplete.

- [ ] **Step 3: Switch the signup limiter to Memory**

In `internal/http/server.go`, replace lines 64-69 (the limiter block and its comment) with:

```go
	// Rate limiters for the public auth surface. All in-process: state resets on
	// restart, which is acceptable while the API runs a single task.
	signupLimiter := ratelimit.NewMemory(3, time.Hour)
	loginLimiter := ratelimit.NewMemory(10, time.Minute)
	refreshLimiter := ratelimit.NewMemory(30, time.Minute)
```

- [ ] **Step 4: Verify**

```bash
go build ./... && go test ./internal/http/... -count=1
```

Expected: build succeeds; tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/http/server.go internal/http/middleware/ratelimit_test.go
git commit -m "refactor: move the signup limiter to Memory

Adds the regression test for signup's typo tolerance first, so the
behavior is pinned before the Postgres backend is removed."
```

---

### Task 4: Remove the Postgres limiter, its table, and its cleanup job

**Files:**
- Delete: `internal/ratelimit/postgres.go`, `internal/ratelimit/postgres_test.go`
- Delete: `sql/queries/rate_limit.sql`
- Delete: `internal/http/cleanup_internal_test.go`
- Create: `sql/migrations/0019_drop_rate_limit_events.up.sql`, `sql/migrations/0019_drop_rate_limit_events.down.sql`
- Modify: `internal/http/server.go` (remove the cleanup goroutine and its call site)
- Modify: `internal/testdb/testdb.go:161-162`
- Regenerate: `internal/store/rate_limit.sql.go` (deleted), `internal/store/models.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `internal/ratelimit` exports only `Limiter`, `Decision`, `Memory`, `NewMemory`, `NewMemoryWithClock`.

- [ ] **Step 1: Write the migration**

Create `sql/migrations/0019_drop_rate_limit_events.up.sql`:

```sql
-- The rate limiter is now entirely in-process (internal/ratelimit/memory.go),
-- so nothing reads or writes this table.
DROP TABLE IF EXISTS rate_limit_events;
```

Create `sql/migrations/0019_drop_rate_limit_events.down.sql`:

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

Migration `0018` is already applied in production and is left untouched.

- [ ] **Step 2: Delete the Go and SQL sources**

```bash
rm internal/ratelimit/postgres.go \
   internal/ratelimit/postgres_test.go \
   sql/queries/rate_limit.sql \
   internal/http/cleanup_internal_test.go
```

`cleanup_internal_test.go` contains only `TestDeleteExpiredRateLimitEvents`, which tests the function being removed in Step 4, so the whole file goes.

- [ ] **Step 3: Regenerate sqlc**

```bash
sqlc generate
git status --short
```

Expected: `internal/store/rate_limit.sql.go` deleted, `internal/store/models.go` modified (the `RateLimitEvent` struct removed). Both must be committed together — committing one without the other breaks the build.

- [ ] **Step 4: Remove the cleanup goroutine**

In `internal/http/server.go`, delete these three declarations entirely — `rateLimitRetention`, `deleteExpiredRateLimitEvents`, and `runRateLimitCleanup` (the block from `// rateLimitRetention is how long...` to the end of the file).

Then delete the call site inside `Run`:

```go
	if s.Queries != nil {
		go s.runRateLimitCleanup(ctx)
	}
```

Then remove the now-unused import `"github.com/jackc/pgx/v5/pgtype"` from the import block.

Leave `errCh := make(chan error, 3)` as it is — the cleanup goroutine never wrote to it, so the buffer size is unrelated.

- [ ] **Step 5: Drop the table from the truncate list**

In `internal/testdb/testdb.go`, remove the `"rate_limit_events",` entry (line 162) from `truncateTables`. Also update the comment above it (lines 155-158), which cites the table as a past omission — it now reads as though the table still exists:

```go
// truncateTables lists every table that accumulates rows written by app or
// test code and so must be emptied between tests. It is a manually maintained
// list, not derived from the schema — TestTruncateAllCoversEveryTable in
// truncate_test.go checks it against the live database on every run so an
// omission (this has happened twice) fails loudly instead of leaking rows
// across tests.
```

- [ ] **Step 6: Apply the migration to the test database**

```bash
make db-up && make migrate-test
```

Expected: migration `0019` applies without error.

- [ ] **Step 7: Verify the build and full suite**

```bash
go build ./... && make test
```

Expected: build succeeds. All tests PASS, including `TestTruncateAllCoversEveryTable`, which now validates the shortened list against the live schema — if it fails, either the migration did not apply or the truncate list still names the dropped table.

- [ ] **Step 8: Commit**

```bash
git add -A internal/ratelimit internal/store internal/http internal/testdb sql
git commit -m "refactor: remove the Postgres rate-limit backend

All limits are in-process now, so the rate_limit_events table, its
queries and generated code, and the hourly cleanup goroutine have no
remaining consumer. Migration 0018 stays applied; 0019 drops the table."
```

---

### Task 5: Rate-limit the remaining public routes

Adds IP-keyed limits to `/auth/logout`, `/ical/{token}`, and `/readyz`. `/healthz` stays exempt.

**Files:**
- Modify: `internal/http/middleware/ratelimit.go` (constants)
- Modify: `internal/http/server.go:71-84`
- Test: `internal/http/middleware/ratelimit_test.go:297-299` (contract test), `internal/http/server_test.go`

**Interfaces:**
- Consumes: `middleware.RateLimit`, `ratelimit.NewMemory`.
- Produces: constants `middleware.EndpointLogout = "logout"`, `middleware.EndpointIcalFeed = "ical_feed"`, `middleware.EndpointReadyz = "readyz"`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/http/server_test.go`. These follow the existing `TestServer_RefreshIsRateLimited` pattern — a `Server` with no DB, hitting routes that answer without a database round trip.

```go
func TestServer_IcalFeedIsRateLimited(t *testing.T) {
	pool := testdb.MustOpen(t)
	s := &hs.Server{
		DB:         pool,
		Queries:    store.New(pool),
		JWTSigner:  auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL: time.Hour,
	}
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	// The ical feed limit is 60/min. An unknown token 404s after one indexed
	// lookup — which is exactly the cheap-to-send, not-free-to-serve request
	// the limit exists to cap.
	for i := range 60 {
		resp, err := http.Get(srv.URL + "/ical/not-a-real-token.ics")
		require.NoError(t, err)
		resp.Body.Close()
		require.NotEqual(t, http.StatusTooManyRequests, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Get(srv.URL + "/ical/not-a-real-token.ics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

func TestServer_ReadyzIsRateLimited(t *testing.T) {
	pool := testdb.MustOpen(t)
	s := &hs.Server{
		DB:         pool,
		Queries:    store.New(pool),
		JWTSigner:  auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL: time.Hour,
	}
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	for i := range 30 {
		resp, err := http.Get(srv.URL + "/readyz")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestServer_LogoutIsRateLimited(t *testing.T) {
	pool := testdb.MustOpen(t)
	s := &hs.Server{
		DB:         pool,
		Queries:    store.New(pool),
		JWTSigner:  auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL: time.Hour,
	}
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	for i := range 30 {
		resp, err := http.Post(srv.URL+"/auth/logout", "application/json", nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.NotEqual(t, http.StatusTooManyRequests, resp.StatusCode, "request %d", i+1)
	}

	resp, err := http.Post(srv.URL+"/auth/logout", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

// /healthz is the ALB health check target. Rate limiting it would let a
// request flood fail the health check and cycle otherwise-healthy tasks.
func TestServer_HealthzIsNeverRateLimited(t *testing.T) {
	pool := testdb.MustOpen(t)
	s := &hs.Server{
		DB:         pool,
		Queries:    store.New(pool),
		JWTSigner:  auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL: time.Hour,
	}
	srv := httptest.NewServer(s.Router())
	defer srv.Close()

	// Well past every other limit in the router.
	for i := range 200 {
		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i+1)
	}
}
```

Extend the constant contract test at `internal/http/middleware/ratelimit_test.go:297-299`:

```go
	require.Equal(t, "signup", middleware.EndpointSignup)
	require.Equal(t, "login", middleware.EndpointLogin)
	require.Equal(t, "refresh", middleware.EndpointRefresh)
	require.Equal(t, "logout", middleware.EndpointLogout)
	require.Equal(t, "ical_feed", middleware.EndpointIcalFeed)
	require.Equal(t, "readyz", middleware.EndpointReadyz)
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go test ./internal/http/... -run 'TestServer_IcalFeed|TestServer_Readyz|TestServer_Logout|TestServer_Healthz|TestEndpointConstants' -v
```

Expected: compile failure on the undefined constants; once those exist, the three limiting tests FAIL because no limit is wired.

- [ ] **Step 3: Add the constants**

In `internal/http/middleware/ratelimit.go`, extend the `const` block:

```go
	// Public (IP-keyed)
	EndpointLogout   = "logout"
	EndpointIcalFeed = "ical_feed"
	EndpointReadyz   = "readyz"
```

- [ ] **Step 4: Wire the routes**

In `internal/http/server.go`, add three limiters beside the existing ones:

```go
	logoutLimiter := ratelimit.NewMemory(30, time.Minute)
	icalFeedLimiter := ratelimit.NewMemory(60, time.Minute)
	readyzLimiter := ratelimit.NewMemory(30, time.Minute)
```

Then replace the public route block (lines 71-84) with:

```go
	// Public
	//
	// /healthz is deliberately NOT rate limited: it is the ALB health check
	// target (terraform/prod/alb.tf), so limiting it would let a flood fail the
	// health check and cycle healthy tasks. It does no work beyond writing a
	// static body, so there is nothing to protect.
	r.Get("/healthz", handlers.Healthz())
	r.With(middleware.RateLimit(readyzLimiter, middleware.EndpointReadyz)).
		Get("/readyz", handlers.Readyz(s.DB))
	// Public iCal feed — token in URL is the credential. The 32-byte token is
	// not guessable; the limit caps DB lookups and calendar renders from any one
	// source. 60/min is ~1 req/sec, far above real calendar-client polling.
	r.With(middleware.RateLimit(icalFeedLimiter, middleware.EndpointIcalFeed)).
		Get("/ical/{token}", handlers.GetIcalFeed(s.Queries))

	// Auth (public)
	r.With(middleware.RateLimitOnSuccess(signupLimiter, middleware.EndpointSignup)).
		Post("/auth/signup", handlers.Signup(s.Queries, s.JWTSigner, s.RefreshTTL, s.DefaultCityID))
	r.With(middleware.RateLimit(loginLimiter, middleware.EndpointLogin)).
		Post("/auth/login", handlers.Login(s.Queries, s.JWTSigner, s.RefreshTTL))
	r.With(middleware.RateLimit(refreshLimiter, middleware.EndpointRefresh)).
		Post("/auth/refresh", handlers.Refresh(s.Queries, s.JWTSigner))
	r.With(middleware.RateLimit(logoutLimiter, middleware.EndpointLogout)).
		Post("/auth/logout", handlers.Logout(s.Queries))
```

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/http/... -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/http/middleware/ratelimit.go internal/http/middleware/ratelimit_test.go internal/http/server.go internal/http/server_test.go
git commit -m "feat: rate limit logout, the ical feed, and readyz

Reverses the prior spec's exclusion of /ical/{token}: that rationale
conflated a per-subscription limit with a per-IP one. /healthz stays
exempt as the ALB health check target, now with a test pinning it."
```

---

### Task 6: Rate-limit the authenticated surface

Adds the group-wide per-user net plus the four stacked buckets.

**Files:**
- Modify: `internal/http/middleware/ratelimit.go` (constants)
- Modify: `internal/http/server.go:86-108`
- Test: `internal/http/middleware/ratelimit_test.go` (contract test), `internal/http/server_test.go`

**Interfaces:**
- Consumes: `middleware.RateLimitByUser` from Task 2.
- Produces: constants `EndpointAuthedWrite = "authed_write"`, `EndpointManualInterests = "manual_interests"`, `EndpointSpotifyExchange = "spotify_exchange"`, `EndpointIcalToken = "ical_token"`. (`EndpointAuthed` already exists from Task 2.)

- [ ] **Step 1: Write the failing tests**

Append to `internal/http/server_test.go`. These need a real user and access token, so they build a full server and sign up first.

```go
// authedTestServer returns a running server plus an access token for a fresh user.
func authedTestServer(t *testing.T, email string) (*httptest.Server, string) {
	t.Helper()
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	s := &hs.Server{
		DB:            pool,
		Queries:       q,
		JWTSigner:     auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute),
		RefreshTTL:    time.Hour,
		DefaultCityID: uuid.UUID(city.ID.Bytes).String(),
	}
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var su struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&su))
	resp.Body.Close()
	require.NotEmpty(t, su.AccessToken)

	return srv, su.AccessToken
}

func doAuthed(t *testing.T, srv *httptest.Server, method, path, token string) int {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// authed_write is 30/min and stacks on the 120/min authed net, so it trips first.
func TestServer_AuthedWriteIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "write-limit@example.com")

	for i := range 30 {
		code := doAuthed(t, srv, http.MethodDelete, "/me/not-interested", token)
		require.NotEqual(t, http.StatusTooManyRequests, code, "request %d", i+1)
	}

	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", token))
}

// The group-wide net covers routes with no bucket of their own, like GET /me.
func TestServer_AuthedNetIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "net-limit@example.com")

	for i := range 120 {
		code := doAuthed(t, srv, http.MethodGet, "/me", token)
		require.NotEqual(t, http.StatusTooManyRequests, code, "request %d", i+1)
	}

	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodGet, "/me", token))
}

// Two users must not share a budget — this is the whole point of user keying.
func TestServer_AuthedLimitIsPerUser(t *testing.T) {
	srv, alice := authedTestServer(t, "alice-limit@example.com")

	for range 30 {
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", alice)
	}
	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", alice))

	// A second user on the same server and the same source IP.
	body, _ := json.Marshal(map[string]string{"email": "bob-limit@example.com", "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var bob struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&bob))
	resp.Body.Close()

	require.NotEqual(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodDelete, "/me/not-interested", bob.AccessToken),
		"bob must have his own budget despite sharing alice's IP")
}

// ical_token is 10/hour and stacks on the net, so it trips well before it.
func TestServer_IcalTokenMintingIsRateLimited(t *testing.T) {
	srv, token := authedTestServer(t, "ical-token-limit@example.com")

	for i := range 10 {
		code := doAuthed(t, srv, http.MethodPost, "/me/ical-token", token)
		require.NotEqual(t, http.StatusTooManyRequests, code, "request %d", i+1)
	}

	require.Equal(t, http.StatusTooManyRequests,
		doAuthed(t, srv, http.MethodPost, "/me/ical-token", token))
}
```

Extend `TestEndpointConstants` with the remaining constants, and fix its doc
comment — it currently tells the reader that every constant has a matching alarm,
which stops being true in this task:

```go
// These endpoint values are the metric `endpoint` dimension emitted on a 429.
// A subset is also keyed on by the CloudWatch alarms in
// terraform/prod/observability.tf — if you change one of those, update that
// file's ratelimit_alarms map or the matching alarm goes blind. The rest are
// emitted only and have no alarm to keep in sync.
func TestEndpointConstants(t *testing.T) {
```

```go
	require.Equal(t, "authed", middleware.EndpointAuthed)
	require.Equal(t, "authed_write", middleware.EndpointAuthedWrite)
	require.Equal(t, "manual_interests", middleware.EndpointManualInterests)
	require.Equal(t, "spotify_exchange", middleware.EndpointSpotifyExchange)
	require.Equal(t, "ical_token", middleware.EndpointIcalToken)
```

- [ ] **Step 2: Run them to verify they fail**

```bash
go test ./internal/http/... -run 'TestServer_Authed|TestServer_IcalToken|TestEndpointConstants' -v
```

Expected: compile failure on undefined constants; then the limiting tests FAIL — no 429 ever arrives.

- [ ] **Step 3: Add the constants**

In `internal/http/middleware/ratelimit.go`, extend the `const` block (`EndpointAuthed` is already present from Task 2):

```go
	// Authenticated (user-keyed)
	EndpointAuthedWrite     = "authed_write"
	EndpointManualInterests = "manual_interests"
	EndpointSpotifyExchange = "spotify_exchange"
	EndpointIcalToken       = "ical_token"
```

Then rewrite the block's header comment (lines 17-19), which currently claims every value is mirrored as an alarm key:

```go
// Rate-limit endpoint identifiers. Each is emitted as the `endpoint` metric
// dimension VALUE on a 429. A subset — signup, login, refresh, authed,
// manual_interests, spotify_exchange, ical_feed — is also mirrored as alarm map
// keys in terraform/prod/observability.tf; keep those in sync. The rest are
// emitted only, and are queryable in CloudWatch Logs Insights without an alarm.
```

- [ ] **Step 4: Wire the authenticated group**

In `internal/http/server.go`, add the limiters beside the others:

```go
	// Authenticated, keyed on user ID. The net covers every route in the group,
	// including ones added later; the rest stack on top of it.
	authedLimiter := ratelimit.NewMemory(120, time.Minute)
	authedWriteLimiter := ratelimit.NewMemory(30, time.Minute)
	manualInterestsLimiter := ratelimit.NewMemory(60, time.Hour)
	spotifyExchangeLimiter := ratelimit.NewMemory(10, time.Hour)
	icalTokenLimiter := ratelimit.NewMemory(10, time.Hour)
```

Then replace the authenticated group (lines 86-108) with:

```go
	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(s.JWTSigner))
		// Safety net across the whole group. Installed with Use, not With, so a
		// route added later is covered by default rather than silently unlimited.
		r.Use(middleware.RateLimitByUser(authedLimiter, middleware.EndpointAuthed))

		// Reads — covered by the net alone.
		r.Get("/me", handlers.GetMe(s.Queries))
		r.Get("/me/manual-interests", handlers.ListManualInterests(s.Queries))
		r.Get("/me/spotify-interests", handlers.SpotifyInterests(s.Queries))
		r.Get("/integrations/spotify/connect", handlers.SpotifyConnect(s.SpotifyClient, s.OAuthHMACKey))
		r.Get("/integrations/spotify/status", handlers.SpotifyStatus(s.Queries))
		r.Get("/me/calendar", handlers.GetMyCalendar(s.Queries))
		r.Get("/events/{id}", handlers.GetEventByIDForUser(s.Queries))

		// Writes.
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Delete("/me", handlers.DeleteMe(s.Queries))
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Patch("/me/match-threshold", handlers.UpdateMatchThreshold(s.Queries))
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Post("/me/not-interested", handlers.AddNotInterested(s.Queries))
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Delete("/me/not-interested", handlers.ResetNotInterested(s.Queries))
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Delete("/integrations/spotify", handlers.SpotifyDisconnect(s.Queries))
		r.With(middleware.RateLimitByUser(authedWriteLimiter, middleware.EndpointAuthedWrite)).
			Delete("/me/ical-token", handlers.DeleteIcalToken(s.Queries))

		// Both publish to the interests queue, so both cost downstream compute.
		// One shared budget, so exhausting adds never blocks deletes.
		r.With(middleware.RateLimitByUser(manualInterestsLimiter, middleware.EndpointManualInterests)).
			Post("/me/manual-interests", handlers.CreateManualInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))
		r.With(middleware.RateLimitByUser(manualInterestsLimiter, middleware.EndpointManualInterests)).
			Delete("/me/manual-interests/{id}", handlers.DeleteManualInterest(s.Queries, s.QueuePublisher, s.InterestsQueueURL))

		// Spends Spotify API quota that is shared across all users, so one
		// abusive account can break the integration for everyone.
		r.With(middleware.RateLimitByUser(spotifyExchangeLimiter, middleware.EndpointSpotifyExchange)).
			Post("/integrations/spotify/exchange", handlers.SpotifyExchange(
				s.Queries, s.SpotifyClient, s.SpotifyCipher, s.OAuthHMACKey,
				s.QueuePublisher, s.InterestsQueueURL))

		// Mints a fresh token on every call.
		r.With(middleware.RateLimitByUser(icalTokenLimiter, middleware.EndpointIcalToken)).
			Post("/me/ical-token", handlers.CreateIcalToken(s.Queries, s.IcalBaseURL))
	})
```

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/http/... -count=1 -v
```

Expected: PASS. `TestServer_AuthedNetIsRateLimited` issues 121 requests and is the slowest test here.

- [ ] **Step 6: Run the full suite**

```bash
make test
```

Expected: all packages PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/http/middleware/ratelimit.go internal/http/middleware/ratelimit_test.go internal/http/server.go internal/http/server_test.go
git commit -m "feat: rate limit the authenticated API surface per user

A 120/min net covers every route in the RequireAuth group so new routes
are limited by default, with tighter buckets stacked on the routes that
publish to SQS or spend shared Spotify quota."
```

---

### Task 7: Add CloudWatch alarms for the new buckets

**Files:**
- Modify: `terraform/prod/observability.tf:1-30`

**Interfaces:**
- Consumes: the `endpoint` dimension values defined in Tasks 5 and 6.
- Produces: four new `aws_cloudwatch_metric_alarm.ratelimit` instances.

- [ ] **Step 1: Update the header comment**

Replace lines 1-8 of `terraform/prod/observability.tf`:

```hcl
# Rate-limit rejection alerting.
#
# The app emits a CloudWatch EMF metric on each 429 (see internal/observability):
# namespace "HeresWhatsHappening/api", metric "RateLimitRejections", dimension
# "endpoint". The app defines eleven endpoint values in
# internal/http/middleware/ratelimit.go; the subset alarmed below MUST match
# those constants exactly — TestMetricContractConstants guards the app side.
# Values not listed here are still emitted and queryable in Logs Insights, they
# just do not page. The metric is sparse (a data point only when a rejection
# occurs), so the alarms treat missing data as not breaching.
```

- [ ] **Step 2: Add the alarm entries**

Extend the `ratelimit_alarms` local (lines 24-29) to:

```hcl
  ratelimit_alarms = {
    signup  = { threshold = 1, description = "Signup rate-limit rejections — a real user hitting 3/hour is rare; likely a bot or a bug." }
    login   = { threshold = 20, description = "Sustained login rate-limit rejections — possible credential stuffing." }
    refresh = { threshold = 50, description = "Sustained refresh rate-limit rejections." }

    authed           = { threshold = 50, description = "Sustained authenticated rate-limit rejections — an account is being scripted or a client is looping." }
    manual_interests = { threshold = 10, description = "Manual-interest rejections — each allowed call publishes to the interests queue, so this caps runaway downstream compute." }
    spotify_exchange = { threshold = 5, description = "Spotify OAuth exchange rejections — this quota is shared across all users, so abuse here breaks the integration for everyone." }
    ical_feed        = { threshold = 100, description = "Sustained iCal feed rejections — an unauthenticated route being flooded from one source." }
  }
```

Thresholds are the Sum of rejections over one 5-minute period. `authed_write`, `ical_token`, `logout`, and `readyz` are deliberately not alarmed — an individual rejection there is unremarkable and the only useful response would be to read the logs.

- [ ] **Step 3: Validate**

```bash
cd terraform/prod && AWS_PROFILE=servant terraform validate
```

Expected: `Success! The configuration is valid.`

- [ ] **Step 4: Review the plan**

```bash
cd terraform/prod && AWS_PROFILE=servant terraform plan
```

Expected: exactly 4 resources to add (`aws_cloudwatch_metric_alarm.ratelimit["authed"]`, `["manual_interests"]`, `["spotify_exchange"]`, `["ical_feed"]`), 0 to change, 0 to destroy. Anything else means the locals block was edited wrong — do not proceed.

- [ ] **Step 5: Commit**

```bash
git add terraform/prod/observability.tf
git commit -m "feat: alarm on the new rate-limit rejection buckets

Adds alarms for authed, manual_interests, spotify_exchange, and
ical_feed. The remaining new buckets emit metrics without paging."
```

`terraform/prod` auto-applies via CodeBuild on merge — no manual apply.

---

## Verification

After all seven tasks:

- [ ] `make test` passes from a clean database (`make db-reset && make migrate-test && make test`).
- [ ] `go build ./...` succeeds and `git grep -n "rate_limit_events\|ratelimit.NewPostgres"` returns only the `0019` down-migration and spec/plan documents.
- [ ] `git grep -n "Endpoint" internal/http/middleware/ratelimit.go` lists eleven constants.
- [ ] `terraform plan` in `terraform/prod` is clean after the merge applies.

## Deployment note

`TRUST_PROXY` is still required for every IP-keyed limit and still does not propagate through the normal deploy flow — see the runbook in `docs/superpowers/specs/2026-07-22-api-rate-limiting-design.md`. It is already set on the running task from the previous rate-limiting release; this change does not alter that. The authenticated limits added here are user-keyed and unaffected by it.
