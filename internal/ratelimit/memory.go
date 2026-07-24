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

// Allow consumes one token for key. It never returns an error.
func (m *Memory) Allow(_ context.Context, key string) (Decision, error) {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

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
