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
