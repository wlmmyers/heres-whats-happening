package ratelimit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

// A cancelled context (used above) fails every query, so it can't isolate a
// failure in the oldest-event lookup from the count query that runs first.
// failingQueryRowDBTX does that: it wraps a real DBTX and forces QueryRow to
// fail only for statements containing a chosen substring, delegating
// everything else untouched.
type failingQueryRowDBTX struct {
	store.DBTX
	failOn string
}

var errSimulatedQuery = errors.New("simulated query failure")

func (f *failingQueryRowDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, f.failOn) {
		return failingRow{}
	}
	return f.DBTX.QueryRow(ctx, sql, args...)
}

type failingRow struct{}

func (failingRow) Scan(dest ...any) error { return errSimulatedQuery }

// Over the limit, the count query already has the answer needed to decide
// Allowed; the oldest-event lookup only refines RetryAfter. A fault there must
// still surface to the caller rather than be silently discarded, without
// changing the decision itself.
func TestPostgres_ReturnsErrorWhenOldestEventQueryFails(t *testing.T) {
	pool := testdb.MustOpen(t)
	// "ORDER BY created_at ASC" appears only in OldestRateLimitEvent's SQL, not
	// CountRateLimitEvents's, so only the oldest-event lookup is made to fail.
	q := store.New(&failingQueryRowDBTX{DBTX: pool, failOn: "ORDER BY created_at ASC"})
	l := ratelimit.NewPostgres(q, "signup", 1, time.Hour)
	ctx := context.Background()

	require.NoError(t, l.Record(ctx, "1.1.1.1"))

	d, err := l.Allow(ctx, "1.1.1.1")
	require.Error(t, err, "a fault in the oldest-event query must surface")
	require.ErrorIs(t, err, errSimulatedQuery)
	require.False(t, d.Allowed, "the caller was already over the limit per the count query")
	require.Equal(t, time.Hour, d.RetryAfter, "falls back to the full window when the oldest-event lookup fails")
}
