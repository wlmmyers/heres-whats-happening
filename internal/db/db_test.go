package db

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestNewPool_PingSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, testdb.DSN())
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, pool.Ping(ctx))
}

func TestNewPool_BadDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := NewPool(ctx, "postgres://nope:nope@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	require.Error(t, err)
}

// staleDSN returns the test DSN with its password replaced by a wrong value,
// simulating a credential that was rotated out from under a running task, plus
// the correct password a provider would fetch fresh from Secrets Manager.
func staleDSN(t *testing.T) (dsn, correctPassword string) {
	t.Helper()
	u, err := url.Parse(testdb.DSN())
	require.NoError(t, err)
	correctPassword, _ = u.User.Password()
	u.User = url.UserPassword(u.User.Username(), "stale-rotated-out-password")
	return u.String(), correctPassword
}

// The point of the rotation fix: BeforeConnect must fetch the *current* password
// from the provider and override the stale one baked into the DSN, so a new
// connection succeeds even after the password the task started with is invalid.
func TestNewPoolWithPassword_ProviderOverridesStalePassword(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dsn, correct := staleDSN(t)
	pool, err := NewPoolWithPassword(ctx, dsn, func(context.Context) (string, error) {
		return correct, nil
	})
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, pool.Ping(ctx))
}

// Control: without the provider, the stale DSN must fail — proving the test
// above passes because of the override, not because the password still works.
func TestNewPool_StaleDSNFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dsn, _ := staleDSN(t)
	_, err := NewPool(ctx, dsn)
	require.Error(t, err)
}

// A provider failure must surface as a connection error, not a silent fallback
// to the stale DSN password.
func TestNewPoolWithPassword_ProviderErrorFailsConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewPoolWithPassword(ctx, testdb.DSN(), func(context.Context) (string, error) {
		return "", errors.New("secretsmanager unavailable")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "secretsmanager unavailable")
}

// Connections must rotate on a bounded clock so the pool picks up DNS changes
// after an RDS failover, rebalances across replicas, and bounds server-side
// memory growth. pgxpool's default is 1h with NO jitter, which expires every
// connection in the same second — a reconnect herd that, because BeforeConnect
// fetches the password from Secrets Manager, lands inside the acquire path.
func TestNewPool_RotatesConnectionsOnAJitteredClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, testdb.DSN())
	require.NoError(t, err)
	defer pool.Close()

	cfg := pool.Config()
	require.GreaterOrEqual(t, cfg.MaxConnLifetime, 30*time.Minute, "lifetime must be at least 30m")
	require.LessOrEqual(t, cfg.MaxConnLifetime, 60*time.Minute, "lifetime must be at most 60m")
	require.Greater(t, cfg.MaxConnLifetimeJitter, time.Duration(0),
		"zero jitter expires the whole pool at once")
}

// A warm connection must be ready when work arrives. MinConns alone does not
// give this: it floors TOTAL connections, so the one connection it guarantees
// is checked out under any load and the next acquirer still pays a cold connect
// plus a Secrets Manager round trip. MinIdleConns is the knob that floors IDLE
// connections (pgxpool/pool.go:562 reconciles them independently).
func TestNewPool_KeepsAWarmIdleConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, testdb.DSN())
	require.NoError(t, err)
	defer pool.Close()

	cfg := pool.Config()
	require.GreaterOrEqual(t, cfg.MinIdleConns, int32(1), "no idle floor means every quiet period costs a cold connect")
	require.GreaterOrEqual(t, cfg.MinConns, int32(1))
}

// pgxpool pings any connection idle for more than a second before handing it
// out (the default ShouldPing, pgxpool/pool.go:266). With no PingTimeout that
// ping inherits the caller's entire context, so one black-holed socket — the
// normal state of a pooled connection after an RDS failover — consumes a whole
// 5s request budget before Acquire even tries the next connection.
//
// The bound also has to stay small enough that Acquire's retry loop (which
// destroys a failed connection and tries another, up to MaxConns+1 times) can
// clear several stale connections inside one request budget.
func TestNewPool_BoundsTheLivenessPing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, testdb.DSN())
	require.NoError(t, err)
	defer pool.Close()

	cfg := pool.Config()
	require.Greater(t, cfg.PingTimeout, time.Duration(0),
		"an unbounded ping inherits the caller's whole budget")
	require.LessOrEqual(t, cfg.PingTimeout, time.Second,
		"must leave room for Acquire to retry other connections within a 5s handler budget")
}
