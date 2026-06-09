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
