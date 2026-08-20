package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wmyers/heres-whats-happening/internal/observability"
)

// SampleInterval is how often the pool is sampled. It matches CloudWatch's
// one-minute standard resolution — sampling faster would emit points CloudWatch
// only aggregates away.
const SampleInterval = time.Minute

// counters holds the monotonic pgxpool totals from the previous sample, so the
// next one can be expressed as a delta.
type counters struct {
	acquires     int64
	waits        int64
	canceled     int64
	waitDuration time.Duration
}

// StatsSampler converts pgxpool's since-creation counters into per-interval
// deltas. WaitCount and WaitDuration are the cheapest database observability
// available: rising wait time means the pool is too small or something is
// holding connections too long, and InUse vs Idle shows utilisation.
//
// Not safe for concurrent use — one sampler per pool, driven by one Run loop.
type StatsSampler struct {
	pool *pgxpool.Pool
	prev counters
}

func NewStatsSampler(pool *pgxpool.Pool) *StatsSampler {
	return &StatsSampler{pool: pool}
}

// Sample reads the pool's current statistics and returns them with the counters
// differenced against the previous call. The first call reports everything
// since the pool was created, which is correct: nothing has been reported yet.
func (s *StatsSampler) Sample() observability.PoolSample {
	st := s.pool.Stat()
	cur := counters{
		acquires:     st.AcquireCount(),
		waits:        st.EmptyAcquireCount(),
		canceled:     st.CanceledAcquireCount(),
		waitDuration: st.EmptyAcquireWaitTime(),
	}
	out := observability.PoolSample{
		InUseConns: st.AcquiredConns(),
		IdleConns:  st.IdleConns(),
		TotalConns: st.TotalConns(),
		MaxConns:   st.MaxConns(),

		Acquires:         cur.acquires - s.prev.acquires,
		Waits:            cur.waits - s.prev.waits,
		CanceledAcquires: cur.canceled - s.prev.canceled,
		WaitDuration:     cur.waitDuration - s.prev.waitDuration,
	}
	s.prev = cur
	return out
}

// Run samples every interval until ctx is cancelled, passing each sample to
// emit, then emits one final sample on the way out. Intended to be started as a
// goroutine alongside the process's main work; emit must not block for long.
//
// The shutdown sample is what makes this useful for the scheduled commands: a
// match run finishing in seconds never reaches a 60s tick, and would otherwise
// report nothing at all for the run.
func (s *StatsSampler) Run(ctx context.Context, interval time.Duration, emit func(observability.PoolSample)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			emit(s.Sample())
			return
		case <-t.C:
			emit(s.Sample())
		}
	}
}

// StartStatsSampler samples pool in the background and publishes each sample as
// a CloudWatch EMF metric line dimensioned on service ("api", "match", ...).
// It returns immediately; the goroutine exits when ctx is cancelled.
func StartStatsSampler(ctx context.Context, pool *pgxpool.Pool, service string) {
	s := NewStatsSampler(pool)
	go s.Run(ctx, SampleInterval, func(sample observability.PoolSample) {
		observability.Default.PoolStats(service, sample)
	})
}
