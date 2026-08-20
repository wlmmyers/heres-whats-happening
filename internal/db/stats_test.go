package db

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmyers/heres-whats-happening/internal/observability"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func testPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := NewPool(ctx, testdb.DSN())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}

// pgxpool's counters are monotonic since pool creation. Emitting them raw would
// make a CloudWatch Sum meaningless (it would sum a running total), so each
// Sample must report only what happened since the previous one.
func TestStatsSampler_ReportsDeltasNotCumulativeTotals(t *testing.T) {
	ctx, pool := testPool(t)
	s := NewStatsSampler(pool)

	for i := 0; i < 5; i++ {
		_, err := pool.Exec(ctx, "SELECT 1")
		require.NoError(t, err)
	}
	first := s.Sample()
	require.GreaterOrEqual(t, first.Acquires, int64(5), "first sample covers the 5 acquires so far")

	for i := 0; i < 3; i++ {
		_, err := pool.Exec(ctx, "SELECT 1")
		require.NoError(t, err)
	}
	second := s.Sample()
	require.EqualValues(t, 3, second.Acquires,
		"second sample must cover only the 3 new acquires, not all 8")
}

// A sample with no activity between it and the previous one must report zero,
// not repeat the last non-zero delta.
func TestStatsSampler_QuietIntervalReportsZero(t *testing.T) {
	ctx, pool := testPool(t)
	s := NewStatsSampler(pool)
	_, err := pool.Exec(ctx, "SELECT 1")
	require.NoError(t, err)
	s.Sample()

	require.Zero(t, s.Sample().Acquires, "no acquires happened in between")
}

// The gauges describe utilisation right now, so unlike the counters they are
// absolute rather than differenced.
func TestStatsSampler_ReportsUtilisationGauges(t *testing.T) {
	ctx, pool := testPool(t)
	_, err := pool.Exec(ctx, "SELECT 1")
	require.NoError(t, err)

	got := NewStatsSampler(pool).Sample()
	require.EqualValues(t, 10, got.MaxConns, "MaxConns is the pool ceiling, not a delta")
	require.GreaterOrEqual(t, got.TotalConns, int32(1))
	require.GreaterOrEqual(t, got.IdleConns, int32(1), "the idle floor keeps a warm connection")
}

func TestStatsSampler_RunEmitsOnEachTickUntilContextCancelled(t *testing.T) {
	_, pool := testPool(t)
	runCtx, cancel := context.WithCancel(context.Background())

	var mu sync.Mutex
	var got []observability.PoolSample
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewStatsSampler(pool).Run(runCtx, 10*time.Millisecond, func(s observability.PoolSample) {
			mu.Lock()
			got = append(got, s)
			mu.Unlock()
		})
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) >= 3
	}, 2*time.Second, 5*time.Millisecond, "Run must emit once per tick")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return when its context is cancelled")
	}
}

// The signal item 6 exists for. Hold every connection in the pool, then make
// one more acquire block, and confirm the wait shows up. If this metric stays
// flat under genuine exhaustion it is worse than useless — it would read as
// "the pool is fine" during exactly the incident it is meant to catch.
func TestStatsSampler_ReportsWaitsWhenThePoolIsExhausted(t *testing.T) {
	ctx, pool := testPool(t)
	max := int(pool.Config().MaxConns)

	held := make([]*pgxpool.Conn, 0, max)
	for i := 0; i < max; i++ {
		c, err := pool.Acquire(ctx)
		require.NoError(t, err)
		held = append(held, c)
	}

	blocked := make(chan *pgxpool.Conn, 1)
	go func() {
		c, err := pool.Acquire(ctx)
		if err == nil {
			blocked <- c
		}
	}()

	// Let the goroutine reach the point of blocking, then free a slot for it.
	time.Sleep(50 * time.Millisecond)
	held[0].Release()
	held = held[1:]
	select {
	case c := <-blocked:
		c.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("the blocked acquire never completed")
	}
	for _, c := range held {
		c.Release()
	}

	got := NewStatsSampler(pool).Sample()
	require.GreaterOrEqual(t, got.Waits, int64(1), "an acquire that blocked on an empty pool must be counted")
	require.Greater(t, got.WaitDuration, time.Duration(0), "time spent blocked must be measured")
}

// Ties deadlines (item 3) to observability (item 6): when a query's context
// expires while it is still queued for a connection, that must surface as a
// distinct signal rather than being invisible or masquerading as a plain wait.
func TestStatsSampler_ReportsAcquiresCancelledByAnExpiredDeadline(t *testing.T) {
	ctx, pool := testPool(t)
	max := int(pool.Config().MaxConns)

	held := make([]*pgxpool.Conn, 0, max)
	for i := 0; i < max; i++ {
		c, err := pool.Acquire(ctx)
		require.NoError(t, err)
		held = append(held, c)
	}

	deadlined, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := pool.Acquire(deadlined)
	require.Error(t, err, "the pool is full, so this acquire cannot succeed before its deadline")

	for _, c := range held {
		c.Release()
	}

	got := NewStatsSampler(pool).Sample()
	require.GreaterOrEqual(t, got.CanceledAcquires, int64(1),
		"a deadline firing while queued for a connection must be visible")
}

// A scheduled job often finishes well inside one sample interval — match's
// median run is about 3s against a 60s interval. Without a final sample at
// shutdown those runs would report no pool metrics whatsoever, which is
// precisely when a connection problem would otherwise go unseen.
func TestStatsSampler_RunEmitsAFinalSampleOnShutdown(t *testing.T) {
	ctx, pool := testPool(t)
	_, err := pool.Exec(ctx, "SELECT 1")
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	var got []observability.PoolSample
	done := make(chan struct{})
	go func() {
		defer close(done)
		// An interval far longer than the run, so only a shutdown sample can fire.
		NewStatsSampler(pool).Run(runCtx, time.Hour, func(s observability.PoolSample) {
			got = append(got, s)
		})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	require.Len(t, got, 1, "a run shorter than one interval must still report once")
	require.GreaterOrEqual(t, got[0].Acquires, int64(1), "the final sample must carry the run's activity")
}
