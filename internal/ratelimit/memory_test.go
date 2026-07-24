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
