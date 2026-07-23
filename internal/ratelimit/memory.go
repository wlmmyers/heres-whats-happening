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
