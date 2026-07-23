package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// internalFakeClock is a hand-advanced clock, local to this white-box test
// file. memory_test.go's fakeClock lives in package ratelimit_test and is not
// visible here.
type internalFakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newInternalFakeClock() *internalFakeClock {
	return &internalFakeClock{t: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *internalFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *internalFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Without a hard cap, a flood of unique keys (e.g. an attacker rotating
// source IPs) never leaves any bucket idle long enough for sweepLocked to
// reclaim it, so the map grows without bound. This test fails against the old
// code, which has no hardBucketCap and no eviction path.
func TestMemory_HardCapBoundsGrowthUnderUniqueKeyFlood(t *testing.T) {
	clk := newInternalFakeClock()
	l := NewMemoryWithClock(5, time.Minute, clk.Now)
	ctx := context.Background()

	// Drive more than hardBucketCap unique keys through Allow without ever
	// advancing the clock, so every bucket is always "freshly touched" and
	// ineligible for the idle sweep.
	total := hardBucketCap + 1000
	for i := 0; i < total; i++ {
		_, err := l.Allow(ctx, fmt.Sprintf("k-%d", i))
		require.NoError(t, err)
	}

	require.Less(t, len(l.buckets), hardBucketCap,
		"the hard cap must bound the map even when nothing is ever idle")
}

// The ordinary sweep (triggered at maxBucketsBeforeSweep) must run at most
// once per window, or a map hovering just above the threshold turns every
// subsequent Allow call into a full O(n) scan under the lock.
func TestMemory_SweepIsThrottledToOncePerWindow(t *testing.T) {
	clk := newInternalFakeClock()
	l := NewMemoryWithClock(5, time.Minute, clk.Now)
	ctx := context.Background()

	// Cross maxBucketsBeforeSweep with fresh, unique keys. The check runs
	// before insertion, so the map only reaches the threshold after the last
	// of these calls returns — no sweep has run yet.
	for i := 0; i < maxBucketsBeforeSweep; i++ {
		_, err := l.Allow(ctx, fmt.Sprintf("k-%d", i))
		require.NoError(t, err)
	}
	require.True(t, l.lastSweep.IsZero(), "no sweep should have run before the threshold was crossed")

	// This call sees len(m.buckets) >= maxBucketsBeforeSweep and lastSweep
	// still zero, so it triggers the first sweep.
	_, err := l.Allow(ctx, "trigger-sweep")
	require.NoError(t, err)
	firstSweep := l.lastSweep
	require.False(t, firstSweep.IsZero(), "crossing the threshold should trigger a sweep")

	// A second Allow call at the same instant (well within the window) must
	// not re-run the sweep.
	_, err = l.Allow(ctx, "second-call")
	require.NoError(t, err)
	require.Equal(t, firstSweep, l.lastSweep,
		"a second Allow within the same window must not re-run the sweep")
}

// Once the hard cap forces an eviction, the least-recently-seen buckets must
// go first — evicting a live, recently-active bucket would be needlessly
// disruptive when an idle one is available instead.
func TestMemory_EvictOldestPrefersLeastRecentlySeen(t *testing.T) {
	clk := newInternalFakeClock()
	l := NewMemoryWithClock(5, time.Minute, clk.Now)

	l.mu.Lock()
	l.buckets["old"] = &bucket{lastSeen: clk.Now()}
	clk.Advance(time.Hour)
	l.buckets["recent"] = &bucket{lastSeen: clk.Now()}
	l.evictOldestLocked(1)
	l.mu.Unlock()

	_, oldExists := l.buckets["old"]
	_, recentExists := l.buckets["recent"]
	require.False(t, oldExists, "the least-recently-seen bucket must be evicted first")
	require.True(t, recentExists, "the recently-seen bucket must survive")
}
