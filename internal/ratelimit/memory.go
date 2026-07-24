package ratelimit

import (
	"context"
	"sort"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxBucketsBeforeSweep is the size at which we start sweeping idle buckets,
// at most once per window.
const maxBucketsBeforeSweep = 10_000

// hardBucketCap bounds the map even under a flood of unique keys, where no
// bucket is ever idle enough for sweepLocked to reclaim it.
const hardBucketCap = 50_000

type bucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// Memory is an in-process token-bucket Limiter. State is per-process: it resets
// on restart and is not shared across tasks, which is acceptable while the API
// runs a single ECS task (terraform/prod/ecs_api.tf desired_count).
//
// Allow peeks and Record spends. Every caller must pair them — an Allow with no
// matching Record consumes nothing.
type Memory struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	limit  rate.Limit
	burst  int
	window time.Duration
	now    func() time.Time

	lastSweep time.Time
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

// Sweep drops buckets untouched for more than two windows and returns how many
// were removed. A bucket refills completely within one window, so anything idle
// that long is necessarily full and carries no state worth keeping.
func (m *Memory) Sweep(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweepLocked(now)
}

func (m *Memory) sweepLocked(now time.Time) int {
	m.lastSweep = now
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

// evictOldestLocked deletes the n buckets with the oldest lastSeen. It is
// called only once the hard cap is hit and sweepLocked found nothing idle to
// reclaim, so evicting a batch (rather than one entry per call) amortizes the
// sort across the many requests before the cap is reached again.
func (m *Memory) evictOldestLocked(n int) {
	if n <= 0 {
		return
	}
	type entry struct {
		key      string
		lastSeen time.Time
	}
	entries := make([]entry, 0, len(m.buckets))
	for k, b := range m.buckets {
		entries = append(entries, entry{key: k, lastSeen: b.lastSeen})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastSeen.Before(entries[j].lastSeen)
	})
	if n > len(entries) {
		n = len(entries)
	}
	for _, e := range entries[:n] {
		delete(m.buckets, e.key)
	}
}

var _ Limiter = (*Memory)(nil)
