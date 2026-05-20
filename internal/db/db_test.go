package db

import (
	"context"
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
